import './KindChip.css'

/** The three kinds of run the CLI starts. Anything else is shown as written. */
export type RunKind = 'triage' | 'rca' | 'fix'

export interface KindChipProps {
  /** `triage`, `rca` or `fix`. An unknown kind is drawn in the triage hue, verbatim. */
  kind: RunKind | string
}

/**
 * What a run is: triage, RCA or fix, as a small tracked chip.
 *
 * Three fills, one per kind: the highlight for a triage, a tint of the triaged
 * hue for an RCA, a tint of the blocked hue for a fix. The word is always
 * present, in uppercase because it is a category label rather than a sentence
 * — the one place in the app that tracked capitals are allowed, since the chip
 * is read as a mark rather than as prose.
 */
export default function KindChip({ kind }: KindChipProps): JSX.Element {
  return (
    <span className="sd-kind" data-kind={kind} dir="ltr">
      {kind}
    </span>
  )
}
