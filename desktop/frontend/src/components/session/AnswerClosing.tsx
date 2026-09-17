import './inspector.css'

/*
 * The run's closing card on the transcript.
 *
 * It used to be the whole answer: root cause, evidence, blast radius,
 * proposed fix, reply draft, open questions — the note again, in worse
 * type, above a Note tab holding the same words. Two reading surfaces for
 * one document is one too many, and the transcript's copy was the weaker of
 * the two.
 *
 * So the transcript ends with what a card is for: the title, the three
 * facts a reader scans, and the way through to the note. The "revised after
 * your steer" stamp stays, because it is about this run rather than about
 * the answer.
 */

export interface AnswerClosingProps {
  title: string
  /** The classification the answer gave itself: `bug`, `data`, `config`. */
  classification?: string
  confidence?: string
  openQuestions?: number
  at?: string
  /** The answer was written after a steer. */
  revised?: boolean
  /** Opens the inspector's Note tab. Absent on a run whose note is not there yet. */
  onOpenNote?: () => void
  /** A fix run ends in its change, not in a note. */
  changes?: boolean
  onOpenChanges?: () => void
}

type Tone = '' | 'done' | 'triaged' | 'blocked' | 'failed'

function confidenceTone(confidence: string): Tone {
  if (/^high/i.test(confidence)) return 'done'
  if (/^med/i.test(confidence)) return 'blocked'
  if (/^low/i.test(confidence)) return 'failed'
  return ''
}

function Chip({ label, value, tone = '' }: { label: string; value: string; tone?: Tone }): JSX.Element {
  return (
    <span className="si-chip" data-tone={tone || undefined}>
      {label} <b>{value}</b>
    </span>
  )
}

export default function AnswerClosing({
  title,
  classification,
  confidence,
  openQuestions = 0,
  at,
  revised = false,
  onOpenNote,
  changes = false,
  onOpenChanges,
}: AnswerClosingProps): JSX.Element {
  return (
    <section className="si-close" aria-label={changes ? 'Fix report' : 'Answer'} data-testid="answer-card">
      <p className="si-close__kicker">
        <span>{changes ? 'Fix report' : 'Answer'}</span>
        {at ? (
          <span>
            <bdi>{at}</bdi>
          </span>
        ) : null}
        {revised ? <span>revised after your steer</span> : null}
      </p>
      {title ? (
        <h2 className="si-close__title sd-bidi" dir="auto">
          {title}
        </h2>
      ) : null}
      <div className="si-close__chips">
        {classification ? <Chip label="classification" value={classification} tone="triaged" /> : null}
        {confidence ? <Chip label="confidence" value={confidence} tone={confidenceTone(confidence)} /> : null}
        {openQuestions > 0 ? <Chip label="open questions" value={String(openQuestions)} tone="blocked" /> : null}
      </div>
      {changes ? (
        onOpenChanges ? (
          <button type="button" className="si-close__go" onClick={onOpenChanges}>
            Review the changes →
          </button>
        ) : null
      ) : onOpenNote ? (
        <button type="button" className="si-close__go" onClick={onOpenNote}>
          Filed as a note →
        </button>
      ) : (
        <p className="si-close__go si-close__go--flat">The note is filed as the run finishes</p>
      )}
    </section>
  )
}

/** An answer a later one superseded: one line, so the revision stands alone. */
export function SupersededAnswer({ at, onOpen }: { at: string; onOpen?: () => void }): JSX.Element {
  return (
    <button type="button" className="si-close si-close--prev" onClick={onOpen} disabled={!onOpen} data-testid="answer-prev">
      <span>
        Answer at <bdi>{at}</bdi> · superseded by the revision below
      </span>
    </button>
  )
}
