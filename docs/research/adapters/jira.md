# Jira adapter research

Scope: a read-only Sirdar adapter for Jira as a **tracker** (issue get/list) and, where the
instance runs Jira Service Management (JSM), as a **helpdesk** (customer conversation thread +
attachments). Covers Jira Cloud and Jira Data Center/Server. All claims are sourced; anything I
couldn't verify against a primary or credible secondary source is flagged as unverified.

## 1. REST API

### Base paths

- Jira Cloud platform REST API is versioned; the current version is v3, reached at
  `/rest/api/3/...` on the site's own base URL (`https://your-domain.atlassian.net`) or via
  `https://api.atlassian.com/ex/jira/{cloudId}/rest/api/3/...` when using OAuth 2.0 (3LO)
  ([Jira Cloud platform REST API intro](https://developer.atlassian.com/cloud/jira/platform/rest/v3/intro/)).
- Jira Cloud v2 (`/rest/api/2/...`) is still live and fully supported in parallel with v3 —
  Atlassian has repeatedly said they'll keep developing both "until such time as we tell you
  otherwise." The only functional difference is how rich-text fields are represented (see ADF
  below), not endpoint coverage
  ([Jira Cloud Platform REST API v2](https://developer.atlassian.com/cloud/jira/platform/rest/v2/intro/);
  [Atlassian Developer Community thread on v2→v3 migration](https://community.developer.atlassian.com/t/addon-migration-from-jira-cloud-rest-v2-to-v3/26986)).
- Jira Data Center/Server exposes `/rest/api/2/...` as its REST API; Data Center has not adopted
  ADF or a v3 API — it remains wiki-markup for rich text
  ([Atlassian Community: "Is there a plan to support REST API v3 / ADF in Jira Server?"](https://community.atlassian.com/forums/Jira-questions/Is-there-a-plan-to-support-REST-API-v3-ADF-in-Jira-Server/qaq-p/988943)).

### Get issue by key

`GET /rest/api/3/issue/{issueIdOrKey}` (Cloud) or `GET /rest/api/2/issue/{issueIdOrKey}` (DC)
returns the issue with its `fields` map. Adding `?expand=renderedFields` requests HTML-rendered
versions of rich-text fields alongside the raw ones
([Jira Cloud Rest API — Issues group](https://developer.atlassian.com/cloud/jira/platform/rest/v3/api-group-issues/)).

Two caveats worth designing around:

- On Cloud v3, `expand=renderedFields` does **not** reliably render comment bodies — it can
  return raw ADF instead of HTML for comments even though the issue description itself renders
  correctly. This is a documented, longstanding Jira bug, not adapter error
  ([JRACLOUD-75825](https://jira.atlassian.com/browse/JRACLOUD-75825);
  [Atlassian Developer Community thread](https://community.developer.atlassian.com/t/jira-cloud-rest-api-v3-get-issue-does-not-render-adf-comment-bodies/46624)).
- On Cloud, `description` (and other rich-text fields) is Atlassian Document Format (ADF) — a
  nested JSON document, not a plain string — when fetched via v3. The **same field fetched via
  v2** comes back as plain wiki markup text instead, because v2 is a compatibility layer that
  converts to/from ADF under the hood
  ([Jira Cloud REST API v2 intro](https://developer.atlassian.com/cloud/jira/platform/rest/v2/intro/)).
  This means an adapter that wants plain text/wiki markup without writing an ADF walker can
  simply call the **v2** endpoint on Cloud instead of v3 — full field/expand parity, different
  text representation. On DC, `/rest/api/2/issue/{key}` already returns wiki markup natively;
  there is no ADF version to choose between.

### Search (JQL)

This is the most consequential recent change for any new Jira client:

- Atlassian announced on 2024-10-31 that the legacy search endpoints
  (`GET`/`POST /rest/api/3/search`, and the `/2` and `/latest` equivalents) would be removed on
  **Jira Cloud**, with the shutdown process completing by end of August 2025. Cloud now returns
  **`410 Gone`** on those endpoints
  ([Adaptavist: Atlassian REST API Search Endpoints Deprecation](https://docs.adaptavist.com/sr4jc/latest/release-notes/breaking-changes/atlassian-rest-api-search-endpoints-deprecation);
  [go-atlassian issue #345](https://github.com/ctreminiom/go-atlassian/issues/345);
  [devlake issue reporting the 410](https://github.com/apache/incubator-devlake/issues/8544)).
- The replacement is **`POST /rest/api/3/search/jql`** (and `/2/search/jql`, `/latest/search/jql`).
  It takes `jql`, `fields`, `expand`, `maxResults` in the body. Pagination changed from
  `startAt`/`total` to a cursor: the response includes `nextPageToken`, which you pass back in
  the next request; there is no longer a `total` count in the standard response
  ([go-atlassian issue #345](https://github.com/ctreminiom/go-atlassian/issues/345);
  [Atlassian Community: Optimizing Data Retrieval with /rest/api/3/search/jql Pagination](https://community.atlassian.com/forums/Jira-questions/Optimizing-Data-Retrieval-with-rest-api-3-search-jql-Pagination/qaq-p/2957628)).
  The `validate` query parameter that the old search had has no replacement on the new endpoint
  ([go-atlassian issue #345](https://github.com/ctreminiom/go-atlassian/issues/345)).
- This deprecation is **Cloud-only**. Jira Data Center/Server's `/rest/api/2/search` is
  unaffected and continues to use the classic `startAt`/`maxResults`/`total` pagination — DC has
  its own multi-year deprecation cadence separate from Cloud's
  ([Atlassian Community: "When are JQL search endpoints /rest/api/2/search and /rest/api/3/search
  really being removed?"](https://community.atlassian.com/forums/Jira-questions/When-are-JQL-search-endpoints-rest-api-2-search-and-rest-api-3/qaq-p/3029221)).
- Real-world reports say the new `nextPageToken` mechanism has rough edges: some integrations
  have hit tokens that loop back to page one instead of advancing, and general reports of
  "intermittent problems" with cursor-based search
  ([Atlassian Community: "REST: The new /rest/api/3/search/jql endpoint is a complete disaster"](https://community.atlassian.com/forums/Jira-questions/REST-The-new-rest-api-3-search-jql-endpoint-is-a-complete/qaq-p/3101716);
  [atlassian-mcp-server issue #118](https://github.com/atlassian/atlassian-mcp-server/issues/118);
  [sooperset/mcp-atlassian issue #1295, "jira_search returns empty results — deprecated GET /search
  (410)"](https://github.com/sooperset/mcp-atlassian/issues/1295)). An adapter should treat a
  `nextPageToken` that repeats as a hard stop (cap total pages fetched) rather than trust it
  unconditionally.

### Comments

`GET`/`POST /rest/api/3/issue/{issueIdOrKey}/comment` (v2 equivalent on DC and Cloud-v2). On
Cloud v3, comment `body` is ADF (a `{"type":"doc","version":1,"content":[...]}` document); a
plain string body is rejected with a 400
([Atlassian Community: "JIRA Cloud V3 API: Issue creating comment"](https://community.atlassian.com/forums/Jira-questions/JIRA-Cloud-V3-API-Issue-creating-comment/qaq-p/940351);
[Jira Cloud Rest API — Issue comments group](https://developer.atlassian.com/cloud/jira/platform/rest/v3/api-group-issue-comments/)).
As noted above, `expand=renderedFields`/rendered comment bodies are unreliable on v3, so for a
read-only adapter, fetching comments via v2 (plain text) is the simpler path on Cloud; on DC,
v2 is the only option and already returns wiki markup.

### Attachments

`fields.attachment[]` on the issue payload carries each attachment's metadata, including a
`content` URL to download the binary
([Jira Cloud Rest API — Issue attachments group](https://developer.atlassian.com/cloud/jira/platform/rest/v3/api-group-issue-attachments/)).
Two authentication subtleties matter for a stdlib-HTTP adapter:

- `X-Atlassian-Token: no-check` is required when **uploading** an attachment (it defeats Jira's
  XSRF check on multipart POSTs) — it is not relevant to downloads
  ([Atlassian Support: how to add an attachment via REST API](https://support.atlassian.com/jira/kb/how-to-add-an-attachment-to-a-jira-cloud-issue-using-rest-api/)).
- **Downloading** is the trickier side. On Cloud, `fields.attachment[].content` is generally
  fetchable with the same Basic-auth header (email + API token) used for the rest of the REST
  API. On Data Center/Server, attachment download URLs (`/secure/attachment/{id}/{filename}`)
  are served by the web application layer rather than the REST layer, and several
  Atlassian-support and community threads report that a **Personal Access Token in the
  `Authorization: Bearer` header does not reliably authenticate against that URL** — the request
  can get redirected to an SSO/login page instead of the file. The documented workaround is to
  authenticate once (Basic auth or cookie-based login) to obtain a session cookie and use that
  cookie jar for the download request
  ([Atlassian Support: How to download attachments using REST API and SSO](https://support.atlassian.com/jira/kb/how-to-download-attachments-using-rest-api-and-sso/);
  [Atlassian Community: "Personal Access token is not effective to authenticate to get
  attachments"](https://community.atlassian.com/forums/Jira-questions/Personal-Access-token-is-not-effective-to-authenticate-to-get/qaq-p/1580163);
  [JRASERVER-72019](https://jira.atlassian.com/browse/JRASERVER-72019)). This is a real risk for
  a stdlib-only, cookie-free adapter design on DC and should be flagged as an edge case (see
  §7) — it may need a fallback to session-cookie auth specifically for the attachment-download
  code path even when PAT/Bearer works everywhere else.

### Changelog / transitions (read-only history)

Not deep-dived beyond confirming it's a standard, documented part of the issue resource
(`?expand=changelog`, and `/issue/{key}/transitions` for the *available* transitions) — this is
uncontroversial and I did not find anything version- or deprecation-relevant here, so it's not
elaborated further; **unverified in depth** beyond that it exists in both API versions.

### Jira Service Management (JSM) specifics

JSM "requests" are Jira issues underneath — the same issue key is addressable both via the
plain Jira issue API and via the Service Management API, which adds customer-facing shape on
top. This matters directly for Sirdar's dual tracker/helpdesk role, because for Jira+JSM the
tracker and the helpdesk may be *the same issue*, not two systems joined by a reference (see §4).

- Request detail: `GET /rest/servicedeskapi/request/{issueIdOrKey}` on Cloud
  ([Jira Service Management Cloud REST API — request group](https://developer.atlassian.com/cloud/jira/service-desk/rest/api-group-request/));
  the DC/Server equivalent is documented under
  [`api-group-customer-request`](https://developer.atlassian.com/server/jira-servicedesk/rest/v1005/api-group-customer-request/).
- Comments: `/rest/servicedeskapi/request/{issueIdOrKey}/comment`, with a boolean `public` field
  distinguishing a customer-visible comment from an internal (agent-only) one. A customer caller
  of the API only ever sees public comments; an agent/licensed caller sees both
  ([Atlassian dev docs, summarized via web search](https://developer.atlassian.com/cloud/jira/service-desk/rest/api-group-request/)).
  This is the natural mapping for Sirdar's `Thread`/`Message.Role` (customer vs agent) and for
  deciding which comments are safe to hand to a coding agent versus internal-only chatter.
- Participants: `GET /rest/servicedeskapi/request/{issueIdOrKey}/participant` lists the people
  copied on the request, with `POST`/`DELETE` to manage them
  ([go-atlassian docs: Participants](https://docs.go-atlassian.io/jira-service-management/request/participants)).
- Detecting whether a given Jira project/issue is JSM-backed: a project fetched via the platform
  API carries `projectTypeKey: "service_desk"` for JSM projects
  ([Atlassian Developer Community: "Distinguish JIRA Service Desk and other JIRA project
  types"](https://community.developer.atlassian.com/t/distinguish-jira-service-desk-and-other-jira-project-types/3945)).
  There's a 1:1 mapping between a Service Desk and a project, and `GET
  /rest/servicedeskapi/servicedesk` lets you resolve a project to its `serviceDeskId`
  ([Atlassian Support: Find the service desk ID for your JSM Cloud project](https://support.atlassian.com/jira/kb/find-the-service-desk-id-for-your-jira-service-management-cloud-project/)).

## 2. Auth

- **Cloud, API token (Basic auth)**: `Authorization: Basic base64(email:api_token)`, generated
  from id.atlassian.com. This is the simplest, most widely documented path and works against
  both v2 and v3
  ([Basic auth for REST APIs — Jira Cloud platform](https://developer.atlassian.com/cloud/jira/platform/basic-auth-for-rest-apis/)).
- **Cloud, OAuth 2.0 (3LO)**: authorization-code grant through a browser consent screen; scopes
  are declared per-app and enforced alongside (not instead of) Jira/JSM's own permission model.
  Relevant read scopes: `read:jira-work` (issues, projects, etc.), `read:jira-user` (user
  profiles), and `read:servicedesk-request` (JSM requests)
  ([Jira scopes for OAuth 2.0 (3LO) and Forge apps](https://developer.atlassian.com/cloud/jira/platform/scopes-for-oauth-2-3LO-and-forge-apps/);
  [JSM scopes for OAuth 2.0 (3LO) and Forge apps](https://developer.atlassian.com/cloud/jira/service-desk/scopes-for-oauth-2-3LO-and-forge-apps/)).
  Atlassian's own guidance is to prefer "classic" scopes and to consult the REST API docs
  operation-by-operation to build the scope list
  ([Determining the Scopes Required for an Operation](https://developer.atlassian.com/cloud/oauth/getting-started/determining-scopes/)).
  With 3LO, calls are made to `https://api.atlassian.com/ex/jira/{cloudId}/...` rather than the
  site's own domain, and the `cloudId` for a given site is resolved via `GET
  https://api.atlassian.com/oauth/token/accessible-resources` using the access token — there is
  no other supported way to derive it from the token itself
  ([Making Calls to API](https://developer.atlassian.com/cloud/oauth/getting-started/making-calls-to-api/);
  [Atlassian Community: "How to identify the site (cloudId) from an OAuth 2.0 (3LO) access
  token?"](https://community.atlassian.com/forums/Jira-questions/How-to-identify-the-site-cloudId-from-an-OAuth-2-0-3LO-access/qaq-p/3022480)).
  OAuth 3LO is a much heavier lift for a headless Go adapter (needs a redirect URI, a consent
  step, and refresh-token handling) than an API token, and given Sirdar's read-only,
  single-operator design, an API token is the pragmatic default; 3LO is worth supporting only if
  multi-user/delegated access becomes a requirement.
- **Data Center/Server, Personal Access Tokens (PAT)**: `Authorization: Bearer <token>`,
  available since Jira Server/Data Center 8.14.0; a PAT inherits the creator's own permissions
  ([Using Personal Access Tokens — Atlassian Confluence docs](https://confluence.atlassian.com/enterprise/using-personal-access-tokens-1026032365.html);
  [JRASERVER-67869: Ability to generate API token](https://jira.atlassian.com/browse/JRASERVER-67869)).
  As noted in §1, PAT Bearer auth is reliable against `/rest/api/2/...` but can be unreliable
  against the attachment-download URL specifically.
- **Rate limits**: Cloud enforces three independent limiting mechanisms simultaneously — an
  hourly points-based quota (most GETs on core objects cost 1 point; identity/user endpoints cost
  more; the default Global Pool tier is roughly 65,000 points/hour shared across tenants),
  per-second burst limits on individual endpoints, and per-issue write-rate limits. On any
  breach, Jira returns `429` with `Retry-After` (seconds to wait), `X-RateLimit-Limit`,
  `X-RateLimit-Remaining`, `X-RateLimit-Reset` (on 429 responses), and a `RateLimit-Reason`
  header identifying which of the three limits tripped
  ([Rate limiting — Jira Cloud platform](https://developer.atlassian.com/cloud/jira/platform/rate-limiting/)).
  Atlassian's own recommendation is: honor `Retry-After` as a floor, then exponential backoff
  with jitter, capped at a handful of retries, and to react differently by `RateLimit-Reason`
  (back off entirely on quota exhaustion; only throttle the specific endpoint on a burst limit)
  ([same source](https://developer.atlassian.com/cloud/jira/platform/rate-limiting/)). Data
  Center has its own, separately configured, admin-controlled rate limiting
  ([Improving instance stability with rate limiting — Confluence docs](https://confluence.atlassian.com/adminjiraserver/improving-instance-stability-with-rate-limiting-983794911.html)),
  which a read-only adapter mostly just needs to honor the same `Retry-After`-and-backoff pattern
  for, without assuming the specific headers match Cloud's.

## 3. Atlassian MCP

Two distinct options exist, and they answer different questions.

### Official Atlassian Remote MCP Server (`mcp.atlassian.com`)

Atlassian's own hosted, remote MCP server, now GA, connecting Jira, Confluence, JSM, Bitbucket,
and Compass to any MCP-capable client
([atlassian/atlassian-mcp-server on GitHub](https://github.com/atlassian/atlassian-mcp-server);
[Atlassian: Extend Atlassian into any AI assistant using MCP](https://www.atlassian.com/platform/rovo-mcp)).

- **Tools**: exposed as permission-scoped groups rather than one tool per REST endpoint —
  `read_jira`, `write_jira`, `search_jira`, and equivalents for the other products — behind
  natural-language-driven workflows like "find all open bugs" or "create a story"
  ([atlassian/atlassian-mcp-server README, via WebFetch summary](https://github.com/atlassian/atlassian-mcp-server)).
  I could not confirm an exhaustive, stable list of individual tool names from what's public;
  treat the exact tool surface as **unverified in detail** beyond the permission-group framing.
- **Auth**: two modes. The default, interactive path is a browser-based OAuth 2.1 flow — the
  client opens a consent screen, the user logs in, and the resulting token is cached by the MCP
  client (Claude Code, Claude Desktop, Cursor, VS Code, etc.), with periodic re-auth as tokens
  expire. There is also a **headless mode**: API-token authentication (personal API tokens over
  Basic auth, or service-account API keys over Bearer) intended for backend/automated,
  non-interactive setups — but an organization admin must explicitly enable API-token auth for
  the site before a headless client can use it
  ([atlassian/atlassian-mcp-server, via WebFetch summary](https://github.com/atlassian/atlassian-mcp-server)).
  It's explicitly documented as usable from Claude Code and Codex among other clients
  ([same source](https://github.com/atlassian/atlassian-mcp-server)).
- **Read-only**: the permission-group model (`read_jira` vs `write_jira`) implies a read-only
  posture is available by only granting the read groups, though I did not find an explicit
  single "read-only mode" toggle documented the way sooperset/mcp-atlassian has one — **this
  granularity claim should be treated as likely but not confirmed against primary docs**.

### `sooperset/mcp-atlassian` (OSS)

A widely used, independently maintained, self-hosted MCP server for Jira and Confluence
([sooperset/mcp-atlassian on GitHub](https://github.com/sooperset/mcp-atlassian)).

- Exposes a large tool surface (reported as up to 98 tools across Jira+Confluence, including
  `jira_search`, `jira_get_issue`, `jira_create_issue`, `jira_update_issue`,
  `jira_transition_issue`, etc.) ([README, via WebFetch summary](https://github.com/sooperset/mcp-atlassian/blob/main/README.md)).
- Supports **both** Cloud and Server/Data Center (DC v8.14+) in one codebase, unlike the official
  server which is Cloud-first ([README, via WebFetch summary](https://github.com/sooperset/mcp-atlassian/blob/main/README.md)).
- Auth: Cloud API tokens, DC Personal Access Tokens, and OAuth 2.0, configured via environment
  variables (`JIRA_URL`, `JIRA_USERNAME`, `JIRA_API_TOKEN`, `JIRA_PERSONAL_TOKEN` for DC, etc.)
  ([README, via WebFetch summary](https://github.com/sooperset/mcp-atlassian/blob/main/README.md)).
- Has a documented `READ_ONLY_MODE` environment flag that blocks write tools entirely — the
  explicit read-only control the official server doesn't clearly document
  ([README, via WebFetch summary](https://github.com/sooperset/mcp-atlassian/blob/main/README.md)).
- Runs headless out of the box (uvx, Docker, pip, from source; env-var config), so it's fully
  scriptable without any browser step when using API token/PAT auth
  ([README, via WebFetch summary](https://github.com/sooperset/mcp-atlassian/blob/main/README.md)).
- It inherited the same Cloud search-endpoint deprecation pain as everyone else — there's an open
  issue where `jira_search` returned empty results because it was still hitting the deprecated
  `GET /search` (now `410`) before being updated to the new endpoint
  ([sooperset/mcp-atlassian issue #1295](https://github.com/sooperset/mcp-atlassian/issues/1295)).

### MCP's role relative to Sirdar's host-side adapter

These are solving different problems and Sirdar needs both, not one instead of the other:

- The **host-side adapter** (this document's subject) is Sirdar's own deterministic fetch: it
  runs before the coding agent is invoked, assembles a fixed ticket bundle (fields, description,
  thread, attachments) via a small number of known REST calls, and hands that bundle to the
  agent as static input. It's auditable, reproducible, and doesn't depend on what the agent
  decides to ask for.
- An **MCP server** (either Atlassian's own or `sooperset/mcp-atlassian`) is useful *inside* the
  agent's own session — evidence gathering the agent does interactively, mid-task, when it
  discovers it needs something the initial bundle didn't include: "what's the current status of
  the blocking ticket," "are there other tickets against this same component," "what did the
  triage note on the parent epic say." That's exploratory and open-ended in a way the host-side
  adapter's fixed `Get`/`List` calls are not, and it's naturally scoped to what the agent decides
  it needs rather than what Sirdar pre-fetches.
- **Can an MCP be driven headlessly by a Go program?** Only the token/API-key auth paths of
  either server qualify — the official server's headless mode (admin-enabled API-token auth) and
  `sooperset/mcp-atlassian`'s env-var-configured token/PAT auth both avoid the interactive
  browser OAuth step
  ([atlassian/atlassian-mcp-server, via WebFetch summary](https://github.com/atlassian/atlassian-mcp-server);
  [sooperset/mcp-atlassian README, via WebFetch summary](https://github.com/sooperset/mcp-atlassian/blob/main/README.md)).
  In principle a Go program could speak MCP's JSON-RPC-over-stdio or Streamable-HTTP transport to
  either server directly, the same way it would to any MCP server, but doing so just to fetch a
  ticket would duplicate the REST calls the host-side adapter already makes directly and adds a
  process/protocol layer for no benefit — MCP is the right tool for the agent's own tool-calling
  loop, not for Sirdar's host-side fetch step. I found no evidence either server exposes anything
  a plain REST call doesn't already give a stdlib HTTP client.

## 4. Mapping recommendations

| Sirdar field | Jira Cloud v3 | Jira DC v2 | Notes |
|---|---|---|---|
| `Key` | `key` | `key` | e.g. `PROJ-123`. |
| `Title` | `fields.summary` | `fields.summary` | |
| `Description` | `fields.description` (ADF) | `fields.description` (wiki markup string) | On Cloud, fetch via v2 instead of v3 to get plain wiki markup and skip ADF parsing (§1); see below for ADF→markdown if v3 is used anyway. |
| `Priority` | `fields.priority.name` | `fields.priority.name` | |
| `Status` | `fields.status.name` | `fields.status.name` | Consider also `fields.status.statusCategory.key` (`new`/`indeterminate`/`done`) for JQL-style filtering logic without hardcoding workflow status names. |
| `Assignee` | `fields.assignee.displayName` | `fields.assignee.displayName` | Null when unassigned. |
| `URL` | `self` is the API URL; build the browser URL as `{site}/browse/{key}` | same pattern | `self` is not user-facing. |
| `HelpdeskRef` | see below | see below | |
| `CreatedAt` / `UpdatedAt` | `fields.created` / `fields.updated` | same | ISO-8601 with offset. |
| `Fields` (map) | `issuetype` (`fields.issuetype.name`), `parent` (`fields.parent.key`, present when the issue is a sub-task or, in the new hierarchy, has any parent), `labels` (`fields.labels`, `[]string`) | same for `issuetype`/`labels`; **epic link** is `fields.customfield_XXXXX` (commonly, but not guaranteed, `customfield_10014` — it's configurable per-instance and must be discovered via `/rest/api/2/field` by name, not hardcoded) | Cloud has replaced the old "Epic Link" custom field with a standard `parent` field in the issue view and on creation/transition, and is deprecating the Epic Link/Parent Link custom fields in the REST API and webhooks; Data Center has **not** received this change and still uses a per-instance custom field for epic linkage ([Atlassian Support: Introducing the new Parent field](https://support.atlassian.com/jira-software-cloud/docs/upcoming-changes-epic-link-replaced-with-parent/); [Atlassian Developer Community: Deprecation of the Epic Link, Parent Link and other related fields](https://community.developer.atlassian.com/t/deprecation-of-the-epic-link-parent-link-and-other-related-fields-in-rest-apis-and-webhooks/54048); [Atlassian Community: "Will 'epic-link replaced with parent' feature come to the Data Center version?"](https://community.atlassian.com/forums/Jira-questions/Will-quot-epic-link-replaced-with-parent-quot-feature-come-to/qaq-p/2773775)). An adapter targeting both Cloud and DC needs a small discovery step (`GET /rest/api/2/field`, match on field `name == "Epic Link"`) rather than a hardcoded `customfield_10014`, since the numeric ID is instance-specific even on DC. |

### ADF → markdown

If the adapter does use Cloud v3 (e.g., because it also wants `renderedFields` HTML, or wants to
stay on the "current" API), the description/comment bodies arrive as ADF JSON, not text. Options:

- **`github.com/ajbeck/adf-to-markdown`** — a Go package converting ADF JSON to Markdown; per its
  Go package listing it targets Go 1.25+ and needs `GOEXPERIMENT=jsonv2`, which is a nontrivial
  toolchain constraint for a stdlib-only project
  ([pkg.go.dev: adfmarkdown](https://pkg.go.dev/github.com/ajbeck/adf-to-markdown)).
- **`github.com/jcstorino/jira-cli/pkg/adf`** — an ADF package embedded inside a `jira-cli` fork,
  translating ADF to other formats including markdown; it's a sub-package of an application, not
  a standalone library, so pulling it in means vendoring or reimplementing rather than a clean
  `go get`
  ([pkg.go.dev: adf package](https://pkg.go.dev/github.com/jcstorino/jira-cli/pkg/adf)).
  **Unverified**: I did not confirm this package's stability or maintenance status beyond the
  pkg.go.dev listing.
- No other mature, standalone Go ADF→text library turned up in research; the ecosystem is
  stronger in TypeScript (`adf-to-markdown` npm package, `julianlam/adf-to-md`) and Python
  (`atlas_doc_parser`) than in Go
  ([npm: adf-to-markdown](https://www.npmjs.com/package/adf-to-markdown);
  [julianlam/adf-to-md](https://github.com/julianlam/adf-to-md);
  [atlas_doc_parser docs](https://atlas-doc-parser.readthedocs.io/en/latest/01-Atlassian-Document-Format-Parser/)).
- Given Sirdar's stdlib-only rule and read-only scope, the pragmatic recommendation is: **don't
  parse ADF at all.** ADF is a documented, versioned JSON schema
  ([Atlassian Document Format structure](https://developer.atlassian.com/cloud/jira/platform/apis/document/structure/)),
  simple enough that a minimal recursive walker (handle `paragraph`, `text` with `marks`,
  `heading`, `bulletList`/`orderedList`/`listItem`, `codeBlock`, `blockquote`, `hardBreak`, link
  marks, and fall through unknown nodes to their text content) covers the overwhelming majority
  of real ticket descriptions in under ~150 lines of Go, with zero new dependencies. Reserve this
  only for Cloud, since DC never produces ADF; and prefer fetching the same field via v2 on Cloud
  when only plain text is needed, sidestepping the walker entirely for the common case.

### `HelpdeskRef`

Three shapes are plausible depending on how the instance is configured; the adapter should
support the first two and treat the third as a fallback:

1. **Same-issue JSM request** (most common, and the cleanest mapping): if the issue's project has
   `projectTypeKey == "service_desk"` ([Atlassian Developer Community: distinguishing JSM
   projects](https://community.developer.atlassian.com/t/distinguish-jira-service-desk-and-other-jira-project-types/3945)),
   the tracker ticket *is* the helpdesk ticket — `HelpdeskRef` is simply the same `Key`, and
   `helpdesk.get`/`helpdesk.threads` call the `/rest/servicedeskapi/request/{key}` and
   `/rest/servicedeskapi/request/{key}/comment` endpoints against that same key rather than
   resolving a separate ID.
2. **A URL or ticket ID parsed from the description** (Sirdar's documented fallback pattern,
   per the harness's existing interface note that `HelpdeskRef` is "a reference to a helpdesk
   ticket parsed from the description") — relevant when the *helpdesk* is a different product
   entirely (e.g., a Zoho Desk ticket number pasted into a Jira bug's description by whoever
   triaged it in). This is vendor-specific text scraping, not a Jira API concern.
3. **A custom field** carrying an external ticket ID/URL — some instances wire up a dedicated
   custom field (via an app or manual convention) rather than embedding it in free text; if
   present, this is more reliable than description-scraping and the adapter's config should let
   an operator name which `customfield_XXXXX` (or field name, resolved via `/rest/api/2/field`)
   to check first, falling back to description-parsing.

### `List` → JQL

Sirdar's filter fields map onto JQL clauses directly:

```
assignee = currentUser() AND statusCategory != Done
```

for the default (unfiltered-assignee) case, or explicit values for `assignee`/`status`; `parent`
maps to `parent = KEY` (sub-tasks/children) — note that on Cloud, `parent` in JQL now covers both
classic sub-task parents and the new Epic-as-parent hierarchy uniformly, which is part of why
Cloud collapsed Epic Link into `parent` (§ mapping table above). `limit` maps to `maxResults` in
the `/search/jql` request body, capped and paginated via `nextPageToken` as described in §1.

## 5. Existing Go clients

- **`andygrunwald/go-jira`** — MIT licensed, actively maintained (repository activity as recent
  as August 2026) ([andygrunwald/go-jira on GitHub](https://github.com/andygrunwald/go-jira);
  [pkg.go.dev: jira package](https://pkg.go.dev/github.com/andygrunwald/go-jira)). It does not
  implement every Jira endpoint, but supports calling arbitrary/unimplemented endpoints directly
  through its underlying HTTP client as an escape hatch
  ([same source](https://github.com/andygrunwald/go-jira)). I found **no confirmation** that it
  has adopted the new `/search/jql` endpoint or ADF-native v3 request/response shapes — its
  design historically centers on v2-shaped structs, and I could not verify current v3/ADF/new-
  search support from available sources. **Treat go-jira's v3/ADF/new-search-endpoint support as
  unverified — likely partial or absent** and confirm directly against its source before relying
  on it for anything beyond basic v2-style GET/search.
- **`ctreminiom/go-atlassian`** — MIT licensed
  ([README](https://github.com/ctreminiom/go-atlassian/blob/main/README.md)), with explicit,
  separately versioned clients for Jira v2, Jira v3, Jira Agile, Jira Service Management,
  Confluence v1/v2, Admin, Assets, and Bitbucket, and documented ADF support "in a subset of the
  API" for the v3 client ([README](https://github.com/ctreminiom/go-atlassian/blob/main/README.md)).
  It was directly affected by the Cloud search-endpoint deprecation (its existing code used the
  removed `/search` endpoints) and has an open, maintainer-assigned issue tracking migration to
  `/search/jql` and `nextPageToken` — as of the issue's last confirmed state, the fix was
  "in progress" and I could not verify it had shipped and released
  ([go-atlassian issue #345](https://github.com/ctreminiom/go-atlassian/issues/345)). This is
  materially more Jira-API-version-aware than `go-jira` (it actually has a v3/ADF-specific
  client), which makes it the better *reference* even if Sirdar doesn't vendor it.
- **Recommendation given Sirdar's stdlib-only rule**: don't take a dependency on either. Neither
  library changes the underlying calculus — Jira's REST API is plain JSON over HTTP with Basic
  or Bearer auth, well within reach of `net/http` and `encoding/json`, and the one place a
  library would save real effort (ADF parsing) is narrow enough to hand-roll (§4). Both libraries
  are, however, useful as **reference implementations** — `go-atlassian` in particular, given its
  v3-aware design and its issue tracker's live documentation of the search-endpoint migration —
  worth reading when implementing the adapter's search and ADF-adjacent code, even without
  importing them.

## 6. Automation / webhooks (future trigger)

- Jira Cloud webhooks are registered via `POST /rest/api/3/webhook` (apps using OAuth 2.0 or
  Connect) and can subscribe to events including `jira:issue_created`, `jira:issue_updated`, and
  `jira:issue_deleted`, optionally scoped with a `jqlFilter` or `fieldIdsFilter` so the webhook
  only fires for matching issues
  ([Atlassian Community: "How can I register a webhook programmatically from a connected
  app?"](https://community.atlassian.com/forums/Jira-questions/How-can-I-register-a-webhook-programmatically-from-a-connected/qaq-p/2895205);
  [Webhooks — Jira Cloud platform](https://developer.atlassian.com/cloud/jira/platform/webhooks/)).
  There is no single webhook event specifically named "issue assigned" — assignment is a field
  change captured under `jira:issue_updated`, so a "new ticket assigned to me → triage" trigger
  needs to inspect the webhook payload's changelog for an `assignee` field change (or use
  `fieldIdsFilter` to restrict delivery to updates touching the assignee field) rather than
  subscribing to a dedicated assignment event.
- **Jira Automation** (the no-code rule engine built into both Cloud and Data Center) offers this
  more directly at the product level: its trigger library includes an issue-transition/edit
  trigger that can be scoped to "create, edit, transition, or assign" operations specifically,
  and a rule's `THEN` action can be an outbound webhook POST to an external URL — effectively
  letting a Jira Automation rule be the thing that calls into a Sirdar-adjacent trigger endpoint
  when an issue is assigned, without Sirdar needing to poll or run its own webhook receiver logic
  beyond accepting the POST
  ([Confluence: Jira automation triggers](https://confluence.atlassian.com/spaces/AUTOMATION/pages/993924804/Jira+automation+triggers);
  [Atlassian Developer: Automation webhooks (JSM)](https://developer.atlassian.com/cloud/jira/service-desk/automation-webhooks/)).
  Automation rule webhook calls include a configurable `X-Automation-Webhook-Token` header for
  the receiver to verify the caller
  ([Confluence: Jira automation triggers](https://confluence.atlassian.com/spaces/AUTOMATION/pages/993924804/Jira+automation+triggers)).
- Given Sirdar's design as a locally-run harness (not a hosted service with a public endpoint),
  webhooks imply either (a) exposing a receiver via a tunnel while Sirdar is running — awkward
  for a tool meant to be invoked on demand — or (b) treating this as a later, optional
  "watch mode" rather than the default `tracker.list`-driven triage flow. Worth revisiting once
  Sirdar has any long-running component at all; premature for the current fetch-and-hand-off
  design.

## Recommended adapter design

**Role**: `tracker` always; `helpdesk` conditionally, only for issues in a `service_desk`-type
project (JSM). An instance without JSM should advertise `tracker` only in `describe`.

**Config keys** (env or Sirdar's credential-ref convention):

- `JIRA_BASE_URL` — site URL (`https://foo.atlassian.net` for Cloud, or the DC instance's own
  base URL).
- `JIRA_DEPLOYMENT` — `cloud` | `datacenter`, or auto-detected (see below) with this as an
  override.
- `JIRA_AUTH_MODE` — `token` (Cloud API token, Basic) | `pat` (DC Bearer) | `oauth` (Cloud 3LO,
  lower priority — see §2).
- `JIRA_EMAIL` + `JIRA_API_TOKEN` (Cloud Basic), or `JIRA_PAT` (DC Bearer), or
  `JIRA_OAUTH_CLIENT_ID`/`JIRA_OAUTH_CLIENT_SECRET`/stored refresh token (Cloud 3LO) — credential
  values themselves sourced via Sirdar's env/keychain-ref convention, never inline.
- `JIRA_EPIC_LINK_FIELD` — optional override for the DC epic-link custom field name/ID, to skip
  the `/rest/api/2/field` discovery call on repeated runs once known.
- `JIRA_HELPDESK_REF_FIELD` — optional custom-field name carrying an external helpdesk
  reference, checked before falling back to description-parsing (§4, option 3).

**Endpoints used**:

- `GET /rest/api/2/issue/{key}` for `tracker.get` (v2 on Cloud too, for plain-text description —
  §1/§4), with `?expand=changelog` only if/when change history is needed.
- `POST /rest/api/2/search/jql` (Cloud) / `GET /rest/api/2/search` (DC) for `tracker.list`,
  building JQL from the filter params per §4.
- `GET /rest/api/2/field` once per process (cached), for epic-link/custom-field discovery.
- JSM role: `GET /rest/servicedeskapi/request/{key}` for `helpdesk.get`,
  `GET /rest/servicedeskapi/request/{key}/comment` for `helpdesk.threads` (mapping `public` to
  customer/agent `Role`), and the issue's own `fields.attachment[]` (from the already-fetched
  tracker payload) for `helpdesk.attachments`.
- `GET /rest/api/2/serverInfo` (DC) or hitting `/rest/api/3/...` and checking for a `410`/`404`
  vs. success (Cloud) as part of deployment auto-detection, described next.

**Cloud vs. DC detection**: rather than trusting a config flag alone, probe once at startup:
attempt `GET {base}/rest/api/2/serverInfo` (present on both) and inspect the response's
`deploymentType` field if present, or fall back to whether the base URL matches
`*.atlassian.net`/a configured Cloud pattern. Cache the result for the adapter process's
lifetime; this decides which search endpoint and pagination style to use, and whether ADF is a
possible response shape at all.

**Edge cases to handle explicitly**:

- **ADF**: only possible on Cloud, and only if v3 is used anywhere (prefer v2 to avoid it per
  §4); if it must be parsed, use a small hand-rolled walker, not a dependency requiring
  `GOEXPERIMENT=jsonv2`.
- **Pagination**: Cloud `/search/jql` uses `nextPageToken`; cap the number of pages fetched (e.g.
  20) and treat a repeated token as a bug in the upstream API, not an infinite scroll, given the
  documented looping issue (§1). DC keeps `startAt`/`total`; use that directly, no token.
  Whichever mode, respect `limit` from Sirdar's filter as a hard cap on total issues returned,
  independent of page size.
- **Attachments auth**: Basic/Bearer works for the JSON metadata call on both Cloud and DC. For
  the actual binary download, expect Basic auth to work reliably on Cloud; on DC, be prepared for
  a PAT/Bearer 401/redirect-to-login on the attachment URL specifically, and fall back to
  authenticating once via a session (cookie-based) request to obtain a cookie jar for that one
  call, per the documented DC workaround (§1). This is a genuine risk area worth a specific
  integration test against a real DC instance before shipping.
- **Rate limiting**: implement the `Retry-After`-then-exponential-backoff-with-jitter pattern
  from §2 for both deployments; on Cloud, branch on `RateLimit-Reason` if present to decide
  whether to pause globally or just for the current endpoint.
- **JSM detection**: check `projectTypeKey` once per project (cache by project key) rather than
  per-issue, to avoid an extra API call per ticket.
- **Missing/renamed fields**: epic-link field ID is instance-specific and not guaranteed to be
  `customfield_10014`; always resolve by name via `/rest/api/2/field`, never hardcode the number.

## Open questions

- Whether Sirdar wants OAuth 2.0 (3LO) support at all in v1, given the added complexity
  (redirect URI, consent flow, refresh-token storage) versus a single-operator API-token/PAT
  design — recommend deferring it until there's a concrete multi-user need.
- Whether to support Data Center's classic pre-8.14 auth (Basic auth with username/password, or
  Jira's older OAuth 1.0a "application links") for instances that haven't rolled out PATs yet —
  not researched here; PAT (8.14+) is assumed to be the baseline.
- The DC attachment-download-auth edge case (§1, "Edge cases") needs to be validated against a
  real Data Center instance; all sourcing for it is secondary (support KB articles and community
  threads), not confirmed firsthand.
- Exact, current tool list and scope-mapping for the official Atlassian Remote MCP Server
  (`mcp.atlassian.com`) is not fully documented publicly in a way I could verify beyond
  permission-group names (`read_jira`, `write_jira`, `search_jira`); if Sirdar's agent-side
  tooling ever depends on specific tool names/schemas from that server, verify directly against
  a live connection rather than this document.
- Whether `ctreminiom/go-atlassian`'s `/search/jql` migration (tracked in issue #345) has
  shipped in a released version as of today (2026-09-10) — the issue's state as researched was
  "in progress," not confirmed released; check the changelog directly if this library is ever
  reconsidered as a reference or dependency.
- No first-party Go ADF→markdown library with confirmed stability was found; the two candidates
  surfaced (`ajbeck/adf-to-markdown`, `jcstorino/jira-cli/pkg/adf`) are both lightly verified —
  worth a second look before the recommendation to hand-roll a walker is treated as final,
  in case either has matured since this research.
