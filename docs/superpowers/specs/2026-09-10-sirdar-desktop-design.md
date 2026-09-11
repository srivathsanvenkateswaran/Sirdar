# Sirdar desktop and `sirdar serve` (design)

Date: 2026-09-10. Status: decided by the maintainer ("choose the most efficient and start"),
built on branch `desktop`. Companion: `2026-09-10-sirdar-v0-triage-core-design.md`.

## Goal

A second surface for the same core: a desktop app for macOS, Windows and Linux, and a
browser mode (`sirdar serve`) that ships the same UI inside the CLI binary. Both let an L2
engineer see the ticket queue, start triage and RCA runs, watch the agent work live, read and
approve notes, see quota, and check the workspace's health. Nothing in the UI writes to a
helpdesk or tracker in this version.

## Decisions

| Decision | Choice | Why |
|---|---|---|
| Shell | Wails v2.15 (Go + OS webview) | one codebase; system WebView (WKWebView, WebView2, WebKitGTK); ~10 MB binary, low RAM; links the existing Go packages in-process. Wails v3 is still beta (v3.0.0-beta.19 on 2026-09-10). |
| Second shell | `sirdar serve --addr 127.0.0.1:7777 --open` | same frontend embedded in the CLI; useful over SSH and for anyone who does not want an app |
| Frontend | React 19 + TypeScript + Vite, no CSS framework, `react-markdown` for notes | minimal dependency surface, fast dev loop |
| Integration with the core | file-based observation | the app launches runs through `run.Runner` and watches `.sirdar/runs/**/state.json` and `events.jsonl` as the source of truth; no changes to `internal/run`; runs started from a terminal appear too |
| Live updates | Wails runtime events in the app; Server-Sent Events in browser mode | stdlib `net/http` suffices, no WebSocket dependency |
| Workspaces | `~/.sirdar/workspaces.json`, a list of roots | multi-repo from one window |
| Fix flow | not in this version | the core has no fix runner yet; the UI shows the note and the approval state only |

## Layout on the `desktop` branch

```
desktop/                       Wails v2 project (main package: desktop/main.go, wails.json)
desktop/frontend/              Vite + React + TS app (shared by both shells)
desktop/frontend/src/api/      types.ts (contract below), transport.ts (Wails or HTTP+SSE)
internal/app/                  application service: registry, service, watcher, quota
internal/httpapi/              JSON + SSE handlers, embedded UI
internal/httpapi/ui/dist/      frontend build output (gitignored; .gitkeep committed)
cmd/sirdar/cmd_serve.go        `sirdar serve`
Makefile                       ui (build frontend and copy to internal/httpapi/ui/dist), desktop (wails build), serve
.github/workflows/desktop.yml  wails build matrix (macos, windows, ubuntu), artifacts only
```

## internal/app

```go
type Registry struct{ Path string }                       // ~/.sirdar/workspaces.json
func (r *Registry) List() ([]Workspace, error)
func (r *Registry) Add(root string) (Workspace, error)     // validates config.Load(root)
func (r *Registry) Remove(id string) error

type Workspace struct{ ID, Name, Root, Provider, Model, NotesDir string }  // ID = sha1(root)[:12]

type Service struct { /* registry, deps builder, watcher, job table */ }
func New(reg *Registry, build DepsBuilder, opts Options) *Service
type DepsBuilder func(cfg *config.Config, provider, model string, stderr io.Writer) (run.Deps, func(), error)

func (s *Service) Workspaces() ([]Workspace, error)
func (s *Service) AddWorkspace(root string) (Workspace, error)
func (s *Service) RemoveWorkspace(id string) error
func (s *Service) Queue(ctx, wsID string, f QueueFilter) ([]Ticket, error)   // tracker.List + latest run per key; ErrUnsupported when no tracker
func (s *Service) Runs(wsID, key string) ([]RunSummary, error)
func (s *Service) Run(wsID, runID string) (RunDetail, error)
func (s *Service) Events(wsID, runID string, after int) ([]RunEvent, int, error) // lines after index; returns next index
func (s *Service) Note(wsID, runID string, kind string) (string, error)        // markdown
func (s *Service) Prompt(wsID, runID string) (string, error)
func (s *Service) StartTriage(ctx, wsID string, keys []string, o TriageOptions) (JobID, error) // async
func (s *Service) StartRCA(ctx, wsID, key string, o RCAOptions) (JobID, error)
func (s *Service) Resume(ctx, wsID, runID, answer string) (JobID, error)
func (s *Service) Cancel(jobID JobID) error                                     // only jobs this process started
func (s *Service) Register(wsID string) ([]store.RegisterRow, error)
func (s *Service) Doctor(ctx, wsID string) ([]Check, error)
func (s *Service) Quota() []Quota
func (s *Service) Subscribe() (<-chan Event, func())                          // fan-out; unsubscribe func
```

