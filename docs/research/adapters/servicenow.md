# ServiceNow ITSM — what the adapter was built against

`docs/research/adapters/helpdesks.md` has a ServiceNow section, written when most of
`developer.servicenow.com` and much of `docs.servicenow.com` were answering automated fetches
with 403s. This file records what was re-checked while `internal/source/servicenow` was written
(11 September 2026), and — more usefully — which parts of the adapter rest on a documentation
page and which rest on platform knowledge that a live instance should settle.

## Confirmed against a live documentation page

Both of these pages loaded and were read in full during this pass.

**Table API** — [c_TableAPI.html](https://www.servicenow.com/docs/access?topicname=c_TableAPI.html)

- `GET /api/now/table/{tableName}` lists records; `GET /api/now/table/{tableName}/{sys_id}`
  returns one.
- `sysparm_query` is the encoded query that filters the result set, with `=`, `!=`, `LIKE`,
  `STARTSWITH`, `ENDSWITH`, `^` for AND and `^OR` for OR.
- `sysparm_limit` (default 10000) and `sysparm_offset` (default 0) are the pagination pair.
- `sysparm_fields` is the comma-separated field list.
- `sysparm_display_value` returns display values (`true`), database values (`false`) or both
  (`all`); `sysparm_exclude_reference_link` drops the API link on a reference field.
- The response carries a `Link` header with `next`/`prev`/`first`/`last` and an `X-Total-Count`.
- The documented examples authenticate with HTTP basic (`--user 'username':'password'`).

**Attachment API** —
[c_AttachmentAPI.html](https://www.servicenow.com/docs/access?topicname=c_AttachmentAPI.html)

- `GET /api/now/attachment` returns metadata for several attachments;
  `GET /api/now/attachment/{sys_id}` for one; `GET /api/now/attachment/{sys_id}/file` streams
  the bytes.
- The list endpoint takes `sysparm_query`, `sysparm_limit` (default 1000) and `sysparm_offset`.
- Metadata fields include `sys_id`, `file_name`, `content_type`, `size_bytes`, `table_name`,
  `table_sys_id`, `download_link`, `sys_created_on`, `sys_created_by`.
- The documented examples authenticate with HTTP basic.

**OAuth 2.0, inbound** —
[c_OAuthApplications.html](https://www.servicenow.com/docs/access?topicname=c_OAuthApplications.html)

- The page describes the "OAuth external client scenario (Inbound)", where the instance provides
  an endpoint for third-party clients to pull data, and says OAuth 2.0 "lets users access
  instance resources through external clients by obtaining a token rather than by entering login
  credentials with each resource request."
- It does **not** state the header the token travels in. `Authorization: Bearer <token>` is the
  OAuth 2.0 bearer-token standard and is what the adapter sends, but that specific detail is
  inferred rather than quoted.

## Inferred — verify against a live instance before trusting it

These are the claims the adapter acts on that no page read in this pass confirmed. Each is
well-established platform knowledge; none of it was re-verified here, and the searches that would
have chased the rest were out of budget.

1. **`sys_journal_field` holds the comment and work-note history**, one row per entry, keyed by
   the record's `sys_id` in `element_id` and the field name in `element` (`comments` for
   customer-visible, `work_notes` for internal), with `value`, `sys_created_on` and
   `sys_created_by`. This is the single most important unverified claim, exactly as
   `helpdesks.md` flagged it. It is also the most likely to be *unreadable* rather than wrong:
   `sys_journal_field` is ACL-restricted on plenty of instances, and an integration user who can
   read `incident` cannot always read it. The adapter therefore treats a failure here as a
   warning on the bundle, not a failed fetch — the record's own description still reaches the
   agent.
2. **`incident` field names**: `number`, `short_description`, `description`, `state`, `priority`,
   `urgency`, `impact`, `category`, `contact_type`, `assigned_to`, `opened_by`, `caller_id`,
   `company`, `opened_at`, `sys_created_on`, `sys_updated_on`, `active`. Drawn from the standard
   ITSM schema, not read out of the incident dictionary. A missing field costs that one value,
   not the fetch: every field is read individually and an absent key maps to an empty string.
3. **Dot-walking in `sysparm_fields`** — `caller_id.user_name`, `caller_id.email`,
   `company.sys_id`, and `assigned_to.user_name` in a query. Widely used; not confirmed on a page
   here. An instance that will not serve a dot-walk simply omits the key, which is why the
   thread's role assignment falls back to matching the caller's display name and `CustomerID`
   is left empty rather than filled with the company's name.
4. **`javascript:gs.getUserID()` as an encoded-query value** for `ListFilter.Assignee: "me"`.
   Standard idiom, unconfirmed here.
5. **`elementIN comments,work_notes`** — the `IN` operator in an encoded query. `^OR` would have
   been the alternative, and its precedence across a three-condition query is the kind of thing
   that silently returns the wrong set, so `IN` was preferred.
6. **`nav_to.do?uri={table}.do?sys_id={sys_id}`** as the human-facing record URL, from
   `helpdesks.md` rather than from a page read here.
7. **Rate limits** are configured per instance by the customer's own admin — there is no global
   published figure to code against. The adapter honours `Retry-After` up to 30 seconds and
   reports `rate_limited` otherwise, which is the only behaviour that does not depend on knowing
   the number.

## Deliberate consequences of `sysparm_display_value=true`

The house pattern asks for display values so a reference field reaches the agent as the name a
human reads. Two things follow, and both are documented in `docs/adapters.md` rather than left to
be discovered:

- A reference field carries **no id at all** in that mode. `CustomerID` therefore comes from the
  dot-walked `company.sys_id`, and is empty on an instance that will not serve it.
- Timestamps are rendered **in the integration user's display timezone, with no offset named**.
  The adapter reads a naive timestamp as UTC, so an integration user on any other timezone
  shifts every time in the bundle by that offset. The fix is a UTC integration user, which is
  ordinary practice for a ServiceNow integration account, and the adapter docs ask for one.

## Not pursued

- Whether a newer dedicated Journal or Comments API has superseded the `sys_journal_field`
  pattern. `helpdesks.md` raised this and it remains open.
- Any official ServiceNow MCP server. `helpdesks.md` found none and nothing here changes that.
- `sc_task` and `sn_customerservice_case` as alternative tables. The adapter's `table` setting
  points at them and the field set is the ITSM one, so a CSM shop will likely want a different
  field list; that is a configuration surface nobody has asked for yet.
