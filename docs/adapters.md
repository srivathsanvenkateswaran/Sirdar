# Adapters

Sirdar reads tickets from a tracker (e.g. Jira) and a helpdesk (e.g. Zoho
Desk) through *adapters*. Four trackers and three helpdesks ship built into
the Sirdar binary — Jira, Linear, Azure DevOps, and Rally for trackers; Zoho
Desk, Zendesk, Freshdesk, Help Scout, Intercom, and HubSpot Service Hub for
helpdesks — configured directly in
`.sirdar/config.yaml`, no separate process required. Anything else talks to
Sirdar through the external adapter protocol described below: a small
line-delimited JSON protocol over stdin/stdout, which also stays available
for the built-in adapters if you'd rather run your own integration against
them.

## Built-in tracker adapters

Each built-in adapter implements the `tracker` role; some also implement
`helpdesk` where the tracker itself carries (or can be made to carry) the
customer conversation. Credentials are never literal values in config: use
`env:NAME` or `keychain:SERVICE` references, same as the built-in helpdesk
adapters below. See `docs/config.md` for the full key reference.

### Jira

```yaml
sources:
  tracker:
    adapter: jira
    baseUrl: https://acme.atlassian.net
    deployment: cloud          # cloud | datacenter | auto (default: probes /rest/api/2/serverInfo)
    email: env:JIRA_EMAIL
    apiToken: env:JIRA_API_TOKEN
    projectKey: OMNI           # optional, scopes List
    epicLinkField: ""          # optional, Data Center only
```

Data Center uses a personal access token instead of email/token:

```yaml
sources:
  tracker:
    adapter: jira
    baseUrl: https://jira.internal.acme.com
    deployment: datacenter
    pat: env:JIRA_PAT
```

**Getting credentials.** Cloud: create an API token at id.atlassian.com
(Settings → Security → API tokens) and use it with your account email over
Basic auth. Data Center (8.14+): create a Personal Access Token under your
profile's Personal Access Tokens page; it inherits your own permissions, so
create it under a read-only or restricted account if you don't want the
adapter to see everything you can. No specific scope picker exists for
either kind — the token/PAT carries whatever the issuing account can already
read, so use an account whose permissions match what the adapter should see.

**Get / List.** `Get` fetches `/rest/api/2/issue/{key}` (v2 on both Cloud and
Data Center, so descriptions come back as wiki markup rather than Atlassian
Document Format JSON). `List` builds a JQL query from the filter
(`assignee`, `status`, `parent`) and runs it against `/rest/api/2/search`.

**Thread and attachments.** An issue is its own helpdesk ticket when its
project's `projectTypeKey` is `service_desk` (Jira Service Management):
`HelpdeskRef` is set to the same issue key, and the helpdesk view reads
`/rest/servicedeskapi/request/{key}` and its comments, mapping a comment's
`jsdPublic` flag to `customer` (public) vs `agent` (internal) role.
Attachments are downloaded from each attachment's `content` URL with the
same auth header; inline wiki-markup references (`!screenshot.png!`,
`[^report.pdf]`) in a comment body are matched back to attachment IDs so a
comment's `AttachmentIDs` reflects what it actually references.

**Known limitations.**
- On Jira Data Center, a Personal Access Token can be rejected on the
  attachment-download URL specifically (it's served by the web layer, not
  the REST layer) and the request redirected to an SSO login page instead of
  the file. The adapter refuses to follow a redirect off the Jira host and
  refuses to write an HTML response to disk; a failure like this is reported
  as a warning on the download, not a fatal error, but it means some
  attachments may be missing from the bundle on an SSO-fronted DC instance.
- `HelpdeskRef` is only ever the issue's own key (JSM case). No fallback to
  a custom field or description text is implemented in the adapter itself —
  see "helpdeskRef fallback" below.

### Linear

```yaml
sources:
  tracker:
    adapter: linear
    apiKey: env:LINEAR_API_KEY
    teamKey: ENG                # optional, scopes List to one team
```

**Getting credentials.** Create a personal API key under Linear's Settings →
Security & access → Personal API keys. It's unscoped — it carries whatever
the creating account can already see in the workspace — so use an account
with read access only to what the adapter should reach. Sent as the raw key
value in the `Authorization` header (no `Bearer` prefix).

**Get / List.** `Get` resolves an identifier like `ENG-123` directly through
Linear's GraphQL `issue(id:)` field. `List` runs the `issues(filter:)`
connection, translating `assignee`, `status`, and `parent` into an
`IssueFilter`, paginating with `first`/`after` beyond a single page.

