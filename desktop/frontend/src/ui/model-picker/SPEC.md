# Model picker

## What it is

The chip that says which provider and model a session will run on, and the
popover that changes it. Used on New session (the Model chip beside Playbook),
in the session composer (read-only: a steer resumes the same session), and on
Eval's Provider row, where a Change button opens the same popover.

Built. `desktop/frontend/src/ui/model-picker/`. Replaces the Provider and
Model fields that used to sit under New session's More options and inside
Eval's Provider dialog (`components/run/ProviderFields.tsx`, deleted).

The lists it draws from live in `src/lib/models.ts`: a curated list per
provider with "CLI default" first, then the names the research observed on
the wire, then "Other…" for free text. Nothing in a list is guessed; a
provider whose names were never seen offers free text with a hint saying
where a name would come from.

## Anatomy

- `span.sd-model-picker` — the wrapper, `position: relative`, so the popover
  hangs under the trigger.
- `button.sd-model-chip` — the trigger. The mocks' `.chip`: 32 tall,
  `--sd-rule` hairline at `--sd-radius-pill`, `padding-inline: 12px`,
  `--sd-text-ledger`. Holds `.sd-model-chip__label` ("Model"), a small
  provider mark pulled 4px into the padding, and `.sd-model-chip__value` in
  the ledger face and third ink: `claude · Sonnet 5`, `claude · CLI default`,
  or `claude · CLI default · last used claude-sonnet-5`. `aria-haspopup="dialog"`
  and `aria-expanded`.
- With `trigger="change"` the trigger is a pale Button reading "Change" and
  the row draws the words itself.
- `div.sd-model-picker__popover[role="dialog"]` — non-modal, absolute under
  the trigger, 560px or the viewport less the gutters. The dialog's surface:
  `--sd-surface`, `--sd-rule-strong`, `--sd-radius-md`, `--sd-shadow-soft`.
- `.sd-model-picker__columns` — `200px minmax(0, 1fr)`; one column under
  720px.
- Two `div.sd-model-picker__list[role="listbox"]`, each under a
  `.sd-model-picker__heading` (Provider, Model). Options are
  `div.sd-model-picker__option[role="option"]`, 36 tall at `--sd-radius-sm`:
  the provider rows carry the mark, the name and a `.sd-model-picker__tag`
  reading "workspace default" on the workspace's own; the model rows carry
  the label and an optional `.sd-model-picker__note`. The selected row has
  `.sd-model-picker__check`, a 16px stroke check.
- `.sd-model-picker__other` — the free-text row: an `input.sd-model-picker__input`
  labelled "Other model" (visually hidden) and a `.sd-model-picker__hint`
  under it naming where a model id for this provider was observed.
- `.sd-model-picker__foot` — Done, a small pale Button at the inline end.

## States

| State | What changes |
|---|---|
| rest | Chip in `--sd-ink-2`, value in `--sd-ink-3`. |
| hover | Chip border to `--sd-rule-strong`, label to `--sd-ink`. Option to `--sd-nav-hover`. |
| active / pressed | n/a. The popover opening is the feedback. |
| focus-visible | The shell's ring on the chip, on each option, on the box and on Done. |
| open | `aria-expanded="true"`, chip drawn as hovered. Focus moves to the selected provider. |
| selected | `aria-selected="true"`: `--sd-card-row` fill, `--sd-ink` at weight 500, and the check. Never the fill alone. |
| nothing chosen | The chip reads `<provider> · CLI default`; with `lastUsed` set, `· last used <id>` follows. |
| workspace model | An override of neither provider nor model shows the workspace's configured model by its label, or the id itself when the list has no label for it (`sonnet` stays `sonnet`; the CLI takes the alias). |
| free text | Any id the list lacks checks "Other…" and fills the box. The choice is the trimmed text. |
| disabled | The chip at `opacity: .45`; nothing opens. |
| read-only | `readOnly` names the reason: the chip is a disabled button with that reason as its `title` and in its accessible name, at full opacity — a fact, not a control. |
| no provider | The value reads "not set" and no mark is drawn; the popover still opens so one can be chosen. |
| loading | n/a. The lists are static. |
| error | n/a. A wrong id is the provider's to refuse when the run starts. |
| empty | n/a. Every list has "CLI default" and "Other…". |
| RTL | The popover hangs from the inline start, the columns and the check swap sides with the page, the chip's value and the box stay `dir="ltr"` because a model id is Latin. |

## Tokens used

- `--sd-surface`, `--sd-sheet` — the popover and the box
- `--sd-rule`, `--sd-rule-strong` — the chip, the popover and the box
- `--sd-card-row`, `--sd-nav-hover` — selected and hovered rows
- `--sd-ink`, `--sd-ink-2`, `--sd-ink-3` — words
- `--sd-radius-pill`, `--sd-radius-md`, `--sd-radius-sm` — chip, popover, rows and box
- `--sd-shadow-soft` — the popover
- `--sd-space-1` … `--sd-space-4` — gaps and padding
- `--sd-text-ledger`, `--sd-text-body`, `--sd-text-meta`, `--sd-text-micro` — type
- `--sd-font-ui`, `--sd-font-mono` — the label and the value

## Do / Don't

- **Do** apply every choice as it is made. The chip is the truth; Done only
  closes. Escape and a click outside close the same way and keep the choice.
- **Don't** invent a model name for a list. A name goes in `lib/models.ts`
  only once the research has seen it on the wire; until then the provider
  offers free text and a hint.
- **Do** pass ids through unchanged. The Claude CLI takes `opus`, `sonnet`
  and `haiku` as well as full ids; the picker does not correct a reader who
  typed one.
- **Do** say what "CLI default" turned out to be last time: the New session
  chip appends the newest run's reported model on that provider.
- **Don't** offer `agy`. It is disabled, and a provider a run cannot start on
  is not a choice.
- **Do** make the chip read-only where the choice is already made — a steer
  continues the run the session has, on the model it has.

## Accessibility

The trigger is a button with `aria-haspopup="dialog"`, `aria-expanded` and
`aria-controls` naming the popover. The popover is `role="dialog"`,
non-modal, named "Provider and model". Each list is a single-select
`listbox` with roving tabindex: one tab stop, and ArrowUp / ArrowDown move
focus and selection together and wrap; Home and End jump. Enter on a
provider moves to its models; Enter on a model is Done; Enter on Other…
steps into the box; Enter in the box is Done. Escape closes. Focus goes to
the selected provider on open and back to the trigger on close, on every
path. The free-text box has a visually hidden label, "Other model". The
read-only chip is a disabled button whose accessible name carries the reason.
Contrast: the value is `--sd-ink-3` on `--sd-surface`, **5.25:1** light and
**5.26:1** dark; the selected row is `--sd-ink` on `--sd-card-row`,
**15.70:1** light and **12.51:1** dark. No motion.

## Changelog

### 2026-09-16
Added, for the model-picker round. New component: the Provider and Model
fields under More options and the Eval dialog were selects and a text box,
and the chip they fed was not a control.
