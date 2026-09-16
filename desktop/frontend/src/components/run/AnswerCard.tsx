import ReactMarkdown from 'react-markdown'
import type { RunEvent } from '../../api/types'
import { fieldLabel, formatCost, isBlank, isURL, parseAnswer } from '../../lib/events'

/** Past this depth a nested object is shown as JSON rather than more rows. */
const MAX_DEPTH = 2

/** Prose longer than this, or with a line break, is drawn as markdown. */
const PROSE_MIN = 120

function Scalar({ value }: { value: string | number | boolean }) {
  if (typeof value === 'string') {
    if (isURL(value)) {
      return (
        <a href={value.trim()} target="_blank" rel="noreferrer">
          {value.trim()}
        </a>
      )
    }
    if (value.includes('\n') || value.length > PROSE_MIN) {
      return (
        <div className="md answer-prose" dir="auto">
          <ReactMarkdown>{value}</ReactMarkdown>
        </div>
      )
    }
    return <span dir="auto">{value}</span>
  }
  return <span>{String(value)}</span>
}

function Value({ value, depth }: { value: unknown; depth: number }) {
  if (typeof value === 'string' || typeof value === 'number' || typeof value === 'boolean') {
    return <Scalar value={value} />
  }
  if (Array.isArray(value)) {
    if (value.every((v) => typeof v === 'string' || typeof v === 'number' || typeof v === 'boolean')) {
      return (
        <ul className="answer-list">
          {value.map((v, i) => (
            <li key={i}>
              <Scalar value={v as string | number | boolean} />
            </li>
          ))}
        </ul>
      )
    }
    if (depth < MAX_DEPTH) {
      return (
        <ol className="answer-items">
          {value.map((v, i) => (
            <li key={i}>
              <Value value={v} depth={depth + 1} />
            </li>
          ))}
        </ol>
      )
    }
  }
  if (value !== null && typeof value === 'object' && depth < MAX_DEPTH) {
    return <AnswerFields data={value as Record<string, unknown>} depth={depth + 1} />
  }
  return <pre className="answer-json">{JSON.stringify(value, null, 2)}</pre>
}

/**
 * The schema's fields as label/value rows. A string is prose, a URL a link,
 * a list of strings a list, and an object one more level of rows; past two
 * levels the value is shown as JSON. Blank fields are left out, because a
 * schema has many optional keys and an empty row says nothing.
 */
export function AnswerFields({
  data,
  depth = 0,
}: {
  data: Record<string, unknown>
  depth?: number
}) {
  const entries = Object.entries(data).filter(([, v]) => !isBlank(v))
  if (entries.length === 0) return <p className="answer-empty">Every field is empty.</p>
  return (
    <dl className="answer-fields" data-depth={depth}>
      {entries.map(([key, value]) => (
        <div className="answer-row" key={key}>
          <dt>{fieldLabel(key)}</dt>
          <dd>
            <Value value={value} depth={depth} />
          </dd>
        </div>
      ))}
    </dl>
  )
}

/**
 * The run's answer: the final event's JSON as a card headed "Answer", its
 * fields as rows, and the raw JSON behind a disclosure for the reader who
 * wants the exact string the schema validated. A final event that carries
 * prose instead — a fix run's closing line — is drawn as prose; one that
 * carries nothing says the run finished. The "Note filed" banner above the
 * stream stays the pointer to the note itself.
 */
export default function AnswerCard({ event, at }: { event: RunEvent; at: string }) {
  const text = event.payload?.text ?? ''
  const answer = parseAnswer(text)
  const turns = event.payload?.turns
  const cost = event.payload?.costUsd
  const meta = [
    turns !== undefined ? `${turns} ${turns === 1 ? 'turn' : 'turns'}` : '',
    cost !== undefined ? formatCost(cost) : '',
  ].filter(Boolean)

  let pretty = text
  if (answer) {
    try {
      pretty = JSON.stringify(JSON.parse(text), null, 2)
    } catch {
      pretty = text
    }
  }

  return (
    <div className="msg msg--answer" data-testid="answer-card">
      <span className="msg-at">{at}</span>
      <section className="answer" aria-label="Answer">
        <div className="answer-head">
          <h2 className="answer-title">Answer</h2>
          {meta.length > 0 ? <span className="answer-meta">{meta.join(' · ')}</span> : null}
        </div>
        {answer ? (
          <AnswerFields data={answer} />
        ) : text ? (
          <div className="md answer-prose" dir="auto">
            <ReactMarkdown>{text}</ReactMarkdown>
          </div>
        ) : (
          <p className="answer-empty">The run finished without a structured answer.</p>
        )}
        {answer ? (
          <details className="answer-raw">
            <summary>Raw JSON</summary>
            <pre className="call-pre">{pretty}</pre>
          </details>
        ) : null}
      </section>
    </div>
  )
}
