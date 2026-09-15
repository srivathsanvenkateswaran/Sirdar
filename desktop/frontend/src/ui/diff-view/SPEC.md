# Diff view

## What it is

A fix run's change as a reviewer reads it: the unified patch `Transport.runDiff`
answers, drawn file by file and hunk by hunk, with Keep and Drop on each hunk
while the change can still be edited. Used in the app on the Change review
screen and in the session's Changes pane, which is why it lives in the library
rather than in either screen.

Built. `desktop/frontend/src/ui/diff-view/`. New in the 2026-09-15 screens
round; the app had no diff before `runDiff` existed. `patch.ts` beside it is
the parser, exported as `parsePatch`, and its hunk numbering is the service's:
`hunks[i]` of a file is the hunk `dropHunk({ path, hunk: i })` reverts.

## Anatomy

- `div.sd-diff[dir="ltr"]` — the whole view. `--sd-font-mono` at
  `--sd-text-ledger` (13.5px) on a 1.7 line. Left-to-right whatever the pane
  around it: code is written that way.
- `article.sd-diff__file[data-path]` — one file. Its header
  (`.sd-diff__filehead`, padding 14/20/6) carries the path, `+n` in the done
  hue and `−n` in the failed hue, and the status word (`new`, `modified`,
  `deleted`, `renamed`) in the UI face. The counts and the word come from the
  service's `files` list when the view is handed one, and from the patch text
  otherwise.
- `section.sd-diff__hunk` — one hunk. Its bar (`.sd-diff__hunkhead`) is the
  `@@ … @@ section` line on `--sd-sunk` between two `--sd-rule` hairlines,
  padding 6/20, with the actions at the inline end.
- `.sd-diff__acts` — Keep (pale, 32 tall) and Drop (ghost, 32 tall). Keep
  becomes **Kept** with a check icon and `aria-pressed="true"` once filed.
  Drawn only when `editable`.
- `.sd-diff__line[data-type]` — one line: a 3.6rem number column
  (`.sd-diff__no`, `--sd-ink-3`, right-aligned, the old number for a deletion
  and the new one otherwise) and the code (`.sd-diff__code`, `white-space:
  pre`, tab-size 4), which opens with the mark git prints: `+`, `−`, or a
  space.
- `p.sd-diff__stub` — the one paragraph the view shows instead of lines: the
  Split stub, the empty change, or the truncation notice under the files.

## States

| State | What changes |
|---|---|
| rest | Context lines in `--sd-ink`; an added line on a 10% tint of `--sd-st-done` with its text in the hue; a deleted line on a 10% tint of `--sd-st-failed` likewise. The mark is the copy of the tint. |
| hover | n/a on lines. The buttons hover as Button does. |
| active / pressed | Keep pressed is **Kept**: `aria-pressed="true"`, the check icon, the label in `--sd-ink`. |
| focus-visible | The shell's ring on each button; lines are not focusable. |
| disabled | While a drop is in flight the hunk's Keep is `disabled` and Drop reads **Dropping…** with `busy`; other hunks stay live. |
| loading | n/a. The screen loads the patch and hands it over whole. |
| error | n/a here. A refused drop is the screen's to report beside the view. |
| empty | `The change is empty.` when the patch parses to no file. |
| selected | `activePath` marks one file `data-active="true"` and scrolls its header into view. Nothing else is drawn for it; the rail beside the view is what shows the selection. |
| read-only | `editable=false`: no buttons at all. A pushed branch or a gone worktree is read but not edited. |
| truncated | The files draw as usual and a `role="status"` line under them says the patch was cut. |
| split | `mode="split"` draws the stub sentence and nothing else. |
| RTL | `dir="ltr"` on the root: numbers stay on the left of the code they number, the marks stay before the text. The buttons' labels follow the page's language. |

## Tokens used

- `--sd-font-mono`, `--sd-text-ledger` — the lines
- `--sd-font-ui`, `--sd-text-body` — the status word and the stubs
- `--sd-ink` / `--sd-ink-2` / `--sd-ink-3` — code, file header, numbers and hunk header
- `--sd-st-done` — added lines (text, and a 10% tint behind them) and `+n`
- `--sd-st-failed` — deleted lines (text, and a 10% tint) and `−n`
- `--sd-sunk`, `--sd-rule` — the hunk bar
- `--sd-space-2`, `--sd-space-3`, `--sd-space-5` — gaps and the stub's padding

## Do / Don't

- **Do** hand the view the patch and the decisions and keep the state in the
  screen. Two screens show the same change; a view that held its own Kept
  set would show two different reviews of one commit.
- **Don't** compute a hunk's index anywhere but `parsePatch`. The service
  counts hunks per file from zero, and a screen that counted lines or files
  its own way would drop the wrong hunk — the etag catches a stale patch, not
  a wrong index.
- **Do** re-key decisions after a drop. The hunks after the dropped one move
  up by one; a decision left under the old key marks the wrong hunk kept.
- **Don't** draw Keep and Drop on a read-only change. `dropHunk` refuses a
  pushed branch and a missing worktree with 409; a button that is always
  refused is worse than none.
- **Don't** remove the marks to save the column. The tint is the second copy
  of the change, not the first.

## Accessibility

Each hunk is a `section` named by its file and number; its lines are a
`role="table"` of rows with the number and the code as cells, and the `+`/`−`
mark sits in the code cell so a screen reader reads it with the line. Keep is
a toggle button (`aria-pressed`); Drop is a plain button that reads
**Dropping…** and refuses a second press while it works. Contrast: code is
`--sd-ink` on `--sd-sheet`, **17.44:1** light and **14.02:1** dark; an added
line is `--sd-st-done` on its own 10% tint over the sheet, **5.17:1** light
and **7.10:1** dark; a deleted line is `--sd-st-failed` likewise, **5.62:1**
light and **6.22:1** dark; the hunk header is `--sd-ink-3` on `--sd-sunk`,
**4.56:1** light, the library's tightest pair. Nothing here moves, so there is no reduced-motion answer to
give.

## Changelog

### 2026-09-15
Added, with the parser, for the Change review screen and the session's
Changes pane. Split mode is a stub that says so.
