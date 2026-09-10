# Helpdesk adapters: survey of candidate vendors

Scope: what a Sirdar `helpdesk` adapter needs from each vendor — get ticket by id, get the
ordered conversation thread (author role, public/private), and download attachments — using
plain Go and stdlib `net/http`, read-only, with credentials passed as `env:`/`keychain:`
references per `docs/config.md`. Zoho Desk (`internal/source/zohodesk`) is the only adapter
built so far; this survey is for picking and scoping the next ones.

Research pass: web search and vendor-doc fetches, September 2026. Every non-obvious claim is
cited; anything that couldn't be confirmed against an official page in this pass is marked
**unverified**. Two vendor doc sites (`developer.salesforce.com`, most of
`docs.servicenow.com`/`developer.servicenow.com`) returned 403s to automated fetches during this
research, so the Salesforce and part of the ServiceNow sections lean more heavily on
unverified general platform knowledge than the others — flagged inline and again in Open
questions.

---

## Zendesk Support

**Base URL.** `https://{subdomain}.zendesk.com/api/v2/...` — the subdomain is the whole account
identifier. No distinct regional/data-centre subdomain variant is documented for the core
Ticketing API; a config only needs the one subdomain string
([API reference](https://developer.zendesk.com/api-reference/ticketing/tickets/tickets/)).

**Auth.** API token over HTTP Basic auth — username `{agent_email}/token`, password the token
(`curl -u agent@example.com/token:TOKEN ...`) — or OAuth Bearer, which Zendesk is nudging
integrations toward but isn't required
([Security and authentication](https://developer.zendesk.com/api-reference/introduction/security-and-auth/),
[OAuth migration](https://developer.zendesk.com/documentation/authentication/oauth-migration/)).
For a static-credential stdlib client, API token + Basic is the simpler fit — no refresh loop.

**Three calls.**
- Ticket: `GET /api/v2/tickets/{id}.json` → `subject`, `status` (`new|open|pending|hold|solved|closed`),
  `priority` (`urgent|high|normal|low`), `via` (creation channel), `requester_id`, `submitter_id`,
  `organization_id`, `url`, `created_at`, `updated_at`
  ([Tickets](https://developer.zendesk.com/api-reference/ticketing/tickets/tickets/)). Contact/org
  are IDs only — resolving a name/email needs `GET /api/v2/users/{requester_id}.json` (or
  `?include=users` side-loading) and `GET /api/v2/organizations/{organization_id}.json`.
- Thread: `GET /api/v2/tickets/{ticket_id}/comments.json`, paginated, creation order. Each comment
  has `author_id`, `public` (bool), `body`/`html_body`/`plain_body`, `created_at`, `type`
  (`Comment`/`VoiceComment`), `attachments[]` with `content_url`
  ([Ticket Comments](https://developer.zendesk.com/api-reference/ticketing/tickets/ticket_comments/)).
  There's no role field on the comment itself — resolve `author_id` against
  `GET /api/v2/users/{id}.json`, whose `role` is `"end-user" | "agent" | "admin"`
  ([Users](https://developer.zendesk.com/api-reference/ticketing/users/users/)).
- Attachment: fetch the comment attachment's `content_url` with the same auth header; Zendesk
  responds 302 with a temporary signed `Location`, which is then GET-able with no further auth
  ([Accessing end user uploaded attachments](https://developer.zendesk.com/documentation/ticketing/managing-tickets/accessing-end-user-uploaded-attachments/)).

**Customer vs. agent / public vs. private.** `Ticket Comment.public` (bool) is the public/private
split. Customer vs. agent is derived, not inline: resolve `author_id` → `User.role`.

**Side Conversations** (`GET /api/v2/tickets/{ticket_id}/side_conversations`, Suite
Professional+) are a separate feature for messaging people *outside* the primary ticket thread
(email/Slack/Teams), logged against the ticket but not part of the comment stream
([Side Conversations](https://developer.zendesk.com/api-reference/ticketing/side_conversation/side_conversation/)).
Not the same thing as an internal note (those are just `public:false` comments). Sirdar's
"conversation thread in order" doesn't need this endpoint.

**Pagination.** Cursor (recommended): `page[size]` (max 100), `page[after]`/`page[before]`,
`meta.has_more`, `links.next`. Offset (legacy default): `page`/`per_page` (max 100), capped at
100 pages / 10,000 resources before it 400s
([Pagination](https://developer.zendesk.com/api-reference/introduction/pagination/)). Use cursor.

**Rate limits.** Team 200 rpm, Professional 400 rpm, Enterprise 700 rpm; High Volume API add-on
raises the account ceiling to 2500 rpm. Sub-limits: List Tickets past page 500 → 50 rpm; Update
Ticket → 30/10min per user per ticket; Incremental Exports → 10 rpm (30 with add-on); Ticket
Attachment Content → 2500 rpm
([Rate limits](https://developer.zendesk.com/api-reference/introduction/rate-limits/)). 429s carry
`Retry-After`.

**MCP.** Confirmed. Zendesk announced an MCP client and server at Relate, May 2026 (client early
access from June 2026); the server runs at `https://{subdomain}.zendesk.com/api/mcp`, OAuth,
read+write scopes
([TechRadar](https://www.techradar.com/pro/zendesk-becomes-the-latest-to-adopt-mcp-to-futureproof-customers-in-the-ai-first-era),
[Zendesk support article](https://support.zendesk.com/hc/en-us/articles/10497779528730-Connecting-to-MCP-servers-and-using-MCP-tools-in-action-flows),
[Zendesk blog](https://www.zendesk.com/blog/zendesk-insights/innovation/zendesk-ai-mcp-client/)).
Not relevant to a stdlib REST adapter.

**Go clients.** `nukosuke/go-zendesk` ([GitHub](https://github.com/nukosuke/go-zendesk),
[pkg.go.dev](https://pkg.go.dev/github.com/nukosuke/go-zendesk)) — MIT, community, reasonably
active, cursor-pagination iterators, mock package. For three read-only GETs against a plain JSON
REST API, stdlib `net/http` is the better call — no dependency, no abstraction to work around.

---

## Freshdesk

**Base URL.** `https://{domain}.freshdesk.com/api/v2/{resource}`
([API docs](https://developers.freshdesk.com/api/)). No documented regional host variant —
**not found**; everything routes through the account subdomain.

**Auth.** HTTP Basic with the account API key as username and the literal string `X` as password
(`curl -u apikey:X ...`). This is the only documented auth mode for the core Ticketing API — no
OAuth — which makes it trivially the simplest option among all vendors surveyed here
([API docs](https://developers.freshdesk.com/api/)).

**Three calls.**
- Ticket: `GET /api/v2/tickets/{id}`, optionally `?include=conversations`. Fields: `id`,
  `subject`, `status` (int enum: 2 Open, 3 Pending, 4 Resolved, 5 Closed), `priority` (int enum:
  1 Low, 2 Medium, 3 High, 4 Urgent), `source` (int channel enum: 1 Email, 2 Portal, 3 Phone,
  7 Chat, 9 Feedback Widget, 10 Outbound Email), `requester_id`, `company_id`, `created_at`,
  `updated_at` ([API docs](https://developers.freshdesk.com/api/)). No API-returned web URL —
  construct `https://{domain}.freshdesk.com/a/tickets/{id}` client-side.
- Thread: `?include=conversations` on the ticket GET embeds only up to 10 recent conversations
  ([community note on the 10-item cap](https://community.freshworks.com/api-and-webhooks-11406/not-receiving-all-conversations-through-freshdesk-api-23736)).
  For a complete ordered thread use the dedicated, paginated
  `GET /api/v2/tickets/{id}/conversations` instead. Fields: `id`, `body`/`body_text`, `incoming`
  (bool), `private` (bool), `user_id`, `attachments[]` (`id`, `name`, `content_type`, `file_size`,
  `attachment_url`), `created_at`, `updated_at`. The numeric `source` mapping on a conversation
  (reply vs. note vs. forwarded email) is **unverified** — not confirmed against an official page
  in this pass.
- Attachment: attachment objects carry `attachment_url`, a direct CDN link
  (`https://cdn.freshdesk.com/data/helpdesk/attachments/production/{id}/original/{filename}`).
  Whether that URL needs the same Basic-auth header or is already pre-signed is **unverified** —
  try authenticated first, fall back to unauthenticated on 401.

**Customer vs. agent / public vs. private.** `private` (bool) on the conversation object is the
public/private split — `true` = internal note, agent-only visibility
([Freshdesk conversations model](https://www.eesel.ai/blog/freshdesk-conversations-api);
field name from [Freshworks SDK docs](https://developers.freshworks.com/freshdesk-sdk/docs/ConversationsApi.html)).
There's no explicit role field for customer vs. agent — use `incoming` (customer-originated
inbound = `true`) combined with resolving `user_id` against Contacts
(`GET /api/v2/contacts/{id}`) or Agents (`GET /api/v2/agents/{id}`). Entries with no matching
contact/agent id should be treated as `system`.

**Pagination.** `page` (1-indexed), `per_page` (default 30, max 100); a `link` response header
carries the next-page URL.

**Rate limits.** Per the official page: Blossom/Garden 3,000 calls/hour, Estate/Forest
5,000 calls/hour, with an older per-minute framing also present for other tiers (Growth 100 rpm,
Pro 400 rpm, Enterprise 700 rpm) — the docs mix hourly and per-minute framing by plan generation,
so treat the tier mapping as plan-dependent and read `X-RateLimit-Remaining`/`Retry-After` off
live responses rather than hardcoding
([API docs, rate limit section](https://developers.freshdesk.com/api/#ratelimit)).

**MCP.** Confirmed but limited: Freshworks has an MCP integration in Early Access/Beta, restricted
to Enterprise-plan Freshdesk accounts, API-key auth
([Freshdesk MCP EAP](https://support.freshdesk.com/support/solutions/articles/50000012670-model-context-protocol-mcp-integration-in-freshdesk-eap-),
[Freshworks Developer Docs](https://developers.freshworks.com/docs/agentic-dev-tools/mcp-server/)).
Not GA, not relevant to a plain REST adapter.

**Go clients.** Several small community projects (`abemedia/go-freshdesk`,
`mattbaird/freshdesk4go`, `askasoft/gofresh`, others) — none dominant, maturity unverified
per-repo. Stdlib `net/http` is the sound choice: Basic-auth REST/JSON, no OAuth dance.

### Freshservice differences

Same Freshworks platform, same general shape, different base and ITSM object types.

- Base: `https://{domain}.freshservice.com/api/v2/`; API-key Basic auth (username/password auth
  for the API was deprecated May 31, 2023) ([api.freshservice.com](https://api.freshservice.com/)).
- Ticket: `GET /api/v2/tickets/{id}` — superset of Freshdesk's fields (`requester_id`,
  `responder_id`, `group_id`, `due_by`, `custom_fields`, `tags`, plus `workspace_id` on
  workspace-enabled accounts).
- Conversations: `GET /api/v2/tickets/{id}/conversations`, with a reply/note write-endpoint split
  mirroring Freshdesk's. Whether the conversation object carries the same explicit `private`
  boolean is **unverified** in this pass — likely, given the shared platform, but confirm against
  a live account before relying on it.
- Same `page`/`per_page` pagination.
- Rate limits (official, per-minute by plan): Starter 100, Growth 200, Pro 400, Enterprise 500,
  with endpoint sub-limits (e.g. View Ticket 50/80/140/160 across the same tiers); paid add-ons
  raise Pro/Enterprise to 1,000–2,000 rpm ([api.freshservice.com](https://api.freshservice.com/v2/#ratelimit)).
- MCP: a separately announced, also-EAP "Inbound MCP" for Freshservice
  ([Freshservice MCP EAP](https://support.freshservice.com/support/solutions/articles/50000012678-model-context-protocol-mcp-integration-in-freshservice-eap-)).

---

## Intercom

**Base URL.** Three fixed regional hosts by workspace data residency: `https://api.intercom.io`
(US, default), `https://api.eu.intercom.io` (EU), `https://api.au.intercom.io` (AU). Calling the
US host from a non-US workspace gets proxied, but Intercom recommends calling the workspace's own
regional host directly
([API reference](https://developers.intercom.com/docs/references/rest-api/api.intercom.io),
[REST APIs overview](https://developers.intercom.com/docs/build-an-integration/learn-more/rest-apis)).

**Auth.** A static access token (Bearer, minted once in Developer Hub) for a single-workspace
private app, or OAuth for public multi-tenant apps. For Sirdar's static-credential model the
access token is the intended, simplest mechanism
([Authentication](https://developers.intercom.com/docs/build-an-integration/learn-more/authentication)).

**Three calls.**
- Ticket: `GET /tickets/{ticket_id}` → `id`, `ticket_id` (the id shown in Intercom's UI),
  `ticket_type`, `ticket_state`, `contacts`, `admin_assignee_id`, `created_at`, `updated_at`. No
  documented `priority` or web-URL field on the Ticket model — priority would need a
  workspace-defined `ticket_attributes` value if one exists; no "view in app" link is returned
  ([Ticket reference](https://developers.intercom.com/docs/references/rest-api/api.intercom.io/tickets/ticket)).
  Tickets have their own thread via `ticket_parts` (cap 500), distinct from any linked
  Conversation ([Tickets](https://developers.intercom.com/docs/references/rest-api/api.intercom.io/tickets)).
- Thread: `GET /conversations/{conversation_id}` → `state`, `priority` (`none|low|medium|high|urgent`),
  `admin_assignee_id`, `team_assignee_id`, `created_at`/`updated_at`, plus embedded
  `conversation_parts.conversation_parts[]` — **hard cap of 500 parts per response**, so a long
  thread needs its own parts-pagination handling beyond that cap
  ([Retrieve conversation](https://developers.intercom.com/docs/references/rest-api/api.intercom.io/conversations/retrieveconversation)).
- Attachment: parts carry `part_attachment` objects (`name`, `url`, `content_type`, `filesize`,
  `width`, `height`) ([Part attachment model](https://developers.intercom.com/docs/references/rest-api/api.intercom.io/models/part_attachment)).
  Per a community thread (not the formal reference, so **community-sourced, not independently
  confirmed**), these CDN `url` values are pre-signed with roughly a 30-minute expiry — download
  promptly, re-fetch the conversation if expired
  ([community thread](https://community.intercom.com/conversations-9/handling-and-accessing-file-attachment-7598)).

**Customer vs. agent / public vs. private.** Each part's `author.type` distinguishes
`user`/`lead` (customer) from `admin`/`bot`/`team` (agent) — this enum list is consistent with
Intercom's documented terminology elsewhere but the full `conversation_part` schema page 404'd
during this research, so **not independently re-verified against a live schema**; spot-check
before hard-coding. `part_type` is the public/private split: `"comment"` = customer-visible,
`"note"` = internal-only, admin-to-admin
([Ticket reply model](https://developers.intercom.com/docs/references/rest-api/api.intercom.io/models/ticket_reply),
[Reply to a conversation](https://developers.intercom.com/docs/references/rest-api/api.intercom.io/conversations/replyconversation)).

**Pagination.** Cursor: `per_page` (max 150), `starting_after` from `pages.next.starting_after`.
Stateless cursor — concurrent record changes can cause duplicates/gaps
([Cursor pagination](https://developers.intercom.com/docs/build-an-integration/learn-more/rest-apis/pagination-cursor)).

**Rate limits.** 10,000 req/min per app, 25,000 req/min per workspace, enforced as a rolling
budget in 10-second windows (~166 req/10s); 429 on breach; higher limits available on request
([Rate limiting](https://developers.intercom.com/docs/references/rest-api/errors/rate-limiting)).

**MCP.** Confirmed official: the Intercom MCP Server, 14 tools (search/fetch conversations,
contacts, companies, articles, internal-note creation) — **not read-only**, write scopes required
for note creation ([MCP guide](https://developers.intercom.com/docs/guides/mcp)). A third-party
directory claims US/EU-only, no AU yet — **unverified against Intercom's own page**.

**Go clients.** Official `github.com/intercom/intercom-go` — thin wrapper, but the repo's own
README says Intercom is running it on limited maintenance capacity (critical issues only) while
building a dedicated SDK team ([repo](https://github.com/intercom/intercom-go)). Given that and
the narrow three-call surface, stdlib `net/http` is the better choice — no dependency risk, and
the whole client is a bearer header plus three GETs.

---

## HubSpot Service Hub

**Base URL.** Single global host, `https://api.hubapi.com` — no documented region variant.
Legacy API keys were deprecated November 30, 2022 in favor of private-app tokens
([Authentication overview](https://developers.hubspot.com/docs/apps/developer-platform/build-apps/authentication/overview)).

**Auth.** Private-app access token (`pat-na1-...`/`pat-eu1-...`, Bearer header), generated once
under Settings → Integrations → Private Apps, shown only at creation — versus a full OAuth app
for multi-tenant integrations. For a single-account static-credential stdlib client, the
private-app token is the direct fit — no refresh flow
([Authentication overview](https://developers.hubspot.com/docs/apps/developer-platform/build-apps/authentication/overview)).

**Three calls.**
- Ticket: `GET /crm/v3/objects/tickets/{ticketId}?properties=...&associations=...`. Relevant
  properties: `subject`, `content`, `hs_pipeline`, `hs_pipeline_stage` (status),
  `hs_ticket_priority`, `hs_ticket_category`, `createdate`, `hs_lastmodifieddate`. Properties are
  opt-in per request via `properties`; `associations` pulls linked contacts/companies in the same
  call. No API-returned record URL — a UI link (e.g.
  `https://app.hubspot.com/contacts/{portalId}/ticket/{ticketId}`) is an inferred pattern, **not
  confirmed in the reference**
  ([Tickets guide](https://developers.hubspot.com/docs/guides/api/crm/objects/tickets)).
- Thread: HubSpot tickets don't carry an inline thread — a ticket is associated to a Conversations
  Inbox thread via the general Associations API
  (`PUT /crm/v3/objects/tickets/{ticketId}/associations/{toObjectType}/{toObjectId}/{associationTypeId}`),
  read back off the ticket's `associations` block. This specific ticket↔thread association
  mechanism is corroborated only by a community thread, **not a primary reference page** —
  flagged as the weakest-verified claim in this survey
  ([community thread](https://community.hubspot.com/t/associating-a-conversation-to-ticket/108218)).
  Once `threadId` is known: `GET /conversations/v3/conversations/threads/{threadId}/messages`
  (and `GET /conversations/v3/conversations/threads` to list/filter threads by ticket/contact/inbox)
  — this part is on HubSpot's own reference
  ([Conversations guide](https://developers.hubspot.com/docs/api-reference/legacy/conversations/guide)).
- Attachment: message `attachments[]` holds `type: "FILE"` objects referencing a file by `fileId`,
  resolving to an absolute URL. The exact field set (id/name/size/mime) and whether the download
  URL needs an `Authorization` header or is pre-signed is **not spelled out on the reference
  page — unverified**, needs a live-account check before implementation.

**Customer vs. agent / public vs. private.** Messages carry `senders[]`/`recipients[]` with an
`actorId` whose *prefix* encodes role, not an explicit type field: `A-` agent/HubSpot user, `V-`
visitor/contact, `E-` email address, `I-` integration, `S-` system — plus an `L-` prefix the
source page associated with "customer agent," which is less certain and should be spot-checked
against a live payload. The `type` field on a message is the public/private split: `"MESSAGE"`
(customer-facing) vs. `"COMMENT"` (internal-only, never sent to the visitor); `"WELCOME_MESSAGE"`
also appears. `direction` (`INCOMING`/`OUTGOING`) is separate and orthogonal
([Conversations guide](https://developers.hubspot.com/docs/api-reference/legacy/conversations/guide)).

**Pagination.** Cursor: `after`/`limit`, threads/messages endpoints cap at 500 per page — notably
higher than CRM object endpoints (100/page typical, 10,000-record ceiling per search query before
needing to chunk by filter). Cursor returned as `paging.next.after`
([Conversations guide](https://developers.hubspot.com/docs/api-reference/legacy/conversations/guide)).

**Rate limits.** Private-app tiers (burst/10s, daily cap): Free/Starter 100/10s, 250,000/day;
Professional 190/10s, 625,000/day; Enterprise 190/10s, 1,000,000/day; a paid add-on raises burst
to 250/10s plus +1,000,000/day. CRM **search** endpoints are separately capped at 5 req/s per
account ([Usage guidelines](https://developers.hubspot.com/docs/developer-tooling/platform/usage-guidelines)).
No documented figure specific to the Conversations threads/messages endpoints — **unverified**,
assume the general app-level limits apply.

**MCP.** Confirmed official: HubSpot MCP Server, public beta announced May 6, 2025 — read/write
across CRM objects including tickets, plus engagements, with read-only org context and marketing
assets and an explicit carve-out for sensitive/PHI properties; OAuth 2.0 via HubSpot User-level
Apps ([Changelog](https://developers.hubspot.com/changelog/mcp-server-beta)). A separate claim of
GA on April 13, 2026 could not be corroborated on HubSpot's own changelog — **treat GA date as
unverified**, beta is the only officially confirmed state found.

**Go clients.** No official Go SDK found (no `hubspot/hubspot-api-go` repo, no Go entry on
HubSpot's SDK listings) — absence-of-evidence, not a confirmed negative from a canonical index,
but consistent with what's publicly known. Given that and the narrow endpoint surface, stdlib
`net/http` is the clear choice.

---

## Jira Service Management

Overlaps with Sirdar's plain Jira tracker adapter: a JSM "customer request" is the same
underlying Jira issue viewed through the service-desk lens. Fetching the issue body can reuse the
tracker adapter's plumbing; what's JSM-specific is the request-portal metadata (request type,
SLA, portal status) and — critically — the public/internal distinction on comments, which vanilla
Jira issues don't carry natively. The vanilla comment endpoint only exposes a `public`/`jsdPublic`
flag *because* a Service Desk project is attached
([community: `jsdPublic`](https://community.atlassian.com/forums/Jira-questions/Jira-Cloud-API-for-Creating-Comments-on-Issue-has-a-jsdPublic/qaq-p/868324),
[JSDCLOUD-7997](https://jira.atlassian.com/browse/JSDCLOUD-7997)). Practical implication: a JSM
helpdesk adapter should hit the Service Desk REST API (`servicedeskapi`) directly rather than
reimplement JSM semantics over the tracker adapter — they can share HTTP/auth plumbing but should
be separate adapter implementations.

**Three calls.**
- Ticket: `GET https://{site}.atlassian.net/rest/servicedeskapi/request/{issueIdOrKey}` →
  `issueId`, `issueKey`, `summary`, `requestTypeId`, `serviceDeskId`, `createdDate` (multiple
  formats), `currentStatus` (name/category/date), `reporter` (account id, name, email, timezone),
  `requestFieldValues` (label/value pairs; hidden fields excluded for customer-scoped tokens),
  `_links` (portal URL, agent view, Jira REST self-link)
  ([API group: request](https://developer.atlassian.com/cloud/jira/service-desk/rest/api-group-request/)).
  No direct `priority`/`channel` field — likely inside `requestFieldValues` or needs cross-
  referencing the underlying issue via `/rest/api/3/issue/{key}` — **unverified exact shape**,
  check against a live tenant. Customer-scoped tokens only see requests they created,
  were created on behalf of, or participate in.
- Thread: `GET /rest/servicedeskapi/request/{issueIdOrKey}/comment` (paginated `start`/`limit`,
  `expand`) → `author`, `body`, `created`, and a boolean `public` — `false` = internal/agent-only,
  `true` = customer-visible
  ([API group: request](https://developer.atlassian.com/cloud/jira/service-desk/rest/api-group-request/);
  semantics corroborated by
  [mcp-atlassian #847](https://github.com/sooperset/mcp-atlassian/issues/847) and
  [community: private comment in JSD](https://community.atlassian.com/forums/Jira-questions/create-private-comment-in-jiraservicedesk/qaq-p/2146244)).
  Single comment: `GET .../comment/{commentId}`. Exact attachment-id field name on a comment is
  **unverified** — the OpenAPI schema wasn't retrievable in this pass. JSM has no distinct
  "system" author on this endpoint — status-transition events would need the issue changelog
  instead, **unverified**.
- Attachment: metadata via `GET/POST /rest/servicedeskapi/request/{issueIdOrKey}/attachment`;
  byte download via the platform v3 endpoint `GET /rest/api/3/attachment/content/{id}` (supports
  `Range`) — same Basic/OAuth auth header as everything else in normal configurations
  ([Jira Cloud platform: issue attachments](https://developer.atlassian.com/cloud/jira/platform/rest/v3/api-group-issue-attachments/);
  [community: download attachment](https://community.developer.atlassian.com/t/download-attachment-from-rest-api/40860)).
  Atlassian's own support docs caveat that bulk REST attachment downloading "is not a supported
  use case" in some contexts — a footnote, not a blocker; the endpoint is documented and works
  ([support KB](https://support.atlassian.com/jira/kb/how-to-download-attachments-using-rest-api-and-sso/)).

**Base URL / auth.** `https://{site}.atlassian.net` for both `/rest/api/3/*` and
`/rest/servicedeskapi/*`. Basic auth with `base64(email:api_token)` (the fit for Sirdar's
env/keychain model) or OAuth 2.0 (3LO) for apps acting on a user's behalf
([intro](https://developer.atlassian.com/cloud/jira/platform/rest/v3/intro/),
[Basic auth](https://developer.atlassian.com/cloud/jira/platform/basic-auth-for-rest-apis/),
[OAuth 2.0 (3LO)](https://developer.atlassian.com/cloud/jira/software/oauth-2-3lo-apps/)).
Read-only JSM scopes: `read:servicedesk-request` (classic) or granular
`read:request:jira-service-management` + `read:user:jira`.

**Pagination.** Platform v3 search: `startAt`/`maxResults`. `servicedeskapi` list endpoints
(comments, attachments, participants): `start`/`limit`.

**Rate limits.** Atlassian moved Forge/Connect/OAuth (3LO) apps to a points-based quota
(effective March 2, 2026); plain API-token traffic — what Sirdar would use — stays on the older
burst-rate regime. 429s can carry `Retry-After` and `X-RateLimit-Reset`
([Rate limiting](https://developer.atlassian.com/cloud/jira/platform/rate-limiting/),
[Atlassian blog on the change](https://www.atlassian.com/blog/development/evolving-api-rate-limits)).

**MCP.** Confirmed official and first-party (distinct from the older community project): the
Atlassian Remote MCP Server at `https://mcp.atlassian.com/v1/mcp/authv2`, covering Jira,
Confluence, JSM, Bitbucket, Compass; OAuth 2.1 or API tokens; reported GA February 4, 2026
([GitHub](https://github.com/atlassian/atlassian-mcp-server),
[Atlassian blog](https://www.atlassian.com/blog/announcements/remote-mcp-server),
[product page](https://www.atlassian.com/platform/remote-mcp-server)). The community
`sooperset/mcp-atlassian` (~5,400 stars, self-hosted, also works against Server/Data Center
8.14+) predates it and remains the fallback for self-hosted instances.

**Go clients.** `andygrunwald/go-jira` — MIT, ~1.6k stars, actively released, mature for core
Jira issue/comment/attachment operations
([repo](https://github.com/andygrunwald/go-jira), [pkg.go.dev](https://pkg.go.dev/github.com/andygrunwald/go-jira)).
It's scoped to the core Jira REST API, not `servicedeskapi` — **not confirmed** that it wraps
JSM-specific endpoints, so a JSM adapter likely needs hand-rolled stdlib calls regardless. Given
Sirdar's stdlib-only constraint this is moot either way, but worth noting go-jira doesn't remove
the JSM-specific work.

---

## Help Scout

**Base URL / auth.** `https://api.helpscout.net/v2/`; token endpoint
`https://api.helpscout.net/v2/oauth2/token`
([Authentication](https://developer.helpscout.com/mailbox-api/overview/authentication/)). OAuth2
only — no API-key mode. Two flows: Client Credentials (server-to-server, no refresh token,
~2-day-lived access token re-minted on 401 — the fit for Sirdar's read-only model, stored as a
client id/secret pair) or Authorization Code (acting on behalf of a specific user). The
client-credentials app must be tied to an active invited user on the account.

**Three calls.**
- Ticket: `GET /v2/conversations/{id}` → `subject`, `status`
  (`active|open|pending|closed|spam`), `type` (`email|chat|phone`), `primaryCustomer` (id, name,
  email — the contact/account), `createdBy`, `createdAt`, a `threads` count (published,
  non-note threads), portal/app URL under `_links`
  ([Get Conversation](https://developer.helpscout.com/mailbox-api/endpoints/conversations/get/)).
  Threads aren't inlined by default — add `?embed=threads` or use the dedicated endpoint below. No
  explicit priority field confirmed — Help Scout leans on status + custom fields/tags instead;
  needs a live-tenant check.
- Thread: `GET /v2/conversations/{conversationId}/threads`, in order. Each has `id`, `type`,
  `status`, `state` (`published|draft|hidden|review|bounced`), `body`, `createdBy` (`type:
  "user"` or `"customer"`), `createdAt`, `_embedded.attachments`. `type` alone answers both of
  Sirdar's questions: `"customer"` = inbound customer message (public), `"message"` with
  `createdBy.type: "user"` = public staff reply, `"note"` = internal-only, `"lineitem"` = a
  state-change event with no body (maps to `system`); other values: `beaconchat`, `chat`,
  `forwardchild`, `forwardparent`, `phone`
  ([List Threads](https://developer.helpscout.com/mailbox-api/endpoints/conversations/threads/list/)).
  This is the cleanest single-field role+visibility model of any vendor surveyed here.
- Attachment: `GET /v2/conversations/{conversationId}/attachments/{attachmentId}/file` (raw
  binary, `Content-Disposition`) or `.../data` (JSON `{"data": "<base64>"}`), both
  `Authorization: Bearer {access_token}`. The exact doc-page slugs 404'd on direct fetch during
  this research (confirmed instead via the docs-portal navigation and the shape of the JSON
  example) — high-confidence but not screenshot-verified; re-check before finalizing.

**Pagination.** `?page=N`; most list endpoints default to 50/page, List Conversations defaults to
25/page. `_links` carries `self`/`next`/`previous`/`first`/`last`, plus a page object with
`size`/`totalElements`/`totalPages`/`number`.

**Rate limits.** The official page confirms the mechanism (read = 1 unit, write = 2 units toward
the per-minute budget; 429 on exceed; `X-RateLimit-Limit-Minute`,
`X-RateLimit-Remaining-Minute`, `X-RateLimit-Retry-After` headers) but not exact numeric tiers —
secondary sources cite roughly 200/min Standard, 400/min Plus, 800/min Pro, which is **not
independently verified from an official page**
([Rate limiting](https://developer.helpscout.com/mailbox-api/overview/rate-limiting/)).

**MCP.** No official Help Scout MCP server found — Help Scout's own developer navigation makes no
mention of one; only a third-party community project
(`drewburchfield/help-scout-mcp-server`) surfaced. Treated as verified-absent for this pass.

**Go clients.** None found — a GitHub search turned up nothing, and Help Scout's own SDK listing
covers PHP (and a Laravel wrapper) only. Stdlib `net/http` is the only real option here.

---

## Front

**Base URL / auth.** `https://api2.frontapp.com`. API tokens (Settings → Developers,
`Authorization: Bearer {token}`) — the simpler fit for a single-tenant read-only adapter — or
OAuth 2.0 for public/multi-tenant apps
([Authentication](https://dev.frontapp.com/docs/authentication),
[API tokens](https://dev.frontapp.com/docs/create-and-revoke-api-tokens)).

**Three calls.**
- Ticket: `GET /conversations/{id}` → `id`, `subject`, `status`, `status_id`/`status_category`,
  `assignee` (teammate object), `recipient` (contact handle + role), `tags`, `is_private`,
  `created_at`/`updated_at` (Unix), `ticket_ids`, `_links.related` pointers to `messages` and
  `comments` ([Conversations](https://dev.frontapp.com/reference/conversations)). No first-class
  priority field confirmed — Front models urgency via tags/custom fields; needs a live-tenant
  check.
- Thread: Front deliberately splits the thread into two resources rather than one feed with a
  flag. `GET /conversations/{id}/messages` — customer-facing, externally sent/received content:
  `id`, `type`, `is_inbound` (bool), `is_draft`, `created_at`, `author`, `recipients`,
  `body`/`text`/`blurb`, `attachments`. `GET /conversations/{id}/comments` — internal-only
  teammate notes ("discussions" in the UI), explicitly "never sent and cannot be shared outside
  of Front": `id`, `author` (teammate), `body`, `posted_at`, `is_pinned`
  ([Messages](https://dev.frontapp.com/reference/messages),
  [Comments](https://dev.frontapp.com/reference/comments)). Sirdar's adapter merges both, sorts
  by timestamp, and maps `messages` with `is_inbound: true` → customer/public, `is_inbound:
  false` → agent/public, all `comments` → agent/private. No documented `system` author — status-
  change/assignment events live in a separate `events` sub-resource, not either feed
  (**unverified** whether that's worth surfacing).
- Attachment: attachment objects carry a `url` (plus `metadata.cid` for inline images),
  authenticated the same way as any other API call — no separate pre-signed/unauthenticated
  scheme confirmed. Message-level file downloads also work via
  `https://api2.frontapp.com/download/{file_id}`. The dedicated attachments schema page 404'd on
  direct fetch — re-verify before finalizing.

**Pagination.** Cursor: `limit` (default 50, max 100), opaque `page_token`; response carries
`_pagination.next` as a ready-to-use full URL or `null` — trust `next`, don't infer end-of-list
from a short page.

**Rate limits.** Per-minute by plan: Starter 50, Professional 100, Enterprise 200; partner/OAuth
integrations get 120 rpm enforced per-company; burst allowance = half the plan rate, 10-minute
replenishment window; resource tiers layer on top (Tier 1: 1 req/s for analytics/exports; Tier 2:
5 req/s per resource for conversations/messages/channels; "message seen": 10 req/hour per
message). Headers: `x-ratelimit-limit`, `-remaining`, `-reset`, `-burst-limit`, `-burst-remaining`;
429s carry `retry-after` ([Rate limiting](https://dev.frontapp.com/docs/rate-limiting)).

**MCP.** Confirmed official, hosted: `https://mcp.frontapp.com/mcp` (streamable HTTP), OAuth 2.1
+ PKCE, per-user identity (no Dynamic Client Registration — OAuth app credentials must be
configured explicitly). Exposes `search_conversations`, `read_conversation`, `read_message`,
`get_attachment` directly relevant to Sirdar's three operations, plus write tools a read-only
adapter wouldn't call ([MCP Server docs](https://dev.frontapp.com/docs/mcp-server)). An earlier
MCP server version is deprecated — cite the current page.

**Go clients.** Only `spirosoik/go-front` (~8 stars, last updated 2018) — not viable as a
maintained dependency ([repo](https://github.com/spirosoik/go-front)). Stdlib `net/http` is the
right call.

---

## Gorgias

**Base URL / auth.** `{subdomain}.gorgias.com`
([OAuth2 docs](https://developers.gorgias.com/docs/oauth2-authentication-for-creating-apps-with-gorgias)).
Private/custom integrations use HTTP Basic auth — username = the account's login email, password
= the API key ([Download File reference](https://developers.gorgias.com/reference/download-file)).
Public apps must use OAuth2 (authorize at `/oauth/authorize`, token at `/oauth/token`; access
tokens expire in 24h, refresh tokens don't). For Sirdar's single-tenant read-only case, Basic auth
with a long-lived API key (env/keychain-sourced) fits directly.

**Three calls.**
- Ticket: `GET /api/tickets/{id}` ([reference](https://developers.gorgias.com/reference/get-ticket)),
  Basic auth, optional `relationships` expansion. Fields: `subject`, `status`, `channel`, `via`,
  `customer`, `assignee_user`, `created_datetime`, `updated_datetime`, `closed_datetime`, `uri`
  (an API URI, not a browser link — construct
  `https://{subdomain}.gorgias.com/app/ticket/{id}` for a human URL)
  ([Ticket object](https://developers.gorgias.com/reference/the-ticket-object)). **No `priority`
  field** on the Ticket object — priority lives in tags/custom fields, not as a first-class
  attribute, contrary to what a generic field checklist would assume.
- Thread: `GET /api/messages?ticket_id={id}` — not a ticket-scoped sub-path — with `limit`
  (default 30, max 100), `order_by` (default `created_datetime:desc`), `cursor`
  ([List Messages](https://developers.gorgias.com/reference/list-messages)). The TicketMessage
  object: `sender`/`receiver` (user-or-customer objects), `from_agent` (bool — this is the
  customer/agent field), `public` (bool — "whether the message was sent/received by a customer;
  internal notes are not public" — this is the public/private field), `via`/`channel`/`source`,
  `body_text`/`body_html`, `created_datetime`/`sent_datetime`, `attachments[]`
  ([TicketMessage object](https://developers.gorgias.com/reference/the-ticketmessage-object)).
  No distinct "system" sender type is documented — system events, if surfaced at all, would come
  from the Ticket object's `events` array, which the schema marks deprecated.
- Attachment: File objects carry only `content_type`, `name`, `size`, `url` — no separate id, the
  URL is the identifier. Download:
  `GET /api/{file_type}/download/{domain_hash}/{resource_name}`, Basic auth, 307 redirect to a
  signed time-limited URL — stdlib `net/http`'s default redirect-following handles this
  transparently ([Download File](https://developers.gorgias.com/reference/download-file)).

**Pagination.** Cursor: `cursor`, `limit` (default 30), `order_by`; envelope `data`,
`meta.next_cursor`, `meta.prev_cursor` ([Pagination](https://developers.gorgias.com/reference/pagination)).

**Rate limits.** Leaky-bucket: API-key integrations 40 req/20s (2 req/s), OAuth2 apps 80 req/20s
(4 req/s); Enterprise accounts get the same ratios in a 10s window. 429 with `Retry-After` and
`X-Gorgias-Account-Api-Call-Limit` (`current/limit`) headers
([Limitations](https://developers.gorgias.com/reference/limitations)).

**MCP.** No official Gorgias MCP server found in docs or a GitHub org search —
**unverified rather than confirmed absent**, since search tooling was constrained in this pass.

**Go clients.** None found. Stdlib `net/http` is the right call — small, well-shaped REST API.

---

## ServiceNow ITSM

Most of `developer.servicenow.com` and much of `docs.servicenow.com` returned 403s to automated
fetches in this pass; the Table API and Attachment API pages did load and are cited directly, the
rest leans on general, well-established ServiceNow platform knowledge that should be re-verified
against a live instance before being treated as settled.

**Ticket mapping.** A "ticket" = a record in the `incident` table (or `sc_task`/
`sn_customerservice_case` for other ITSM/CSM flows; `incident` is the default helpdesk analogue).

**Base URL / Table API.** `https://{instance}.service-now.com`. Table API:
`/api/now/table/{tableName}` (list, filterable) or `/api/now/table/{tableName}/{sys_id}` (single
record; 200 with the record, 404 if missing)
([Table API docs](https://www.servicenow.com/docs/access?topicname=c_TableAPI.html), fetched
live). Confirmed live this pass. For `incident`, expect `short_description`, `state` (status),
`priority`, `sys_created_on`/`sys_updated_on`, `caller_id` (contact), `company` (customer/
account), `sys_id` (build a human URL as
`https://{instance}.service-now.com/nav_to.do?uri=incident.do?sys_id={sys_id}`) — these specific
field names are drawn from general ServiceNow schema knowledge, **not confirmed against the
incident dictionary itself** in this pass.

**Thread — journal fields.** `comments` and `work_notes` on `incident` are journal-type fields;
their history is retrievable from the `sys_journal_field` table, e.g.
`GET /api/now/table/sys_journal_field?sysparm_query=element_id={sys_id}^element=comments` for
customer-visible comments and `element=work_notes` for internal-only notes, ordered by
`sys_created_on`. This is well-established, widely documented ServiceNow behavior, but **could
not be re-confirmed against a live official doc page in this pass** (repeated 404s, and no search
budget left to chase it further) — flag as the single most important claim in this section to
verify before an adapter ships, including whether a newer dedicated Journal/Comments API has
superseded this pattern.

**Attachments.** Confirmed live this pass
([Attachment API docs](https://www.servicenow.com/docs/access?topicname=c_AttachmentAPI.html)):
`GET /api/now/attachment` lists metadata (`sysparm_query`, e.g. `table_sys_id={sys_id}`, plus
`sysparm_limit`/`sysparm_offset`, default limit 1000) — filename, size, content-type, download
links. `GET /api/now/attachment/{sys_id}/file` streams the binary; metadata also comes back in an
`X-Attachment-Metadata` response header; `Accept` filters by content type. Auth is HTTP Basic in
the documented examples; requires read role on the target table plus attachment-read role.

**Auth.** The fetched Table API doc's examples use HTTP Basic; OAuth2 and API-key auth are
well-documented general ServiceNow REST capabilities but weren't spelled out on the page fetched
in this pass — **not directly confirmed from that page**.

**Pagination.** `sysparm_limit` (default 10,000), `sysparm_offset` (default 0); `Link` header
(`next`/`prev`/`first`/`last`) and `X-Total-Count`.

**Rate limits.** Not a fixed global number — ServiceNow rate limits are configured per-instance by
the customer's admin via REST API rate limit rules. A design doc should say "check the target
instance's configured limits," not cite a figure.

**MCP.** No official, product-grade ServiceNow MCP server confirmed. A search of the ServiceNow
GitHub org surfaced only small, mostly archived research/internal repos referencing MCP — not a
supported ITSM data-access server. Whether Now Assist / AI Agent Studio separately acts as an MCP
client is **unverified — not confirmed either way** in this pass, web search budget ran out
before it could be chased down.

**Go clients.** No mature official or widely-adopted Go SDK found; stdlib `net/http` against the
Table/Attachment REST APIs matches what community tooling also does.

---

## Salesforce Service Cloud

`developer.salesforce.com` returned 403s to every fetch attempted in this pass, and
`help.salesforce.com` served a JS shell with no extractable content. Everything below is general,
well-established Salesforce platform knowledge, **not confirmed against a live official page in
this research pass** — the highest-priority section to re-verify before an adapter is built.

**Ticket mapping / base URL.** The Case object is the ticket. Base URL is the org's instance URL,
typically `https://{instance}.my.salesforce.com` (My Domain) or the legacy
`https://{instance}.salesforce.com`.

**Ticket.** `GET /services/data/v{version}/sobjects/Case/{id}` → `Subject`, `Status`, `Priority`,
`Origin` (channel), `ContactId`/`AccountId`, `CreatedDate`/`LastModifiedDate`/`ClosedDate`. Human
URL is typically `https://{instance}.lightning.force.com/lightning/r/Case/{id}/view`.

**Thread.** No single unified conversation endpoint — build one by querying two object types via
SOQL against `/services/data/v{version}/query?q=...` and interleaving by timestamp:
`CaseComment` (internal-only by design, distinguished by `IsPublished` bool, plus `CommentBody`,
`CreatedById`, `CreatedDate`, `ParentId`) and `EmailMessage` (the customer-facing email thread,
`Incoming` bool distinguishing inbound customer email from outbound agent email, plus
`TextBody`/`HtmlBody`, `FromAddress`, `ToAddress`, `MessageDate`, `ParentId`). `CaseComment.IsPublished`
and `EmailMessage.Incoming` are the standard, widely-documented field names for this — but
**unverified against a live Object Reference page in this pass**; confirm before an adapter
depends on them.

**Attachments.** Two mechanisms: the legacy `Attachment` object (Salesforce is moving away from
it) and the current `ContentDocumentLink` → `ContentVersion` model (Salesforce Files), which new
development should target. Binary download is via `ContentVersion.VersionData` (a base64 field)
or `/sobjects/ContentVersion/{id}/VersionData`.

**Auth.** OAuth2 via connected apps. JWT bearer flow is the recommended non-interactive,
server-to-server pattern for a headless read-only adapter; the username-password OAuth flow has
been getting phased out across recent Salesforce releases — treat JWT bearer as the only durable
non-interactive option, though the exact deprecation release/date is **not confirmed** in this
pass.

**Pagination.** SOQL results page via `nextRecordsUrl` in the response body once results exceed
the default batch size (2,000 records/page).

**Rate limits.** Per-org rolling 24-hour API call limits that scale by edition/license
(materially higher for Enterprise/Unlimited than Professional) — exact current numbers **not
confirmed** in this pass; these shift by release and edition, so treat as "check the org's
current limit," not a fixed figure.

**MCP.** Confirmed via GitHub: `salesforcecli/mcp`, Apache 2.0, 60+ tools across 14 toolsets
(SOQL queries, metadata deploy/retrieve, Apex, LWC, DevOps Center)
([repo](https://github.com/salesforcecli/mcp)). It's a CLI-adjacent org-development/admin tool,
not purpose-built as a read-only case/thread reader — but since it exposes arbitrary SOQL it
could technically answer Sirdar's three operations. Worth a mention as an alternative integration
path, distinct from a bespoke adapter.

**Go clients.** No mature official Go SDK; community libraries are thin REST wrappers. The main
implementation cost for a stdlib adapter is SOQL query construction (string-building with correct
escaping for the `q=` parameter), not the HTTP mechanics.

---

## Zoho Desk (already implemented)

`internal/source/zohodesk` is the one adapter that exists today: API v1
(`{BaseURL}/api/v1/tickets/{id}` for the ticket, `orgId` header plus
`Authorization: Zoho-oauthtoken {token}` for auth, an OAuth refresh-token flow sitting outside
the adapter itself to keep the token fresh). Three quirks worth calling out for anyone building
the next adapter against this one as a template, all confirmed directly from the working code
(`internal/source/zohodesk/client.go`, `attachments.go`) rather than the vendor docs: (1) the
conversation feed is two-tier — `GET /api/v1/tickets/{id}/conversations` lists entries typed
`"thread"` or `"comment"`, and a `"thread"` entry only carries a summary until you follow up with
`GET /api/v1/tickets/{id}/threads/{threadId}?include=plainText`, which is the only way to get a
clean text body instead of raw HTML (falling back to `summary` then `content` if `plainText` is
empty); (2) inline attachments aren't in any `attachments[]` array at all — they're `<img
src="...inlineattachments...">` tags buried in a thread or comment's HTML `content`, which the
adapter has to regex out and resolve into synthetic `inline-N.png` attachment references
alongside the real `attachments[]` entries; (3) role/visibility aren't uniform across entry
types — a `"thread"` entry's customer/agent split comes from `direction` (`"in"` = customer), a
`"comment"` entry's comes from `commenterType` (`CONTACT`/`END_USER` = customer), and a
comment's public/private split comes from `isPublic` (absent/true = public), so there's no single
field name that works across the whole feed the way Help Scout's or Gorgias's does. IM-session
(chat) transcripts were expected to need separate handling but turned out to surface through the
same `inlineattachments`-style URL pattern as other inline images, so no separate code path was
needed for them in practice — flagged as based on the implemented behavior, not a vendor doc
statement, since Zoho's own API reference page (`desk.zoho.com/DeskAPIDocument`) renders as an
SPA that this pass's fetch tooling couldn't extract text from.

---

## Ranking: what to build next

By rough SMB/mid-market support-team share and how much of the "three calls" surface is already
clean vs. needs derivation/cross-referencing:

1. **Zendesk Support** — the largest SMB/mid-market installed base of the group and the most
   thoroughly documented API surveyed here. The only real friction is that customer/agent role
   isn't inline on a comment (needs a `users/{id}` lookup), which is a one-line cache, not a
   design problem.
2. **Freshdesk** — comparable share to Zendesk in the price-sensitive SMB segment, and the
   simplest auth of any vendor here (API key as a Basic-auth username, no OAuth at all). The
   `private` boolean is inline on every conversation; only the attachment-URL auth requirement is
   genuinely unverified and worth a five-minute check against a trial account before shipping.
3. **Intercom** — strong share among PLG/SaaS support teams, and a clean single `part_type`
   split for public/private. The 500-part cap and the separate Ticket/Conversation split add real
   but manageable complexity.
4. **Help Scout** — smaller install base than the above three, but the cleanest data model
   surveyed: one `type` enum on the thread endpoint answers both the role and the visibility
   question at once, and OAuth2 client-credentials is a good match for Sirdar's static-credential
   model. High value-per-hour-of-implementation, worth doing early even though its market share
   doesn't justify first place on its own.
5. **HubSpot Service Hub** — large SMB reach through the CRM bundle, but the ticket→conversation-
   thread association is the least officially documented mechanism in this survey (one community
   thread, no primary reference page). Confirm that association call against a live portal before
   committing to it.
6. **Front** — solid small-team share; the messages/comments split is a minor extra merge step,
   not a real obstacle.
7. **Gorgias** — narrow but deep share specifically in e-commerce/Shopify-adjacent support teams;
   worth it if that's a segment Sirdar wants to reach, otherwise lower priority given the smaller
   overall addressable base.
8. **Jira Service Management** — high overlap with the tracker adapter Sirdar already has lowers
   the marginal cost, but the JSM-specific pieces (public/private comments, request metadata) are
   still a distinct implementation, and JSM's addressable base skews toward teams that already
   have a Jira tracker adapter anyway.
9. **Salesforce Service Cloud** — huge overall CRM footprint but skews enterprise, and is the
   most expensive to build of the group: SOQL query construction, JWT-bearer OAuth, and a
   two-object (CaseComment + EmailMessage) thread merge, on top of being the least-verified
   section in this survey.
10. **ServiceNow ITSM** — enterprise IT-ops niche, the least aligned with an SMB/mid-market
    support target. The journal-field comment model and per-instance-configurable rate limits are
    the most bespoke of any vendor here.

## Proposed common config shape

Extending the existing `sources.helpdesk` block in `docs/config.md` (which today only knows
`adapter: exec | zohodesk`) with one `adapter` value per built-in vendor, a shared `auth` object,
and vendor-specific extras kept to the minimum each API actually requires:

```yaml
sources:
  helpdesk:
    adapter: zendesk        # zendesk | freshdesk | freshservice | intercom | hubspot |
                             # jsm | helpscout | front | gorgias | servicenow | salesforce |
                             # zohodesk | exec
    baseUrl: https://acme.zendesk.com   # or {subdomain}/{instance}/{domain} where a vendor
                                         # config would rather take a bare name than a full URL
    auth:
      type: basic            # basic | bearer | oauth2_client_credentials | oauth2_jwt_bearer
      username: env:ZENDESK_EMAIL       # present only for basic
      token: env:ZENDESK_API_TOKEN      # credential ref, per docs/config.md's env:/keychain: rule
```

Per-vendor deltas on top of that shared shape, following the pattern the Zoho Desk adapter
already sets with `orgId`:

| adapter | extra fields | auth.type |
|---|---|---|
| `zendesk` | — | `basic` (`{email}/token` as username, API token as password) |
| `freshdesk` / `freshservice` | — | `basic` (API key as username, literal `X` as password) |
| `intercom` | `region: us\|eu\|au` (selects the host) | `bearer` |
| `hubspot` | — | `bearer` (private-app token) |
| `jsm` | — | `basic` (email + API token) |
| `helpscout` | `clientId`, `clientSecret` (both credential refs) | `oauth2_client_credentials` |
| `front` | — | `bearer` (API token) |
| `gorgias` | — | `basic` (login email as username, API key as password) |
| `servicenow` | `instance` | `basic` (or `bearer` once OAuth2 support is confirmed) |
| `salesforce` | `instance`, `clientId`, `privateKey: keychain:...` | `oauth2_jwt_bearer` |
| `zohodesk` | `orgId` | `bearer` (`Zoho-oauthtoken` scheme; refresh sits outside the adapter) |

Every field carrying a secret stays a credential reference (`env:NAME` or `keychain:SERVICE`),
never a literal, per the existing rule in `docs/config.md`.

## Open questions

- **HubSpot's ticket→conversation-thread association** is corroborated only by a community
  forum post in this pass, not a primary HubSpot reference page. Confirm the exact association
  type/endpoint against a live portal before building the adapter.
- **ServiceNow's `sys_journal_field` comments/work_notes query pattern** is standard, widely
  known ServiceNow behavior but couldn't be re-verified against a live official doc page here
  (403s, then exhausted search budget). Also check whether a newer dedicated Journal/Comments API
  has superseded it.
- **The entire Salesforce section** needs a follow-up pass with working access to
  `developer.salesforce.com` (this session's fetches were all blocked). `CaseComment.IsPublished`,
  `EmailMessage.Incoming`, the JWT-bearer-only auth guidance, and the rate-limit figures are all
  standard Salesforce platform knowledge but none were confirmed live here.
- **Attachment-URL auth requirements are unconfirmed for three vendors**: Freshdesk/Freshservice
  (`attachment_url` — Basic-auth-protected or pre-signed?), HubSpot (`fileId`-derived URL —
  Bearer-protected or pre-signed?), Front (message-level `url` — same question). Each is a
  five-minute check against a trial/sandbox account and should happen before the corresponding
  adapter ships, not be assumed from the pattern of other vendors.
- **MCP servers are moving targets and not adapter-relevant either way.** Zendesk (announced May
  2026, client early access from June), HubSpot (beta since May 2025, a claimed April 2026 GA
  unconfirmed), Freshdesk/Freshservice (separate EAP programs), Intercom, Front, and Atlassian all
  have official or semi-official MCP offerings; Help Scout, Gorgias, and ServiceNow do not appear
  to (though the ServiceNow negative is weakly verified). None of this changes the adapter design
  — Sirdar's adapters are hand-rolled stdlib Go clients, not MCP clients — but it's worth
  revisiting if Sirdar ever wants an MCP-based integration path as an alternative to writing
  adapters.
- **Several vendors' ticket objects don't carry every field the harness wants.** Gorgias and
  Intercom have no first-class `priority` field on the ticket/ticket resource itself (Gorgias:
  tags/custom fields; Intercom: workspace-defined `ticket_attributes`), and several vendors
  return no ticket URL at all (HubSpot, Front, Gorgias — client-constructed instead). The
  `ticket.HelpdeskTicket.Fields` map the Zoho Desk adapter already uses for `departmentId`/
  `ticketNumber`/etc. is the existing escape hatch for this; each new adapter will need its own
  judgment call about what goes in the typed fields vs. `Fields`.
- **Rate limits are plan-tier dependent for almost every vendor surveyed** (Zendesk, Freshdesk,
  Freshservice, HubSpot, Front, Gorgias) and instance-configurable for ServiceNow. An adapter
  should read `Retry-After`/`X-RateLimit-*` response headers and back off accordingly rather than
  hardcoding a number from this document, which will drift.
- **Intercom's attachment-URL TTL (~30 minutes)** comes from a community post, not the formal
  API reference — worth confirming directly (fetch, then immediately re-fetch after 35 minutes
  against a real attachment) before an adapter relies on "download promptly" as its only
  mitigation.
