# Front: what the adapter was built against

`docs/research/adapters/helpdesks.md` has the survey-level Front entry, written from a
first pass over the developer site and flagging several fields as needing a live-tenant
check. This page records what was verified before `internal/source/front` was written,
and what is still inference. It is written against Front's Core API as documented in
September 2026.

Front's docs site serves a machine-readable copy of every page at the same URL with a
`.md` suffix, listed in [llms.txt](https://dev.frontapp.com/llms.txt). Those `.md`
pages carry the OpenAPI schemas and the full response examples that the rendered HTML
pages do not, and they are what every "confirmed" line below rests on.

## Endpoints the adapter calls

| Call | Endpoint | Scope | Used for |
|---|---|---|---|
| Ping | `GET /teammates?limit=1` | `teammates:read` | doctor's authenticated round trip |
| Ticket | `GET /conversations/{id}` | `conversations:read` | `Helpdesk.Get` |
| Thread, public half | `GET /conversations/{id}/messages?limit=100` | `messages:read` | `Helpdesk.Threads`, `Attachments` |
| Thread, internal half | `GET /conversations/{id}/comments?limit=100` | `comments:read` | `Helpdesk.Threads`, `Attachments` |
| Attachment bytes | `GET /download/{attachment_link_id}` | `attachments:read` | `Helpdesk.Attachments` |

Base URL `https://api2.frontapp.com`, `Authorization: Bearer {token}`.

## Confirmed

- **Auth and base URL.** `https://api2.frontapp.com`; the token "MUST be preceded by
  `Bearer`". API tokens are minted under Settings → Developers; OAuth 2.0 is for public
  multi-tenant apps ([Authentication](https://dev.frontapp.com/docs/authentication.md),
  [API tokens](https://dev.frontapp.com/docs/create-and-revoke-api-tokens.md)).
- **Conversation shape.** `GET /conversations/{conversation_id}` returns `id`, `type`
  (`conversation` | `discussion` | `task`), `subject`, `status` (`archived` |
  `unassigned` | `deleted` | `assigned`), `status_id`, `status_category` (`open` |
  `waiting` | `resolved`), `ticket_ids`, `assignee`, `recipient`, `tags`, `links`,
  `custom_fields`, `created_at`, `updated_at`, `waiting_since`, `is_private`,
  `scheduled_reminders`, `metadata`, and `_links.related` pointers to `messages`,
  `comments`, `events`, `followers`, `inboxes` and `last_message`
  ([Get conversation](https://dev.frontapp.com/reference/get-conversation-by-id.md)).
- **No priority field.** The conversation schema has none. Front models urgency with
  tags and custom fields, so `Priority` is left empty rather than read off a tag.
- **Two thread resources, not one.** `messages` is what was sent to and received from
  the customer; `comments` is the teammate notes, which Front describes as never sent
  and unable to be shared outside of Front
  ([Messages](https://dev.frontapp.com/reference/messages),
  [List conversation comments](https://dev.frontapp.com/reference/list-conversation-comments.md)).
- **Message shape.** `id`, `message_uid`, `type` (the channel: `email`, `sms`,
  `whatsapp`, `custom`, `internal`, …), `is_inbound`, `draft_mode` (`shared` |
  `private` | null), `error_type`, `version`, `created_at`, `subject`, `blurb`,
  `author` (a teammate), `recipients`, `body` (HTML), `text` (plain), `attachments`,
  `signature`, `metadata` ([Get message](https://dev.frontapp.com/reference/get-message.md)).
- **Author type enum.** `user` | `visitor` | `ai` | `api` | `application` |
  `bulk_reply` | `csat` | `integration` | `macro` | `rule` | `smart_csat` (same page).
  Everything but `user` and `visitor` is Front acting on its own, which is where the
  adapter's `system` role comes from.
- **Recipient shape.** `name` (nullable), `handle`, `role` (`from` | `to` | `cc` |
  `bcc` | `reply-to`) (same page). `role: "from"` is how the sender of an inbound
  message is named, since `author` is a teammate field.
- **Comment shape.** `id`, `author`, `body`, `posted_at`, `attachments`, `is_pinned`,
  and `_links.related.mentions`
  ([List conversation comments](https://dev.frontapp.com/reference/list-conversation-comments.md)).
- **Fractional timestamps.** The comments example carries `"posted_at":
  1698943401.378`, so timestamps are unix seconds as a JSON number with a fractional
  part, not integers (same page).
- **Attachment shape.** `id` (`fil_…`), `filename`, `url`, `content_type`, `size`, and
  `metadata` with `is_inline` and `cid`
  ([List conversation messages](https://dev.frontapp.com/reference/list-conversation-messages.md)).
- **Attachment download is authenticated, not pre-signed.** `GET
  /download/{attachment_link_id}` requires the bearer header and the `attachments:read`
  scope, and returns the binary with `Content-Type` and `Content-Length`
  ([Download attachment](https://dev.frontapp.com/reference/download-attachment.md)).
  This settles the question `helpdesks.md` left open, and it is the reason Front is the
  one built-in helpdesk with no fetch-only host tier.
- **Pagination.** `_pagination.next` is a ready-to-use full URL or null; `_results`
  holds the page. `limit` defaults to 50 and maxes at 100. Front's own instruction is
  "Always use the `next` value to determine if there are additional results", and
  explicitly warns that the API "may return fewer items than requested without
  signaling the end of results" ([Pagination](https://dev.frontapp.com/docs/pagination)).
  That is why the adapter follows `next` rather than stopping on a short page.
- **Rate limits.** 429 carries `retry-after` in seconds; `x-ratelimit-limit`,
  `-remaining`, `-reset`, `-burst-limit` and `-burst-remaining` ride along on every
  response. Plan limits are per minute: Starter 50, Professional 100, Enterprise 200
  ([Rate limiting](https://dev.frontapp.com/docs/rate-limiting.md)). `httpx.RetryAfter`
  reads the header as-is; header names are case-insensitive, so the lowercase spelling
  in Front's docs needs no special handling.
- **Web URL.** `https://app.frontapp.com/open/{conversation_id}`
  ([Conversations](https://dev.frontapp.com/docs/conversations.md)).

## Inferred, and what the adapter does about it

- **Per-company API host.** Front's reference examples show `_links.self` and an
  attachment `url` on `https://yourCompany.api.frontapp.com/…`, while every prose page
  names `api2.frontapp.com`. Whether a real tenant's responses carry the company form,
  the `api2` form, or both could not be settled without a live workspace. The adapter
  trusts both — `api2.frontapp.com` as the base, plus a dot-boundary
  `.api.frontapp.com` rule — and grants the credential to each, since a download from
  either is an authenticated call. `app.frontapp.com` and bare `frontapp.com` are *not*
  trusted: the token has no business going anywhere but the API host family.
- **`updated_at` on a conversation.** It is in the response schema, but Front's own
  example object shows only `created_at` and `waiting_since`. The adapter reads
  `updated_at` and falls back to `waiting_since`, because an empty `UpdatedAt` reads as
  "never touched".
- **`is_draft`.** Current schemas expose `draft_mode` (null when sent); older Front
  material describes an `is_draft` boolean. Both are decoded, and either one marks a
  draft, so a workspace still served the old shape does not have its drafts read as
  real replies.
- **Attachment id vs `attachment_link_id`.** The download endpoint's path parameter is
  named `attachment_link_id`, and the messages example shows an attachment whose `id`
  is `fil_3q8a7mby` alongside `"url": "…/download/fil_3q8a7mby"` — the same value. The
  adapter prefers the `url` Front sent and only builds `/download/{id}` when there is
  none, so the inference is a fallback rather than the main path.
- **Comment body content type.** Front's comment example is plain prose, and no schema
  says whether the field is HTML. The adapter converts a body containing `<` through
  `htmltext` and keeps one without it verbatim, so a plain multi-line note does not have
  its line breaks collapsed by an HTML converter.
- **Inbound means the customer.** `is_inbound` is documented as "received" rather than
  "from the customer". The adapter maps inbound to `customer` and never maps an
  outbound message to the customer whatever its author says, because a message wrongly
  attributed to the customer reads as the customer's own words.
- **Comment `limit` parameter.** The messages list documents `limit`, `page_token`,
  `sort_by` and `sort_order`; the comments list page documents no query parameters. The
  adapter sends `limit=100` to both — a list endpoint ignoring an unknown query
  parameter is the harmless failure mode, and the page size only affects how many
  round trips the feed takes.

## Not used

- **`GET /conversations/{id}/events`.** Status changes, assignments and tag edits live
  here rather than in either thread feed. Sirdar's thread is what was said, so the
  events feed is left alone; the cost is that a conversation's own history is not part
  of the bundle.
- **Front's hosted MCP server** (`https://mcp.frontapp.com/mcp`, OAuth 2.1 + PKCE). It
  exposes read tools that overlap this adapter's three calls, but it is a per-user
  OAuth identity and a second moving part for no read Sirdar cannot make itself.
- **`spirosoik/go-front`**, the only Go client, last touched in 2018. Stdlib
  `net/http` on `internal/source/httpx`, like every other built-in adapter.
