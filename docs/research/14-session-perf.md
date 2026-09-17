# Why the Session window feels choppy

An investigation, not a fix round. Everything below was measured against
`sirdar serve` on the sandbox workspace (`~/Documents/Personal/sirdar-sandbox/app`,
key `SBX-1`), in headless Brave over the DevTools protocol and — for the same
page, the same server, back to back — inside a real WKWebView, the engine the
Wails shell draws in. No run was started; the live stream was replayed by
appending a recorded `events.jsonl` into a run directory the watcher was
tailing, and both replay runs were deleted afterwards.

## The short answer

The transcript is not slow to draw. It is slow to **arrive**.

Every live line the Session window shows comes off disk through a 500ms
poller, so a provider writing twenty lines a second reaches the reader as one
lump about once a second. Nothing on the page drops a frame doing it — the
frames are fine, there is just nothing new in most of them. Beside an Electron
app that pushes each token as its SDK produces it (`04-harness-internals.md`:
T3 Code is event-sourced, Agent SDK in-process, WS RPC to the renderer), a
transcript that advances in one-second steps reads as a stuttering UI even
though every step is drawn in 16.7ms.

The web view is not the problem. WKWebView costs a fraction of a millisecond
more per frame than Chromium on this page and nothing that reads as choppy.

## What was measured, and how

- `desktop/frontend/scripts/session-trace.mjs` (new): opening a run, rolling a
  wheel through the transcript and back with the frame gaps sampled in the
  page, a keystroke in the composer, the header's layout switcher, the hover
  card, and a live stream replayed one line at a time. React commits and the
  components each one re-rendered come from the DevTools hook production React
  calls on every commit; a sampling CPU profile runs alongside the scroll and
  the stream.
- `desktop/frontend/scripts/perf-trace.mjs` (existing, repaired): its run
  scenario still looked for `[data-testid="event-stream"] section[aria-label^="turn"]`
  and its "Show everything" scenario for a toggle that no longer exists —
  both markup from before the Conversation layout landed, so the script had
  been failing at the run step. The run scenario now finds the rows of
  whichever layout is current; the "Show everything" scenario is gone with the
  control it drove.
- `wkperf`, a ~250-line Swift harness (kept out of the repository, in the
  session scratchpad) that loads the same URL in a `WKWebView`, injects the
  same instrumentation as a `WKUserScript` at document start, and drives it
  through `callAsyncJavaScript`.
- Commands, for anyone repeating this:

      go build -o /tmp/sirdar-perf ./cmd/sirdar        # after `make ui`
      /tmp/sirdar-perf serve --addr 127.0.0.1:47391 --workspace ~/Documents/Personal/sirdar-sandbox/app
      cd desktop/frontend
      node scripts/session-trace.mjs --url http://127.0.0.1:47391 \
        --workspace e9b3aab69e16 --run 20260915T121105Z-076d --rounds 5 \
        --chrome "/Applications/Brave Browser.app/Contents/MacOS/Brave Browser"

**Read the absolute numbers with a wide error bar.** The machine carried a
load average between 50 and 122 throughout (other sessions running vitest, Go
tests and a Next dev server), and the same scenario moved between 200ms and
380ms across runs. What holds regardless is the shape: the ratios between
scenarios, the frame distributions, the commit and render counts, and the
Chromium-versus-WebKit comparison, which was taken back to back on one build.

Two more things about the method. `whenPainted` resolves two animation frames
after its DOM condition holds, which puts a floor of about 33ms under every
"→ painted" figure: anything reported near 33ms happened on the next frame and
is not a delay. And the hover figure includes the card's own deliberate open
delay.

## Ranked findings