**Thread and attachments.** Linear has no first-class customer/agent
distinction on a comment — every comment is authored by a workspace member
— so the helpdesk view's thread is just the issue's own comments, all
`agent` role, useful when a team runs support conversations directly inside
Linear rather than through a separate helpdesk. Attachments downloads cover
files Linear itself hosts: entries in `issue.attachments` pointing at
`uploads.linear.app`, plus images embedded in the description and in each
comment's Markdown body. Attachments pointing at another system (a GitHub
PR, a Zendesk ticket) are links, not files, and are not downloaded.

**Known limitations.**
- Linear's API does not expose the full body of a linked external
  helpdesk conversation (Zendesk, Intercom, Front). When an issue was
  created from a support conversation via Linear's Customer Requests
  feature, the adapter can only recover a link to that conversation (see
  `HelpdeskRef` below) — the transcript itself has to come from that
  helpdesk's own adapter, not from Linear.
- `HelpdeskRef` is derived from `issue.attachments`: the first entry whose
  `sourceType` is `zendesk`, `intercom`, or `front`, or — when Linear hasn't
  classified the attachment — the first attachment URL whose host looks like
  a known helpdesk (Zendesk, Intercom, Front, Zoho Desk, Freshdesk, Help
  Scout, Helpshift). If none match, `HelpdeskRef` is left empty.

### Azure DevOps

```yaml
sources:
  tracker:
    adapter: azdo
    orgUrl: https://dev.azure.com/acme   # or a Server collection URL
    project: OmniPlatform
    pat: env:AZDO_PAT
    helpdeskLinkDomain: acme.service-now.com   # optional
    helpdeskField: ""                          # optional, custom field fallback
```

**Getting credentials.** Create a Personal Access Token under User settings
→ Personal access tokens, with the **Work Items (Read)** scope
(`vso.work`) — this is the minimum needed for `Get`, `List`, comments, and
attachment reads. Sent as HTTP Basic auth with an empty username:
`Authorization: Basic base64(":" + PAT)`.

**Get / List.** `Get` fetches
`/_apis/wit/workitems/{id}?$expand=all&api-version=7.1` — fields, relations,
and links in one call. `List` runs a two-step WIQL query: a `POST
.../wiql` returning matching work item ids, then `POST
.../workitemsbatch` in chunks of up to 200 to pull field values (Azure
DevOps' own per-call ceiling), with `errorPolicy: Omit` so one inaccessible
id doesn't fail the whole batch. A Bug's description prefers `System.State`
Description text but falls back to `Microsoft.VSTS.TCM.ReproSteps` (Bugs use
ReproSteps in place of Description on their default form).

**Thread and attachments.** Comments come from
`/_apis/wit/workItems/{id}/comments?$expand=renderedText`, following
`continuationToken` across pages; Azure DevOps has no customer/agent
distinction on a comment, so every message maps to `agent` role. Attachments
are `AttachedFile` relations on the work item, downloaded from
`/_apis/wit/attachments/{guid}?fileName=...&download=true` with the same PAT.

**Known limitations.**
- Azure DevOps' API does not associate an attachment with the specific
  comment it was added alongside — attachments are work-item-level, not
  comment-level. The adapter exposes all of a work item's attachments as a
  flat list; it cannot tell you which comment (if any) a given attachment
  belongs to.
- `HelpdeskRef` is derived two ways, in order: a `Hyperlink` relation whose
  host ends in `helpdeskLinkDomain` (when configured), else the value of the
  `helpdeskField` custom field (when configured). Leave both unset and
  `HelpdeskRef` stays empty.

### Rally

```yaml
sources:
  tracker:
    adapter: rally
    baseUrl: https://rally1.rallydev.com   # default; override for a regional subscription
    apiKey: env:RALLY_API_KEY
    workspace: /workspace/12345678901
    project: /project/12345678902          # optional
    types: [Defect, HierarchicalRequirement]
    helpdeskField: c_ZendeskTicketID        # optional
```

**Getting credentials.** Create an API key under My Settings → Access → API
Keys, with grant type **ALM WSAPI Read-only** — Rally's own read-only grant,
the right fit for a harness that never writes back. Sent as the
`ZSESSIONID` header on every WSAPI request. API keys aren't supported on
`sandbox.rallydev.com` or on-premises Rally; those still need basic auth,
which this adapter does not implement.

**Get / List.** Rally has no single "artifact" endpoint — Defect, Story
(`HierarchicalRequirement`), Task, and the rest are separate WSAPI
collections. `Get` tries FormattedID-prefix heuristics first (`DE` →
Defect, `US`/`S` → HierarchicalRequirement, and so on), then falls through
the configured `types` list in order, querying each collection by
`FormattedID` until one hits. `List` sweeps the same configured `types` and
merges results, honoring `workspace`/`project` scoping and Rally's 200-row
page cap.

**Thread and attachments.** The thread comes from the artifact's
`Discussion` (`ConversationPost` objects); Rally does not distinguish
customer from agent authorship, so every message maps to `agent` role, same
as Azure DevOps. Attachments are fetched via the artifact's `Attachments`
collection, following each entry's `Content` reference to an
`AttachmentContent` object and base64-decoding its `Content` field.

**Known limitations.**
- Trying multiple artifact types in order to resolve one FormattedID means
  `Get` on a key Rally doesn't have costs one WSAPI query per candidate type
  until one matches (or all fail) — keep `types` short and ordered by how
  common each type is in your workspace to minimize this.
- `HelpdeskRef` comes only from the configured `helpdeskField` custom field
  (typically `c_`-prefixed). Rally has no native helpdesk linkage — every
  Zendesk/ServiceNow connector on the market writes to a custom field or a
  free-text location of its own choosing, so there's no field name the
  adapter can assume without configuration.
- Rally throttles a user to 12 concurrent requests; sustained excess slows
  responses rather than returning a clean error, so a large `List` sweep
  across several `types` can feel slow under load rather than failing
  outright.

## Built-in helpdesk adapters

Zoho Desk, Zendesk, Freshdesk, Help Scout, Intercom and HubSpot Service Hub
implement the `helpdesk` role only — `Get`, `Threads`, `Attachments`, no
`List` — and are compiled into Sirdar the same way the four trackers above
are, no separate process required.
Credentials are `env:NAME` or `keychain:SERVICE` references, same as
everywhere else in Sirdar; see `docs/config.md` for the full key reference.

### Zoho Desk

```yaml
sources:
  helpdesk:
    adapter: zohodesk
    orgId: "60044805777"
    baseUrl: https://desk.zoho.com
    auth:
      clientId: keychain:zoho-desk-client-id
      clientSecret: keychain:zoho-desk-client-secret
      refreshToken: keychain:zoho-desk-refresh-token
```

See `docs/config.md`'s "Zoho Desk OAuth" section for the refresh-token
grant, the `token:` alternative for a run you're watching rather than
scheduling unattended, and the per-data-centre `baseUrl`/`accountsUrl`
table.

**Getting credentials.** Register a Self Client at Zoho's [API
console](https://api-console.zoho.com/) (or the regional equivalent for
`.in`/`.eu`/`.com.au`), grant it read scopes covering tickets, conversations
and attachments, and exchange the resulting grant token once for the
refresh token `auth:` uses — Sirdar mints its own access tokens from there,
retrying once with a freshly minted one whenever Desk answers 401.

