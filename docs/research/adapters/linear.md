# Linear as a Sirdar source adapter

Research date 2026-09-10. Scope: can Linear serve as Sirdar's `tracker` role (and
partly `helpdesk`, via Customer Requests), what a Go adapter would call, and what's
worth revisiting later (MCP, the Agents platform). Every claim below is sourced;
anything I couldn't pin down with a fetched page is flagged `[unverified]`.

## 1. GraphQL API

Endpoint: `https://api.linear.app/graphql`, single POST endpoint, standard GraphQL
over HTTP ([Getting started](https://linear.app/developers/graphql)).

### Fetching an issue

`issue(id:)` takes either the entity UUID or the human identifier directly —
`issue(id: "BLA-123")` works, no separate identifier-to-UUID lookup needed
([Getting started](https://linear.app/developers/graphql), confirmed independently
against a community CLI's query examples,
[linear-cli SKILL.md](https://github.com/0xBigBoss/linear-cli/blob/main/skills/linear/SKILL.md)).
This is the field Sirdar's `tracker.get` maps onto directly — no need for
`issueSearch` or a `team{key}` + `number` filter just to resolve `ENG-123`.

Representative query (field names confirmed against the linear-cli examples and
Linear's own filtering/pagination docs):

```graphql
query {
  issue(id: "ENG-123") {
    id
    identifier
    title
    description
    priority
    priorityLabel
    state { name type }
    assignee { id name email }
    team { id key name }
    project { id name }
    parent { id identifier }
    labels { nodes { id name } }
    url
    createdAt
    updatedAt
  }
}
```

`priority` is an integer 0–4 (0 = no priority, 1 = urgent, 2 = high, 3 = normal,
4 = low is the commonly documented mapping); `priorityLabel` is the string form.
I found `priority`/`priorityLabel` named consistently across the community
references above, but did not fetch Linear's own Apollo-hosted schema page
successfully (it returned only a page title, no field list, on fetch) — treat the
exact 0–4 → label mapping as `[unverified]` pending a direct schema/introspection
check, though it matches what every third-party integration guide states.

For host-side listing (Sirdar's `tracker.list`), use the root `issues(filter:, first:,
after:)` connection rather than the singular `issue`, with an `IssueFilter` input.
Documented filter shapes ([Filtering](https://linear.app/developers/filtering)):

```graphql
query($filter: IssueFilter!) {
  issues(filter: $filter, first: 50) {
    nodes { id identifier title state { name type } assignee { name } }
    pageInfo { hasNextPage endCursor }
  }
}
```

Filter examples confirmed: `{ assignee: { email: { eq: "..." } } }`,
`{ team: { id: { eq: "..." } } }`, `{ state: { type: { eq: "started" } } }` or
`{ state: { name: { eq: "In Progress" } } }`, with comparison operators `eq`, `in`,
`startsWith`, `contains`, `lt`, `gt` available on most scalar sub-filters
([Filtering](https://linear.app/developers/filtering)).

### Comments

`issue.comments` returns a connection of `Comment` objects; the body is Markdown.
Community documentation consistently shows `body` (Markdown string), `user` (author),
`createdAt`, and a `parent` reference for threaded replies, plus `url`
([linear-cli SKILL.md](https://github.com/0xBigBoss/linear-cli/blob/main/skills/linear/SKILL.md);
`CommentCreateInput` reference,
[Apollo Studio schema](https://studio.apollographql.com/public/Linear-API/variant/current/schema/reference/inputs/CommentCreateInput)).
I did not get an authoritative field-by-field dump of the `Comment` type itself from
Linear's own docs — the shape above is corroborated by multiple third-party sources
but not by a fetched Linear schema page, so treat exact field names (`user` vs
`author`) as `[unverified]` until checked against a live introspection query, which
is cheap to do once you have an API key.

### Attachments

`issue.attachments` returns `Attachment` objects representing links to external
resources (GitHub PRs, Slack messages, Zendesk/Intercom conversations, Figma files,
plain URLs). Documented fields: `url` (also the dedupe key — no two attachments on
one issue share a URL), `title`, `subtitle`, `metadata` (integration-specific,
schema varies by source), and `sourceType` (derived from the source metadata —
`"github"`, `"slack"`, `"zendesk"`, `"intercom"`, or `"unknown"`)
([Attachments](https://linear.app/developers/attachments); source type behavior per
search-indexed docs excerpt, same page).

This is distinct from **uploaded files embedded in a Markdown description or
comment** — those are plain Markdown image/file links pointing at
`uploads.linear.app`, not `Attachment` GraphQL objects. Files there live in Linear's
private cloud storage and require the same auth as the GraphQL API itself: an OAuth
bearer token as `Authorization: Bearer <token>`, or a raw API key (unprefixed, no
`Bearer`) in `Authorization`, sent as a normal HTTP header on the download request
([File storage authentication](https://linear.app/developers/file-storage-authentication)).
So an adapter that wants to fetch a screenshot embedded in a description has to
parse the Markdown, find `uploads.linear.app` URLs, and re-fetch each with the same
credential — Linear does not proxy or re-host them anywhere public.

### Issue history

A GitHub issue thread on Linear's own SDK repo shows an `issueHistory` field
supporting `after`/`before`/`first`/`last`/`orderBy` pagination and returning
change records (`relationChanges`, `addedLabelIds`, `removedLabelIds`, state and
assignee changes) ([linear/linear#91](https://github.com/linear/linear/issues/91)).
I could not confirm this against Linear's own current docs page — `[unverified]`,
and likely lower priority for Sirdar than comments/attachments since the harness's
`Fields` map doesn't need a full audit trail.

### Pagination

Cursor-based, Relay-style: `first`/`after` (forward), `last`/`before` (backward);
response carries `pageInfo { hasNextPage endCursor }`. Default page size when no
argument is given is 50. Results default to `createdAt` ordering; pass
`orderBy: updatedAt` to page by modification time, useful for polling
([Getting started](https://linear.app/developers/graphql), pagination section).

### Complexity limits and rate limits

Two independent throttles, both tracked hourly per authenticated user (or per app
in the OAuth-app case):

| Auth | Requests/hour | Complexity points/hour |
|---|---|---|
| API key | 2,500 | 3,000,000 |
| OAuth app token | 5,000 | 2,000,000 |
| Unauthenticated | 600 | 100,000 |

A single query is capped at 10,000 complexity points regardless of auth type
([Rate limiting](https://linear.app/developers/rate-limiting)).

Response headers on every request let a client self-throttle without guessing:
`X-RateLimit-Requests-Limit/-Remaining/-Reset`,
`X-RateLimit-Endpoint-Requests-Limit/-Remaining/-Reset` + `X-RateLimit-Endpoint-Name`,
and `X-Complexity` plus `X-RateLimit-Complexity-Limit/-Remaining/-Reset`
([Rate limiting](https://linear.app/developers/rate-limiting)).

### Error shape

Linear follows standard GraphQL: HTTP 200 is not a success guarantee — always check
the top-level `errors` array. A rate-limited request returns HTTP 400 with an error
in that array carrying `extensions.type` (or `extensions.code` per some
documentation) set to a string like `"RATELIMITED"`; authentication/authorization
failures surface similarly with codes such as `"AUTHENTICATION_ERROR"` and
`"FORBIDDEN"`, alongside an `extensions.userPresentableMessage` meant for surfacing
to a human ([Getting started](https://linear.app/developers/graphql); rate-limit
error shape per [Rate limiting](https://linear.app/developers/rate-limiting) and
corroborating third-party guides — the exact error-code string casing/spelling is
`[unverified]` against Linear's own docs and worth confirming by triggering one).
This maps cleanly onto Sirdar's adapter error codes: `RATELIMITED` → `rate_limited`,
auth errors → `auth`, a `null` `issue` result for an unknown identifier → `not_found`.

## 2. Auth

Two supported mechanisms ([OAuth 2.0 Authentication](https://linear.app/developers/oauth-2-0-authentication)):

- **Personal API key.** Created under Settings → Security & access. Sent as the raw
  key value in `Authorization` (no `Bearer` prefix). Documented as the recommended
  path "for personal scripts" — i.e. exactly Sirdar's use case, one operator running
  their own harness against their own workspace.
- **OAuth2**, recommended when building something distributed to other people's
  workspaces. Standard authorization-code flow (`https://linear.app/oauth/authorize`
  → `https://api.linear.app/oauth/token`), PKCE supported. Access tokens last 24
  hours and need refreshing; all OAuth2 apps were migrated to a refresh-token system
  on 2026-04-01. Scopes are granular: `read` (always present, read-only across the
  user's account), `write`, `issues:create`, `comments:create`, `timeSchedule:write`,
  `admin`, plus agent-specific scopes covered below.

**Actor mode**: appending `actor=app` to the OAuth authorization URL makes actions
attributed to the app itself rather than the authorizing user — Linear frames this
as being for "agents and service accounts." Requesting it requires workspace admin
approval during install. `actor=user` (default) attributes everything to whoever
authorized the app
([OAuth actor authorization](https://linear.app/developers/oauth-actor-authorization);
[Agents platform](https://linear.app/developers/agents)).

**Minimal read-only key**: a personal API key used only for `query { issue }`,
`query { issues }`, `comments`, `attachments` calls is fully sufficient and requires
no scope negotiation — API keys aren't scoped the way OAuth tokens are; they carry
whatever permissions the creating user has in the workspace
([OAuth 2.0 Authentication](https://linear.app/developers/oauth-2-0-authentication)).
This is the simplest fit for Sirdar's read-only, single-operator adapter model and
matches the `env:`/`keychain:` credential-reference pattern Sirdar's `zohodesk`
adapter already uses.

## 3. Linear MCP and the Agents platform

**MCP server** — official, hosted by Linear, not something Sirdar would run itself:

- Streamable HTTP (current): `https://mcp.linear.app/mcp`
- Read-only variant: `https://mcp.linear.app/mcp/readonly` — "only ever exposes
  read tools"
- SSE (deprecated, kept for older clients): `https://mcp.linear.app/sse`

Auth is OAuth 2.1 with dynamic client registration by default; a client can instead
pass an OAuth token or API key directly as `Authorization: Bearer <token>`. Claude
Code needs an explicit `/mcp` invocation post-connect to trigger the auth flow;
Codex needs the `experimental_use_rmcp_client` flag before MCP works at all
([MCP server](https://linear.app/docs/mcp)). Tool count: docs describe "25+ tools"
covering issue/project/comment CRUD and search, without an itemized list in the
pages I fetched — the exact tool manifest is `[unverified]`; it's discoverable at
runtime via MCP `tools/list` against a live connection, which I didn't have
credentials to do here.

**This is not a fit for Sirdar's adapter layer.** Sirdar's adapter contract wants
a deterministic, scriptable `tracker.get`/`tracker.list` call that returns a fixed
Go struct — host-side, no LLM in the loop, no tool-selection ambiguity. MCP is built
for the *opposite*: handing a capability to an agent that decides which tool to call
and how to interpret free-form results. Using Linear's MCP server for ticket fetch
would mean either (a) running an agent turn just to extract structured fields, which
is slower and non-deterministic, or (b) driving the MCP tool calls directly from Go
code, which gains nothing over calling the GraphQL API directly and adds a
dependency on Linear's hosted MCP infrastructure and its own OAuth dance. The
GraphQL adapter is strictly simpler for the host-side role. MCP is the right layer
for the *coding agent itself* if the user wants Claude Code to have live, open-ended
Linear access during a triage session (separate from Sirdar's harness-side fetch) —
worth keeping distinct in the design.

**Agents platform** (separate from MCP): Linear now supports registering an app as
a first-class "agent" that can be assigned issues as a delegate. Registration is
through the same OAuth app config, with `actor=app`, plus two agent-specific
optional scopes: `app:assignable` (lets the app be assigned/delegated an issue) and
`app:mentionable` (lets the app respond to @-mentions); `customer:read/write` and
`initiative:read/write` are also available for agents that touch those entity types
([Agents platform](https://linear.app/developers/agents)).

When a human assigns an issue to the agent, Linear fires a `created`
`AgentSessionEvent` webhook carrying an `agentSession` object with issue context; the
agent is expected to acknowledge fast — emit a `thought` activity within 10 seconds,
and generally respond within the session or risk being marked unresponsive. Note
Linear's framing: the agent becomes the issue's **delegate**, not its assignee —
"humans maintain ownership while agents act on their behalf"
([Agents platform](https://linear.app/developers/agents)). Activity streamed back
during a session comes in five types — `thought`, `elicitation`, `action`,
`response`, `error` — each postable via `agentActivityCreate` (GraphQL mutation) or
a TypeScript SDK method; a `promptContext` field on the session gives the agent a
pre-formatted XML context blob (issue metadata, parent/project, comment thread,
workspace/team guidance rules) so it doesn't have to assemble that itself
([Developing the Agent Interaction](https://linear.app/developers/agent-interaction)).

Separately, Linear ships a first-party **"Linear agent" for Intercom, Zendesk, and
Gong** (Business/Enterprise plans, Gong on Enterprise only): a one-click "turn this
support conversation into a Linear issue" feature that reads the full external
conversation and files one or more issues with a summary, screenshots, and customer
attribution, routed into the team's triage queue
([Linear agent for Intercom, Zendesk, Gong changelog, 2025-12-11](https://linear.app/changelog/2025-12-11-linear-agent-for-intercom-zendesk-gong)).
This is a different thing from the general Agents platform above — it's Linear's own
built-in feature, not something Sirdar would register as.

**Could Sirdar register as a Linear agent?** Mechanically, yes — nothing in the
docs restricts `app:assignable`/`app:mentionable` registration to SaaS vendors.
Practically, this is a different integration shape than Sirdar's current one:
it would mean Sirdar runs a long-lived webhook receiver (with `Linear-Signature`
HMAC verification, 5–10 second response SLAs) rather than being invoked on demand
by a human running `sirdar triage <key>`, and it would mean streaming intermediate
activity back into Linear's UI via `agentActivityCreate` rather than writing a note
to disk. That's a materially different product — closer to "Sirdar as a hosted
service Linear can assign work to" than "Sirdar as a CLI a person points at a
ticket." Worth a future spike, but out of scope for the adapter being designed here;
flagged again under Open Questions.

## 4. Helpdesk linkage (Customer Requests)

Linear's **Customer Requests** feature (shipped December 2024) is the mechanism for
tying an issue back to a support conversation in Intercom, Zendesk, or Front. A
`Customer` record (`id`, `name`) can have one or more `CustomerNeed` records
attached to an issue; a `CustomerNeed` carries `customerId`, `issueId`,
`attachmentId`, `priority` (0/1), `body` (Markdown, optional), and `creatorId`
([Managing Customers](https://linear.app/developers/managing-customers)).

Critically: `customerNeedCreate` accepts an `attachmentUrl` (e.g. an Intercom
conversation permalink) and Linear auto-creates an `Attachment` from it — "all
requests with a source URL are backed by an Attachment"
([Managing Customers](https://linear.app/developers/managing-customers)). So the
path to a `HelpdeskRef` is: `issue.attachments` → filter to entries whose
`sourceType` is `intercom`/`zendesk`/`front` → read that attachment's `url` (the
helpdesk's own conversation/ticket link) and/or its `metadata` for a ticket ID. The
exact shape of `metadata` for Zendesk/Intercom attachments (does it carry a raw
numeric ticket ID separately from the URL?) is `[unverified]` — the docs describe
`metadata` as "varying by source type" without publishing the Zendesk/Intercom
schema.

**Is the external conversation body readable through Linear's own API, or only via
the helpdesk's API?** Nothing in the fetched Linear docs shows a field on
`Attachment` or `CustomerNeed` that carries the full conversation transcript —
`CustomerNeed.body` is described as the *request* text (what the customer is
asking for, presumably summarized/extracted at creation time), not a live mirror
of the ongoing thread. Linear's own "Linear agent for Intercom/Zendesk" reads the
full conversation on the helpdesk side at issue-creation time and writes a summary
plus attachment link into Linear — it does not appear to give third-party API
consumers a way to pull that same full-thread text back out of Linear afterward.
This means for Sirdar's `helpdesk` role, Linear is not sufficient on its own: an
adapter would derive `HelpdeskRef` from the Zendesk/Intercom attachment URL/ID on
the Linear side, then use Sirdar's *separate* Zendesk or Intercom-flavored helpdesk
adapter (Sirdar already has a working `zohodesk` built-in adapter as a template) to
fetch the actual thread. Treat this whole paragraph's absence-of-a-field claim as
`[unverified]` in the strict sense — I did not find a documented field, which is
different from confirming none exists; worth a direct schema introspection check
(`query { __type(name: "CustomerNeed") { fields { name } } }`) before finalizing.

## 5. Webhooks

Standard webhook payload for data-change events (issue create/update, etc.):
`action` (`create`/`update`/`remove`), `type` (`"Issue"`, `"Comment"`, etc.),
`actor` (user, OAuth client, or integration), `createdAt`, `data` (the full
serialized entity, same shape as the GraphQL type), `url`, `updatedFrom` (previous
values of changed fields, update-only), `webhookTimestamp` (Unix ms),
`webhookId` ([Webhooks](https://linear.app/developers/webhooks) via fetch summary;
page also states the Issue payload "reflects that of the corresponding GraphQL
entity").

Signature verification: header `Linear-Signature`, value is a hex-encoded
HMAC-SHA256 of the **raw** request body using the webhook's signing secret. Verify
against the raw bytes, not a re-serialized JSON object — re-stringifying changes key
order/whitespace and breaks the signature. Linear also recommends checking
`webhookTimestamp` is within 60 seconds of receipt to guard against replay
([Webhooks](https://linear.app/developers/webhooks)).

`AgentSessionEvent` is a separate webhook category, opt-in per OAuth app
("agent session events" webhook category), used only for the Agents platform
described in §3 — a `created` event must get an activity or URL update within 10
seconds or the session is marked unresponsive
([Agents platform / agent session webhooks, search-indexed summary](https://linear.app/developers/agents)).
This is the trigger Sirdar would use for a future "issue assigned → auto-triage"
flow, separate from and complementary to a plain `Issue` webhook on
create/assignee-change, which would work for a simpler "poll on webhook, then fetch
via GraphQL" pattern without registering as a full agent.

Go's `net/http` plus `crypto/hmac`/`crypto/sha256` from stdlib is sufficient to
verify `Linear-Signature` — no library needed.

## 6. Existing Go clients

- `github.com/chainguard-sandbox/go-linear` — a generated Go SDK, CLI, and MCP
  server for Linear, built for both humans and AI agents (uses `ophis` to expose
  Cobra commands as MCP tools) ([pkg.go.dev](https://pkg.go.dev/github.com/chainguard-sandbox/go-linear)).
  The `chainguard-sandbox` org prefix signals this is an internal experiment/sandbox
  project from Chainguard, not an officially blessed Linear SDK — maintenance status
  and long-term support are `[unverified]`, and I would not take a runtime
  dependency on it for an open-source harness meant to work reliably years out.
- General-purpose Go GraphQL clients exist (`shurcooL/graphql`,
  `hasura/go-graphql-client`, `Khan/genqlient`, `machinebox/graphql`) but none are
  Linear-specific; using one would still mean hand-writing every query/mutation
  string and every response struct against Linear's schema.
- No actively-maintained, widely-adopted, Linear-official Go client turned up in
  this search.

**Conclusion: stdlib `net/http` + hand-written GraphQL query strings is the sane
path**, matching what Sirdar's `zohodesk` adapter already does for its REST API and
what the adapter contract in `docs/adapters.md` assumes (adapters are "Go, stdlib
HTTP, credentials via env/keychain refs, read-only"). A GraphQL request is just a
POST with a JSON body (`{"query": "...", "variables": {...}}`) and a JSON response
— no code generation step is required to get a working, small adapter; hand-rolled
structs for exactly the fields Sirdar needs (per §1's query) keep the adapter's
surface area minimal and auditable.

## Recommended adapter design

A built-in `linear` adapter (same pattern as the existing built-in `zohodesk`
adapter in `internal/source/zohodesk`, config-driven like it, `tracker` role only —
Linear's own API doesn't carry the full-thread helpdesk conversation, see §4) is
the shape that fits Sirdar's existing conventions best; the same design also works
as an out-of-tree `exec` adapter if a private/generalised build is preferred.

### Config keys (parallel to `sources.*.zohodesk` in `docs/config.md`)

| Key | Required | Meaning |
|---|---|---|
| `sources.tracker.adapter` | yes | `linear` |
| `sources.tracker.baseUrl` | no, default `https://api.linear.app/graphql` | override for testing/proxying |
| `sources.tracker.token` | yes | `env:NAME` or `keychain:SERVICE`, resolves to a personal API key |
| `sources.tracker.teamKey` | no | default team key, used only if `tracker.list` needs a team scope and none is given in the filter |

No OAuth config block for v1: a personal API key is simpler, matches "one operator,
one workspace," needs no refresh-token handling, and satisfies the read-only
requirement without scope negotiation (§2).

### Auth

`Authorization: <token>` header, raw key value, no `Bearer` prefix (API-key form
per §2). Resolved once at adapter startup via Sirdar's existing credential-ref
resolution, held in memory only, never logged — same rule Sirdar already applies to
`zohodesk.token` per `docs/config.md`.

### Queries

- `tracker.get({key})` → single POST:
  ```graphql
  query($id: String!) {
    issue(id: $id) {
      identifier title description priority priorityLabel
      state { name type }
      assignee { name email }
      team { key name }
      project { name }
      parent { identifier }
      labels { nodes { name } }
      url createdAt updatedAt
      attachments { nodes { url title subtitle sourceType metadata } }
    }
  }
  ```
  A `null` `issue` in the response (no top-level error) is Linear's not-found case
  for an unknown identifier — map to `not_found`. This also means the response body
  can be checked purely structurally, no separate existence-check request needed.

- `tracker.list({assignee, status, parent, limit})` → `issues(filter:, first:)`,
  building the `IssueFilter` incrementally:
  - `assignee` → `{ assignee: { email: { eq: <assignee> } } }` if it looks like an
    email, else `{ assignee: { name: { eq: <assignee> } } }` — Sirdar's protocol
    doesn't distinguish, and Linear's filter needs to pick a sub-field, so try the
    email-shaped path first for anything containing `@`.
  - `status` → `{ state: { name: { eq: <status> } } }` (string match against the
    workflow state's display name — Linear's status vocabulary is per-workspace, so
    exact-match on name is the least surprising default; `state.type` in
    `{triage, backlog, unstarted, started, completed, canceled}` is available if a
    coarser category filter is wanted later).
  - `parent` → `{ parent: { identifier: { eq: <parent> } } }`.
  - `limit` → `first: <limit>` (or `first: 50`, Linear's own default, if `limit` is
    0/absent — per the adapter contract's "0 or absent = adapter's own
    default/no cap," but Linear caps single-query complexity at 10,000 points
    regardless, so don't attempt to request an unbounded `first` in one call; if
    `limit` exceeds a safe single-page size, the adapter should paginate
    internally with `after`/`pageInfo.hasNextPage` and concatenate, transparently
    to the protocol).
  - Filters the adapter doesn't map cleanly (nothing beyond these three keys is
    defined in the protocol) are simply the ones above — no unmapped case to
    silently drop.

- Comments, only if/when Sirdar's protocol grows a way to expose them (currently
  `TrackerTicket` has no comments field — they'd land in `Fields` as a rendered
  blob, or wait for a protocol extension): `issue.comments.nodes { body user { name }
  createdAt }`, paginated the same way as `tracker.list`.

### Mapping to Sirdar's `TrackerTicket`

| Sirdar field | Linear source |
|---|---|
| `Key` | `identifier` |
| `Title` | `title` |
| `Description` | `description` (Markdown, passed through as-is — Sirdar's own note templates already render Markdown) |
| `Priority` | `priorityLabel` (string; more directly useful to an agent prompt than the raw 0–4 int) |
| `Status` | `state.name` |
| `Assignee` | `assignee.name` (empty string if unassigned — `assignee` is nullable) |
| `URL` | `url` |
| `HelpdeskRef` | derived, see below |
| `CreatedAt` / `UpdatedAt` | `createdAt` / `updatedAt`, parsed as RFC3339 (Linear's timestamps are ISO 8601/RFC3339) |
| `Fields` | everything else worth keeping structured but not promoted to a named field: `team.key`, `project.name`, `parent.identifier`, `labels` (joined), `state.type`, `priority` (raw int) |

`HelpdeskRef` derivation: scan `attachments.nodes` for the first entry whose
`sourceType` is `zendesk`, `intercom`, or `front`; set `HelpdeskRef` to that
attachment's `url` (the most reliably present field per §4 — a raw numeric ID isn't
confirmed to exist in `metadata` for every source type). Sirdar's own note that
`HelpdeskRef` falls back to the tracker key if absent (per `docs/adapters.md`'s
description of how Sirdar resolves it before calling `helpdesk.*`) means an issue
with no linked helpdesk attachment just degrades to tracker-only, no adapter-side
special-casing needed.

### Role mapping for comments (if/when added)

Linear has no first-class notion of "customer" vs "agent" on a `Comment` — every
comment is authored by a Linear workspace user. So a Linear-native comment should
map to `Role: agent` or `Role: system` (e.g. bot-authored comments, if `user` is
null and only an app/integration actor is present), never `customer` — a real
customer's words only enter Sirdar's thread via the *helpdesk* adapter (Zendesk/
Intercom/Zoho), not via Linear. This matters for prompt construction downstream:
Linear-side comments are internal team discussion, not customer voice, and should
probably be kept separate from (or clearly labeled apart from) the helpdesk thread
rather than merged into one `Role`-tagged stream.

### Attachment download auth

For `helpdesk.attachments`-equivalent behavior on the tracker side (if Sirdar ever
wants to pull an image embedded in a Linear description), reuse the same
`Authorization` header/value used for GraphQL requests when fetching any
`uploads.linear.app` URL found in Markdown — no separate credential, per §1's file
storage note. This only applies to inline-uploaded files; `Attachment` objects
pointing at Zendesk/Intercom/GitHub/etc. are external URLs requiring *that* vendor's
own auth to fetch, not Linear's.

### Error mapping

| Linear condition | Sirdar code |
|---|---|
| `issue` is `null`, no GraphQL error | `not_found` |
| `errors[].extensions.type` (or `.code`) `AUTHENTICATION_ERROR`/`FORBIDDEN` | `auth` |
| HTTP 400 with `RATELIMITED` in `errors[]` | `rate_limited` |
| any other non-2xx, or a malformed/unparseable response | `internal` |
| method requested that the adapter doesn't implement (`helpdesk.*`) | `unsupported`, since this is a `tracker`-only adapter |

## Open questions

1. **Exact field names on `Comment` and the 0–4 `priority` → `priorityLabel`
   mapping** aren't confirmed against Linear's own live schema in this research —
   worth one introspection query (`query { __type(name: "Comment") { fields { name
   } } }`) once an API key exists, before locking the Go structs.
2. **`Attachment.metadata` shape for Zendesk/Intercom sources** — does it carry a
   bare numeric/string ticket ID separately from `url`? Matters for how clean
   `HelpdeskRef` derivation can be; currently the design falls back to the
   attachment URL itself, which should always work but is less tidy than an ID.
3. **Whether any field, anywhere in Linear's schema, mirrors the live helpdesk
   conversation body** — I found no evidence of one, but absence-of-evidence here
   is genuinely unverified, not confirmed-absent. If one exists, it could let a
   `helpdesk` role piggyback on the tracker adapter's token instead of needing a
   separate Zendesk/Intercom credential.
4. **Should Sirdar register as a Linear agent** (§3) rather than only doing
   on-demand host-side fetch? This is a different product shape — a
   webhook-driven service instead of a CLI — and probably belongs in a future
   design doc, not this adapter.
5. **`issueHistory` field** — real but unconfirmed against current docs; decide
   whether Sirdar's `Fields` map wants any of it (e.g. "reopened N times") or
   whether that's scope creep for a triage harness.
6. **Rate-limit backoff policy** — Linear hands back three sets of headers (§1);
   worth deciding whether the adapter should proactively throttle using
   `X-RateLimit-*-Remaining` or just retry-on-`rate_limited` and let Sirdar's own
   retry/backoff (if any exists at the call site) handle it.
