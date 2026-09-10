# Tracker adapters: Jira, Linear, Azure DevOps, Rally (plan)

Date: 2026-09-10. Branch `adapters`. Research: `docs/research/adapters/{jira,linear,azure-devops,rally}.md`.
Spec authority: `docs/superpowers/specs/2026-09-10-sirdar-v0-triage-core-design.md`, section "Ticket sources".

## Goal

Built-in, read-only Go adapters so any team can point Sirdar at their tracker. Each adapter
implements `source.Tracker` and, where the tracker carries the conversation, exposes
`source.Helpdesk` through `Client.Helpdesk()` (the pattern `plugin.Client` uses). Stdlib HTTP
only; HTML bodies go through `internal/source/htmltext`.

## Shared design (binding for all four)

- Package `internal/source/<name>` exporting `Config` (plain strings; secrets are already
  resolved by the wiring layer) and `New(cfg Config, hc *http.Client) (*Client, error)`.
  `Client` implements `source.Tracker`; `Client.Helpdesk() source.Helpdesk` when supported;
  `Client.Ping(ctx) error` for `doctor`; `Client.Warnings() []string` (`source.Warner`).
- Error mapping: 401/403 → `source.Auth`; 404 → `source.NotFound`; 429 → `source.RateLimited`
  after honouring one `Retry-After` wait ≤ 30 s; other non-2xx and decode errors →
  `source.Internal` with method, path, status and ≤ 200 bytes of body; never the credential.
- `HelpdeskRef`: set only from the tracker's native linkage (below); leave `""` otherwise.
  A generic description-regex fallback is applied later by the wiring layer, configured per
  workspace (`sources.tracker.helpdeskRef: {pattern, idPattern}`), so the Zoho-URL rule stops
  being hardcoded anywhere.
- Descriptions and comments: Markdown where the API gives Markdown (Linear, ADO comments,
  Jira v2 wiki markup passed through as text), otherwise `htmltext.ToMarkdown`; inline image
  URLs collected as attachments.
- Threads: one `ticket.Message` per comment, `Role` = `customer` for external/customer authors
  where the API can tell (JSM customer, ADO/Rally: never; default `agent`), `system` for
  automation; private notes keep `Author` suffixed " (internal)".
- Attachments: downloaded with the same auth into `dir/<index>-<safe name>`; names sanitised
  (`filepath.Base`, strip separators, fallback `attachment`); `Path` = `filepath.Base(dir)/<file>`;
  per-file failures recorded in warnings, not fatal unless all fail.