| # | Symptom | Measurement | Cause | Fix size |
|---|---|---|---|---|
| 1 | A running session advances in lumps about once a second instead of streaming | A line written every 50ms for 6.2s changed the transcript **8 times**: median gap between changes **848ms**, p95 **1441ms**. On the wire the same writes arrived as bursts of 8–27 `run.event` frames with 500–1639ms between bursts; per-line latency up to 1.6s | `internal/app/watcher.go` polls every registered workspace's run directories every `DefaultInterval` = 500ms and tails `events.jsonl`; `watcher.go:287` is the **only** producer of `run.event` in the codebase, so an in-process run's output still goes disk → poll → UI in both the Wails app and `sirdar serve` | Medium. Either publish from the runner straight into `Service.publish` as lines are written (the watcher stays for runs this process did not start), or cut the interval and wake the poller on a write |
| 2 | The served UI freezes hard — nothing loads, not even fonts — for ~12s | Reproducible 2 of 2: reloading `sirdar serve` in a browser, the **6th consecutive load** stalls. `/api/workspaces/…/runs`, `/queue`, `/config/summary`, `/quota` and two `.woff2` files never receive a status; the board sits empty at "0 runs · 0 live". The 7th load is fine again | One `EventSource` per subscriber (`api/transport.ts:231`), opened by the store *and* by `useRunFeed`, Eval, NewSession and Register — opening a session takes 2 of the browser's 6 connections per host. A page's stream is held open server-side until a keepalive write fails, and `keepalive` is **15s** (`internal/httpapi/server.go:66`); six of those fill the pool and every request, static files included | Small. One shared stream the whole app subscribes to, and a keepalive short enough (2–3s) that a dead stream is noticed before six of them accumulate |
| 3 | The window takes a beat to appear at all | Warm board paint **314ms** on the quietest pass, 435–620ms on a middling one, 1.9s at load 120; one **830 kB** JS chunk (258 kB gzip), 216 kB CSS, and 5 self-hosted woff2 totalling 388 kB — 14 requests through 6 sockets, one of which the event stream holds for the life of the page. Vite says so itself: "Some chunks are larger than 500 kB" | No code splitting: every screen, `react-markdown` and the whole `src/ui` library are in the entry chunk. Newsreader ships two faces (132 kB + 147 kB) for the note body | Medium. Lazy-load the screens behind the route and the Markdown renderer behind the note; subset or drop the Newsreader italic |
| 4 | Opening a run, and switching layout, land with a visible pause and no feedback | Conversation open to first painted row **273ms / 378ms** across two passes, Document **330ms**, Workbench **347ms** (2270-event run). Layout switch: → Workbench 250ms, → Document 212ms, → Conversation 191ms, worst observed 601ms; longest task 21–39ms | The budget in HANDOFF is "long-run open under 300ms", which Document and Workbench miss. The switcher unmounts one layout and mounts another whole tree with nothing on screen in between | Small for the feedback (keep the header and show the new layout's skeleton); medium to get under budget |
| 5 | Events that draw nothing still cost renders | Replaying 120 `system` events into a 2100-event run changed **0 characters** of the transcript and still produced **35 React commits**. Across a mixed slice: **5.4 component renders per streamed line**, ~1 commit per 3 lines | `useSessionModel` rebuilds the entire model from the whole events array whenever the array identity changes (`screens/session/model.ts:500`), and `useRunFeed` copies the list per event; neither asks whether the new events are ones any layout draws | Small. Skip the rebuild when the appended events are kinds the model discards, or key the memo on the count of rendered events |
| 6 | Typing in the composer "feels" heavy | Median keystroke → painted **30–32ms** with a ~33ms measurement floor, so effectively the next frame. 8 components re-render per key — Composer, ComposerCard, ModelPicker, two ChipMenus, ProviderMark, Button — and `fitRows` in a `useLayoutEffect` forces ~2 layouts per key | Real but not felt: the renders are cheap and the layout is on one textarea | None needed. Recorded so it is not chased |
| 7 | "Is it WKWebView?" | Same build, same server, back to back — open **210ms** (WK) vs **179–273ms** (Brave); scroll frames median **17.0ms** vs **16.7ms**, p95 20ms vs 16.8ms, worst 27ms vs 16.8ms, **zero** frames over two vsyncs in either; keystroke 32ms vs 31ms; streaming 4.7 vs 5.4 component renders per line; warm boot 327ms vs 314ms | WebKit is marginally more variable per frame and no more than that | None. The web view is not the cause |
| 8 | The transcript is not virtualised | A 2270-event run renders **16 rows and 353 DOM nodes** in Conversation; a full wheel roll of 7496px over 881 frames dropped **nothing** past two vsyncs, with 0 forced layouts per round | Tool calls are grouped into stacks, so the flow stays short whatever the log length | None. Recorded so nobody builds a virtualiser that is not needed |
| 9 | A burst could silently lose transcript lines | `Service.publish` drops a subscriber's oldest event when its channel is full; the depth is `DefaultBuffer` = 256 (`internal/app/service.go:57`) | A 500ms poll over a fast provider can exceed 256 lines; the dropped index leaves a hole the UI's dedupe will never fill | Small, and worth doing with #1 — pushing per line makes bursts of that size impossible |

## What the `ui-snappy` round left in good shape

The budgets that round set still hold where it set them, and the machinery it
built is doing its job:

- Sidebar navigation to a painted screen: **31–45ms**, against an 80ms budget.
- The board at rest: **0.4 commits a second**, 15 component renders over 5s —
  the `Age` leaves and nothing else. An open run at rest: **0 commits**.
- The transcript's scroll: 0 forced layouts per round, no frame past two
  vsyncs (finding #8).

The 16ms coalescing in `api/coalesce` is working too — it is just downstream of
a 500ms poll, so it has almost nothing left to coalesce.

## What the Wails shell is doing, and what it is not

`desktop/main.go` was read against the brief's list and there is nothing to
toggle:

- `EnableFraudulentWebsiteDetection` is not set, so it is off. So is
  `EnableDefaultContextMenu`. No debug flags are left on; `-debug` only ever
  came from `wails build -debug`, which the Makefile does not use.
- Not `Frameless`, and no `Mac` options at all, so `WebviewIsTransparent` and
  `WindowIsTranslucent` are both false. Translucency is the one macOS option
  that would cost real compositing work, and it is off.
- The GPU comment in `run()` is accurate: `WebviewGpuPolicyAlways` is set for
  Linux, where WebKitGTK defaults to software rasterisation, and macOS
  WKWebView draws on the GPU with no switch to set.

So the Wails configuration is already right, and the one-line config toggle
this brief allowed has nothing to point at.

The desktop app and `sirdar serve` differ in exactly one way that matters
here: the Wails transport listens with `EventsOn` over the runtime bridge
(`api/transport.ts:473`) and opens no sockets, so finding #2 is a `sirdar
serve`-in-a-browser problem only. Finding #1 hits both — `desktop/main.go`'s
`forward()` relays the same watcher fan-out.

## Left for the owner

Attaching Safari's Web Inspector to the shipped WKWebView needs a GUI step
this session cannot take: `safaridriver` refuses with "You must enable 'Allow
remote automation' in the Developer section of Safari Settings". The Swift
harness was written to get the WebKit numbers without it, and finding #7 is
its answer; the Inspector is still the way to confirm them against the real
Wails app rather than a bare `WKWebView`.

Headless Brave itself ran fine under the harness sandbox — no CVDisplayLink
trouble. Only the Swift `WKWebView` harness needed the sandbox lifted, because
it opens a real window.

## What the fix round changed (2026-09-17, branch `ui-perf-fix`)

Findings #1, #2, #9, #5 and the feedback half of #4 were implemented on
`ui-perf-fix` and measured against the tree this report was written on
(`7a4a024`), back to back on one machine, one afternoon, in headless Brave
against `sirdar serve` on the sandbox workspace at port 47392. #3, the code
splitting, is untouched and is still a round of its own.

Both builds served the same board, the same 2270-event run
(`20260915T121105Z-076d`) and the same replayed run — a copy of that log cut
to 2100 lines, marked running, appended to by `session-trace.mjs` and deleted
afterwards. The absolute numbers still carry the wide error bar the method
section describes; what is worth reading is the ratios and the shapes.

| Finding | Scenario | Before | After |
|---|---|---|---|
| #1 | 120 lines written 50 ms apart, from the process hosting the run, timed at the service boundary (`SIRDAR_PERF=1 go test ./internal/app -run TestEventLatency`) | watcher poll: **median 224 ms** per line, p95 471 ms; the transcript would move **13 times**, gap median **499 ms** | in-process sink: **median 0 ms** per line, p95 0 ms, max 1 ms; the transcript moves **120 times**, gap median **50 ms**, p95 51 ms |
| #1 | the same 120 lines written by another process (the CLI) | 36 commits over 6.1 s | unchanged by design — the watcher still polls at 500 ms, and it is the only thing that can see a run this process did not start |
| #2 | ten consecutive loads of the board, each a full navigation | loads 1–4 at 334/546/321/355 ms, **load 5 never painted** inside the probe's 15 s, and every scenario after it failed too | ten loads, **281–353 ms**, none over 400 ms, the whole script through to the end |
| #5 | 120 lines the transcript draws nothing for, one every 50 ms, through the watcher | 0 characters changed, **36 React commits** | 0 characters changed, **22 commits** |
| #5 | the same 120 lines delivered one at a time, which is what the in-process sink now does (vitest, `screens/session/streamCost.test.tsx`) | 120 renders of the screen, one a line | **1 render**, and no card below it drawn at all |
| #5 | 12 bursts of 12 mixed lines (10 quiet, a call and its result) into the 2100-line run | 103 commits, **2047 component renders**, 14.2 a line; main-thread 2263 ms, longest task 122 ms | 91 commits, **423 component renders**, 2.9 a line; main-thread 1984 ms, longest task 112 ms |
| #5 | a line that does draw (vitest) | — | **2 renders**: the screen, and the card that arrived. The fifteen cards above it and the answer below are the elements of the render before |
| #4 | layout switch, the 2100-line run | → Workbench 150 ms, → Document 149 ms, → Conversation 144 ms, with the window empty between the two | → Workbench 129 ms, → Document 114 ms, → Conversation 128 ms, the previous layout painted throughout |
| #4 | opening that run | 138–366 ms across passes | 128 ms |
| — | warm board paint, 3 rounds | cold 349 ms, warm median 350 ms | cold 277 ms, warm median 299 ms |
| — | sidebar navigation | → Register 43 ms, → Eval 49 ms, → Board 49 ms | → Register 48 ms, → Eval 49 ms, → Board 49 ms |
| — | keystroke in the board filter | median 32.4 ms | median 32.5 ms |
| — | board at rest, 10 s | 12 commits, 65 component renders | 12 commits, 65 component renders |

Read with the findings they answer:

**#1 is two numbers because it is two paths.** A run the desktop shell or
`sirdar serve` is hosting now hands every line it writes to the service as it
writes it, so the reader sees it in under a millisecond and the transcript
advances every 50 ms instead of roughly once a second. A run the CLI started
is still found by the watcher's 500 ms poll, which is all a separate process
leaves to find, and its numbers are the before column on purpose. The two
share a per-run cursor, so the poll never re-delivers what the sink already
sent.

**#2 reproduced harder than the report described.** The report saw the sixth
consecutive load stall for twelve seconds; on this build the fifth never
painted at all inside a fifteen-second probe, and the browser stayed out of
sockets for the rest of the run. One shared stream per page, a 5 s keepalive,
a server that drops a stream whose write fails, and closing the stream on
`pagehide` so a bfcached page holds nothing: ten loads, none over 353 ms.

**#5 splits the same way #1 does.** Through the watcher the poll had already
batched the quiet lines, so holding them saves 36 commits down to 22. Through
the new per-line path there is no batching to inherit, and without the hold
each of the 120 lines would have rendered the screen on its own — the fix is
what keeps the faster delivery from costing more than it gains. The burst
figure is the render cost of a line that does draw: 14.2 components a line
down to 2.9, because the rows the model kept are now the elements React kept.

**#8 still holds and #6 still needs nothing.** The transcript is 16 rows and
333 nodes whatever the log length, no frame in any scroll went past two
vsyncs on either build, and the keystroke is unchanged at the measurement
floor. Nothing here made the board slower: it is the same 12 commits and 65
renders at rest, and the same navigation.

`perf-trace.mjs` grew three repairs in the course of this: it prints every
load in order rather than only the median, a load that never paints is
reported as the finding it is instead of ending the run, and a scenario whose
probe no longer matches the markup is skipped the way `session-trace.mjs`
already skipped its layout scenario. The hover probe is one of those — it
looks for `.sd-session-card`, and on a long enough run it stops matching.
