/** The hue an outline chip may carry, named for the state it borrows. */
export type ChipTone = 'done' | 'blocked' | 'failed'

/** The hue each verdict takes: held, half held, did not hold. */
export const VERDICT_TONES: Record<string, ChipTone> = {
  confirmed: 'done',
  partial: 'blocked',
  wrong: 'failed',
}

/**
 * A small outlined word in a table cell: a triage confidence, a verdict.
 *
 * The word is the fact and the tone is a second copy of it, so a chip with no
 * tone is still readable and a chip with one still reads without colour. An
 * empty value draws a dash in the third ink rather than nothing, so an empty
 * cell can be told from a cell that failed to render.
 */
export default function OutlineChip({
  value,
  tone,
}: {
  value?: string
  tone?: ChipTone
}): JSX.Element {
  if (!value) {
    return (
      <span className="register-none" aria-label="none">
        —
      </span>
    )
  }
  return (
    <span className="register-chip" data-tone={tone}>
      {value}
    </span>
  )
}
