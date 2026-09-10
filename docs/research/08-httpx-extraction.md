# Follow-up: the HTTP helpers every adapter now carries its own copy of (2026-09-10)

Written after the whole-branch review of `adapters`. Nothing here is implemented; this is the
inventory a later extraction task starts from, plus the reason each helper is worth sharing and
the reason none of them was shared while the adapters were being written.

Seven adapters live under `internal/source`: `jira`, `linear`, `azdo`, `rally` (trackers),
`zendesk`, `freshdesk`, `zohodesk` (helpdesks). Each is a self-contained stdlib HTTP client, and
they were built in parallel on purpose — a shared client written before four of them existed
would have been designed against one API's quirks. Now that all seven exist, the same eight
shapes appear in every one of them, with small differences that are mostly accidents of who
wrote which file rather than differences the APIs actually demand. A bug fixed in one copy is a
bug still live in six.

The proposed home is `internal/source/httpx`. The proposed contents:

| Helper | What it does | Copies live in |
|---|---|---|
| `Trust` | Decides whether a URL taken out of a response body may be fetched with a credential: host match (normalised — lowercased, trailing root dot dropped, default port removed), https required unless the configured base URL is itself http, userinfo refused. Some adapters also answer a second question — trusted to fetch, but not to send the credential to — for a first-party CDN. | `jira/attachments.go` (`normalizeHost`, `trustedURL`, `trustedRawURL`), `azdo/attachments.go` (`trusted`, `normalizedHost`, `orgName`), `rally/client.go` (`normalizeHost`, `trustedURL`), `zendesk/client.go` (`hostTrust`, `trustedNextPage`), `freshdesk/client.go` (`hostKey`, `attachmentTrust`, `urlTrust`), `linear/mapping.go` (`isUploadURL`) |
| `RedirectPolicy` | An `http.Client.CheckRedirect` that applies a `Trust` to every hop and stops the chain after N (3 in five adapters, 5 in Jira). Exists because Go strips `Authorization` on a cross-host hop but still makes the request and still writes the answer to disk — and strips nothing at all from a custom auth header like Rally's `ZSESSIONID`. | `jira/attachments.go` (`downloadClient`), `azdo/attachments.go` (`downloadClient`), `rally/client.go` (`checkRedirect`), `zendesk/attachments.go` (`attachmentHTTPClient`), `freshdesk/client.go` (`checkRedirect`), `linear/client.go` (`downloadClient`) |
| `RetryAfter` | Parses `Retry-After` in both documented forms (delta-seconds, HTTP-date), refuses a negative or unparseable value, and reports whether the wait is inside the 30 s ceiling the shared design sets. Paired with a context-aware sleep (`sleepCtx`). | `rally/client.go`, `zendesk/client.go`, `freshdesk/client.go` (`retryAfter`, `sleepCtx`), `jira/client.go`, `azdo/client.go`, `linear/client.go` |
| `ReadLimited` | Reads at most N bytes and *fails* when the reader had more to give, so a truncated body is never decoded as a short but well-formed result. | `rally/client.go`, `freshdesk/client.go` (both named `readLimited`), plus the inline `io.LimitReader(body, max+1)` pattern in `zendesk/client.go` and `jira/client.go` |
| `Download` | Streams one response body to a destination path under a byte ceiling, removes the partial file on failure, and returns the served `Content-Type`. Some adapters additionally refuse an HTML body where a binary was expected — that is the sign-in page an SSO redirect serves. | `jira/attachments.go` (`download`, `isHTML`), `azdo/attachments.go` (`download`), `zendesk/attachments.go` (`downloadAttachment`), `freshdesk/client.go` (`downloadTo`), `linear/client.go` (`download`); `rally/attachments.go` decodes base64 instead and needs only the ceiling |
| `SanitizeName` | Turns an API-supplied filename into a safe path component: `filepath.Base`, separators and control characters stripped, `""`/`.`/`..` → `attachment`, capped at 120 bytes without splitting a rune and preserving the extension (`capBytes`, `truncateValidUTF8`). Seven byte-identical copies but for Rally's extra backslash pass. | `jira/attachments.go`, `azdo/attachments.go`, `rally/attachments.go`, `zendesk/attachments.go`, `freshdesk/attachments.go`, `linear/mapping.go`, `zohodesk/attachments.go` |
| `Warnings` | The per-ticket warning store behind `source.Warner`: a mutex-guarded `map[id][]string`, written as a call ends and drained by `WarningsFor(id)`. The adapters disagree on two points a shared version has to settle — replace or append across the Get/Threads/Attachments calls that make up one bundle, and whether an identical line is deduped. | `jira/client.go` (`collector`, `withCollector`, `warnCtx`, `publish`), `rally/client.go` (`warnBuf`, `putWarnings`), `linear/client.go` (`putWarnings`, `addWarnings`), `zendesk/client.go` (`addWarnings`, `takeWarnings`), `freshdesk/client.go` (`putWarnings`, `takeWarnings`), `azdo/client.go` (`putWarnings`) |
| `Limit` | Applies the `List` bounds from the shared design — a default when the caller names none, a hard ceiling, a warning when a caller's request is capped — and the per-page size that follows from them. Also the page-count cap that stops a server which paginates for ever (`maxSearchPages`, `maxCommentPages`, `maxConversationPages`, `maxPageSize`). | `jira/search.go`, `jira/mapping.go`, `linear/client.go`, `azdo/wiql.go`, `rally/query.go`, `zendesk/client.go`, `freshdesk/client.go` |

## What the extraction has to decide, not just move

- **`Trust` is not one predicate.** Jira and Rally trust exactly one host. Azure DevOps also
  trusts `dev.azure.com` and `{org}.visualstudio.com` for the configured organisation. Zendesk
  and Freshdesk trust a set of first-party CDN suffixes to *fetch from* while refusing to send
  the credential there. Linear trusts one fixed host that is not the API host at all. The shared
  type needs a host rule plus a per-host credential decision, not a boolean.
- **The http exception is per-adapter.** Zendesk and Azure DevOps have a legitimate plain-HTTP
  configuration (a self-hosted collection, a `baseUrl` override); Freshdesk and Linear do not,
  because their base URL is always https by construction. The rule "https, or the configured
  base URL's own scheme" covers both, but only if every adapter actually records its base
  scheme.
- **Warning semantics are a behaviour change, not a refactor.** Zendesk appends across calls and
  now dedupes; Rally, Linear and Freshdesk replace. Picking one changes what reaches
  `prepare.go`, so it needs its own test pass rather than riding along with the move.
- **Do not extract the mapping.** `source.Error` codes, role mapping, and the response envelopes
  are genuinely per-API. This is an HTTP-plumbing package, not an adapter framework.
