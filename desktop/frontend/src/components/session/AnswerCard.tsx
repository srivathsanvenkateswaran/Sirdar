import type { MouseEvent } from 'react'
import type { Marker as MarkerModel } from '../../lib/evidence'
import { fieldLabel, isBlank, isURL } from '../../lib/events'
import StatusBadge from '../../ui/status-badge'
import EvidenceList from './Evidence'
import type { SessionStep } from './model'
import { evidenceOf } from './model'
import { Markdown, RefProse, ReplyLetter, Section, type MarkerHandlers } from './Prose'

export interface AnswerCardProps {
  /** The final event's JSON, parsed. */
  answer: Record<string, unknown>
  markers: MarkerModel[]
  hotMarker?: string
  onMarker?: (id: string, event: MouseEvent<HTMLElement>) => void
  steps: SessionStep[]
  onGoToStep?: (index: number) => void
  /** `2 turns · $1.02`, when the final event said. */
  meta?: string
  /** The letter's actions after Copy: Open desk. */
  replyActions?: React.ReactNode
}

function str(v: unknown): string {
  return typeof v === 'string' ? v : ''
}

function asRecord(v: unknown): Record<string, unknown> | undefined {
  return typeof v === 'object' && v !== null && !Array.isArray(v) ? (v as Record<string, unknown>) : undefined
}

function list(v: unknown): string[] {
  return Array.isArray(v) ? v.map((x) => (typeof x === 'string' ? x : JSON.stringify(x))) : []
}

/** The keys the card draws as sections; everything else falls to the generic rows. */
const KNOWN = new Set([
  'ticket',
  'title',
  'complaint',
  'complaintOriginal',
  'customerReplyDraft',
  'timeline',
  'reproSteps',
  'rootCause',
  'blastRadius',
  'classification',
  'proposedFix',
  'openQuestions',
])

function Scalar({ value, handlers }: { value: string | number | boolean; handlers: MarkerHandlers }): JSX.Element {
  if (typeof value === 'string') {
    if (isURL(value)) {
      return (
        <a href={value.trim()} target="_blank" rel="noreferrer">
          {value.trim()}
        </a>
      )
    }
    return <RefProse text={value} className="sn-p" {...handlers} />
  }
  return <span>{String(value)}</span>
}

/**
 * Whatever fields the card did not recognise, as label/value rows: a string
 * is prose, a URL a link, a list a list, an object one more level of rows,
 * and past two levels the value is JSON. Blank fields are left out.
 */
export function AnswerFields({ data, depth = 0, handlers }: { data: Record<string, unknown>; depth?: number; handlers: MarkerHandlers }): JSX.Element | null {
  const entries = Object.entries(data).filter(([, v]) => !isBlank(v))
  if (entries.length === 0) return null
  return (
    <dl className="sn-fields" data-depth={depth}>
      {entries.map(([key, value]) => (
        <div className="sn-fields__row" key={key}>
          <dt className="sn-meta__k">{fieldLabel(key)}</dt>
          <dd>
            {typeof value === 'string' || typeof value === 'number' || typeof value === 'boolean' ? (
              <Scalar value={value} handlers={handlers} />
            ) : Array.isArray(value) && value.every((v) => typeof v !== 'object') ? (
              <ul className="sn-qs">
                {value.map((v, i) => (
                  <li key={i}>
                    <Scalar value={v as string} handlers={handlers} />
                  </li>
                ))}
              </ul>
            ) : asRecord(value) && depth < 2 ? (
              <AnswerFields data={value as Record<string, unknown>} depth={depth + 1} handlers={handlers} />
            ) : (
              <pre className="sn-pre">{JSON.stringify(value, null, 2)}</pre>
            )}
          </dd>
        </div>
      ))}
    </dl>
  )
}

/**
 * The run's structured answer, rendered as the finding it is: the title,
 * the root cause with its `file:line` references carrying evidence markers,
 * the evidence as callouts, confidence and classification, blast radius,
 * the proposed fix, the reply draft as a letter, and the raw JSON behind a
 * disclosure for the reader who wants the exact string the schema
 * validated. A field the schema does not name falls to generic rows.
 */
