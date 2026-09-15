<!--
  Copied from desktop/frontend/src/ui/diff-view/SPEC.md by
  desktop/frontend/scripts/sync-specs.mjs. Edit the original, then run
  `node desktop/frontend/scripts/sync-specs.mjs` from the frontend.
-->
# Diff view

## What it is

A fix run's change as a reviewer reads it: the file rows with their counts,
then every hunk with its lines numbered, and Keep or Drop on each hunk. The
session's Changes tab draws it at 480 wide; the change review draws the same
component full width.

Built. `desktop/frontend/src/ui/diff-view/`, used in the app's session
screen. It reads a patch through `lib/diff.ts` and changes nothing itself:
Keep is a mark the reader makes, Drop hands the hunk to the caller, whose
`Transport.dropHunk` reverts it and answers with the diff as it stands after.

## Anatomy

- `div.sd-diff[role="region"][dir="ltr"]` — the whole view, a column.
- `div.sd-diff__files[role="list"]` — the file rows, `--sd-rule` under them.
- `button.sd-diff__file[role="listitem"][data-on]` — one file: 44 tall
  (`--sd-nav-row-h`), `padding-inline: 24px`, mono `--sd-text-meta`. The path
  ellipsises; `span.sd-diff__add` (`--sd-st-done`) and `span.sd-diff__del`
  (`--sd-st-failed`) carry the counts; `span.sd-diff__word` says `new`,
  `deleted`, `renamed`, or `reviewed` once every hunk in the file is kept.
  Clicking a row scrolls the body to that file.
- `div.sd-diff__body` — the scrolling hunks, mono `--sd-text-ledger` on a
  1.7 line.
- `div.sd-diff__filehead` — the file's path and count above its hunks, drawn
  only when there is more than one file.
- `div.sd-diff__hunk` — the `@@` header on `--sd-sunk` between two
  `--sd-rule` hairlines, with `span.sd-diff__acts` at the inline end: Keep
  (pale, small, `aria-pressed`, reads Kept with a check once pressed) and
  Drop (ghost, small).
- `p.sd-diff__refused[role="alert"]` — why a Drop was refused, under the
  hunk it was for, in `--sd-st-failed`.
- `div.sd-diff__line[data-t]` — one line: a `3.2rem` number gutter
  (`span.sd-diff__n`, `aria-hidden`) and the text with its marker. Added
  lines sit on a 10% tint of `--sd-st-done`, deleted on 10% of
  `--sd-st-failed`, each with the hue as text.
- `p.sd-diff__note` — the truncation notice, when the patch was cut.

## States

| State | What changes |
|---|---|
| rest | Every hunk offers Keep and Drop; the first file's row is `data-on`. |
| hover | A file row takes `--sd-nav-hover`. The buttons hover as Button does. |
| active / pressed | Keep pressed is `aria-pressed="true"`, reads Kept, and carries a check. Pressing it again unmarks. |
| focus-visible | The shell's ring on the row and on each button. Never removed. |
| disabled | `editable={false}`: Drop is disabled with `readOnlyReason` as its title, for a change that was pushed or whose worktree is gone. Keep stays, since it is a note the reader makes. |
| loading | `dropping` names the hunk whose Drop is in flight: its button is busy and reads Dropping…. |
| error | A refusal from the service is drawn under the hunk it was for, as an alert, and the hunk stays. |
| empty | No files: "No change to show." and nothing else. |
| selected | The file row whose hunks were last scrolled to is `data-on` and `aria-current`. |
| RTL | Nothing mirrors: the view is `dir="ltr"` and `unicode-bidi: isolate`, because a path or a line of code laid out right to left is a different one. The pane around it may mirror. |

## Tokens used

- `--sd-st-done` / `--sd-st-failed` — the counts, the added and deleted lines, and their 10% tints
- `--sd-sunk` / `--sd-rule` — the hunk header and its hairlines
- `--sd-card-row` / `--sd-nav-hover` — the current file row and its hover
- `--sd-ink` / `--sd-ink-2` / `--sd-ink-3` — text, the file head, the gutter
- `--sd-font-mono`, `--sd-font-ui`
- `--sd-text-meta`, `--sd-text-ledger`
- `--sd-nav-row-h` — the file row height
- `--sd-space-2` … `--sd-space-5`

## Do / Don't

- **Do** keep Keep and Drop on the hunk. A toolbar that acts on "the
  selected hunk" makes every click a question of what was selected.
- **Don't** let Keep change anything on disk. It is the reader's own mark;
  the service is only told about a Drop.
- **Do** hand back the etag with a Drop. The index is read from one patch
  and must never be applied to another; the service refuses a stale etag and
  the refusal is shown under the hunk.
- **Don't** hide Drop on a read-only change. Show it disabled with the
  reason, so the reader knows the change exists and why it cannot be edited.
- **Do** draw the whole patch the service sent, unknown lines included. A
  hole in a diff is worse than an odd line.

## Accessibility

The view is a `region` named by `label`; the file rows are a list of
buttons, the current one `aria-current`; each file and each hunk is a
`section` named by its path or its `@@` header, so a screen reader can
move between them. Keep is a toggle (`aria-pressed`); Drop is a plain
button, busy while in flight. A refusal is `role="alert"`. The line-number
gutter is `aria-hidden` and the marker (`+`, `-`, space) is part of the
line's text, so an added line reads as added without its colour. Contrast:
`--sd-st-done` on its 10% tint over the sheet **5.9:1**, `--sd-st-failed`
**6.5:1**, the gutter `--sd-ink-3` **4.6:1** on the sheet in light; higher
in dark. No motion.

## Changelog

### 2026-09-15
Added, from `.files`, `.file`, `.diff`, `.hunk` and `.dl` in
`docs/design/2026-09-15-screens/Session.html` and `Review.html`. Keep is a
mark rather than a call because the review API has only a Drop; the "Kept"
state with its check comes from the review mock.
