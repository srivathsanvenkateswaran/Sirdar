# Contributing to Sirdar

Sirdar is a personal open-source project with one maintainer
([@srivathsanvenkateswaran](https://github.com/srivathsanvenkateswaran)). Contributions are
welcome, but review happens on my schedule, not on a service-level agreement.

## Build and test

```
make build   # ./sirdar
make test    # go test ./...
make vet     # go vet ./...
make ui      # build the shared React frontend and stage it for internal/httpapi to embed
```

`make ui` runs `npm ci` and `npm run build` under `desktop/frontend`, then copies the output
into `internal/httpapi/ui/dist`. Run it before `make build` if you changed anything under
`desktop/frontend` and want the embedded `sirdar serve` UI to reflect it.

The desktop app is a separate build, driven by Wails:

```
cd desktop && wails build
```

This needs the Wails v2 CLI (`go install github.com/wailsapp/wails/v2/cmd/wails@v2.15.0`) and a
working frontend build, so run `make ui` first if `desktop/frontend/dist` is stale.

Provider tests never touch a real CLI. The test binary replays a canned stream-json script and
is handed to the provider as `SessionSpec.Binary`, so `go test ./...` runs offline and looks up
nothing on `PATH`. If you're adding provider behavior, add a fixture rather than shelling out to
a real `claude` or `codex` binary in a test.

## Branching and review

One feature per branch, branched off `main`. Keep a branch to one concern — an adapter, a
provider change, a CLI command — so a review has one thing to evaluate rather than several
unrelated ones bundled together. Open a pull request against `main` and expect review comments
before merge; a PR that touches adapter code or anything that makes a credentialed HTTP request
gets read for the host-trust checklist below specifically, not just skimmed.

## The read-only guarantee

Sirdar reads tickets and writes notes. It never opens a pull request, never comments on a
tracker or helpdesk, and never mutates anything outside its own `.sirdar/runs/` directory and
the configured notes directory. This is a design invariant, not an implementation detail, and it
is enforced in three independent places:

- `PermissionPolicy` in `internal/provider/policy.go` denies `Edit`, `Write`, `MultiEdit`, and
  `NotebookEdit` outright (`AlwaysDenied`), and only allows a `Bash` command whose every segment
  matches an allow-list pattern and stays inside the workspace root (`MatchCommand`).
- The Claude provider passes `--disallowedTools Write,Edit,MultiEdit,NotebookEdit` to the CLI as
  belt-and-braces alongside the policy.
- The Codex provider runs with `sandbox: read-only`.
- `provider: openai` never offers a write tool in the first place — the tool set Sirdar's own
  agent loop exposes (`internal/agenttools`) has no file-mutation tool to deny.

Do not add a tool, a provider flag, or an agent-loop capability that lets a triage or RCA run
write to the tracker, the helpdesk, or a file outside its run directory, even behind a flag
defaulted off. If a feature seems to need that, it's the wrong feature for this tool — say so in
the PR description and we can talk about it, but the read-only guarantee is not something a
single PR gets to relax.

## Writing an adapter

An adapter — built-in under `internal/source/`, or an external process speaking the protocol in
`docs/adapters.md` — talks to a tracker or helpdesk API. The checklist:

- **Standard library only.** No HTTP client library, no vendor SDK. Every built-in adapter is a
  plain `net/http` client; keep it that way so nothing pulls in a dependency with its own
  security surface.
- **Use the `httpx` helpers once they exist** (`docs/research/08-httpx-extraction.md` inventories
  them: host trust, redirect policy, retry-after parsing, limited reads, download, filename
  sanitizing, warnings, list limits). Until the extraction lands, follow the same patterns the
  existing seven adapters use — copy their shape, not just their intent.
- **Host-trust every credentialed fetch.** Any URL your adapter got from an API response body —
  not from config — must be checked against the ticket's own host before your client sends the
  Authorization header to it. This covers the obvious case (an attachment download) and the ones
  that are easy to miss: a pagination `next` link, and a redirect Location header. Go's
  `http.Client` strips `Authorization` on a cross-host redirect but still follows the redirect and
  still writes whatever comes back to disk, so a `CheckRedirect` that applies the same host check
  to every hop is required, not optional — see `jira/attachments.go`'s `downloadClient` for the
  pattern. A custom auth header (Rally's `ZSESSIONID`) isn't stripped by Go at all, so those
  adapters need the redirect check even more.
- **Warn per ticket, don't fail the run.** A missing attachment, a truncated thread, a dropped
  page — report it as a warning on that ticket's bundle (`source.Warner`) so it shows up in the
  prompt and the note, rather than failing the whole `Get`/`Threads`/`Attachments` call over one
  bad item.
- **Map errors to the shared codes**: `not_found`, `auth`, `unsupported`, `rate_limited`,
  `internal` (see `docs/adapters.md`'s Error codes table). Don't invent new codes; the caller
  branches on these.
- **Test with `httptest` fixtures.** Every built-in adapter's tests spin up an
  `httptest.Server` and assert against canned responses — no test may reach a real API. Cover the
  host-trust behavior explicitly: a fixture that serves a redirect or a pagination link pointing
  off-host, asserting the adapter refuses it.
- **Never log credentials.** Not in an error message, not in a debug line, not in a warning
  surfaced to the run. If a request fails with a 401, say that it failed — don't include the
  token, the Authorization header, or the resolved secret in what you report.

## Writing a provider

A provider (`internal/provider/{claude,codex,openai}`) adapts one agent CLI or loop to the
`provider.Session` contract in `internal/provider/provider.go`. The checklist:

- **Honor the events contract.** `Events()` returns a channel that closes when the session ends,
  for any reason — success, failure, cancellation. Every event kind you emit
  (`assistant_text`, `tool_started`, `tool_finished`, `permission`, `usage`, `rate_limited`,
  `question`, `final`, `system`, `error`) should carry `Raw` set to the original provider line, so
  a bug in your own parsing can still be diagnosed from the recorded event.
- **Implement `CloseInput` correctly.** A CLI reading stream-json on stdin holds its stdout open
  waiting for the next message, so a session only ends once its input is closed. `CloseInput`
  must be idempotent (safe to call more than once) and a no-op for a provider with no input
  stream to close (Codex: one turn's completion already ends the exchange).
  See `internal/provider/claude/claude.go`.
- **Drain before you `Wait`.** The runner (`internal/run/execute.go`) always drains `Events()` to
  its close before calling `Wait()`, and may call `Send` from inside that drain (the schema-retry
  turn). A provider's `Wait` must not block on anything that only happens as a side effect of the
  caller reading events, or the two goroutines deadlock. See the comments on `codex.go`'s
  `closeStream` and `openai/loop.go`'s `CloseInput` for the ordering this depends on.
- **Respect the permission policy, don't reimplement it.** `PermissionPolicy.Decide` in
  `internal/provider/policy.go` is the single source of truth for what a tool call may do.
  `AlwaysAllowed` and `AlwaysDenied` are shared between Claude/Codex's tool names and Sirdar's own
  agent-loop tool names on purpose — add a new read-only tool there rather than special-casing it
  in a provider.
- **Produce structured output**, not prose the caller has to parse. `SessionSpec.OutputSchema`
  is the JSON schema the final answer must validate against; a provider that can't validate
  locally should still ask the model for JSON and let the runner's schema-retry turn handle a
  failure.

## Commit style

- Imperative subject line (“Add Freshdesk attachment host check”, not “Added” or “Adds”).
- No AI attribution of any kind — no `Co-Authored-By` trailer naming an AI tool or model, no
  “Generated with” footer, regardless of what wrote the diff. A commit is authored by the person
  who reviewed and is responsible for it.
- Keep a commit to one logical change; a large feature can still be several commits on the same
  branch.

## Keeping docs in sync

Every key in `.sirdar/config.yaml` is documented in `docs/config.md`: its default, what it means,
and an example where the shape isn't obvious. A PR that adds, renames, or changes the default of
a config key must update `docs/config.md` in the same PR — a config key with no entry there is
effectively undocumented, whatever the code comments say. `docs/adapters.md` gets the equivalent
treatment for a new built-in adapter or a protocol change to the external adapter interface.
