# Azure DevOps adapter research

Research date: 2026-09-10. Scope: Azure DevOps Services (`dev.azure.com`) and Azure DevOps Server (on-prem), for a read-only Go `tracker` (+ optional `helpdesk`) adapter per Sirdar's adapter contract (stdlib HTTP, env/keychain creds, no third-party Go deps by default).

## 1. Work Item Tracking REST API

### Get a single work item

```
GET https://dev.azure.com/{organization}/{project}/_apis/wit/workitems/{id}?api-version=7.1
GET https://dev.azure.com/{organization}/{project}/_apis/wit/workitems/{id}?fields={fields}&asOf={asOf}&$expand={$expand}&api-version=7.1
```

`$expand` is an enum: `None`, `Relations`, `Fields`, `Links`, `All`. `All` returns fields, relations, and links in one call — this is what Sirdar's `GetByKey` should use, since the ticket contract needs fields (title/description/state/etc.) and relations (attachments, parent, hyperlinks) together. `fields` is a comma-separated allow-list, usable instead of `$expand=all` when only specific fields are needed (e.g. for `List` follow-up hydration). `asOf` fetches a historical revision. [Get Work Item](https://learn.microsoft.com/en-us/rest/api/azure/devops/wit/work-items/get-work-item?view=azure-devops-rest-7.1)

Response shape: `{ id, rev, fields: {...}, relations: [{rel, url, attributes}], _links: {...}, url }`. `fields` is a flat map keyed by reference name (e.g. `System.Title`) to a JSON value — this maps directly onto Sirdar's `Fields map` concept, with the named fields (`Key`, `Title`, etc.) pulled out explicitly. [Get Work Item](https://learn.microsoft.com/en-us/rest/api/azure/devops/wit/work-items/get-work-item?view=azure-devops-rest-7.1)