**Get.** Fetches `/api/v1/tickets/{id}`; `Fields` carries `departmentId`,
`ticketNumber`, `email` and `phone` when Desk returns them.

**Threads and attachments.** `Threads` pages `/api/v1/tickets/{id}/conversations`
and resolves each `thread`-type entry's detail (`plainText`, falling back to
`summary` then `content`), while `comment`-type entries map directly; role
comes from the entry's `direction` (thread: `in` is customer) or
`commenterType` (comment: `CONTACT`/`END_USER` is customer), and a private
comment's author gets the same ` (internal)` suffix the other adapters use.
`Attachments` downloads both listed attachments and inline `<img>`
references matched out of the HTML content.

**Known limitations.**
- Attachment `href`/`src` values are resolved against `baseUrl` when
  relative, but an absolute `http(s)://` one is downloaded as-is with no
  host check — unlike Zendesk and Freshdesk below, this adapter trusts
  whatever host a Desk API response names.
- `doctor`'s probe looks up ticket id `0`, which cannot exist: the 404 Desk
  returns is treated as proof the token and the transport both work, since
  there's no cheaper authenticated endpoint to call.

### Zendesk

```yaml
sources:
  helpdesk:
    adapter: zendesk
    subdomain: acme                        # acme.zendesk.com
    email: env:ZENDESK_EMAIL
    apiToken: env:ZENDESK_API_TOKEN
```

An OAuth bearer token works instead of basic auth — set `oauthToken` and
drop `email`/`apiToken`, never both:

```yaml
sources:
  helpdesk:
    adapter: zendesk
    subdomain: acme
    oauthToken: env:ZENDESK_OAUTH_TOKEN
```

**Getting credentials.** Basic auth: enable token access under Admin
Center → Apps and integrations → APIs → Zendesk API, then generate an API
token from an agent or admin account; it's sent as `{email}/token` with the
token as the password, never the account's own login password. OAuth:
register an app under the same API settings and mint a bearer token through
its own flow; either way the token carries whatever the issuing account can
already see, so use one scoped to what the adapter should read.

