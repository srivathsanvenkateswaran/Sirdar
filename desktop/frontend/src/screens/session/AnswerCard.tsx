import type { ReactNode } from 'react'
import type { RunEvent } from '../../api/types'
import { parseAnswer } from '../../lib/events'
import AnswerClosing from '../../components/session/AnswerClosing'

/*
 * The run's conclusion on the transcript.
 *
 * It was the whole answer once — root cause, evidence, blast radius,
 * proposed fix, reply draft — set above a Note tab holding the same
 * material better formatted. The owner's 2026-09-17 reading found the
 * transcript's copy repeated the note with worse typography and raw
 * markdown asterisks in it, so the card is now what a card is for: the
 * title, three chips, and the way into the note.
 *
 * `Prose` stays here because the Note pane and the workbench both render
 * `file:line` references through it.
 */

/** `ledger.go:33`, `ledger.go:27-34`, `internal/x/y.ts:10`. */
export const REF = /\b((?:[\w.-]+\/)*[\w.-]+\.(?:go|ts|tsx|js|jsx|py|rs|java|kt|rb|php|cs|sql|yaml|yml|json|md|toml|css|html)):(\d+(?:-\d+)?)\b/g

function asRecord(value: unknown): Record<string, unknown> | undefined {
  if (typeof value !== 'object' || value === null || Array.isArray(value)) return undefined
  return value as Record<string, unknown>
}

function str(value: unknown): string {
  return typeof value === 'string' ? value : ''
}

function strings(value: unknown): string[] {
  return Array.isArray(value) ? value.map(str).filter(Boolean) : []
}

/**
 * Prose with its `file:line` references as buttons and its backticked spans
 * as code. `onRef` is the layout's: it scrolls to the call that read the
 * file. Without it a reference is drawn as code and nothing more.
 */
export function Prose({ text, onRef }: { text: string; onRef?: (ref: string) => void }): JSX.Element {
  const out: ReactNode[] = []
  const codeSplit = text.split(/(`[^`]+`)/)
  codeSplit.forEach((chunk, i) => {
    if (chunk.startsWith('`') && chunk.endsWith('`') && chunk.length > 1) {
      out.push(<code key={`c${i}`}>{chunk.slice(1, -1)}</code>)
      return
    }
    let last = 0
    for (const m of chunk.matchAll(REF)) {
      const at = m.index ?? 0
      if (at > last) out.push(chunk.slice(last, at))
      const ref = m[0]
      out.push(
        onRef ? (
          <button key={`r${i}-${at}`} type="button" className="sc-ref" onClick={() => onRef(ref)} title={`Show the call that read ${m[1]}`}>
            {ref}
          </button>
        ) : (
          <code key={`r${i}-${at}`} className="sc-ref">
            {ref}
          </code>
        ),
      )
      last = at + ref.length
    }
    if (last < chunk.length) out.push(chunk.slice(last))
  })
  return <>{out}</>
}

/** The first line of a text with paragraphs in it, for a fix report's title. */
function firstParagraph(text: string): string {
  return (
    text
      .split(/\n{2,}/)
      .map((p) => p.trim())
      .filter(Boolean)[0] ?? ''
  )
}

export interface AnswerCardProps {
  event: RunEvent
  at: string
  /** The answer was written after a steer; the kicker says so. */
  revised?: boolean
  /** The run's kind decides which way through the card leads. */
  kind?: string
  /** Opens the inspector's Note tab. */
  onOpenNote?: () => void
  /** Opens the inspector's Changes tab. */
  onOpenChanges?: () => void
}

export default function AnswerCard({ event, at, revised = false, kind, onOpenNote, onOpenChanges }: AnswerCardProps): JSX.Element {
  const text = event.payload?.text ?? ''
  const answer = parseAnswer(text)
  const isFix = kind === 'fix' || (answer !== undefined && (Array.isArray(answer.testsRun) || Array.isArray(answer.filesChanged)))

  if (isFix) {
    return (
      <AnswerClosing
        title={firstParagraph(str(answer?.summary)) || 'Fix report'}
        at={at}
        changes
        onOpenChanges={onOpenChanges}
      />
    )
  }

  const root = asRecord(answer?.rootCause)
  return (
    <AnswerClosing
      title={str(answer?.title) || (text ? firstParagraph(text) : 'The run finished without a structured answer')}
      classification={str(answer?.classification)}
      confidence={str(root?.confidence) || str(answer?.confidence)}
      openQuestions={strings(answer?.openQuestions).length}
      at={at}
      revised={revised}
      onOpenNote={onOpenNote}
    />
  )
}

export { SupersededAnswer } from '../../components/session/AnswerClosing'