Watcher: polls every 500 ms (configurable) each workspace's `.sirdar/runs/*/*/state.json`
mtime and size; on change emits `run.updated`. For runs in `preparing`/`running` it tails
`events.jsonl` from the last byte offset and emits one `run.event` per new line. Stdlib only.

Quota: derived from `rate_limited`/`system` events whose raw payload is a Claude
`rate_limit_event` (fields `unifiedWindows.five_hour.utilization`, `resetsAt`, `seven_day`)
or a Codex `account/rateLimits/updated` (`primary.usedPercent`, `resetsAt`); the newest per
provider across all watched runs in the last 24 h.

Jobs: `StartTriage` runs `Runner.Triage` in a goroutine with a cancellable context; the job
table maps JobID → cancel; `job.finished` carries the outcomes. Run ids appear through the
watcher.

Event kinds: `run.updated {workspaceId, run}`, `run.event {workspaceId, runId, index, event}`,
`quota.updated {quota}`, `job.finished {jobId, workspaceId, outcomes}`, `log {text}`.

## HTTP API (`internal/httpapi`)

All JSON, all under `/api`, bound to loopback by default. Errors: `{"error":{"code","message"}}`.

```
GET    /api/workspaces                          -> Workspace[]
POST   /api/workspaces          {root}          -> Workspace
DELETE /api/workspaces/{id}
GET    /api/workspaces/{id}/queue?assignee=&status=&limit=  -> Ticket[]   (501 when unsupported)
GET    /api/workspaces/{id}/runs?key=           -> RunSummary[]
GET    /api/workspaces/{id}/runs/{runId}        -> RunDetail
GET    /api/workspaces/{id}/runs/{runId}/events?after=N -> {events: RunEvent[], next: N}
GET    /api/workspaces/{id}/runs/{runId}/note?kind=triage|rca|resolution  -> text/markdown
GET    /api/workspaces/{id}/runs/{runId}/prompt -> text/markdown
POST   /api/workspaces/{id}/triage  {keys[], provider?, model?, dryRun?}  -> 202 {jobId}
POST   /api/workspaces/{id}/rca     {key, prUrl?, resolution?}            -> 202 {jobId}
POST   /api/workspaces/{id}/runs/{runId}/resume {answer?}                 -> 202 {jobId}
POST   /api/jobs/{jobId}/cancel                                            -> 202 | 404
GET    /api/workspaces/{id}/register            -> RegisterRow[]
GET    /api/workspaces/{id}/doctor              -> Check[]
GET    /api/quota                               -> Quota[]
GET    /api/events                              -> text/event-stream; `event: <kind>` + `data: <json>`
GET    /                                         -> embedded UI (index.html fallback for client routes)
```

`sirdar serve [--addr 127.0.0.1:7777] [--open] [--workspace PATH]` registers the current
workspace (or `--workspace`) if not already registered, starts the server, prints the URL,
opens the browser with `--open`. Non-loopback addresses require `--allow-remote` and print a
warning (no auth in this version).

## Frontend contract (`desktop/frontend/src/api/types.ts`)

```ts
export type RunState = 'preparing'|'running'|'completed'|'failed'|'blocked'|'over_budget';
export type NoteKind = 'triage'|'rca'|'resolution';
export interface Workspace { id: string; name: string; root: string; provider: 'claude'|'codex'; model: string; notesDir: string }
export interface Usage { turns: number; inputTokens: number; outputTokens: number; costUsd: number }
export interface RunSummary { runId: string; key: string; kind: 'triage'|'rca'; status: RunState; provider: string; model: string; startedAt: string; updatedAt: string; reason: string; usage: Usage; notes: string[] }
export interface RunDetail extends RunSummary { promptPath: string; bundleDir: string; warnings: string[]; handle: string; budget: { maxTurns: number; maxMinutes: number; maxUsd: number } }
export interface RunEvent { t: string; kind: string; payload: { tool?: string; decision?: string; text?: string; turns?: number; costUsd?: number; raw?: unknown } }
export interface Ticket { key: string; title: string; priority: string; status: string; assignee: string; url: string; helpdeskRef: string; updatedAt: string; latestRun?: RunSummary }
export interface Quota { provider: string; observedAt: string; fiveHour?: { utilization: number; resetsAt: string }; sevenDay?: { utilization: number; resetsAt: string }; usedPercent?: number; resetsAt?: string }
export interface RegisterRow { key: string; kind: string; runId: string; date: string; provider: string; model: string; service: string; classification: string; confidence: string; severity: string; turns: number; costUsd: number; triageVerdict: string; notePath: string }
export interface Check { name: string; ok: boolean; detail: string }
export type AppEvent =
  | { kind: 'run.updated'; workspaceId: string; run: RunSummary }
  | { kind: 'run.event'; workspaceId: string; runId: string; index: number; event: RunEvent }
  | { kind: 'quota.updated'; quota: Quota }
  | { kind: 'job.finished'; jobId: string; workspaceId: string; outcomes: { key: string; status: RunState; runId: string }[] }
  | { kind: 'log'; text: string };
export interface Transport {
  workspaces(): Promise<Workspace[]>; addWorkspace(root: string): Promise<Workspace>; removeWorkspace(id: string): Promise<void>;
  queue(ws: string, f?: { assignee?: string; status?: string; limit?: number }): Promise<Ticket[]>;
  runs(ws: string, key?: string): Promise<RunSummary[]>; run(ws: string, runId: string): Promise<RunDetail>;
  events(ws: string, runId: string, after: number): Promise<{ events: RunEvent[]; next: number }>;
  note(ws: string, runId: string, kind: NoteKind): Promise<string>; prompt(ws: string, runId: string): Promise<string>;
  startTriage(ws: string, keys: string[], o?: { provider?: string; model?: string; dryRun?: boolean }): Promise<{ jobId: string }>;
  startRCA(ws: string, key: string, o?: { prUrl?: string; resolution?: string }): Promise<{ jobId: string }>;
  resume(ws: string, runId: string, answer?: string): Promise<{ jobId: string }>; cancel(jobId: string): Promise<void>;
  register(ws: string): Promise<RegisterRow[]>; doctor(ws: string): Promise<Check[]>; quota(): Promise<Quota[]>;
  subscribe(handler: (e: AppEvent) => void): () => void;
}
```