**Get.** Fetches `/api/v2/tickets/{id}.json` with `users` and
`organizations` side-loaded; `URL` is built from the configured subdomain,
not from anything the API returns.

**Threads and attachments.** `Threads` fetches the ticket's `requester_id`
then pages `/api/v2/tickets/{id}/comments.json`; role is customer when a
comment's author is the requester or the side-loaded author's Zendesk role
is `end-user`, else agent, and a non-public comment's author gets an
` (internal)` suffix. Text prefers `plain_body`, falling back to converting
`html_body` to Markdown. `Attachments` downloads both `attachments[]`
entries and inline `<img>` references out of `html_body`; a host outside
the configured Zendesk instance, `*.zendesk.com`, or `*.zdusercontent.com`
(Zendesk's attachment CDN) is refused, and the client's Authorization
header is only ever sent to the configured instance itself — the CDN hosts
serve pre-signed URLs that need no credential and shouldn't get one.

**Known limitations.**
- `Threads` makes two calls per ticket (the ticket itself, for
  `requester_id`, then the comments page) rather than one — Zendesk's API
  doesn't side-load the requester onto the comments endpoint.
- A 429 is retried once after honouring `Retry-After` up to 30 seconds;
  longer or missing values are reported as rate-limited rather than waited
  out, so a very throttled account does not hang a run.

### Freshdesk

```yaml
sources:
  helpdesk:
    adapter: freshdesk
    domain: acme.freshdesk.com
    apiKey: env:FRESHDESK_API_KEY
```

**Getting credentials.** Copy the account API key from the agent's Profile
Settings page in the Freshdesk UI. It's sent as HTTP Basic auth — the key
as the username, the literal string `X` as the password — Freshdesk's only
documented auth mode; there's no separate OAuth path and no scope picker,
so the key carries whatever the owning agent account can already see.

**Get.** Fetches `/api/v2/tickets/{id}` with `requester`, `company` and
`stats` side-loaded; numeric `status`/`priority`/`source` fields are mapped
to names, with an undocumented value falling back to `status-<n>` /
`priority-<n>` / `source-<n>` rather than going blank.

**Threads and attachments.** Freshdesk has no separate "first message"
endpoint, so `Threads` synthesises one from the ticket's own `description`
(role: customer) and appends the ticket's `conversations` (paginated,
`incoming` marks customer vs. agent, `private` marks an internal note with
an ` (internal)` author suffix); an agent id on a message is resolved to a
display name through a per-client cache, since the same agent typically
appears on several messages. `Attachments` downloads both the ticket-level
and per-conversation `attachments[]` entries; the API key is sent only to
the configured account domain, with Freshdesk's other first-party hosts
(its attachment CDN) trusted to download from but never given the key,
since those URLs are pre-signed.

**Known limitations.**
- `Threads`' pagination is capped at 100 pages of conversations; a ticket
  past that (a genuinely pathological thread) is truncated with a run
  warning rather than swept indefinitely.
- Freshdesk's numeric `source` (channel) enum is only reliably documented
  for a handful of values (email, portal, phone, chat, feedback widget,
  outbound email); anything else maps to `source-<n>` instead of a guessed
  name, same treatment as an undocumented `status`.
- Attachment MIME comes from the API's declared `content_type`; downloads
  don't sniff the response's own `Content-Type`.

### Help Scout

```yaml
sources:
  helpdesk:
    adapter: helpscout
    clientId: keychain:helpscout-client-id
    clientSecret: keychain:helpscout-client-secret
```

**Getting credentials.** Help Scout's Mailbox API 2.0 is OAuth2 only — there
is no API-key mode. Create an app under Your Profile → My Apps with the
Client Credentials flow, tied to an active invited user on the account, and
keep the client id/secret pair. Sirdar mints its own access tokens against
`https://api.helpscout.net/v2/oauth2/token`, caches each until it expires,
and mints again when a call comes back 401 anyway — a token revoked at the
Help Scout console fails long before the expiry it was issued with.

**Get.** Fetches `GET /v2/conversations/{id}?embed=threads`; `Fields`
carries `mailboxId`, `tags`, `number`, `state`, `assignee` and
`customerEmail`. `Priority` is left empty: Help Scout has no priority field,
leaning on status and tags instead, and a guess made from a tag would read
like data the API gave. The URL is built from the id the call was made with,
`https://secure.helpscout.net/conversation/{id}`.

