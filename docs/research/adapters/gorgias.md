# Gorgias helpdesk adapter: what the API actually says

Research pass: `developers.gorgias.com` fetched directly (the site serves a Markdown rendering of
any reference page by appending `.md`, and an OpenAPI fragment on each endpoint page), September
2026. This records what `internal/source/gorgias` was built against, separating what an official
page states from what the adapter infers. The vendor survey in `helpdesks.md` is the earlier,
broader pass; this is the one the code was written from.

## Confirmed against the official reference

**Host and auth.** The OpenAPI `servers` block on every endpoint page is
`https://{domain}.gorgias.com`, and `security` is `basicAuth`. The List Messages page states it
outright: "The username will be the email address of your Gorgias account and the password will
be your API key." OAuth2 exists for public apps distributed through the app store; its access
tokens expire in 24 hours.
([List Messages](https://developers.gorgias.com/reference/list-messages),
[OAuth2](https://developers.gorgias.com/docs/oauth2-authentication-for-creating-apps-with-gorgias))

**Ticket.** `GET /api/tickets/{id}`, path parameter `id` (integer), optional `relationships`
query array, basic auth, and the 200 body is the bare `Ticket` schema — not wrapped in an
envelope. Fields used by the adapter: `id`, `uri`, `status`, `channel`, `via`, `from_agent`,
`spam`, `language`, `subject`, `tags`, `customer`, `assignee_user`, and the ISO-8601 datetimes
`created_datetime`, `updated_datetime`, `closed_datetime`.
**There is no `priority` field on the Ticket object** — the property list has none, and the
`events` array that does exist is marked deprecated.
([Get Ticket](https://developers.gorgias.com/reference/get-ticket),
[The Ticket object](https://developers.gorgias.com/reference/the-ticket-object))

**Messages.** `GET /api/messages`, with query parameters `ticket_id` (integer), `limit`
(default 30, range 1–100), `order_by` (`created_datetime:asc` | `created_datetime:desc`, default
`desc`) and `cursor`. It is a top-level collection, not a ticket sub-path. The response envelope
is `{object, uri, data: [...], meta: {prev_cursor, next_cursor, total_resources}}`.
([List Messages](https://developers.gorgias.com/reference/list-messages),
[Pagination](https://developers.gorgias.com/reference/pagination))

**TicketMessage.** The two booleans the role mapping rests on are documented with distinct
meanings, quoted here because they are easy to conflate:

- `public` — "Whether the message was sent/receive by a customer. Internal notes are not public."
- `from_agent` — "Whether the message was sent by your company to a customer, or the opposite."

Also used: `id`, `ticket_id`, `channel`, `via`, `rule_id` ("ID of the rule which sent the
message, if any"), `subject`, `body_text`, `body_html`, `stripped_text`, `sender` ("The person
who sent the message. It can be a user or a customer"), `attachments`, `created_datetime`,
`sent_datetime`.
([The TicketMessage object](https://developers.gorgias.com/reference/the-ticketmessage-object))

**Internal notes.** The message-creation guide shows an internal note as `channel:
"internal-note"` with `from_agent: true` and no receiver, and the `receiver` property description
says it is "Optional when the source type is `internal-note`".
([Create a new message in ticket via API](https://developers.gorgias.com/docs/create-a-new-message-in-ticket-via-api))

**File.** The File object has exactly four properties: `content_type`, `name`, `size`, `url`.
**No id** — the URL is the identifier.
([The File object](https://developers.gorgias.com/reference/the-file-object))

**Download.** `GET /api/{file_type}/download/{domain_hash}/{resource_name}`, basic auth,
answering `307` — "Redirect to a signed URL that allows timed access to the requested
attachment." The page also says the identifier "can be extracted from the URL of an attachment by
removing the domain part and keeping the rest", which is what makes an attachment's `url` a path
on the account's own host.
([Download a file](https://developers.gorgias.com/reference/download-file))

**Ping endpoint.** `GET /api/account` ("Retrieve your account"), basic auth — the cheapest
authenticated call the API has, and what `sirdar doctor` uses.
([Retrieve your account](https://developers.gorgias.com/reference/get-account))

**Rate limits.** API-key integrations: 40 requests per 20-second window. OAuth2 apps: 80 per 20
seconds. Enterprise accounts get the same caps in a 10-second window. A 429 carries `Retry-After`
(seconds) and `X-Gorgias-Account-Api-Call-Limit` as `current/limit`.
([Rate Limits](https://developers.gorgias.com/reference/limitations))

## Inferred, not confirmed

- **The signed-URL host a download redirects to.** The reference says only "a signed URL". No
  official page names the host, and the search budget for this pass was spent. The adapter
  therefore trusts `*.gorgias.com` and refuses any other redirect target, naming the host in a
  per-ticket warning. Trusting a real storage host is a one-line addition to `gorgiasHosts` once
  somebody has watched a live account do it — guessing at `*.amazonaws.com` would trust every
  bucket on the internet, which is worse than a refused download.
- **Timestamp shape.** Documented as "ISO 8601 datetime" with no layout given. The adapter
  accepts RFC 3339 with and without fractional seconds, and the same shapes with no zone at all
  (read as UTC), rather than assuming one.
- **`status` and `channel` value sets.** The property pages give examples (`"open"`, `"email"`)
  and no enumeration. Both are passed through as strings rather than mapped, so an account with
  an unlisted value loses nothing.
- **`sender` shape.** Documented only as "a user or a customer". The adapter decodes the union of
  the two objects' identifying fields (`id`, `email`, `name`, `firstname`, `lastname`) and uses
  the first that is present, so a shape with fewer of them still yields a byline. The `id` is
  decoded as an integer, which is what both the User and Customer objects document.
- **A `rule_id` message is automation.** The field is documented; treating it as `system` rather
  than `agent` is this adapter's reading, on the grounds that an auto-reply is not a person.
- **Whether the account has an organisation concept.** The Ticket object carries a `customer`
  (a person) and no company object, so `Customer` is filled with the customer's display name and
  `Contact` with their email. If Gorgias has an org model reachable another way, this adapter
  does not use it.
- **No official MCP server.** Still unverified rather than confirmed absent, same as in the
  earlier pass.
