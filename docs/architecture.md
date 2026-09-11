# Architecture

A one-page map of the packages, the run lifecycle, the three read-only layers, and how the
desktop app observes a run. For adapter and provider detail, see `docs/adapters.md` and
`docs/config.md`; for the reasoning behind specific decisions, see `docs/research/` and
`docs/superpowers/specs/`.

## Package map

| Package | Role |
|---|---|
| `cmd/sirdar` | The CLI entry point: `init`, `doctor`, `triage`, `rca`, `resume`, `runs`, `register`, `serve`. Wires config, sources, providers, and the store together per command. |
| `internal/app` | The service layer the desktop app (and `sirdar serve`) binds to: workspace registration, run listing/detail, a filesystem `Watcher` that polls run directories and turns changes into events, budget/quota helpers. |
| `internal/httpapi` | The HTTP+SSE server `sirdar serve` runs: a JSON API over `internal/app.Service` plus `/api/events`, an SSE stream of the watcher's fan-out channel. Embeds the built frontend under `ui/`. |
| `internal/run` | The triage/RCA run loop: `prepare` (fetch ticket, build the bundle and prompt), `execute` (drive a provider session, stream events, enforce budgets, handle the schema-retry turn), `pool` (bounded concurrency across `sirdar triage KEY...`). |
| `internal/provider` | The `Provider`/`Session` contract every agent backend implements (`provider.go`), plus the shared `PermissionPolicy` and command-matching logic (`policy.go`) all three providers are judged by. |
| `internal/provider/claude` | Spawns the `claude` CLI in stream-json mode, translates its output into `provider.Event`s, and runs permission decisions through a `--permission-prompt-tool stdio` hook. |
| `internal/provider/codex` | Spawns `codex app-server` and speaks its JSON-RPC protocol over stdio. |
| `internal/provider/openai` | Sirdar's own agent loop against any OpenAI-compatible Chat Completions endpoint: no CLI spawned, `internal/agenttools` supplies the tool set, `internal/mcpclient` supplies MCP tools. |
| `internal/agenttools` | The read-only tool set `provider/openai`'s loop offers a model directly: file read, glob, grep, a confined `bash`, web fetch/search. No write tool exists in this package. |
| `internal/mcpclient` | An MCP client for `provider/openai`: starts the servers named in a workspace's `.mcp.json`, exposes their tools, and reads `permissions.mcp` to decide which may be called. |
| `internal/source` | Adapters for trackers and helpdesks. `source.go` defines the shared interfaces (`Tracker`, `Helpdesk`, `Warner`); `jira`, `linear`, `azdo`, `rally` (trackers), `zendesk`, `freshdesk`, `zohodesk` (helpdesks) are built-in stdlib HTTP clients; `plugin` is the client side of the external adapter protocol (`docs/adapters.md`); `htmltext` converts HTML ticket bodies to Markdown. |
| `internal/prompt` | Builds the prompt sent to the agent from the bundle, the note schema, and a workspace's playbooks (`playbooks/`, `playbooks.go`); `preamble.md` is the fixed framing every prompt starts with. |
| `internal/note` | Renders, validates, and diffs Triage/RCA/Resolution notes against their templates and frontmatter schema. |
| `internal/store` | Persists run state (`run.go`, `state.json`), the append-only event log (`events.go`, `events.jsonl`), and the issue register (`register.go`) to disk under `.sirdar/`. |
| `internal/config` | Loads and validates `.sirdar/config.yaml`; resolves `env:`/`keychain:` credential references (`creds.go`) and never accepts a literal secret; `scaffold.go` backs `sirdar init`. |
| `internal/ticket` | The normalized ticket types every adapter and the prompt builder share (`TrackerTicket`, `HelpdeskTicket`, `Thread`, `Attachment`), and bundle assembly (`bundle.go`). |
| `desktop/` | The Wails v2 desktop app: `main.go` boots the webview, `bridge.go` exposes `internal/app.Service` methods to the React frontend under `frontend/`, which is the same UI `sirdar serve` serves over HTTP. |

## The run lifecycle

A run moves through `preparing → running → completed`, or off to `failed`, `blocked`, or
`over_budget`.