**Threads and attachments.** Help Scout's thread `type` answers both of
Sirdar's questions on its own, which no other vendor's model does:
`customer` is the person who wrote in, `message`/`reply` a published staff
reply, `note` a staff-only note (author suffixed ` (internal)`), `lineitem`
a state change with no body (role `system`). Anything else — `chat`,
`beaconchat`, `phone`, `forwardchild`, `forwardparent` — falls back to
`createdBy.type`. Bodies are HTML and go through `htmltext`. Draft threads
are left out: an unsent reply is not part of the conversation that happened.
The single-conversation `?embed=threads` response carries no pagination link
of its own, so a full page (the same size Help Scout uses for the dedicated
thread-list endpoint) is the signal that more threads might exist; the rest
is fetched from `GET /v2/conversations/{id}/threads?page=N`, following that
endpoint's own `_links.next` and `page.totalPages`, each page checked against
`api.helpscout.net` before it is fetched.
`Attachments` reads each thread's `_embedded.attachments` and fetches the
bytes from `/v2/conversations/{id}/attachments/{attachmentId}/data`, which
returns them base64-encoded inside JSON.

**Known limitations.**
- Everything, attachment bytes included, comes from `api.helpscout.net`, so
  this adapter has no fetch-only CDN tier: the one trusted host is the one
  that gets the credential, and any other host named by a response — an
  attachment's `_links.data.href`, a `next` page link — is refused with a
  warning.
- Thread pagination is capped at 100 pages; a conversation past that is
  truncated with a run warning rather than swept indefinitely.
- Help Scout's rate-limit tiers are not published as numbers; a 429 is
  retried once after honouring `Retry-After` (or `X-RateLimit-Retry-After`)
  up to 30 seconds, and reported as rate-limited otherwise.

### Intercom

```yaml
sources:
  helpdesk:
    adapter: intercom
    accessToken: env:INTERCOM_ACCESS_TOKEN
```

**Getting credentials.** Mint a workspace access token in Intercom's
Developer Hub (Your apps → Authentication). It carries whatever scopes the
app was granted, so scope it to reading conversations and contacts. Requests
are sent to `https://api.intercom.io` with `Intercom-Version: 2.11` pinned,
so a workspace that moves its default version does not silently reshape the
payloads this adapter decodes.

**Get.** Fetches `GET /conversations/{id}?display_as=plaintext`. `Subject`
is the source message's `subject` where there is one (an email-originated
conversation), then the conversation `title`, then the first line of the
opening message — which is what the Intercom inbox itself shows for a chat.
`Status` is `state`, `Priority` is `priority`. The contact is resolved
through `GET /contacts/{id}` (a conversation carries only ids) with a
per-client cache, and the contact's first company becomes `Customer`. The
web URL needs the workspace's app id, which only `GET /me` carries; it is
read once and cached, and a workspace that will not give one leaves the URL
empty with a warning rather than a wrong link.

**Threads and attachments.** The `source` message comes first, then each
entry in `conversation_parts`. Roles come from `author.type`: `user` and
`lead` are the customer, `admin` and `team` are staff, `bot` is `system`; a
part whose `part_type` is `note` is admin-to-admin and gets the
` (internal)` author suffix. The source message and every part are held to
the same filter: neither text nor an attachment means it is left out rather
than filling the thread with an empty entry — Intercom emits a body-less
part for every assignment, close and reopen, and a source message can
likewise carry an empty `body`. `Attachments` downloads
the source message's and each part's `attachments[]`; the access token goes
only to `api.intercom.io`, while `*.intercom.io`, `*.intercomcdn.com`,
`*.intercomassets.com` and the numbered `intercom-attachments-N.com` family
are trusted to download from and never given the credential, since those
URLs are pre-signed.

**Known limitations.**
- Only the US host is supported. An EU or AU data-residency workspace would
  need `api.eu.intercom.io`/`api.au.intercom.io`; calls to the US host are
  proxied by Intercom, which works but is not what Intercom recommends.
- Conversation parts are whatever the single `GET /conversations/{id}` call
  returns — Intercom caps that at 500 and offers no parts-pagination
  endpoint. When the payload's `total_count` exceeds what it sent, the run
  gets a warning naming both numbers rather than a thread that quietly
  stops.