Relevant fields, confirmed present in the schema/examples across the docs pulled:
- `System.Title` — string
- `System.Description` — HTML string (rich-text control)
- `Microsoft.VSTS.TCM.ReproSteps` — HTML string, Bug work item type only (Description isn't shown on the default Bug form; ReproSteps is the Bug-specific field for what happened / repro)
- `System.State` — string, process-defined (e.g. New/Active/Resolved/Closed, or To Do/Doing/Done depending on process template)
- `Microsoft.VSTS.Common.Priority` — integer (1–4 typically)
- `Microsoft.VSTS.Common.Severity` — string, Bug-specific (e.g. "2 - High")
- `System.AssignedTo` — an identity object: `{displayName, url, _links.avatar.href, id, uniqueName, imageUrl, descriptor}`
- `System.Tags` — semicolon-delimited string
- `System.Parent` — integer work item id (present when set; also derivable from the `System.LinkTypes.Hierarchy-Reverse` relation)
- `System.AreaPath`, `System.IterationPath` — backslash-delimited path strings
- `System.CreatedDate`, `System.ChangedDate` — ISO 8601 UTC timestamps

All confirmed directly in the sample response body of [Get Work Item](https://learn.microsoft.com/en-us/rest/api/azure/devops/wit/work-items/get-work-item?view=azure-devops-rest-7.1) (which shows `AssignedTo`/`CreatedBy`/`ChangedBy` identity objects, `CreatedDate`, `ChangedDate`, `Title`, `Priority`, `Tags`, `AreaPath`, `IterationPath`) and cross-referenced against the [Fields - List](https://learn.microsoft.com/en-us/rest/api/azure/devops/wit/fields/list?view=azure-devops-rest-7.1) reference-name catalog. `ReproSteps` and `Severity` being Bug-type-specific is standard Azure Boards process-template knowledge (Bug work item type uses `Microsoft.VSTS.TCM.ReproSteps` in place of `System.Description` on its default layout) — **not independently re-verified against a live Bug work item's field list in this research pass; treat as high-confidence but unverified by direct API sample.**

`api-version` 7.1 vs 7.2: 7.2 exists as a documented moniker on every WIT endpoint fetched (`Get Work Item`, `Get Work Items Batch`, `Attachments - Get`), but the docs give no changelog delta description between 7.1 and 7.2 for these particular endpoints. Given Sirdar is a new adapter, pin to `7.1` (stable, GA-documented since the routing header split) and treat `7.2` as a forward-compat option — **the specific field/behavior differences between 7.1 and 7.2 for WIT are unverified.**

### Comments

```
GET https://dev.azure.com/{organization}/{project}/_apis/wit/workItems/{workItemId}/comments?api-version=7.1-preview.4
GET .../comments?$top={n}&continuationToken={token}&includeDeleted={bool}&$expand={CommentExpandOptions}&order={asc|desc}&api-version=7.1-preview.4
```

Correction to the brief: comment text is **markdown by default**, not HTML. `Comment.format` is an enum `markdown | html` (a comment can natively be either, depending on how it was authored — the modern web UI writes markdown). `Comment.text` holds the raw text in its native format; `Comment.renderedText` (only populated when `$expand=renderedText` or `all`) holds the HTML-rendered version. For Sirdar's thread-message `Text` field, request `$expand=renderedText` (or `all`) and prefer `renderedText` when present, falling back to `text` — this sidesteps having to run a markdown-vs-HTML branch in the adapter and lets a single HTML→plain-text step handle both. Pagination uses `continuationToken` / `nextPage`, not offset — the adapter needs a token-following loop, not a page-index loop. `mentions[]` gives `{artifactType, artifactId, targetId}` for @-mentions of people or other work items. [Get Comments](https://learn.microsoft.com/en-us/rest/api/azure/devops/wit/comments/get-comments?view=azure-devops-rest-7.1)

`createdBy` / `modifiedBy` are `IdentityRef` objects (`displayName`, `uniqueName`, `id`, `descriptor`) — same shape as `System.AssignedTo` — giving a consistent identity-to-`Author` mapping across fields and comments.

### Attachments

Discovery: attachments surface as `WorkItemRelation` entries with `rel == "AttachedFile"` on the work item fetched with `$expand=relations` or `all`. Each relation's `url` looks like `https://dev.azure.com/{org}/{project}/_apis/wit/attachments/{guid}?fileName=...`, and `attributes` carries the original file name, size, and comment (if any) attached at link time. [Attachments - Get, WorkItemRelation](https://learn.microsoft.com/en-us/rest/api/azure/devops/wit/attachments/get?view=azure-devops-rest-7.1)

Download:
```
GET https://dev.azure.com/{organization}/{project}/_apis/wit/attachments/{id}?fileName={fileName}&download={bool}&api-version=7.1
```
`id` is the attachment GUID (from the relation URL, not a small integer). `download=true` forces `Content-Disposition: attachment`. Response media types are `application/octet-stream` or `application/zip`; no JSON envelope — this is a raw byte stream, same auth header as every other WIT call (PAT/Bearer). [Attachments - Get](https://learn.microsoft.com/en-us/rest/api/azure/devops/wit/attachments/get?view=azure-devops-rest-7.1)

For Sirdar's `AttachmentIDs` on thread messages: comment payloads don't carry attachment references directly in the Comments API response shown above — attachment association with a specific comment isn't exposed by this endpoint (attachments are Bug/task-level via `AttachedFile` relations, not comment-level). If a ticket's thread needs to show "this comment had this attachment," that link has to be inferred out-of-band (e.g. by comment timestamp proximity to relation `attributes` timestamps) or simply not attempted — **Azure DevOps has no first-class comment↔attachment linkage in this API; unverified whether any preview/newer endpoint changes this.** The pragmatic design is to treat all `AttachedFile` relations on the work item as belonging to the ticket as a whole (attach them to the synthetic "first message" or expose them separately), not to any one thread message.

### Links / relations, history

- `relations[]` on the work item (from `$expand=relations`/`all`) is the single source for: parent/child (`System.LinkTypes.Hierarchy-Forward`/`-Reverse`), related (`System.LinkTypes.Related`), attachments (`AttachedFile`), and hyperlinks (`Hyperlink`) — see §4 for the Hyperlink → `HelpdeskRef` derivation.
- Work item history/updates: `GET .../workitems/{id}/updates` (from `_links.workItemUpdates` in the Get Work Item response) returns a revision-by-revision diff feed; `GET .../workitems/{id}/revisions` returns full historical snapshots. Neither was fetched in full detail this pass since Sirdar's contract doesn't currently need a revision/history field — flagged as **available but out of scope** unless a future `Fields` entry wants change history.

### WIQL (work item queries) for `List`

```
POST https://dev.azure.com/{organization}/{project}/{team}/_apis/wit/wiql?api-version=7.1
POST .../wiql?timePrecision={bool}&$top={n}&api-version=7.1
Body: { "query": "<WIQL text>" }
```
[Query By Wiql](https://learn.microsoft.com/en-us/rest/api/azure/devops/wit/wiql/query-by-wiql?view=azure-devops-rest-7.1)

Two-step pattern is mandatory: **WIQL never returns field values, only IDs (and, for tree/oneHop queries, link pairs).** Response `queryType` is `flat | tree | oneHop`; for `flat` queries the payload is `workItems: [{id, url}, ...]`; for `tree`/`oneHop` it's `workItemRelations: [{rel, source: {id,url}, target: {id,url}}, ...]`. Step 2 is `POST .../_apis/wit/workitemsbatch` with the collected IDs to get actual field values. [Query By Wiql](https://learn.microsoft.com/en-us/rest/api/azure/devops/wit/wiql/query-by-wiql?view=azure-devops-rest-7.1)

Example queries for Sirdar's `List`:
```sql
-- assigned to me, not closed
SELECT [System.Id] FROM WorkItems
WHERE [System.AssignedTo] = @Me AND [System.State] <> 'Closed'
ORDER BY [System.ChangedDate] DESC

-- children of a given parent
SELECT [System.Id] FROM WorkItems
WHERE [System.Parent] = {id}
```
`@Me` and comparison/`<>` operators confirmed in Microsoft's own query-operators reference and syntax examples. **`[System.Parent] = {id}` as a flat-query WHERE clause is a common community pattern but not confirmed verbatim as a supported flat-query filter field in the docs pulled this pass** — the officially documented way to get children of a parent is a `tree`/`oneHop` query using `MODE (Recursive)` over `System.LinkTypes.Hierarchy-Forward`, e.g. `SELECT [System.Id] FROM WorkItemLinks WHERE [Source].[System.Id] = {id} AND [System.Links.LinkType] = 'System.LinkTypes.Hierarchy-Forward' MODE (Recursive)`, which returns `workItemRelations` rather than a flat `workItems` list. Verify against a live org before shipping the "children of parent" `List` filter. [WIQL syntax reference](https://learn.microsoft.com/en-us/azure/devops/boards/queries/wiql-syntax?view=azure-devops), [Query operators, macros, variables](https://learn.microsoft.com/en-us/azure/devops/boards/queries/query-operators-variables?view=azure-devops)

Limits: WIQL query text itself is capped at 32K characters. `$top` bounds the WIQL result set size (max IDs the query returns). Separately, `workitemsbatch` accepts **at most 200 ids per call** — documented explicitly ("Gets work items for a list of work item ids (Maximum 200)") — so `List` needs to chunk IDs into batches of ≤200 and issue multiple `workitemsbatch` calls, concatenating results. [Get Work Items Batch](https://learn.microsoft.com/en-us/rest/api/azure/devops/wit/work-items/get-work-items-batch?view=azure-devops-rest-7.1), [WIQL syntax reference](https://learn.microsoft.com/en-us/azure/devops/boards/queries/wiql-syntax?view=azure-devops)

`workitemsbatch` request body: `{ ids: [...], fields: [...], $expand, asOf, errorPolicy: "Fail"|"Omit" }`. `errorPolicy: "Omit"` is useful for Sirdar so a single deleted/inaccessible ID in a batch doesn't 404 the whole `List` call. [Get Work Items Batch](https://learn.microsoft.com/en-us/rest/api/azure/devops/wit/work-items/get-work-items-batch?view=azure-devops-rest-7.1)

### HTML → markdown/plain text

`System.Description` and `Microsoft.VSTS.TCM.ReproSteps` are rich-text/HTML fields (confirmed by field type in the WIT field catalog and by long-standing Azure Boards behavior — the web UI is a WYSIWYG HTML editor for these fields). Comments default to markdown with optional `renderedText` HTML (§ above — a correction to the brief's assumption that comments are HTML). Sirdar needs one HTML→text/markdown normalization path (stdlib `golang.org/x/net/html` is not stdlib proper — pure-stdlib HTML stripping is feasible for the simple tag set Azure Boards' editor produces, but a small vendored/allowed HTML-to-markdown pass is the practical choice; flagged as a design decision, not researched further since it's implementation, not adapter-surface).

## 2. Authentication

### Personal Access Token (PAT) — Basic auth

Format: HTTP Basic with an **empty (or arbitrary, ignored) username** and the PAT as the password, base64-encoded: `Authorization: Basic base64(":" + PAT)`. curl form: `curl -u :{PAT} https://dev.azure.com/{org}/_apis/...`. Scope needed for Sirdar's read-only use: `vso.work` — "Grants the ability to read work items, queries, boards, area and iterations paths, and other work item tracking related metadata. Also grants the ability to execute queries, search work items and to receive notifications about work item events via service hooks." This exact scope is listed as the security requirement on every WIT endpoint fetched (get work item, comments, attachments, WIQL). [Use personal access tokens](https://learn.microsoft.com/en-us/azure/devops/organizations/accounts/use-personal-access-tokens-to-authenticate?view=azure-devops), [Get Work Item - Security](https://learn.microsoft.com/en-us/rest/api/azure/devops/wit/work-items/get-work-item?view=azure-devops-rest-7.1)

PAT format detail (useful for adapter-side validation/redaction): 84 characters, 52 random, with a fixed `AZDO` signature at positions 76–80 for leak-scanning tools. Default max lifetime is governed by org policy (commonly 30–90 days); Microsoft's current guidance is to treat PATs as short-lived and prefer Entra tokens for anything long-running — directly relevant since Sirdar adapters are meant to run unattended. [Use personal access tokens](https://learn.microsoft.com/en-us/azure/devops/organizations/accounts/use-personal-access-tokens-to-authenticate?view=azure-devops)

### Microsoft Entra ID (OAuth)

Azure DevOps' Entra resource id is `499b84ac-1321-427f-aa17-267ca6975798`; the `.default` scope form is `499b84ac-1321-427f-aa17-267ca6975798/.default`, or `https://app.vssps.visualstudio.com/.default` (equivalent resource URI form — both appear in Microsoft's own current docs). Token acquired via standard OAuth2 client-credentials (service principal) or managed-identity flow against `https://login.microsoftonline.com/{tenant-id}/oauth2/v2.0/token`, then sent as `Authorization: Bearer {token}`. [Entra OAuth](https://github.com/MicrosoftDocs/azure-devops-docs/blob/main/docs/integrate/get-started/authentication/entra-oauth.md), [Service principals and managed identities](https://learn.microsoft.com/en-us/azure/devops/integrate/get-started/authentication/service-principal-managed-identity)

### Managed identity

Same Entra token flow, credential source is Azure's Instance Metadata Service instead of a client secret/certificate: `GET http://169.254.169.254/metadata/identity/oauth2/token?api-version=2019-08-01&resource=https://app.vssps.visualstudio.com/` with `Metadata: true` header, only reachable from inside an Azure-hosted compute resource. Not useful if Sirdar runs outside Azure (e.g. locally or on a non-Azure host) — flagged as an auth option only relevant for an Azure-hosted deployment of the harness. The identity (service principal or managed identity) still has to be explicitly added to the Azure DevOps org as a user by a Project Collection Administrator before it can call any API — it does **not** get access automatically from Entra group membership. Tokens expire hourly (vs. PATs' up-to-a-year lifetime), which is the main security argument for preferring this path. [Service principals and managed identities](https://learn.microsoft.com/en-us/azure/devops/integrate/get-started/authentication/service-principal-managed-identity)

### Azure DevOps Server (on-prem)

Same REST surface, different base URL: `https://{server}/{collection}/{project}/_apis/...` (or `http://{server}:{port}/tfs/{collection}/...` for older TFS-style paths). PAT basic-auth works identically. NTLM/Windows auth (via IIS/Kerberos) is the traditional on-prem alternative to a PAT; Microsoft's current guidance explicitly recommends **Kerberos over NTLM** for Azure DevOps Server, and separately warns that **enabling IIS Basic Authentication on the server invalidates PAT usage** — an important interaction for on-prem deployments if the collection is also IIS-Basic-Auth-protected. Entra ID auth does not apply to Server (it's an Entra/cloud-only mechanism) — Server auth options are effectively PAT or Windows-integrated (NTLM/Kerberos). [oauth.md](https://github.com/MicrosoftDocs/azure-devops-docs/blob/main/docs/integrate/get-started/authentication/oauth.md), [Kerberos vs NTLM blog](https://devblogs.microsoft.com/devops/reconfigure-azure-devops-server-to-use-kerberos-instead-of-ntlm/), [PAT FAQ — IIS Basic Auth](https://learn.microsoft.com/en-us/azure/devops/organizations/accounts/use-personal-access-tokens-to-authenticate?view=azure-devops)

### Rate limits

Azure DevOps Services throttles by **Azure DevOps Throughput Units (TSTUs)**: 1 TSTU ≈ average 5-minute load of a typical user; global/per-identity sliding-window limit is **200 TSTUs per 5 minutes**. Exceeding it first causes *delays* (milliseconds up to 30s per request), and sustained excess causes hard `429` responses with body `TF400733: The request has been canceled: Request was blocked due to exceeding usage of resource <resource name> in namespace <namespace ID>.` [Rate and usage limits](https://learn.microsoft.com/en-us/azure/devops/integrate/concepts/rate-limits?view=azure-devops)

Response headers to handle in the adapter's HTTP client:
- `Retry-After` — seconds to wait; per Microsoft, honoring it means retry logic doesn't even need to inspect the status further (the request itself still returns HTTP 200 in some throttling paths, not just 429 — worth defensive handling either way).
- `X-RateLimit-Remaining`, `X-RateLimit-Limit`, `X-RateLimit-Reset` (Unix epoch), `X-RateLimit-Delay`, `X-RateLimit-Cost`, `X-RateLimit-Resource` (human-readable, not for parsing logic).

[Rate and usage limits](https://learn.microsoft.com/en-us/azure/devops/integrate/concepts/rate-limits?view=azure-devops)

Automation identities (service accounts, bots) can be granted higher limits by temporarily assigning the **Basic + Test Plans** access level — an org-admin action, not something the adapter can do itself, but worth documenting as an operational lever if Sirdar's polling volume trips throttling. [Rate and usage limits](https://learn.microsoft.com/en-us/azure/devops/integrate/concepts/rate-limits?view=azure-devops)

## 3. Azure DevOps MCP server

`microsoft/azure-devops-mcp` (npm package `@azure-devops/mcp`) is Microsoft's own, MIT-licensed MCP server, actively published (npm shows frequent releases; the search pass this run saw a release "2 hours old" at v2.10.0 — treat the exact version/cadence as a point-in-time observation, not a stable fact). It's explicitly designed to plug into agent clients including VS Code/GitHub Copilot, Visual Studio 2022, Claude Code, and Cursor via standard MCP config (`.vscode/mcp.json` or equivalent). [microsoft/azure-devops-mcp](https://github.com/microsoft/azure-devops-mcp), [@azure-devops/mcp on npm](https://www.npmjs.com/package/@azure-devops/mcp)

Tool surface: organized into **domains** (`core`, `work`, `work-items`, `search`, `test-plans`, `repositories`, `wiki`, `pipelines`, `advanced-security`) that a client can selectively load to keep the tool list small — the project recently did "a full tool consolidation" with tool renames, so any hardcoded tool-name list should be re-checked against the current Toolset docs before depending on it. [microsoft/azure-devops-mcp](https://github.com/microsoft/azure-devops-mcp)

Auth: four modes selected via `--authentication`: `interactive` (default), `azcli` (delegates to an existing `az login` session), `envvar` (reads a raw bearer token from `ADO_MCP_AUTH_TOKEN`), and `pat` (reads `PERSONAL_ACCESS_TOKEN`, which must itself be **base64 of `<anything>:<PAT>`**, not the raw PAT — a detail worth getting right if Sirdar ever shells out to or mimics this server). [GETTINGSTARTED.md](https://github.com/microsoft/azure-devops-mcp/blob/main/docs/GETTINGSTARTED.md), [DeepWiki: Authentication Setup](https://deepwiki.com/microsoft/azure-devops-mcp/5.2-authentication-setup) (secondary/community-generated source, not Microsoft-primary — cross-check against the repo's own docs before relying on specifics)

Distinguishing MCP-for-the-agent from Sirdar's host-side adapter: the MCP server is meant to sit *inside* the coding agent's tool-call loop, giving the LLM live, on-demand Azure DevOps access (including write operations in some domains) during a session. Sirdar's adapter model is the opposite shape — the **host** fetches one ticket (or a filtered list) up front, in Go, over plain REST, and hands a fixed snapshot to the agent; there's no live tool-calling back into Azure DevOps from the agent. The MCP server is not a substitute for the adapter and isn't Go, so it doesn't help with Sirdar's stdlib-only constraint — it's relevant only as a reference for tool/domain naming conventions or as an optional *separate* integration path if Sirdar ever wants to expose live ADO access to the agent in addition to the host-side fetch.

**Go MCP alternatives**: none found. All Azure DevOps MCP servers turned up in search (`microsoft/azure-devops-mcp`, `Tiberriver256/mcp-server-azure-devops`, `RyanCardin15/AzureDevOps-MCP`, various forks/mirrors) are TypeScript/Node, published as npm packages. **No Go-based Azure DevOps MCP server was found in this research pass; absence is not proof none exists, just that none surfaced in the searches run.**

## 4. Azure Boards ↔ helpdesk integrations, and `HelpdeskRef` derivation

No native, first-party Azure Boards↔ServiceNow/Zendesk/Dynamics integration ships in the box — all three are third-party (Marketplace) connectors:
- **ServiceNow**: multiple Marketplace listings (e.g. "ServiceNow & Azure DevOps (TFS or Cloud) Bidirectional Integration", Exalate) offering two-way sync of incidents/tasks/work items including comments, attachments, and links. [Marketplace listing](https://marketplace.visualstudio.com/items?itemName=vs-publisher-1455028.oim-ServiceNow-adointegration), [Exalate](https://exalate.com/integrations/servicenow-azure-devops/)
- **Zendesk**: a Zendesk Support app ("Azure DevOps Integration") lets agents create/link ADO work items from a Zendesk ticket, and syncs comments/status bidirectionally. [Zendesk Marketplace](https://www.zendesk.com/marketplace/apps/support/394508/azure-devops-integration/); there is also a legacy first-party-adjacent "Zendesk" service hook listed on the Visual Studio Marketplace (`ms-vsts.services-zendesk`), suggesting some historical native service-hook support existed — **not independently verified as still functional/current in 2026.**
- **Dynamics 365**: connectors (Marketplace "Case Management Tool integrate with Dynamics 365 CRM and Azure DevOps", QuantumWhisper's connector) let a CRM case be linked to, or auto-create, an ADO work item, with bidirectional status visibility. [Marketplace](https://marketplace.microsoft.com/en-us/product/dynamics-365/zelitesolutionspvtltd1675496806065.d365c2ado), [QuantumWhisper](https://www.quantumwhisper.com/microsoft-dynamics-365-crm-azure-devops-integration)

How the link actually shows up on the work item side (mechanism, not vendor-specific): Azure Boards' generic mechanism for pointing a work item at an external, non-ADO object is the **Hyperlink** relation type (`rel: "Hyperlink"`, `usage: resourceLink`, confirmed via `az boards work-item relation list-type` output and the Link Types Reference Guide). A `Hyperlink` relation's `url` is the linked-to URL — e.g. a ServiceNow incident permalink or a Zendesk ticket URL — and `attributes.comment` can carry free text (e.g. "ServiceNow INC0012345"). Because *any* of these third-party connectors could equally choose to write a custom field instead of a Hyperlink relation (several connectors advertise "custom field" sync), `HelpdeskRef` derivation should be a two-path lookup: (1) scan `relations[]` for `rel == "Hyperlink"` entries whose `url` host matches a configured helpdesk domain pattern (e.g. `*.service-now.com`, `*.zendesk.com`), and (2) fall back to a configured custom field name (e.g. `Custom.ServiceNowRef`) if the org's integration writes one instead. Both paths are config-driven since no single reference name is guaranteed across integrations. [Link Types Reference Guide](https://learn.microsoft.com/en-us/azure/devops/boards/queries/link-type-reference?view=azure-devops)

Important: "External link type" (as distinct from Hyperlink) is explicitly documented as **only for linking to other Azure DevOps objects** (builds, commits, wiki pages) — "Use an external link type only to link to an Azure DevOps object. To link work items to objects outside Azure DevOps, use a hyperlink." This confirms Hyperlink, not External link, is the correct relation kind to scan for helpdesk cross-links. [Link Types Reference Guide](https://learn.microsoft.com/en-us/azure/devops/boards/queries/link-type-reference?view=azure-devops)

## 5. Service hooks (future trigger)

Azure DevOps Service Hooks can fire an HTTP POST (generic Webhooks consumer, or dedicated per-service consumers) on work item lifecycle events. Confirmed event type IDs relevant to a future trigger: `workitem.created`, `workitem.updated`, `workitem.deleted`, `workitem.restored`, `workitem.commented` (publisher `tfs`, resource name `workitem`). Subscriptions can be filtered server-side by `areaPath`, `workItemType`, and (for `updated`) `changedFields`, avoiding the need for the trigger receiver to filter noise itself. [Service Hook Events](https://learn.microsoft.com/en-us/azure/devops/service-hooks/events?view=azure-devops), [Webhooks with Azure DevOps](https://learn.microsoft.com/en-us/azure/devops/service-hooks/services/webhooks?view=azure-devops)

Payload envelope shape (standard across all ADO service hooks, not work-item-specific): `{ id, eventType, publisherId, scope, message: {text, html, markdown}, detailedMessage: {...}, resource: {...}, resourceVersion, resourceContainers, createdDate }`. The `resource` object's exact shape for `workitem.created`/`workitem.updated` — and how much of it is populated — is controlled by the subscription's "Resource details to send" setting (`All` / `Minimal` / `None`); `Minimal` sends only key identifying fields (id, URL), `All` sends the full work item body similar to a Get Work Item response. **The exact full-`All` JSON shape for `workitem.updated` (e.g. whether it includes a fields-diff or just the post-update snapshot) was not directly fetched from a live payload example in this pass** — Microsoft's events reference lists the event types and settings but the specific worked JSON example for work item events wasn't captured verbatim; a real webhook payload sample (e.g. from `danhellem/azure-devops-work-items-webhook-sample`, a third-party example repo) should be pulled before implementing the trigger receiver. [Service Hook Events](https://learn.microsoft.com/en-us/azure/devops/service-hooks/events?view=azure-devops), [Sample webhook receiver (community)](https://github.com/danhellem/azure-devops-work-items-webhook-sample)

For Sirdar's purposes, the practical design is: subscribe to `workitem.created` and `workitem.updated` with `Minimal` resource detail (id + url only), and have the trigger handler call the adapter's existing `GetByKey` to fetch a full, consistent snapshot rather than trusting the webhook body's field completeness — this also sidesteps the open question above.

## 6. Existing Go clients

`microsoft/azure-devops-go-api` (MIT license) is Microsoft's own, actively maintained thin Go wrapper over the ADO REST APIs — confirmed via `NewPatConnection` (PAT-based connection constructor) and `GetClientByResourceAreaId`, and a `workitemtracking` sub-package exposing `ClientImpl.GetWorkItem(ctx, GetWorkItemArgs)` plus related WIT operations (icons, etc. also present, confirming this is a broad wrapper, not narrowly scoped). It tracks the `v7` API surface (import path includes `/v7`) and shows active-repo signals (CI via GitHub Actions + Azure Pipelines, open issues/PRs). [microsoft/azure-devops-go-api](https://github.com/microsoft/azure-devops-go-api), [workitemtracking package docs](https://pkg.go.dev/github.com/microsoft/azure-devops-go-api/azuredevops/workitemtracking), [azuredevops package docs](https://pkg.go.dev/github.com/microsoft/azure-devops-go-api/azuredevops)

**Recommendation: use stdlib `net/http`, not this library**, consistent with Sirdar's stdlib-only adapter rule. Reasoning:
- The library is a genuine dependency (MIT, but still a third party pulled into `go.mod`), and Sirdar's constraint is explicit about avoiding that.
- The Azure DevOps WIT REST surface Sirdar needs (get-by-id, batch-get, comments, attachments-download, WIQL POST) is small, uniform (`api-version` query param, PAT/Bearer Basic-or-Bearer auth, plain JSON), and doesn't benefit much from a generated-client abstraction — it's arguably *more* code to learn the library's type surface than to write five stdlib HTTP calls with a shared auth/retry wrapper.
- This mirrors whatever pattern Sirdar's other adapters (Jira, etc.) already use for stdlib HTTP + typed response structs — consistency across adapters matters more than saving a few hundred lines here.
- **Caveat**: `azure-devops-go-api`'s maturity assessment here is based on repo signals (license, CI presence, import path) pulled via web search/fetch, not a full read of its source or changelog — commit recency, exact latest-release date, and any known bugs/limitations in its WIT client were not directly verified this pass.

## Recommended adapter design

**Config keys**
- `org` (Services) or `serverURL` + `collection` (Server) — Services base is fixed at `https://dev.azure.com/{org}`; Server base is `https://{server}/{collection}` (or legacy `http://{server}:{port}/tfs/{collection}`).
- `project` — required per WIT endpoint path segment.
- `apiVersion` — default `7.1`.
- `auth.mode` — one of `pat`, `entra-client-credentials`, `entra-managed-identity`, `ntlm` (Server only). Credential material (PAT string, client secret/cert ref, tenant/client IDs) via env/keychain refs per Sirdar's existing adapter convention — never inline in config.
- `helpdeskRef.hyperlinkDomains` — list of hostname globs (e.g. `*.service-now.com`, `*.zendesk.com`) to match against `Hyperlink` relation URLs.
- `helpdeskRef.customField` — optional reference name (e.g. `Custom.ServiceNowRef`) as a fallback/alternate source.
- `list.wiqlTemplate` or discrete `list.assignedToMe` / `list.stateExclude` / `list.parentID` knobs feeding a generated WIQL string — implementation detail, but the config should let an operator override the WIQL directly for org-specific process templates (state names vary by process: Agile/Scrum/CMMI/custom).

**Endpoints used**
- `GetByKey`: `GET .../_apis/wit/workitems/{id}?$expand=all&api-version={v}` (single call, gets fields + relations + links).
- Comments for `helpdesk` thread: `GET .../_apis/wit/workItems/{id}/comments?$expand=renderedText&order=asc&api-version=7.1-preview.4`, following `continuationToken` until exhausted.
- Attachment bytes: `GET .../_apis/wit/attachments/{guid}?fileName={name}&download=true&api-version={v}`, GUID and filename sourced from `AttachedFile` relations on the work item.
- `List`: `POST .../_apis/wit/wiql?api-version={v}` → collect IDs (flat query) → chunk into groups of ≤200 → `POST .../_apis/wit/workitemsbatch` per chunk with `errorPolicy: "Omit"` and `$expand: "All"` (or explicit `fields[]` if `All` proves too heavy for list views).

**Field mapping to Sirdar's `Ticket`**
- `Key` ← `id` (stringified)
- `Title` ← `System.Title`
- `Description` ← `System.Description`, HTML→text; for Bug work item type, fall back to/prefer `Microsoft.VSTS.TCM.ReproSteps` (unverified exact precedence rule — decide per-org or expose both under `Fields`)
- `Priority` ← `Microsoft.VSTS.Common.Priority`
- `Status` ← `System.State`
- `Assignee` ← `System.AssignedTo.displayName` (or `.uniqueName` for a stable identifier)
- `URL` ← work item's `_links.html.href` (web UI link) or construct from org/project/id
- `HelpdeskRef` ← derived per §4 (Hyperlink relation scan, then custom-field fallback)
- `CreatedAt` / `UpdatedAt` ← `System.CreatedDate` / `System.ChangedDate`
- `Fields` map ← everything else: `Severity`, `Tags` (split on `;`), `Parent`, `AreaPath`, `IterationPath`, and any org-specific custom fields

**Thread messages** (`helpdesk` role) ← one comment API call per work item, each `Comment` → `{At: createdDate, Author: createdBy.displayName, Role: "internal" (ADO has no external/customer-facing distinction natively — always internal engineering unless a helpdesk-integration convention marks otherwise), Text: renderedText (HTML→text) or text, AttachmentIDs: []}` — attachment-to-comment linkage isn't provided by the API (§1), so `AttachmentIDs` on individual messages will generally be empty, with all work-item-level attachments exposed once (e.g. attached to a synthetic first message, or as a separate ticket-level list if Sirdar's contract allows that).

**Auth**: default to PAT + Basic (`Authorization: Basic base64(":"+PAT)`, scope `vso.work`) for simplicity and parity with most self-hosted/personal use; document Entra client-credentials as the production-recommended path per Microsoft's own guidance, with the adapter's HTTP client abstracting the header-building so switching modes doesn't touch call sites. Rate-limit handling (Retry-After honoring, `X-RateLimit-*` awareness) belongs in the shared HTTP client wrapper, not per-endpoint code.

## Open questions

- Exact `Microsoft.VSTS.TCM.ReproSteps` vs `System.Description` precedence/co-presence on a real Bug work item — not independently verified against a live API response this pass.
- Whether `[System.Parent] = {id}` is a valid flat-WIQL WHERE clause, or whether children-of-parent must always go through a tree/`MODE (Recursive)` query returning `workItemRelations` instead of `workItems` — needs a live-org check before shipping the `List` parent filter.
- Behavioral/field differences between `api-version=7.1` and `7.2` for the WIT endpoints Sirdar uses — both are documented monikers but no delta was surfaced.
- Full JSON shape of a `workitem.updated` service-hook payload at `Resource details to send = All` (changed-fields diff vs. full snapshot) — not captured from a real payload sample this pass; needed before building the future trigger receiver.
- Whether any Azure DevOps Server (on-prem) version pins to an older max `api-version` than 7.1/7.2 — Server release cadence and which REST versions it exposes per Server version (2022, 2020, etc.) wasn't researched.
- Current functional status of the legacy `ms-vsts.services-zendesk` Marketplace service hook (may be deprecated) — flagged but not confirmed either way.
- No Go-based Azure DevOps MCP server was found; this is an absence-of-evidence finding from the searches run, not a confirmed "none exists."
- `azure-devops-go-api`'s current release cadence/last-commit date wasn't independently verified beyond generic "active" repo signals (CI presence, open issue/PR counts) — worth a direct check if the "use stdlib, not the library" recommendation is ever revisited.
