# UI build: the harness surface (design)

Date: 2026-09-15. Status: approved by the user on the reviewed mocks in
`docs/design/2026-09-15-screens/` (open `index.html` for the walkthrough). Both surfaces are
built from the same frontend: the Wails desktop app (`desktop/`) and `sirdar serve`'s web UI.
The desktop app is the one the user expects to live in.

## What changes

The app stops being a board with a run detail behind it and becomes a session-first harness
surface. The mocks are the visual contract; this file states the behaviour behind each screen
and what the backend already offers. Nothing here widens what a run may do.

### Scale and shell

- Type is re-based to a 16px UI face (Figtree, then Inter, then the system sans), 14px meta,
  12px micro; mono ledger text 13.5px. The serif (Newsreader) stays for note titles and the
  settings page heading only. Controls are 40px tall; setting rows 72px; table rows 48px.
- The sidebar is 248px: the wordmark with the workspace as a badge, six nav rows (Sessions,
  Board, Register, Eval, Library, Settings) at 44px with 20px icons, a Recent sessions list,
  and a footer card with plan usage (the quota chips) and the one New session button.
- The sheet keeps its 16px radius on the warm ground; no shadow anywhere.
- Motion: the sheet's content rises 8px and fades over 320ms on navigation; the settings modal
  scales from 0.98 over 300ms behind a 200ms scrim; both freeze under reduced motion. The
  live-run pulse stays the only loop.
- Provider marks: the vendors' own marks, white on a tile of the brand colour (`claude`
  orange, `qwen` violet, `antigravity` blue, `copilot`/`codex`/`opencode`/`cursor`/`kimi` on
  the ink). Brand colours live in the marks module, not in the tokens file.

### Screens

1. **New session** (`#/new`): a search-style bar for a ticket key or URL, a segmented
   Triage / RCA / Fix, playbook, model and workspace chips, Start. "Landed today" lists the
   queue (`queue()` filtered to the user) as item rows with a Triage button each.
   Start calls `startTriage` / `startRCA` / `startFix` and opens the run.
2. **Session** (`#/run/<id>`): the run's transcript on the left with a banner for the last
   finished step (tests passed, note filed, blocked question), the composer at the bottom:
   Answer while blocked (`resume`), Steer once finished (`steer`), disabled while running
   with Cancel in the topbar. The right pane has Changes (fix runs: `runDiff`, Keep/Drop per
   hunk via `dropHunk`, checks from the run's events, the branch name), Note, Bundle, Tools.
   The topbar shows key, kind, state badge, title, provider mark, model, clock, turns, cost.
3. **Change review** (`#/run/<id>/review`): the same diff full width with the file rail,
   checks, what the agent said (the fix summary), and the resolution note; the footer names
   the worktree and base. Create branch is shown only when the run can be pushed by an API;
   until then the footer shows the branch name and the CLI line that pushes it.
4. **Board** (`#/board`): Jira-shaped cards (title, kind chip, state glyph and word, clock on
   live and blocked, key and provider mark) in status wells with a rail; page head with
   Filters; search; "Landed today" items from the inbound panel's data.
5. **Register** (`#/register`): three stat cards (runs this week, spent, confirmed) beside a
   26-week runs-per-day heatmap, then the table with provider marks; Export CSV builds the
   file client-side from the rows.
6. **Eval** (`#/eval`): page-head actions (Add golden, Open JSON, Run suite), golden set card
   with checkboxes, options (rubric, include RCA, provider), last report table.
7. **Settings** (modal, `#/settings/<page>`): Workspace group — General, Providers, Budgets,
   MCP servers, Try a tool, Permissions, Notes, Notifications, Webhooks; This app — Reading,
   Library, About. Values come from `configSummary` and `doctor`; the config file is not
   written by the app, so a row whose value lives in `.sirdar/config.yaml` offers "Open
   config" rather than an editor. MCP servers lists `mcpServers` with Test (`connect`) per
   server; Try a tool runs `mcpTools` and `mcpCall` live and shows the verdict and result;
   Providers lists every provider with sign-in, fix support and guard from `doctor`.
8. **Library** (`#/library`): the gallery, restyled, with the new components added.

### Transport

`Transport` gains `runDiff`, `dropHunk`, `steer`, `mcpServers(connect)`, `mcpTools`,
`mcpCall`, implemented for HTTP and Wails alike; the Wails bridge binds the three MCP methods.
The fake transport carries them so every screen test runs without a backend.

### Out of scope

Editing `config.yaml` from the app; pushing a fix branch from the app (no API yet); any
change to what a run may read or write.