- Attachment URLs are pre-signed and short-lived (roughly half an hour per a
  community thread, not Intercom's own reference), so a bundle assembled
  long after the conversation was fetched can find them expired; the failure
  is per file, not fatal.

### HubSpot Service Hub

```yaml
sources:
  helpdesk:
    adapter: hubspot
    accessToken: env:HUBSPOT_PRIVATE_APP_TOKEN
```

**Getting credentials.** Create a private app under Settings → Integrations
→ Private Apps and copy its access token (`pat-na1-…`), shown only once. It
needs read scopes for tickets, contacts, companies, conversations and files.
HubSpot retired API keys in November 2022, so this is the only static
credential left.

**Get.** Fetches `GET /crm/v3/objects/tickets/{id}` naming the properties it
wants (`subject`, `content`, `hs_pipeline_stage`, `hs_ticket_priority`,
`createdate`, `hs_lastmodifieddate`, `hubspot_owner_id`) and the
associations it needs (`contacts`, `companies`, `conversations`) — HubSpot
returns only what a request names. `Status` is the pipeline stage id, which
is what the API gives; the stage's label lives on the pipeline object and is
not fetched. The contact and company names are resolved through their own
object endpoints, cached per client. The URL is
`https://app.hubspot.com/contacts/{portalId}/ticket/{id}`, with the portal
id read once from `GET /account-info/v3/details`.

**Threads and attachments.** A HubSpot ticket does not carry its
conversation inline: the ticket's own `content` becomes the first message,
attributed to the customer only when the ticket has a contact association
to name — a ticket created without one is staff content, not a customer's
words, so it maps to `agent` instead. Everything after it comes
from the associated Conversations-inbox thread, through
`GET /conversations/v3/conversations/threads/{threadId}/messages` (paged by
`paging.next.after`, capped at 100 pages). A message's `type` is the
public/private split — `MESSAGE` is customer-facing, `COMMENT` is the
internal note that never reaches the visitor and gets the ` (internal)`
suffix — and the role comes from the sender's `actorId` prefix, since
HubSpot has no author-type field: `V-` visitor and `E-` email address are
the customer, `A-` is an agent, `S-`/`I-` are system and integration.
`Attachments` resolves each message attachment's `fileId` through
`GET /files/v3/files/{fileId}/signed-url` and downloads from the URL that
returns; the access token goes only to `api.hubapi.com`, and HubSpot's file
hosts (`*.hubspotusercontent*.net`, `*.hubspot.com`) are trusted to download
from without it, since the signed URL carries its own signature.

**Known limitations.**
- The ticket→conversation association is the weakest-verified part of
  HubSpot's support model: it is corroborated by a community thread rather
  than a primary reference page (see
  `docs/research/adapters/helpdesks.md`). This adapter is therefore
  defensive about it — the association block is matched on a substring, so
  `conversations`, `conversation` and a prefixed variant all resolve — and a
  ticket with no readable conversation association yields the ticket's
  `content` plus the warning `hubspot: ticket has no associated
  conversation`, never an error.
- The `actorId` prefix table is likewise documented in a guide rather than a
  schema; an unrecognised prefix is treated as staff, which is the safer
  default, since a message wrongly attributed to the customer would read as
  the customer's own words.
- Attachment MIME type is left empty: the message attachment entry carries
  only a file id, and the signed-url response gives a name and an extension
  rather than a content type.
- An `L-` prefix the source page associated with "customer agent" is not
  mapped, on the grounds that guessing wrong here is worse than the default.

## helpdeskRef fallback

None of the four adapters above guess a helpdesk reference from free text —
each only reports `HelpdeskRef` when the tracker's own data model gives an
unambiguous answer (Jira JSM, Linear's Customer Request attachments, an
Azure DevOps Hyperlink/custom field, a Rally custom field). For a workspace
where the link only exists as a pasted URL or ticket number in the
description, the wiring layer applies a generic regex fallback, configured
per workspace:

```yaml
sources:
  tracker:
    helpdeskRef:
      pattern: 'Zoho Ticket URL:\s*(\S+)'
      idPattern: '(\d+)$'
```

`pattern` matches against the ticket description and its one capture group
is the reference; `idPattern`, applied to that capture, extracts the id
passed to `helpdesk.get`/`helpdesk.threads`. Both compile at config load and
both take exactly one capture group. This runs after an adapter's own native
`HelpdeskRef`, only filling in when the adapter left it empty — it replaces
what used to be a hardcoded Zoho-URL rule with something any workspace can
point at its own helpdesk. See `docs/config.md` for the full rules.

## External adapter protocol

Adapters here are separate processes that speak a small line-delimited JSON
protocol over stdin/stdout. Sirdar spawns the adapter, sends it requests,
and reads its responses; the adapter can be written in any language and can
hold whatever credentials or vendor-specific logic it needs, none of which
touches Sirdar's process or its Go types.

This keeps vendor integrations, and any credentials they need, out of
Sirdar's core and out of this repository. An adapter for a proprietary
helpdesk can live in a private repo and never be upstreamed.

`internal/source/plugin` implements the Sirdar-side client (`Start`,
`Client`) that speaks this protocol against a spawned adapter process.
`examples/adapters/file` is a reference adapter, used by the client's own
tests and by `sirdar --dry-run`, that serves the protocol from a static
JSON fixture instead of a real API.

## Transport

The adapter is launched as a subprocess. Its stdin carries one JSON
**request** object per line; its stdout carries one JSON **response**
object per line, in reply to each request, matched by `id`. Anything the
adapter writes to stderr is passed through to Sirdar's own stderr for
diagnostics and is never parsed — an adapter can log freely there.

A request:

```json
{"id": 1, "method": "tracker.get", "params": {"key": "OMNI-1"}}
```

A response, on success:

```json
{"id": 1, "result": {"Key": "OMNI-1", "Title": "Export fails", ...}}
```

or, on failure:

```json
{"id": 1, "error": {"code": "not_found", "message": "tracker ticket not found: OMNI-1"}}
```

`result` and `error` are mutually exclusive. `id` echoes the request's
`id`; adapter output that doesn't parse as JSON, or whose `id` doesn't
match the request just sent, is skipped rather than treated as an error —
this lets an adapter emit occasional stray lines (e.g. a startup banner)
without breaking the exchange, though adapters should prefer stderr for
anything that isn't a response.

Requests are sent one at a time and each is answered before the next is
sent, so an adapter does not need to handle concurrent requests or
out-of-order responses.

## Methods

| method                 | params                          | result                              |
|-------------------------|----------------------------------|--------------------------------------|
| `describe`               | none                              | `{"name","roles","version"}`         |
| `tracker.get`             | `{"key"}`                          | a `ticket.TrackerTicket`               |
| `tracker.list`            | `{"assignee","status","parent","limit"}` (any may be absent) | `[]ticket.TrackerTicket`     |
| `helpdesk.get`            | `{"id"}`                           | a `ticket.HelpdeskTicket`              |
| `helpdesk.threads`        | `{"id"}`                           | a `ticket.Thread` (`[]ticket.Message`) |
| `helpdesk.attachments`    | `{"id","dir"}`                     | `[]ticket.Attachment`                  |
| `shutdown`                | none                              | `null`                               |

`tracker.list`'s `assignee` is matched against the same form the adapter puts in each
returned ticket's `Assignee` field. If that field carries display names, an email address
matches nothing — and an adapter that answers such a filter with an empty list is
indistinguishable, to the operator, from having no open tickets. An adapter should either
resolve the value it was given to the form it stores, or reject an `assignee` it cannot
resolve with an `invalid_request` error. Returning `[]` for an unresolvable filter is the one
answer it must not give.

Ticket types are defined in `internal/ticket`; their JSON field names are
the Go field names verbatim (`Key`, `Title`, `HelpdeskRef`, and so on —
capitalized, no renaming). An adapter that fulfils only one role need not
implement the other role's methods at all; Sirdar checks `describe`'s
`roles` before calling them and never sends a method the adapter didn't
advertise.

### `describe`

Returns the adapter's identity and capabilities. `roles` is a subset of
`["tracker", "helpdesk"]`; an adapter may support one or both. Sirdar
calls `describe` once, lazily, on first use, and caches the result — an
adapter is not asked to `describe` itself more than once per process.

```json
{"id": 1, "result": {"name": "file", "roles": ["tracker", "helpdesk"], "version": "1"}}
```

### `tracker.get` / `tracker.list`

`tracker.get` fetches one ticket by key; `tracker.list` fetches many,
filtered by `assignee`, `status`, `parent` (all optional, all substring-or-
equality is the adapter's choice) and capped at `limit` (0 or absent =
adapter's own default/no cap). An adapter may choose to ignore filters it
doesn't support rather than error — the reference file adapter ignores
all of them and always returns its full fixture.

### `helpdesk.get` / `helpdesk.threads`

Fetch the helpdesk ticket record and its conversation thread,
respectively, both keyed by the helpdesk ticket's own id (not the tracker
key — Sirdar resolves `TrackerTicket.HelpdeskRef`, or falls back to the
tracker key itself, before calling these).

### `helpdesk.attachments`

Downloads every attachment referenced by the ticket's thread into `dir`
(an absolute path; the adapter must create it if missing) and returns
their metadata. Each file is written as `<n>-<name>`, where `n` is the
attachment's 1-based position, to avoid collisions between attachments
that share a filename. The returned `Attachment.Path` is relative to the
*bundle* directory — `dir`'s parent, i.e. the ticket's working directory —
not to `dir` itself:

```json
{"id": "555", "dir": "/abs/bundle/attachments"}
```

```json
{"id": 1, "result": [
  {"ID": "a1", "Name": "shot.png", "MIME": "image/png", "Path": "attachments/1-shot.png"}
]}
```

An adapter that fails to fetch one attachment should still return the
others where possible; Sirdar treats an `Attachments` error as a warning
on the bundle rather than a fatal one, but a partial return only helps if
the adapter makes it.

### `shutdown`

Sent when Sirdar is done with the adapter. The adapter should reply
`{"id": N, "result": null}` and then exit; Sirdar closes the adapter's
stdin right after sending this, so an adapter that instead reads its next
request in a loop will see EOF and can treat that the same way — stdin
EOF and an explicit `shutdown` request are equivalent shutdown signals,
and an adapter should handle whichever arrives.

Sirdar does not wait for the `{"result": null}` reply before proceeding:
it closes stdin and then waits up to 5 seconds for the process to exit on
its own, killing it if it hasn't. An adapter with cleanup to do (flushing
a cache, closing a connection) has that window to do it in before exit;
an adapter that hangs indefinitely is killed, not waited on.

