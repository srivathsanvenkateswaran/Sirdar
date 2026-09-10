# Rally (Broadcom Rally Software, formerly CA Agile Central) — Adapter Research

Researched 2026-09-10. Primary source is Broadcom TechDocs
(`techdocs.broadcom.com/.../valueops/rally/rally-help/...`) plus Broadcom's
knowledge-base articles (`knowledge.broadcom.com/external/article/...`) and
public GitHub client source. Several TechDocs pages required guessing the
correct path — Broadcom has moved Rally docs between at least two URL trees
(`ca-enterprise-software/agile-development-and-management/...` and
`ca-enterprise-software/valueops/rally/...`) and some paths 404 even when
linked from search results. Where I could not load a page directly and relied
on a search-engine summary of it, or on a third-party client's source instead
of the spec itself, I've marked the claim **[unverified from primary docs]**.

---

## 1. Web Services API (WSAPI) v2.0

### Base URL and hosts

- Production base URL: `https://rally1.rallydev.com/slm/webservice/v2.0/`
  — confirmed directly in multiple TechDocs/KB pages, e.g. the WSAPI overview
  page and the Query Parameters page.
  [Web Services API (WSAPI)](https://techdocs.broadcom.com/us/en/ca-enterprise-software/valueops/rally/rally-help/integrating-with-rally/building-rally-integrations/web-services-api-wsapi.html),
  [Query Parameters](https://techdocs.broadcom.com/us/en/ca-enterprise-software/valueops/rally/rally-help/reference/rally-web-services-api/query-parameters.html)
- Object refs returned by the API are full URLs of the form
  `https://rally1.rallydev.com/slm/webservice/v2.0/HierarchicalRequirement/<ObjectID>` —
  same page.
- An `eu1.rallydev.com` host exists and appears in a Broadcom status-page
  incident notice, but I could not find primary WSAPI documentation stating a
  dedicated EU **WSAPI** base URL (as opposed to the EU **web app** host).
  **[unverified from primary docs]** — treat `eu1.rallydev.com` as an
  app/login host, not a confirmed WSAPI endpoint, until a subscription's
  actual API base URL is checked (Rally subscriptions can apparently be
  provisioned on region-specific hosts; the adapter should make the base URL
  configurable rather than hardcoding `rally1`).
  [Rally EU1 unavailable notice](https://status.broadcom.com/notices/jat9navgu65m9iba-rally-eu1-is-current-unavailable-https-eu1-rallydev-com)
- By contrast, Rally's new **MCP server** (see §3) explicitly documents two
  regional endpoints — `https://mcp.rallydev.com/mcp` (NA) and
  `https://mcp-eu.rallydev.com/mcp` (EMEA) — which at least confirms Broadcom
  operates EU-resident infrastructure for Rally generally.
  [Access an MCP Server](https://techdocs.broadcom.com/us/en/ca-enterprise-software/valueops/rally/rally-help/reference/ai/enable-a-model-context-protocol--mcp--server-/access-an-mcp-server.html)

### Format and REST model

- WSAPI 2.0 is REST-based, JSON only (no XML in 2.0).
  [Web Services API (WSAPI)](https://techdocs.broadcom.com/us/en/ca-enterprise-software/valueops/rally/rally-help/integrating-with-rally/building-rally-integrations/web-services-api-wsapi.html)
- The API generally manipulates one object at a time; referenced objects
  (Project, Workspace, etc.) must already exist before being referenced.
  Bulk operations were added later (~2017/2018) but aren't part of the core
  read path. [Rally Web Services API](https://techdocs.broadcom.com/us/en/ca-enterprise-software/valueops/rally/rally-help/reference/rally-web-services-api.html)
- All artifacts are scoped to a Project, which is scoped to a Workspace —
  same page.

### Artifact types

- Core artifact types inherit from a common `Artifact` type: **HierarchicalRequirement**
  (= User Story in the UI), **Defect**, **Task**, **DefectSuite**, **TestCase**,
  **TestSet**. [pyral overview](https://github.com/klehman-rally/pyral/blob/master/doc/source/overview.rst)
- **PortfolioItem** is an abstract type with four standard subtypes — Theme,
  Strategy, Initiative, Feature — addressable as `PortfolioItem/Feature` etc.
  ("dyna-types"). Subscriptions can add custom PortfolioItem subtypes, which
  is a common source of ambiguity the Python toolkit calls out explicitly.
  [pyral overview](https://github.com/klehman-rally/pyral/blob/master/doc/source/overview.rst)
- Custom fields on any artifact type are exposed over WSAPI with a `c_`
  prefix on the element name (e.g. `c_MyCustomField`). **[unverified from
  primary Broadcom docs — sourced from the pyral toolkit docs, which is a
  long-standing de facto reference for WSAPI 2.0 behavior]**
  [pyral overview](https://github.com/klehman-rally/pyral/blob/master/doc/source/overview.rst)

### Get by FormattedID vs ObjectID

- **By FormattedID** (human-readable, e.g. `DE1234`, `US1234`): FormattedID is
  not unique enough on its own to be a URL path segment, so it's fetched via
  the **query** endpoint for the type:
  `GET /defect?query=(FormattedID = "DE1234")&fetch=true` (or an explicit
  fetch list). This pattern is shown consistently across toolkits.
  [Query Discussions and their artifacts](https://knowledge.broadcom.com/external/article/57581/query-discussions-and-their-artifacts.html),
  [Cox-Automotive rally-wsapi README](https://github.com/Cox-Automotive/rally-wsapi/blob/master/README.md)
- **By ObjectID**: direct path access on the typed collection,
  `GET /defect/<ObjectID>` (equivalently via the `_ref` URL returned by any
  prior query) — this is the fast path once you have the internal ID; a
  Broadcom KB article on query performance recommends using ObjectID over
  FormattedID when composing complex queries.
  [WSAPI best practices on filtering/queries](https://knowledge.broadcom.com/external/article/125586/rally-wsapi-best-practices-on-filterin.html)
- Because Sirdar's `Get(key)` contract takes a human key (`DE1234`), and Rally
  artifact types are distinct REST collections (Defect vs HierarchicalRequirement
  vs Task vs DefectSuite vs TestCase), **there is no single "artifact" endpoint
  that takes an arbitrary FormattedID across types** — the adapter must either
  know/guess the type from the FormattedID prefix (`DE`, `US`, `TA`, `TC`, …)
  or query multiple type collections. This prefix convention is widely
  documented in community/toolkit sources but I did not find a canonical
  Broadcom page enumerating the full prefix table. **[unverified from primary
  docs — treat prefix parsing as a heuristic, and confirm/override per
  workspace via config]**

### Fields relevant to Sirdar's Ticket struct

Confirmed via KB articles and toolkit docs (not a single canonical field
reference I could load):

- `Name` (title), `Description` (rich-text/HTML field, WSAPI-native), `Notes`
  (rich-text/HTML), `FormattedID`, `_ref`, `ObjectID`, `CreationDate`,
  `LastUpdateDate`, `Owner` (a User reference; references carry a
  `_refObjectName` convenience field alongside `_ref`), `Project`,
  `Iteration`, `Release`, `Tags`, `Parent`/`Feature` (portfolio hierarchy),
  `State`/`ScheduleState` (Defect uses `State`, HierarchicalRequirement uses
  `ScheduleState`), `Priority`, `Severity` (Defect-specific).
  [How to update Description or Notes field via WS API](https://knowledge.broadcom.com/external/article/57577/how-to-update-description-or-notes-field.html),
  [pyral overview](https://github.com/klehman-rally/pyral/blob/master/doc/source/overview.rst)
- `Description` and `Notes` have a **32KB size limit**; other rich-text
  fields are capped at 2KB. [How to update Description or Notes field via WS
  API](https://knowledge.broadcom.com/external/article/57577/how-to-update-description-or-notes-field.html)
- A KB article warns that WSAPI JSON payload escaping for these fields can
  fail even when the same text works fine through the UI rich-text editor —
  worth noting for anyone later writing to Rally, though Sirdar is read-only.
  [How to update Description or Notes field via WS API](https://knowledge.broadcom.com/external/article/57577/how-to-update-description-or-notes-field.html)
- I could not verify from primary docs whether `Description`/`Notes` are
  guaranteed well-formed HTML fragments vs. plain text with occasional markup
  — treat as "HTML-ish rich text, sanitize/strip before handing to the
  agent." **[unverified]**

### Query syntax

- Queries are `(LHS Operator RHS)` expressions, combinable with `AND`/`OR`,
  e.g. `(Owner.UserName = "x") AND (State != "Closed")`. Nested dotted paths
  (`Owner.UserName`) traverse references.
  [Query Parameters](https://techdocs.broadcom.com/us/en/ca-enterprise-software/valueops/rally/rally-help/reference/rally-web-services-api/query-parameters.html)
- Query parameters (from the TechDocs Query Parameters page, loaded
  directly):
  - `query` — the filter expression, URL-encoded, e.g.
    `query=(FormattedID = S40330)`
  - `fetch` — comma-separated attribute list to populate on returned objects,
    e.g. `fetch=FormattedID,Name,Project,Parent`. A Broadcom KB "data extract
    recommendations" article specifically advises **against** `fetch=true`
    (fetch everything) for volume/performance reasons — request only the
    fields you need.
    [WSAPI Data Extract Recommendations](https://knowledge.broadcom.com/external/article/127874/rally-wsapi-data-extract-recommendation.html)
  - `order` — sort spec, e.g. `order=Name desc`
  - `start` — 1-based start index, default 1
  - `pagesize` — page size; default reported as 20 in the KB/TechDocs
    summary, with **a hard cap of 200 per page** repeatedly cited across
    Broadcom KB and third-party toolkit docs (one summary mentions up to 2000
    for "v2.x" but the 200 ceiling is the number that recurs in the paging
    KB article specifically). Treat the safe assumption as **200 max**.
    [How to page in Rally WS API if TotalResultCount is above maximum limit of 2000](https://knowledge.broadcom.com/external/article/47778/how-to-page-in-ws-api-if-totalresultcoun.html),
    [Query Parameters](https://techdocs.broadcom.com/us/en/ca-enterprise-software/valueops/rally/rally-help/reference/rally-web-services-api/query-parameters.html)
  - `workspace` — scope to a workspace ref/ObjectID
  - `project` — scope to a project ref/ObjectID
  - `projectScopeUp` — include parent projects (default true)
  - `projectScopeDown` — include child projects (default true)
  [Query Parameters](https://techdocs.broadcom.com/us/en/ca-enterprise-software/valueops/rally/rally-help/reference/rally-web-services-api/query-parameters.html)
- Response envelope includes `TotalResultCount`; paging past `start=1801`
  region (i.e. beyond ~2000 total results with 200-per-page paging) requires
  narrowing the query rather than just paging further, per the KB paging
  article linked above.

### Discussion (comment thread)

- An Artifact's `Discussion` attribute is a collection of `ConversationPost`
  objects, each queryable/filterable by `Artifact` ref, with fields including
  `Text` (HTML) and `CreationDate`; posts can be fetched via a
  `conversationpost` collection endpoint or through the artifact's
  `Discussion` collection ref.
  [How to extract artifact discussions using WSAPI](https://knowledge.broadcom.com/external/article/16712/how-to-extract-artifact-discussions-usin.html),
  [Query Discussions and their artifacts](https://knowledge.broadcom.com/external/article/57581/query-discussions-and-their-artifacts.html)
- I could not directly load a canonical schema page enumerating every
  `ConversationPost` field (e.g. whether `User` on a post exposes the same
  `_refObjectName` display-name convenience as `Owner` elsewhere) — assume
  yes by WSAPI convention, but **[unverified from primary docs]**.
- This maps naturally to Sirdar's helpdesk-role thread: `{At: CreationDate,
  Author: User._refObjectName, Role: "customer"/"agent" (not exposed by
  Rally — would need external mapping or default to "unknown"), Text: Text
  (HTML, needs stripping)}`.

### Attachments

- `Attachment` objects hold metadata (name, description, size, user, content
  type) and carry a one-to-one reference to a separate `AttachmentContent`
  object that holds the actual blob as a **base64-encoded `Content`
  field**. This separation (metadata vs. blob object) is repeatedly described
  across Broadcom KB and the RallyTools export scripts.
  [Rally - Attachments: impact of large attachment files](https://knowledge.broadcom.com/external/article?articleId=100260),
  [Rally-Export-Attachments export script](https://github.com/RallyTools/Rally-Export-Attachments/blob/master/export-workspace-attachments.rb)
- Max attachment size cited is **50MB**, with the usual ~33% base64 size
  inflation over the wire on top of that.
  **[unverified from a primary Broadcom docs page — sourced from search-engine
  synthesis of results, not a page I loaded directly; corroborate against the
  current TechDocs attachments page before hardcoding a limit]**
- Retrieval pattern: get the `Attachment` (from the artifact's `Attachments`
  collection ref), follow its `Content` ref to the `AttachmentContent`
  object, fetch that object with `fetch=Content`, base64-decode the `Content`
  field.

### RevisionHistory (audit trail — read-only)

- Every `Artifact` has a `RevisionHistory` attribute pointing to a
  `RevisionHistory` object, which has a `Revisions` collection. Each
  `Revision` has `Description` (auto-generated change summary), `RevisionNumber`,
  `User` (author), and a back-reference to its parent `RevisionHistory` — but
  **no** reference from a Revision back to the originating Artifact, so
  traversal only works artifact → history → revisions, not the reverse.
  [Query Revisions in Rally WS API](https://knowledge.broadcom.com/external/article?articleId=57612)
- This is read-only by nature (Rally generates revisions automatically on
  writes) — fits Sirdar's read-only adapter model well as an optional
  "history" source, though it's not one of the fields Sirdar's `Ticket`
  struct currently models.

---

## 2. Authentication

- **API Keys** are the recommended mechanism. Created under **My Settings →
  Access, API Key → Create**, with a grant type of either "Full Access" or
  "ALM WSAPI Read-only" — the read-only grant is a good fit for Sirdar's
  read-only adapter contract. Keys don't expire, obey the creating user's
  permissions, aren't tied to an additional license, and can be scoped/limited
  by subscription admins. Only the creator can view the key value after
  creation.
  [API Keys — Rally TechDocs](https://techdocs.broadcom.com/us/en/ca-enterprise-software/valueops/rally/rally-help/administration/it-administration/how-users-authenticate/rally-authentication-features/api-keys.html)
- **Header**: the API key (or a session token from login) is sent as the
  `ZSESSIONID` header value on WSAPI requests. This is consistently confirmed
  across Broadcom KB articles and independent toolkits (including the Go
  client below).
  [Rally: Use API Key with cURL](https://knowledge.broadcom.com/external/article/57528/rally-use-api-key-with-curl.html),
  [Rally WSAPI: API Key and OAuth Client FAQ](https://knowledge.broadcom.com/external/article/11568/rally-wsapi-api-key-and-oauth-client-faq.html)
- **Basic auth**: a Broadcom KB article states basic (username/password)
  credential auth **has been deprecated in production** in favor of API
  keys, but remains necessary/available for sandbox environments and
  on-premises appliances. Toolkits (e.g. the Java REST toolkit) have marked
  their basic-auth constructors deprecated accordingly.
  [Rally - WSAPI: Not authorized to perform action: Invalid key](https://knowledge.broadcom.com/external/article/127054/rally-wsapi-not-authorized-to-perform-a.html)
  → Adapter should support API key only; don't build basic-auth as a
  fallback path for production subscriptions.
- API keys are **not supported** on `sandbox.rallydev.com` and **not
  supported on-premises** — those still require basic auth.
  [Rally WSAPI: API Key and OAuth Client FAQ](https://knowledge.broadcom.com/external/article/11568/rally-wsapi-api-key-and-oauth-client-faq.html)
- **Security tokens** (CSRF-style tokens) matter only for write/mutation
  requests under basic auth; irrelevant to a read-only, API-key-based
  adapter. **[stated in KB summaries, not independently re-verified against
  a loaded primary page — low risk since Sirdar doesn't write]**
- **SSO**: subscriptions can route login through SSO; a KB article addresses
  users unexpectedly landing on the native Rally login instead of SSO, which
  implies SSO is common in enterprise subscriptions, but this affects
  interactive login, not API-key-based WSAPI calls, which bypass SSO
  entirely. [Rally: Users seeing Rally login page instead of SSO](https://knowledge.broadcom.com/external/article/202175/rally-users-seeing-rally-login-page-acce.html)
  **[relevance to adapter unverified beyond this inference]**
- **Rate limiting**: a Broadcom KB article states there's no hard cap on
  total requests per hour/day, but the service **throttles a user to 12
  simultaneous (concurrent) requests** — exceeding that causes subsequent
  requests to slow down rather than being rejected outright.
  [Rally - WSAPI: Are there any limitations to the number of API calls?](https://knowledge.broadcom.com/external/article/95713/rally-wsapi-are-there-any-limitations-t.html)
  → Adapter should serialize or cap concurrency well under 12 for a given
  API key/user, and expect slowdown (not necessarily HTTP 429) as the
  throttle signal — confirm actual HTTP status/behavior empirically since I
  could not load the article's full body directly to check.
  **[partially unverified — concurrency number confirmed via KB title/summary,
  exact throttle response behavior not independently verified]**

---

## 3. MCP server and AI integrations (2025–2026)

This is the standout finding: Broadcom has shipped an **official Rally MCP
server**, which is a different thing from the host-side Go adapter Sirdar
needs, but changes the "is there prior art" picture significantly.

- **Official, Broadcom-built.** Documented at TechDocs under "AI in Rally"
  and "Enable a Model Context Protocol (MCP) Server."
  [AI in Rally](https://techdocs.broadcom.com/us/en/ca-enterprise-software/valueops/rally/rally-help/reference/ai.html),
  [Access an MCP Server](https://techdocs.broadcom.com/us/en/ca-enterprise-software/valueops/rally/rally-help/reference/ai/enable-a-model-context-protocol--mcp--server-/access-an-mcp-server.html)
- **General availability announced 2026-03-22** per a third-party coverage
  post (Custom Agile blog); Broadcom's own TechDocs page for MCP access shows
  a "Last Updated" stamp near the present date, consistent with active,
  current documentation rather than a stale/beta page.
  [Rally's MCP Server Is Now Generally Available – Custom Agile](https://www.customagile.com/blog/rally-mcp-server-now-generally-available)
  **[the exact GA date is sourced from a third-party blog, not a Broadcom
  press release I loaded directly — treat the date as approximate/unverified]**
- **Endpoints**: `https://mcp.rallydev.com/mcp` (North America) and
  `https://mcp-eu.rallydev.com/mcp` (EMEA); versioned endpoints also exist
  per the docs (not enumerated in what I could load).
  [Access an MCP Server](https://techdocs.broadcom.com/us/en/ca-enterprise-software/valueops/rally/rally-help/reference/ai/enable-a-model-context-protocol--mcp--server-/access-an-mcp-server.html)
- **Auth**: API key or OAuth client (with callback URL config); requests run
  as the authenticated user, so permissions are inherited from that
  user/key — same page.
- **Enablement is opt-in per subscription**: a Subscription Admin must open a
  support ticket with Broadcom to turn it on; it's not a self-service
  toggle in Rally settings.
  [Rally's MCP Server Is Now Generally Available – Custom Agile](https://www.customagile.com/blog/rally-mcp-server-now-generally-available)
- **Capability scope**: read/query artifacts (work items, sprint stories,
  acceptance criteria), create/update stories and defects, and a
  `get-current-rally-user` sanity-check command; can switch between
  workspaces but operates on one workspace at a time. Broadcom is explicit
  that it covers "the most common workflows," not the full WSAPI surface.
  [Access an MCP Server](https://techdocs.broadcom.com/us/en/ca-enterprise-software/valueops/rally/rally-help/reference/ai/enable-a-model-context-protocol--mcp--server-/access-an-mcp-server.html),
  [Rally's MCP Server Is Now Generally Available – Custom Agile](https://www.customagile.com/blog/rally-mcp-server-now-generally-available)

**MCP-for-the-agent vs. host-side adapter (Sirdar's actual need):** Rally's
MCP server is designed to be pointed at directly by an *agent's* MCP client
(e.g. wired into Cursor or Claude Code), giving the agent live read/write
tool-calls into Rally itself — that's a different architecture from Sirdar's
model, where a Go adapter fetches a ticket **host-side** and hands a static,
pre-fetched `Ticket` struct to the agent. Sirdar deliberately doesn't want the
agent to have live, wide-scoped API access to the tracker; it wants a
narrow, read-only, host-controlled fetch. So Rally's MCP server is not a
substitute for a Sirdar adapter — if anything, its existence is evidence
Broadcom expects WSAPI to remain the durable integration surface (the MCP
server is very likely built as a thin layer over WSAPI itself, though I found
no Broadcom statement confirming that **[unverified]**). No other MCP servers
for Rally (community or generic) turned up in search results beyond this
official one.

---

## 4. Rally ↔ helpdesk linkage (for `HelpdeskRef`)

- Rally historically shipped/supported connectors, including for **Zendesk**
  and **ServiceNow**, under the "CA Agile Central Integrations /
  Connectors" documentation set.
  [CA Agile Central Integrations](https://docs.ca.com/en-us/ca-agile-central/saas/connectors)
- `help.rallydev.com/zendesk` redirects to a general Broadcom Rally landing
  page rather than a live Zendesk-specific doc, suggesting the dedicated
  Zendesk connector doc may have been retired or folded into general
  integration docs. **[confirmed only as a redirect target — could not load
  connector specifics from the destination page]**
- Third-party/marketplace connectors are more active in current search
  results than an official one: **ConnectALL** and **OpsHub** both list
  Rally↔ServiceNow (and broader ITSM) sync products, and a Broadcom KB
  article directly answers "is there an integration with ServiceNow?" —
  confirming ServiceNow integration exists but via connector product(s)
  rather than a first-party built-in field.
  [ConnectALL Rally integration](https://www.connectall.com/integration/rally-software/),
  [ServiceNow & Rally Integration Connector](https://www.quantumwhisper.com/servicenow-agile-central-rallydev-integration),
  [Rally - ServiceNow: Is there an integration?](https://knowledge.broadcom.com/external/article/123105/rally-servicenow-is-there-an-integratio.html)
- **Mechanism for deriving `HelpdeskRef`**: none of these connectors are
  documented (in what I could load) as writing to one single guaranteed
  field. In practice, cross-linking is almost always done via either (a) a
  **custom field** (`c_ZendeskTicketID`, `c_ServiceNowIncident`, etc.,
  connector- or org-specific) or (b) a **Hyperlink-style field** or a plain
  URL embedded in `Description`/`Notes`/a `Tags` value pointing at the
  helpdesk ticket. Rally's `c_`-prefixed custom-field convention (§1) is the
  general extension point these connectors would use.
  **[the specific field name/shape is unverified — it is connector- and
  org-configuration-dependent, not a fixed WSAPI schema element]**
- **Recommendation for the adapter**: don't assume a fixed field name.
  Make `HelpdeskRef` derivation configurable — e.g. a configured custom-field
  name to read (`c_ZendeskTicketID`), or a regex applied to `Description`/
  `Notes`/a specific Tag — since the actual linkage is set up per Rally
  subscription's connector configuration, not by WSAPI itself.

---

## 5. Webhooks (`Rally Webhooks API`)

- Base: `https://rally1.rallydev.com/apps/pigeon/api/v2` (note: a distinct
  service path — `apps/pigeon` — from the core WSAPI `slm/webservice/v2.0`
  path). [Webhooks API — TechDocs](https://techdocs.broadcom.com/us/en/ca-enterprise-software/valueops/rally/rally-help/reference/rally-webhooks/webhooks-api.html)
- Methods: `POST /webhook` (create), `GET /webhook` (list, paginated),
  `GET /webhook/{id}`, `PATCH /webhook/{id}` (update), `DELETE /webhook/{id}`
  — same page.
- Config object fields: `AppName`, `AppUrl`, `Name`, `TargetUrl`,
  `ObjectTypes` (array — e.g. `HierarchicalRequirement`, `Defect`),
  `WebhookFormat` (`attribute_name` seen as an option), and `Expressions`
  (filter conditions as `AttributeName`/`Operator`/`Value` triples) — same
  page.
- **Auth**: API key or `ZSESSIONID` cookie; explicitly **no HTTP Basic**
  support on the webhooks API; unauthenticated calls get 401 — same page.
- I could not load a page enumerating the actual payload/event schema
  delivered to `TargetUrl` (a "Webhooks Payload" TechDocs page exists per
  search results but wasn't fetched directly) — **[payload shape
  unverified]**: [Webhooks Payload](https://techdocs.broadcom.com/us/en/ca-enterprise-software/valueops/rally/rally-help/reference/rally-webhooks/webhooks-payload.html)
- Fits Sirdar's "future trigger" note well: webhook → your own receiver →
  translate to a fetch-by-key call against the adapter, same pattern as
  other trackers, rather than trusting webhook payload content directly.

---

## 6. Existing clients (prior art for the adapter)

- **Go**: no first-party Broadcom Go SDK. Community options found:
  - [Comcast/rally-rest-toolkit](https://github.com/Comcast/rally-rest-toolkit) —
    small Go client; confirmed by reading `rallyclient.go` directly: auths via
    `ZSESSIONID` header, builds URLs as `{apiurl}/{queryType}/{objectID}`,
    supports Query/Get/Create/Update/Delete. Notable quirks worth avoiding in
    a fresh implementation: it uses POST for updates instead of PUT, always
    forces `fetch=true` (contrary to Broadcom's own performance guidance —
    see §1), and swallows HTTP/read errors silently (returns `nil` even on
    failure) — don't copy those patterns.
  - [abourget/rally](https://github.com/abourget/rally) — "Golang bindings to
    the Rally API," not independently inspected beyond the listing.
    **[unverified — not opened]**
  - [ThomasBS/rally](https://github.com/ThomasBS/rally) — Go wrapper,
    reportedly does not support basic auth per its listing.
    **[unverified — not opened]**
  - [mcaulfield/github2rally](https://pkg.go.dev/github.com/mcaulfield/github2rally/rally) —
    narrow Go package for querying/creating defects and users, part of a
    GitHub↔Rally sync tool, not a general client.
  - None of these appear actively maintained or canonical enough to depend on
    as a library; confirms the plan to hand-roll a small stdlib `net/http`
    adapter is reasonable and in line with Sirdar's existing adapter pattern.
- **Python — `pyral`**: the long-standing reference implementation
  (RallyTools/RallyRestToolkitForPython, plus an actively-forked
  `klehman-rally/pyral`). Notable behavior worth mirroring conceptually:
  - Transparently paginates past the 200-row page cap.
  - "Chases" reference URLs (e.g. `Owner`, `Project`) to resolve
    human-readable names rather than exposing bare `_ref`/ObjectID — this
    matches what Sirdar wants for `Assignee`/`Owner` (use `_refObjectName`
    directly from the WSAPI response instead of a second round-trip, since
    WSAPI already returns that convenience field on refs; no need to
    replicate pyral's extra HTTP chase).
  - Strips the `c_` prefix for custom fields in its Python-facing API,
    fitting Sirdar's `Fields map` concept well (store custom fields keyed by
    their human name, prefix elsewhere).
  - Recommends `requests-2.32.x` or newer — irrelevant to a Go rewrite, but
    signals nothing exotic about TLS/HTTP requirements.
  [pyral overview (readthedocs)](https://pyral.readthedocs.io/en/latest/overview.html),
  [pyral overview.rst (GitHub, klehman-rally fork)](https://github.com/klehman-rally/pyral/blob/master/doc/source/overview.rst)
  - **Support status**: per a PyPI project description surfaced in search
    results, `pyral` "no longer has active support as of Dec 31, 2025" —
    worth noting since it's the most commonly cited reference implementation
    and is now effectively frozen/community-maintained only.
    **[unverified against the PyPI page directly — sourced from search
    summary]**
- **Recommendation**: write the adapter directly against WSAPI with Go's
  `net/http` + `encoding/json`, per Sirdar's existing stdlib-only convention.
  No community Go client is worth taking as a dependency; the WSAPI surface
  needed (get-by-query, fetch fields, discussion, attachment content) is
  small enough to implement directly and matches the read-only, credential-
  via-env-or-keychain, no-third-party-deps pattern the other adapters follow.

---

## Recommended adapter design

**Config keys**

- `base_url` (default `https://rally1.rallydev.com/slm/webservice/v2.0`;
  overridable per-subscription/region — see §1 EU-host caveat)
- `api_key_ref` — env var or keychain reference resolving to the Rally API
  key (grant type: ALM WSAPI Read-only, created per §2)
- `workspace_ref` — Workspace `_ref` or ObjectID to scope all queries to
  (required; Rally is multi-workspace and unscoped queries are ambiguous/slow)
- `project_ref` — optional Project `_ref`/ObjectID to further scope
- `project_scope_down` — bool, default true, passed through as
  `projectScopeDown`
- `artifact_types` — ordered list of WSAPI type collections to try when
  resolving a bare key (e.g. `["defect", "hierarchicalrequirement", "task",
  "testcase", "defectsuite"]`), since FormattedID prefixes aren't a
  guaranteed/documented mapping (§1) — try in order, or let config map a
  known prefix (`DE`, `US`, `TA`, `TC`, `DS`) straight to a type to avoid
  multiple round-trips per `Get`
- `helpdesk_ref_field` — name of the custom field (or a small strategy enum:
  `custom_field` / `tag_regex` / `description_regex`) used to derive
  `HelpdeskRef`, since this is connector/org-specific (§4)
- `max_concurrency` — cap well under Rally's 12-simultaneous-request throttle
  (§2); default something conservative like 4

**Get by FormattedID (tracker role)**

1. Determine candidate type(s) from config (prefix map or configured
   ordered list).
2. For each candidate type, `GET {base_url}/{type}?workspace={workspace_ref}
   &query=(FormattedID = "{key}")&fetch=Name,Description,Notes,Priority,
   Severity,State,ScheduleState,Owner,Project,Iteration,Release,Tags,Parent,
   CreationDate,LastUpdateDate,FormattedID,ObjectID` with `ZSESSIONID`
   header set to the API key.
3. Stop at first non-empty `QueryResult.Results`.
4. Map response to Sirdar's `Ticket`: `Key = FormattedID`, `Title = Name`,
   `Description` = HTML-stripped `Description`, `Priority = Priority`,
   `Status = State` or `ScheduleState` depending on type, `Assignee =
   Owner._refObjectName`, `URL` = construct from `_ref` or the app URL
   pattern (Rally app URLs are of the form
   `https://rally1.rallydev.com/#/detail/{type}/{ObjectID}` — **[exact app
   URL pattern unverified from primary docs; confirm against a live
   subscription before hardcoding]**), `CreatedAt = CreationDate`,
   `UpdatedAt = LastUpdateDate`, `Fields` = remaining WSAPI attributes plus
   any `c_`-prefixed custom fields (strip the prefix for the map key,
   per pyral's convention).

**List by filter (tracker role)**

- Same query endpoint with a caller-supplied filter translated into WSAPI
  query syntax (`(Owner.UserName = "x") AND (State != "Closed")`), paged via
  `start`/`pagesize` (cap `pagesize` at 200), scoped by `workspace`/`project`/
  `projectScopeDown`. Explicitly avoid `fetch=true` per Broadcom's own
  performance guidance (§1) — always pass an explicit fetch list matching
  the `Ticket` struct's needs.

**Thread / attachments (helpdesk role, optional)**

- Thread: fetch the artifact's `Discussion` collection (or query
  `conversationpost?query=(Artifact = "{artifact _ref}")`), map each post to
  `{At: CreationDate, Author: User._refObjectName, Role: "unknown" (Rally
  doesn't distinguish customer/agent — see §1), Text: HTML-stripped Text,
  AttachmentIDs: nil}` (Rally discussion posts don't carry attachments
  separately from the artifact's own `Attachments` collection, per what I
  could verify — **[unverified]**).
- Attachments: fetch the artifact's `Attachments` collection, for each
  `Attachment` follow `Content` ref to `AttachmentContent`, `GET` with
  `fetch=Content,ContentType,Name`, base64-decode `Content`. Enforce a
  size guard around the ~50MB figure noted in §1 (unverified precisely —
  confirm/adjust once tested against a real subscription) before decoding
  into memory.

**HTML handling**

- `Description`, `Notes`, and discussion `Text` are rich-text/HTML fields
  (§1). Strip to plain text (or pass through as sanitized HTML, matching
  whatever convention Sirdar's other helpdesk-capable adapters use) before
  populating `Ticket.Description` / thread `Text` — don't hand raw
  Rally-authored HTML straight to the coding agent unfiltered.

---

## Open questions

1. **Regional WSAPI base URL**: is `rally1.rallydev.com` universal for WSAPI
   regardless of subscription region, or do EU/other subscriptions get a
   distinct WSAPI host (as they apparently do for the MCP server)? Needs
   checking against an actual non-US subscription or a Broadcom
   account rep — not resolved from public docs. (§1)
2. **FormattedID → type mapping**: is there a documented, stable prefix
   convention (`DE`→Defect, `US`→HierarchicalRequirement, `TA`→Task,
   `TC`→TestCase, `DS`→DefectSuite) guaranteed across all subscriptions, or
   is it configurable/subscription-specific? If a canonical table exists in
   Broadcom docs I didn't locate it — worth a follow-up search restricted to
   `techdocs.broadcom.com` with a Broadcom account, since some pages appear
   to sit behind a login/paywall that blocked WebFetch. (§1)
3. **Exact attachment size ceiling and AttachmentContent fetch limits** —
   the 50MB figure needs confirming against a current, directly-loaded
   TechDocs page rather than search synthesis. (§1)
4. **Concurrency throttle behavior** — does Rally return HTTP 429, or just
   silently slow down responses, when the 12-simultaneous-request ceiling is
   exceeded? Matters for the adapter's retry/backoff logic. (§2)
5. **`HelpdeskRef` derivation** — there's no single guaranteed WSAPI field;
   confirm with an actual target Rally subscription which connector (if any)
   is in use and what custom field/pattern it writes, then set
   `helpdesk_ref_field` accordingly per deployment. (§4)
6. **Webhook payload schema** — didn't load
   `rally-help/reference/rally-webhooks/webhooks-payload.html` directly;
   needed before building the future webhook-triggered receiver. (§5)
7. **MCP server as data-plane, not just agent tool-calls** — worth checking
   whether Broadcom's MCP server is documented as sitting directly on WSAPI
   (in which case its tool list is a good sanity-check for "what fields does
   Broadcom itself consider the important ones") — I found no such
   confirmation, only inference. (§3)
8. Several Broadcom TechDocs pages I attempted returned 404 (e.g. the first
   WSAPI overview URL under the `ca-enterprise-software/valueops/rally/
   rally-help/integrating-with-rally/...` tree) even though the same path
   worked when reached through a different entry point/redirect — Broadcom's
   docs site structure appears to be mid-migration or has stale internal
   links; a next pass should start from the current TechDocs Rally landing
   page (`https://techdocs.broadcom.com/us/en/ca-enterprise-software/valueops/rally.html`
   or equivalent) and crawl subpages from there rather than trusting
   individual deep links found via search.
