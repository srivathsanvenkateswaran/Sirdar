# Live activity line and chat markdown

Two bounded UI changes the owner approved on 2026-10-04 after the OMNI-3413 dogfood run:
a run that streams its answer for 66 s looked hung, and chat markdown renders without
tables, task lists or highlighted code.

Spec: none separate; the approved designs are restated under each task. Branch `harness-ux`,
worktree `.claude/worktrees/harness-ux`. Frontend lives in `desktop/frontend` (React 18,
TypeScript, Vitest, Testing Library). Run tests with `cd desktop/frontend && npx vitest run <paths>`
and typecheck with `npx tsc --noEmit -p .`.

## Global Constraints

- Agents never commit with an identity other than the repository's own; never pass
  `--author`, `-c user.*`, or set `GIT_*` identity variables. No AI attribution trailers.
- CSS uses the design tokens in `src/styles/tokens.css` (`--sd-ink-*`, `--sd-font-*`,
  `--sd-text-*`, `--sd-space-*`, `--sd-st-*`) and logical properties only (`inline-size`,
  `padding-inline`, never `left`/`width`). Honour `prefers-reduced-motion`.
- Arabic/RTL text keeps working: assistant and note text is `dir="auto"`; existing RTL helpers
  in `src/lib/rtl.ts` stay in use where they are used today.
- One component per concern, shared by all three session layouts (Conversation
  `screens/session/SessionConversation.tsx`, Document `SessionDocument.tsx`, Workbench
  `SessionWorkbench.tsx`). No per-layout copies.
- Every new behaviour has a test; the whole suite (`npx vitest run`) and `tsc` pass at the end of
  each task.
- Comments match the codebase: full sentences saying why, referencing the incident where it
  explains the rule. No commented-out code.

## Task 1: Live activity line above the composer

**Already written (untracked in the worktree, keep and finish):**
`src/lib/activity.ts` + `src/lib/activity.test.ts` (6 passing tests: `currentActivity(events, now)`
returns `{what, label, since, chars?, stalled}` or undefined; `STALL_MS = 120_000`), and
`src/components/session/LiveActivity.tsx` + `live-activity.css` (renders the line with a
1-second ticker; nothing when `working` is false).

**Do:**

1. Mount `<LiveActivity events={events} working={...} />` directly above the composer in all
   three layouts, where `working` is true only when the run's status is `running` (not
   `preparing`, not `blocked`):
   - Conversation: inside `.sc-composer`, before `<Composer`.
   - Document: inside `<main className="sn-doc">`, before `<ComposerStrip`.
   - Workbench: before the `wb-decide` block / `<ComposerCard variant="strip"`.
   Use each layout's existing events list (the same one its transcript is built from).
2. Component test `src/components/session/LiveActivity.test.tsx` with fake timers:
   - renders nothing when `working` is false;
   - shows `Writing the answer`, a ticking `m:ss` elapsed and `· 21.9k characters` for a log
     whose open block is a `StructuredOutput` tool_use with input_json deltas totalling 21,900
     characters (build events like `activity.test.ts` does);
   - after `STALL_MS` with no new event shows `No output for 2:00` (or later) and sets
     `data-stalled`;
   - the label updates when a new event arrives (rerender with an extra `tool_started`).
3. A layout test in `SessionConversation.test.tsx` that a running run shows
   `data-testid="live-activity"` above the composer and a completed run does not.
4. Replay check (not committed): feed
   `/Users/srivathsanv/Documents/Work/Coding/GitHub/OXO.APIs/.sirdar/runs/OMNI-3413/20261004T101505Z-f705/events.jsonl`
   through `currentActivity` at `2026-10-04T10:30:00Z` and confirm it reports `answer` with a
   character count above 10,000; report the output in the task report.

## Task 2: One `ChatMarkdown` component on T3 Code's markdown stack

**Design (approved):** replace the bare `react-markdown` uses with one shared component,
`src/components/markdown/ChatMarkdown.tsx`, built on the stack T3 Code uses
(`react-markdown` ^10 already installed, add `remark-gfm` ^4, `remark-breaks` ^4,
`rehype-sanitize` ^6) plus Shiki for fenced code.

**Do:**

1. Add the dependencies with `npm install` in `desktop/frontend` (exact majors above, plus
   `shiki` ^3). Commit `package.json` and `package-lock.json`.
2. `ChatMarkdown` props: `children: string`, optional `components` overrides (merged over the
   defaults), optional `className`. Plugins: `remarkGfm`, `remarkBreaks`, `rehypeSanitize` with
   the default schema (allow `className` on `code` for `language-*`).
3. Fenced code blocks (`pre > code.language-x`): render a `CodeBlock` with a header (the language,
   and a Copy button that writes the raw code to `navigator.clipboard` and shows `Copied` for
   1.5 s). Highlight with Shiki loaded lazily via dynamic `import('shiki')` the first time a code
   block mounts, a single shared highlighter (themes `github-light` and `github-dark`, chosen by
   the app's current theme attribute on `document.documentElement` — read how the app sets theme
   and follow it), languages loaded on demand; until it resolves, show the plain `<pre><code>`.
   Unknown languages fall back to plain text. Inline code stays `<code>`.
4. Tables (GFM): wrap in a horizontally scrollable container with a Copy button that copies the
   table as Markdown.
5. Links open in a new tab with `rel="noreferrer"`. Task lists render as disabled checkboxes.
6. Keep the existing `file:line` reference buttons: today `src/components/session/Prose.tsx`
   (lines ~105-117, a `Markdown` wrapper with a `code` override) and
   `src/screens/session/AnswerCard.tsx` (`Prose`, `REF` regex) turn inline code that is a
   `path:line` into a button. `ChatMarkdown` must accept that override through `components` so
   those call sites keep the behaviour; move the `Markdown` wrapper onto `ChatMarkdown`.
7. Switch every current `react-markdown` call site to `ChatMarkdown`:
   `screens/session/SessionConversation.tsx` (assistant `say` items),
   `screens/session/NoteDocument.tsx`, `screens/session/workbench/NoteDocument.tsx`,
   `components/session/BundlePane.tsx`, `components/session/Prose.tsx`, and any other
   `from 'react-markdown'` import found by grep. After the task, `react-markdown` is imported
   only by `ChatMarkdown.tsx`.
8. Styles in `src/components/markdown/chat-markdown.css` with tokens: tables with hairline
   borders and padded cells, code blocks on a subtle surface with mono font and
   horizontal scroll, headings scaled for chat (h1-h3 no larger than `--sd-text-title`),
   blockquotes, lists, and GFM alerts as plain blockquotes.
9. Tests `src/components/markdown/ChatMarkdown.test.tsx`: a GFM table renders `<table>` with
   header cells; a task list renders checkboxes; a fenced ```ts block renders a code block with
   the language label and a Copy button that calls `navigator.clipboard.writeText` with the raw
   code; a `<script>` in markdown is not rendered; an inline `src/a.ts:12` with the reference
   override renders the override's button; single newlines become `<br>`. Mock `shiki` in tests.
10. Bundle check: `npm run build` succeeds and Shiki is in a separate chunk (not in the entry
    chunk); report chunk names and sizes in the task report.