## Error codes

| code           | meaning                                                        |
|----------------|-----------------------------------------------------------------|
| `not_found`      | the requested key/id doesn't exist                                |
| `auth`           | the adapter's credentials are missing, expired, or rejected        |
| `unsupported`    | the method isn't implemented by this adapter, or the role wasn't advertised in `describe` |
| `rate_limited`    | the upstream API is throttling; Sirdar may back off and retry later |
| `internal`        | anything else — a parse failure, a network error, a bug            |

An unrecognized method should return `unsupported`:

```json
{"id": 4, "error": {"code": "unsupported", "message": "unknown method foo.bar"}}
```

## The file adapter

`examples/adapters/file` is a complete, minimal adapter: it reads a JSON
fixture (via `-file path/to/tickets.json` or `SIRDAR_FILE_ADAPTER`) shaped
like

```json
{
  "tracker": {"OMNI-1": {"Key": "OMNI-1", "Title": "Export fails", "...": "..."}},
  "helpdesk": {
    "555": {
      "ticket": {"ID": "555", "Subject": "...", "...": "..."},
      "thread": [{"At": "...", "Author": "...", "Role": "customer", "Text": "...", "AttachmentIDs": ["a1"]}],
      "attachments": [{"id": "a1", "name": "shot.png", "mime": "image/png", "content_base64": "..."}]
    }
  }
}
```