1. **Preparing** (`internal/run/prepare.go`): fetch the ticket from the tracker (and helpdesk, via
   `HelpdeskRef` or the regex fallback), download and filter attachments, write `bundle/` and
   `prompt.md` to the run directory, and record `state.json` with `state: preparing`.
2. **Running** (`internal/run/execute.go`): start a provider session with the prompt and the
   workspace's `PermissionPolicy`, stream its events to `events.jsonl`, and watch three budgets —
   turns, wall-clock minutes, USD — against `budget.*` in config. A session whose structured
   output fails schema validation gets one retry turn (`Send`) before the run fails.
3. **Terminal state**: `completed` writes `result.json` and the rendered `note.md` (copied to the
   configured notes directory); `blocked` means the agent asked a question or hit a rate limit and
   is continued with `sirdar resume RUN_ID`; `failed` and `over_budget` end the run without a note.

Each run gets its own directory, `.sirdar/runs/<KEY>/<run-id>/`, holding `bundle/`, `prompt.md`,
`events.jsonl`, `result.json` (on success), `note.md` (on success), and `state.json` throughout.
An `rca` run's bundle additionally carries the triage note under review, the `--resolution` text,
and, when `--pr` is given and `gh` is available, the PR's title, body, and diff.

## The three read-only layers

Sirdar never writes to a tracker or helpdesk and never opens a pull request — it only ever
records, in the RCA and Resolution notes, a fix a human already made. That guarantee is enforced
independently at three layers, so a gap in one doesn't undo it:

1. **`PermissionPolicy`** (`internal/provider/policy.go`), shared by all three providers: `Edit`,
   `Write`, `MultiEdit`, `NotebookEdit` are always denied; a `Bash`/`bash` command is allowed only
   when every segment of it matches an operator-configured glob and every path-shaped argument
   stays inside the workspace root (`MatchCommand`) — a heuristic read of the command text, not a
   sandbox.
2. **Provider-level enforcement**: the Claude provider additionally passes
   `--disallowedTools Write,Edit,MultiEdit,NotebookEdit` to the CLI; the Codex provider runs with
   `sandbox: read-only`; `provider: openai` simply has no write tool in `internal/agenttools` to
   withhold.
3. **MCP tool gating**: `mcp.workspaceOnly` (default `true`) limits which MCP servers a session
   can see to whatever a workspace's `.mcp.json` names; `permissions.mcp` then limits which of
   those servers' tools may be called, by explicit glob or, absent one, a write-verb naming
   heuristic (`internal/mcpclient`, `internal/provider/policy.go`'s `decideMCP`).

## How the desktop app observes a run

The desktop app and `sirdar serve` share one `internal/app.Service` and one React frontend
(`desktop/frontend`); the difference is only the binding. The desktop app (`desktop/bridge.go`)
exposes `Service` methods to the webview directly through Wails bindings; `sirdar serve`
(`internal/httpapi`) exposes the same operations as a JSON HTTP API plus an SSE stream.

Neither hooks into the runner process directly. `internal/app.Watcher` polls every registered
workspace's `.sirdar/runs/` tree on an interval (`DefaultInterval`, 500ms): a changed
`state.json` becomes a `run.updated` event, and a new line appended to an active run's
`events.jsonl` becomes a `run.event`. This is deliberate — a run (`sirdar triage`, `sirdar rca`)
and the app observing it are separate processes with no shared memory, so the run directory on
disk is the only interface between them. `flushGrace` keeps tailing an active run's event log for
a couple of seconds after it leaves `preparing`/`running`, so the poll interval doesn't clip the
last lines a run writes (the final event, the usage tick).

## See also

- `docs/adapters.md` — the built-in adapters and the external adapter protocol in full.
- `docs/config.md` — every `.sirdar/config.yaml` key.
- `docs/research/04-harness-internals.md`, `docs/research/06-wire-formats.md` — how the Claude
  and Codex CLIs' own wire formats map onto the `provider.Event` contract.
- `docs/superpowers/specs/2026-09-10-sirdar-v0-triage-core-design.md`,
  `docs/superpowers/specs/2026-09-10-sirdar-desktop-design.md`,
  `docs/superpowers/specs/2026-09-10-openai-provider-design.md` — the design docs this
  architecture was built from.