export default function AnswerCard({ answer, markers, hotMarker, onMarker, steps, onGoToStep, meta, replyActions }: AnswerCardProps): JSX.Element {
  const handlers: MarkerHandlers = { markers, hotMarker, onMarker }
  const ticket = asRecord(answer.ticket)
  const rootCause = asRecord(answer.rootCause)
  const fix = asRecord(answer.proposedFix)
  const reply = asRecord(answer.customerReplyDraft)
  const evidence = evidenceOf(answer)
  const classification = str(answer.classification)
  const confidence = str(rootCause?.confidence)
  const title = str(answer.title)
  const timeline = Array.isArray(answer.timeline) ? answer.timeline.map(asRecord).filter(Boolean) : []
  const rest = Object.fromEntries(Object.entries(answer).filter(([k]) => !KNOWN.has(k)))
  let pretty = ''
  try {
    pretty = JSON.stringify(answer, null, 2)
  } catch {
    pretty = ''
  }

  return (
    <section className="sn-answer" aria-label="Answer" data-testid="answer-card">
      {title ? (
        <h1 className="sn-note__h1" dir="auto">
          {title}
        </h1>
      ) : null}
      <div className="sn-note__sub">
        <span>The agent's answer{meta ? ` · ${meta}` : ''}</span>
        {ticket ? (
          <>
            <span>·</span>
            <span dir="auto">Ticket: {str(ticket.title) || str(ticket.key)}</span>
          </>
        ) : null}
      </div>
      {(classification || confidence) && (
        <div className="sn-verdict" style={{ marginBlockStart: 12 }}>
          {classification ? <StatusBadge status="completed">{classification}</StatusBadge> : null}
          {confidence ? <span className="sn-chip">confidence {confidence}</span> : null}
        </div>
      )}

      {rootCause ? (
        <Section title="Root cause" tag={[classification, confidence ? `confidence ${confidence}` : ''].filter(Boolean).join(' · ') || undefined}>
          <RefProse text={str(rootCause.hypothesis)} {...handlers} />
          {list(rootCause.codeRefs).length > 0 ? (
            <p className="sn-p">
              Code references:{' '}
              {list(rootCause.codeRefs).map((r, i) => (
                <span key={r}>
                  {i > 0 ? ', ' : ''}
                  <RefProseInline text={r} handlers={handlers} />
                </span>
              ))}
            </p>
          ) : null}
        </Section>
      ) : null}

      {str(answer.complaint) ? (
        <Section title="Customer complaint" tag={str(answer.complaintOriginal) ? 'translated · original' : 'translated'}>
          <div className="sn-complaint">
            <div className="sn-complaint__en">
              <p>{str(answer.complaint)}</p>
            </div>
            {str(answer.complaintOriginal) ? (
              <div className="sn-complaint__ar" dir="rtl">
                <p>{str(answer.complaintOriginal)}</p>
              </div>
            ) : null}
          </div>
        </Section>
      ) : null}

      {evidence.length > 0 ? (
        <Section title="Evidence" tag={`${evidence.length} ${evidence.length === 1 ? 'item' : 'items'} · each traces to a step`}>
          <EvidenceList items={evidence} markers={markers} hotMarker={hotMarker} onMarker={onMarker} steps={steps} onGoToStep={onGoToStep} />
        </Section>
      ) : null}

      {fix ? (
        <Section title="Proposed fix" tag={list(fix.files).join(' · ') || undefined}>
          <RefProse text={str(fix.description)} {...handlers} />
          {str(fix.remediationSql) ? (
            <p className="sn-p">
              <b>Remediation SQL:</b> {str(fix.remediationSql)}
            </p>
          ) : null}
          {str(fix.risks) ? (
            <p className="sn-p">
              <b>Risks:</b> {str(fix.risks)}
            </p>
          ) : null}
        </Section>
      ) : null}

      {str(answer.blastRadius) ? (
        <Section title="Blast radius">
          <RefProse text={str(answer.blastRadius)} {...handlers} />
        </Section>
      ) : null}

      {list(answer.reproSteps).length > 0 ? (
        <Section title="Repro steps">
          <ol className="sn-qs">
            {list(answer.reproSteps).map((s, i) => (
              <li key={i} dir="auto">
                {RefProseInline({ text: s, handlers })}
              </li>
            ))}
          </ol>
        </Section>
      ) : null}

      {timeline.length > 0 ? (
        <Section title="Conversation" tag={`${timeline.length} ${timeline.length === 1 ? 'entry' : 'entries'}`}>
          <ul className="sn-qs">
            {timeline.map((t, i) => (
              <li key={i} dir="auto">
                <b>{str(t?.at)}</b> {str(t?.role)}: {str(t?.summary)}
              </li>
            ))}
          </ul>
        </Section>
      ) : null}

      {list(answer.openQuestions).length > 0 ? (
        <Section title="Open questions" tag={String(list(answer.openQuestions).length)}>
          <ul className="sn-qs">
            {list(answer.openQuestions).map((q, i) => (
              <li key={i} dir="auto">
                {RefProseInline({ text: q, handlers })}
              </li>
            ))}
          </ul>
        </Section>
      ) : null}

      {reply && str(reply.text) ? (
        <Section title="Reply to the customer" tag={`draft · ${str(reply.language) || 'language unknown'} · not sent`}>
          <ReplyLetter language={str(reply.language)} text={str(reply.text)} to={ticket ? str(ticket.customer) : undefined} actions={replyActions} />
        </Section>
      ) : null}

      {Object.keys(rest).length > 0 ? (
        <Section title="Also in the answer">
          <AnswerFields data={rest} handlers={handlers} />
        </Section>
      ) : null}

      {pretty ? (
        <details className="sn-raw">
          <summary>Raw JSON</summary>
          <pre className="sn-pre">{pretty}</pre>
        </details>
      ) : null}
    </section>
  )
}

/** One line of prose inline, no paragraph around it. */
function RefProseInline({ text, handlers }: { text: string; handlers: MarkerHandlers }): JSX.Element {
  return <Markdown text={text} {...handlers} />
}