and serves it: `describe` reports both roles; `tracker.list` ignores its
filter and returns every tracker ticket in the fixture; `helpdesk.attachments`
decodes each fixture attachment's `content_base64` and writes it into the
requested directory. It's what `internal/source/plugin`'s tests build and
drive, and it's the adapter CI's `--dry-run` mode runs Sirdar against, so
a full triage pass can be exercised in CI without any real credentials.

Run it standalone to see the protocol in action:

```sh
go run ./examples/adapters/file -file internal/source/plugin/testdata/tickets.json
```

then type a request line and press enter:

```
{"id": 1, "method": "describe"}
{"id": 2, "method": "tracker.get", "params": {"key": "OMNI-1"}}
```

## Writing a private adapter

An adapter is any executable that:

1. Reads JSON request objects from stdin, one per line.
2. Writes one JSON response object per line to stdout, matching each
   request's `id`, before reading the next request (or as soon as it's
   ready — nothing stops an adapter from answering out of the order
   requests were meant to be handled in, but since Sirdar only ever has
   one request in flight, in practice this just means: answer promptly).
3. Treats EOF on stdin the same as an explicit `shutdown` request, and
   exits soon after either.
4. Sends anything that isn't a protocol response — logs, warnings, stack
   traces — to stderr, never stdout.

Point Sirdar's config at it as a shell command; Sirdar will start it,
speak the protocol above, and stop it when the run ends. Because the
protocol is language-agnostic, a private adapter for an internal or
proprietary system (say, an in-house ticketing tool) can be a short Python
or Node script living entirely outside this repository — it only needs to
implement whichever of `tracker.*` or `helpdesk.*` it advertises in
`describe`, and can return `unsupported` for methods outside its role.

A minimal sketch in Python:

```python
import json, sys

def handle(req):
    if req["method"] == "describe":
        return {"name": "example", "roles": ["tracker"], "version": "1"}
    if req["method"] == "tracker.get":
        key = req["params"]["key"]
        ticket = fetch(key)  # however this adapter talks to its backend
        if ticket is None:
            raise LookupError(key)
        return ticket
    raise NotImplementedError(req["method"])

for line in sys.stdin:
    req = json.loads(line)
    try:
        result = handle(req)
        resp = {"id": req["id"], "result": result}
    except LookupError as e:
        resp = {"id": req["id"], "error": {"code": "not_found", "message": str(e)}}
    except NotImplementedError as e:
        resp = {"id": req["id"], "error": {"code": "unsupported", "message": f"unknown method {e}"}}
    print(json.dumps(resp), flush=True)
    if req["method"] == "shutdown":
        break
```