`transport.ts` exports `createTransport()`: when `window.go?.main?.Bridge` exists (Wails), call
bindings and subscribe through `window.runtime.EventsOn`; otherwise use `fetch` against
`/api` and `EventSource('/api/events')`. JSON field names are camelCase on the wire; the Go
types carry `json:"..."` tags accordingly.

## Screens

1. **Board** (default). Header: workspace switcher, quota meter (five-hour and weekly bars for
   Claude, usedPercent for Codex), "New triage" button (keys input, provider/model override,
   dry-run toggle). Columns: Queue (tickets from the tracker with no run), Gathering
   (preparing/running), Needs input (blocked), Triaged (completed triage without RCA), Done
   (RCA completed), Failed (failed/over_budget). Cards: key, title, priority, elapsed, cost,
   last tool. Clicking a card opens Run detail; a Queue card offers "Triage".
2. **Run detail**. Left: live event stream grouped by turn (tools with inputs collapsed,
   permission denials highlighted, usage ticks, final). Right: tabs Note (markdown), Prompt,
   Bundle (file list with attachment thumbnails), State (JSON). Actions: Resume (with answer
   box when blocked on a question), Cancel (only for jobs this process started), Start RCA
   (form: PR URL, resolution text), Open note in editor (`sirdar://` not needed: reveal path).
3. **Register**. Table from `register.jsonl` grouped per key with verdict badges; filter by
   service and confidence; summary line: hypothesis held X of Y.
4. **Settings**. Workspaces (add by path, remove), Doctor results per workspace, provider
   and billing shown read-only from config, link to `docs/config.md`.

Design direction: quiet, dense, monospace for keys and tool names, one accent colour, works
in light and dark, keyboard: `n` new triage, `/` filter, `esc` back.

## Desktop shell (`desktop/`)

`desktop/main.go` builds `app.Service` with the same `DepsBuilder` the CLI uses (move
`buildDeps` from `cmd/sirdar/wire.go` into `internal/app/wire.go` so both shells share it;
`cmd/sirdar` keeps a thin call). `Bridge` struct exposes the Service methods to the frontend;
a goroutine forwards `Subscribe()` events to `runtime.EventsEmit(ctx, e.Kind, e)`. Window
1200x800, title "Sirdar", app id `dev.sirdar.desktop`. `wails build` per OS; CI matrix
uploads artifacts (no signing or notarisation in this version).

## Testing

- `internal/app`: registry round-trip; watcher detects a state change and tails events
  (write files in a temp workspace, assert events on the channel within 2 s); quota derivation
  from recorded raw payloads; StartTriage with a stub `DepsBuilder` and stub provider →
  `job.finished`, run visible via `Runs`; Cancel of a running job.
- `internal/httpapi`: httptest for every endpoint incl. 501, 404, 202; SSE handler streams two
  events then closes on client disconnect; index fallback.
- Frontend: vitest for the store reducer and the transport's HTTP path against a mocked fetch.
- e2e: `sirdar serve` against the file adapter and the fake claude script from
  `cmd/sirdar/testdata`, driven over HTTP: POST triage → poll runs → completed → GET note.

## Work breakdown (parallel waves)

Wave 1 (independent): D1 scaffold Wails v2 + Vite app + types.ts + Makefile targets + CI;
D2 `internal/app`; D3 `internal/httpapi` + `cmd_serve.go` + embed.
Wave 2 (after D1): D5 transport + store + Board; D6 Run detail; D7 Register + Quota +
Settings; D8 Wails Bridge + events + `desktop/main.go` wiring (after D2).
Wave 3: D9 e2e over HTTP; rebase `desktop` onto `main` once the core fix wave lands; final review.