- `List(filter)`: `Assignee` (`me` means the token's user), `Status` (open-ish default),
  `Parent`, `Limit` (≤ 200, paginate). Return `source.Unsupported` for filters the API cannot express.
- Tests: `httptest` with fixtures shaped exactly as the research reports describe; cover auth
  header, pagination, error mapping, role mapping, attachment download and sanitisation.
- Config shapes (wired in a follow-up task on `main` once the Zoho OAuth change lands):

```yaml
sources.tracker:
  adapter: jira        # baseUrl, deployment: cloud|datacenter|auto, email + apiToken (cloud) | pat (dc), projectKey?, epicLinkField?
  adapter: linear      # apiKey, teamKey?
  adapter: azdo        # orgUrl (https://dev.azure.com/{org} or server collection URL), project, pat, helpdeskLinkDomain?, helpdeskField?
  adapter: rally       # baseUrl (default https://rally1.rallydev.com), apiKey, workspace, project?, types: [Defect, HierarchicalRequirement], helpdeskField?
```

## Per-adapter notes (from the research)

**Jira.** Prefer `/rest/api/2` on both Cloud and DC so bodies are wiki markup, not ADF. Cloud
search is `POST /rest/api/3/search/jql` with `nextPageToken` (old `/search` is 410); DC is
`GET /rest/api/2/search` with `startAt`. Auth: Cloud basic `email:apiToken`; DC `Bearer <pat>`.
Detect deployment via `GET /rest/api/2/serverInfo` (`deploymentType`). Epic/parent: Cloud
`fields.parent`; DC custom field resolved by name through `/rest/api/2/field` (never
hardcoded). JSM: `project.projectTypeKey == "service_desk"` → `HelpdeskRef` = same key and
comments carry `jsdPublic`; customer authors via `/rest/servicedeskapi/request/{key}`
`reporter`. Attachments: `fields.attachment[].content` with auth header and
`X-Atlassian-Token: no-check`; DC may redirect to SSO (warn, don't fail).

**Linear.** GraphQL at `https://api.linear.app/graphql`, `Authorization: <apiKey>` (no
Bearer). `issue(id: "ENG-123")` accepts identifiers. Fields: title, description (Markdown),
priority + priorityLabel, state{name,type}, assignee, team{key}, parent, labels, url,
createdAt, updatedAt, comments (Markdown, user, createdAt, parent), attachments{sourceType,
url, title}. `HelpdeskRef`: first attachment with `sourceType` in zendesk|intercom|front → its
url. Uploads at `uploads.linear.app` need the auth header. Errors inside `errors[]` on 200;
`RATELIMITED` code. `List`: `issues(filter:{assignee:{isMe:{eq:true}}, state:{type:{nin:
["completed","canceled"]}}, parent:{id:{eq}}}, first, after)`.

**Azure DevOps.** `GET {org}/{project}/_apis/wit/workitems/{id}?$expand=all&api-version=7.1`;
fields `System.Title`, `System.Description` (HTML), `Microsoft.VSTS.TCM.ReproSteps` (HTML,
Bugs; append after description), `System.State`, `Microsoft.VSTS.Common.Priority`/`Severity`,
`System.AssignedTo{displayName,uniqueName}`, `System.Tags`, `System.Parent`, `System.AreaPath`,
`System.CreatedDate`, `System.ChangedDate`. Comments `.../workitems/{id}/comments?api-version=7.1-preview.4`
(Markdown by default; `format`). Attachments from `relations[] rel=AttachedFile` →
`url` + `?fileName=`; not linked to comments. `HelpdeskRef`: `Hyperlink` relation whose host
matches `helpdeskLinkDomain`, else the `helpdeskField` custom field. `List`: WIQL POST
`_apis/wit/wiql?api-version=7.1` then `workitemsbatch` in chunks of 200. Auth: basic with
empty user and PAT. Keys are numeric ids; accept `123` or `AB-123`-style prefixes by stripping
non-digits.

**Rally.** WSAPI v2.0 `https://rally1.rallydev.com/slm/webservice/v2.0/`; header
`ZSESSIONID: <apiKey>`. Get by FormattedID: query each configured type
(`/defect?query=(FormattedID = "DE1234")&fetch=<fields>&workspace=<ref>`), first hit wins;
prefix heuristics (DE→Defect, US→HierarchicalRequirement, TA→Task, F→PortfolioItem/Feature)
tried first. Fields: Name, Description (HTML), Notes (HTML), Priority, Severity,
State/ScheduleState, Owner{_refObjectName}, Project, Iteration, Release, Tags, Parent/Feature,
CreationDate, LastUpdateDate, FormattedID, `_ref`. Discussion: `/conversationpost?query=(Artifact = "<ref>")`
(Text HTML, User, CreationDate). Attachments: `/attachment?query=(Artifact = "<ref>")` then
`Content._ref` → `AttachmentContent.Content` base64. `pagesize` ≤ 200, `start` 1-based.
`HelpdeskRef`: `helpdeskField` (`c_` custom field). Throttle: 12 concurrent per user.

## Tasks

A1 `internal/source/jira`, A2 `internal/source/linear`, A3 `internal/source/azdo`,
A4 `internal/source/rally`: parallel, disjoint packages. A5 (after A1–A4 and after the Zoho
OAuth change is merged from `main`): config validation for the four adapters, wiring in
`internal/app/wire.go` / `cmd/sirdar/wire.go`, generic `helpdeskRef` regex config applied in
`internal/run/prepare.go`, `doctor` rows via `Ping`, `docs/config.md`, `sirdar init`
scaffold comments. A6: docs/adapters.md section "built-in trackers" + README table.
