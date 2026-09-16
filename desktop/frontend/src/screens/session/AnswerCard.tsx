import { useState, type ReactNode } from 'react'
import type { RunEvent } from '../../api/types'
import { parseAnswer } from '../../lib/events'
import type { FixReport } from '../../lib/review'
import { noteName } from '../../lib/review'
import Button from '../../ui/button'
import { AnswerFields } from '../../components/session/AnswerCard'
import { BracesIcon, ChevronIcon, NoteIcon } from './icons'

/*
 * The run's conclusion as a rich card at the end of the conversation: the
 * title, the root cause with its `file:line` references live, the evidence,
 * the tags a reader scans first (classification, confidence, counts), the
 * blast radius, the proposed fix, the reply draft in the customer's
 * language, and a footer that says where the note went. A fix run's card is
 * the report: what changed, the risks, the commit.
 *
 * An answer in neither shape falls back to the schema's fields as rows, and
 * Raw JSON stays behind a disclosure for the reader who wants the exact
 * string the schema validated.
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

/** Paragraphs from a text with blank lines in it. */
function paragraphs(text: string): string[] {
  return text
    .split(/\n{2,}/)
    .map((p) => p.trim())
    .filter(Boolean)
}

export type TagTone = 'done' | 'triaged' | 'blocked' | 'failed' | ''

export function Tag({ label, value, tone = '' }: { label: string; value: string; tone?: TagTone }): JSX.Element {
  return (
    <span className="sc-tag" data-tone={tone || undefined}>
      {label} <b>{value}</b>
    </span>
  )
}

function confidenceTone(confidence: string): TagTone {
  if (/^high/i.test(confidence)) return 'done'
  if (/^med/i.test(confidence)) return 'blocked'
  if (/^low/i.test(confidence)) return 'failed'
  return ''
}

interface Evidence {
  source: string
  query: string
  finding: string
  /** The first `file:line` the finding names, for the reference column. */
  ref: string
}

function evidenceOf(value: unknown): Evidence[] {
  if (!Array.isArray(value)) return []
  return value
    .map(asRecord)
    .filter((e): e is Record<string, unknown> => Boolean(e))
    .map((e) => {
      const finding = str(e.finding) || str(e.text) || str(e.summary)
      const ref = new RegExp(REF.source).exec(finding)?.[0] ?? ''
      return { source: str(e.source) || str(e.kind), query: str(e.query), finding, ref }
    })
}

/** How many evidence items the card shows before "N more". */
const EVIDENCE_SHOWN = 5

export interface AnswerCardProps {
  event: RunEvent
  at: string
  /** The answer was written after a steer; the kicker says so. */
  revised?: boolean
  /** The run's kind decides the card's shape. */
  kind?: string
  /** Where the note went, for the footer; the run's recorded path. */
  notePath?: string
  notesDir?: string
  /** A fix run's branch and commit, for the footer. */
  fix?: { branch?: string; commit?: string }
  onRef?: (ref: string) => void
  /** Opens the inspector's Note tab. */
  onOpenNote?: () => void
  /** Opens the inspector's Changes tab. */
  onOpenChanges?: () => void
}

function TriageCard({
  answer,
  at,
  revised,
  notePath,
  notesDir,
  onRef,
  onOpenNote,
  raw,
}: {
  answer: Record<string, unknown>
  at: string
  revised: boolean
  notePath?: string
  notesDir?: string
  onRef?: (ref: string) => void
  onOpenNote?: () => void
  raw: string
}): JSX.Element {
  const [allEvidence, setAllEvidence] = useState(false)
  const root = asRecord(answer.rootCause)
  const hypothesis = str(root?.hypothesis) || str(answer.rootCause)
  const confidence = str(root?.confidence) || str(answer.confidence)
  const evidence = evidenceOf(root?.evidence ?? answer.evidence)
  const codeRefs = strings(root?.codeRefs ?? answer.codeRefs)
  const classification = str(answer.classification)
  const blast = str(answer.blastRadius)
  const fix = asRecord(answer.proposedFix)
  const fixText = str(fix?.description) || str(answer.proposedFix)
  const fixFiles = strings(fix?.files)
  const draft = asRecord(answer.customerReplyDraft)
  const draftText = str(draft?.text) || str(answer.customerReplyDraft)
  const draftLang = str(draft?.language)
  const open = strings(answer.openQuestions)
  const ticket = asRecord(answer.ticket)
  const service = str(ticket?.service) || str(answer.service)
  const title = str(answer.title)

  const shownEvidence = allEvidence ? evidence : evidence.slice(0, EVIDENCE_SHOWN)
  const moreEvidence = evidence.slice(EVIDENCE_SHOWN)

  return (
    <>
      <div className="sc-ans__head">
        <div className="sc-ans__kicker">
          <BracesIcon />
          <span>Answer</span>
          {at ? (
            <>
              <span>·</span>
              <span>{at}</span>
            </>
          ) : null}
          {revised ? (
            <>
              <span>·</span>
              <span>revised after your steer</span>
            </>
          ) : null}
        </div>
        {title ? <h2 className="sc-ans__title">{title}</h2> : null}
        <div className="sc-ans__tags">
          {classification ? <Tag label="classification" value={classification} tone="triaged" /> : null}
          {confidence ? <Tag label="confidence" value={confidence} tone={confidenceTone(confidence)} /> : null}
          {evidence.length > 0 ? <Tag label="evidence" value={String(evidence.length)} /> : null}
          {codeRefs.length > 0 ? <Tag label="code refs" value={String(codeRefs.length)} /> : null}
          {open.length > 0 ? <Tag label="open questions" value={String(open.length)} tone="blocked" /> : null}
          {service ? <Tag label="service" value={service} /> : null}
        </div>
      </div>
      {hypothesis ? (
        <section className="sc-ans__sec">
          <h3>Root cause</h3>
          {paragraphs(hypothesis).map((p, i) => (
            <p key={i} dir="auto">
              <Prose text={p} onRef={onRef} />
            </p>
          ))}
        </section>
      ) : null}
      {evidence.length > 0 ? (
        <section className="sc-ans__sec">
          <h3>Evidence</h3>
          <ul className="sc-ev">
            {shownEvidence.map((e, i) => (
              <li key={i}>
                <span className="sc-ev__ref">
                  {e.ref ? (
                    onRef ? (
                      <button type="button" className="sc-ref" onClick={() => onRef(e.ref)}>
                        {e.ref}
                      </button>
                    ) : (
                      e.ref
                    )
                  ) : (
                    e.source
                  )}
                  {e.query || (e.ref && e.source) ? (
                    <span className="sc-ev__src">{[e.ref ? e.source : '', e.query].filter(Boolean).join(' · ')}</span>
                  ) : null}
                </span>
                <span dir="auto">
                  <Prose text={e.finding} onRef={onRef} />
                </span>
              </li>
            ))}
          </ul>
          {moreEvidence.length > 0 ? (
            <button type="button" className="sc-link sc-ans__more" aria-expanded={allEvidence} onClick={() => setAllEvidence((v) => !v)}>
              {allEvidence
                ? 'Fewer'
                : `${moreEvidence.length} more: ${moreEvidence.map((e) => e.ref || e.query || e.source).filter(Boolean).join(' · ')}`}
            </button>
          ) : null}
        </section>
      ) : null}
      {blast ? (
        <section className="sc-ans__sec">
          <h3>Blast radius</h3>
          {paragraphs(blast).map((p, i) => (
            <p key={i} dir="auto">
              <Prose text={p} onRef={onRef} />
            </p>
          ))}
        </section>
      ) : null}
      {fixText ? (
        <section className="sc-ans__sec">
          <h3>
            Proposed fix {fixFiles.length > 0 ? <span className="sc-ans__h3-note">{fixFiles.join(' · ')}</span> : null}
          </h3>
          {paragraphs(fixText).map((p, i) => (
            <p key={i} dir="auto">
              <Prose text={p} onRef={onRef} />
            </p>
          ))}
        </section>
      ) : null}
      {open.length > 0 ? (
        <section className="sc-ans__sec">
          <h3>Open questions</h3>
          <ol className="sc-ans__list">
            {open.map((q, i) => (
              <li key={i} dir="auto">
                <Prose text={q} onRef={onRef} />
              </li>
            ))}
          </ol>
        </section>
      ) : null}
      {draftText ? (
        <section className="sc-ans__sec">
          <h3>
            Reply draft {draftLang ? <span className="sc-ans__h3-note">{draftLang}</span> : null}
          </h3>
          <div className="sc-draft" dir="auto" lang={draftLang || undefined}>
            {draftText.split('\n').map((line, i) => (
              <span key={i}>
                {line}
                <br />
              </span>
            ))}
          </div>
        </section>
      ) : null}
      <details className="sc-ans__raw">
        <summary>Raw JSON</summary>
        <pre className="sc-code">{raw}</pre>
      </details>
      <div className="sc-ans__foot">
        <NoteIcon />
        {notePath ? (
          <>
            <span>Note saved</span>
            <span className="sc-mono" dir="ltr">
              {noteName(notePath, notesDir)}
            </span>
          </>
        ) : (
          <span>The note is filed as the run finishes</span>
        )}
        {onOpenNote ? (
          <span className="sc-ans__foot-act">
            <Button size="sm" onClick={onOpenNote}>
              Open in Note
            </Button>
          </span>
        ) : null}
      </div>
    </>
  )
}

function FixCard({
  report,
  at,
  fix,
  onRef,
  onOpenChanges,
  raw,
}: {
  report: FixReport
  at: string
  fix?: { branch?: string; commit?: string }
  onRef?: (ref: string) => void
  onOpenChanges?: () => void
  raw: string
}): JSX.Element {
  const [title, ...rest] = paragraphs(report.summary)
  const ok = report.testsRun.filter((t) => /\b(ok|pass|succeed|green)/i.test(t.result)).length
  return (
    <>
      <div className="sc-ans__head">
        <div className="sc-ans__kicker">
          <BracesIcon />
          <span>Fix report</span>
          {at ? (
            <>
              <span>·</span>
              <span>{at}</span>
            </>
          ) : null}
        </div>
        {title ? <h2 className="sc-ans__title">{title}</h2> : null}
        <div className="sc-ans__tags">
          {report.testsRun.length > 0 ? (
            <Tag label="tests" value={`${report.testsRun.length} run · ${ok} ok`} tone={ok === report.testsRun.length ? 'done' : 'blocked'} />
          ) : null}
          {report.filesChanged.length > 0 ? <Tag label="files" value={report.filesChanged.join(' · ')} /> : null}
          <Tag
            label="deviation from note"
            value={report.deviationFromNote ? 'yes' : 'none'}
            tone={report.deviationFromNote ? 'blocked' : 'done'}
          />
          {fix?.branch ? <Tag label="branch" value={fix.branch.length > 18 ? `${fix.branch.slice(0, 17)}…` : fix.branch} /> : null}
        </div>
      </div>
      {rest.length > 0 ? (
        <section className="sc-ans__sec">
          <h3>What changed</h3>
          {rest.map((p, i) => (
            <p key={i} dir="auto">
              <Prose text={p} onRef={onRef} />
            </p>
          ))}
        </section>
      ) : null}
      {report.deviationFromNote ? (
        <section className="sc-ans__sec">
          <h3>Deviation from the note</h3>
          <p dir="auto">{report.deviationFromNote}</p>
        </section>
      ) : null}
      {report.risks ? (
        <section className="sc-ans__sec">
          <h3>Risks</h3>
          {paragraphs(report.risks).map((p, i) => (
            <p key={i} dir="auto">
              <Prose text={p} onRef={onRef} />
            </p>
          ))}
        </section>
      ) : null}
      <details className="sc-ans__raw">
        <summary>Raw JSON</summary>
        <pre className="sc-code">{raw}</pre>
      </details>
      <div className="sc-ans__foot">
        {fix?.commit ? (
          <>
            <span>Committed</span>
            <span className="sc-mono">{fix.commit.slice(0, 7)}</span>
          </>
        ) : (
          <span>Finished</span>
        )}
        {fix?.branch ? (
          <>
            <span>on</span>
            <span className="sc-mono sc-ans__branch" dir="ltr">
              {fix.branch}
            </span>
          </>
        ) : null}
        {onOpenChanges ? (
          <span className="sc-ans__foot-act">
            <Button size="sm" onClick={onOpenChanges}>
              Review changes
            </Button>
          </span>
        ) : null}
      </div>
    </>
  )
}

/** The fix report shape, read off one final event's JSON. */
function reportOf(answer: Record<string, unknown> | undefined): FixReport | undefined {
  if (!answer) return undefined
  const tests = Array.isArray(answer.testsRun) ? answer.testsRun : undefined
  if (!tests && !Array.isArray(answer.filesChanged)) return undefined
  return {
    summary: str(answer.summary),
    filesChanged: strings(answer.filesChanged),
    testsRun: (tests ?? [])
      .map(asRecord)
      .filter((t): t is Record<string, unknown> => Boolean(t))
      .map((t) => ({ command: str(t.command), result: str(t.result) })),
    risks: str(answer.risks),
    deviationFromNote: str(answer.deviationFromNote),
  }
}

export default function AnswerCard({
  event,
  at,
  revised = false,
  kind,
  notePath,
  notesDir,
  fix,
  onRef,
  onOpenNote,
  onOpenChanges,
}: AnswerCardProps): JSX.Element {
  const text = event.payload?.text ?? ''
  const answer = parseAnswer(text)
  let raw = text
  if (answer) {
    try {
      raw = JSON.stringify(JSON.parse(text), null, 2)
    } catch {
      raw = text
    }
  }
  const report = reportOf(answer)
  const triage = answer && !report && (answer.rootCause !== undefined || answer.title !== undefined)

  return (
    <section className="sc-ans" aria-label={report ? 'Fix report' : 'Answer'} data-testid="answer-card">
      {report ? (
        <FixCard report={report} at={at} fix={fix} onRef={onRef} onOpenChanges={onOpenChanges} raw={raw} />
      ) : triage && answer ? (
        <TriageCard answer={answer} at={at} revised={revised} notePath={notePath} notesDir={notesDir} onRef={onRef} onOpenNote={onOpenNote} raw={raw} />
      ) : (
        <>
          <div className="sc-ans__head">
            <div className="sc-ans__kicker">
              <BracesIcon />
              <span>{kind === 'fix' ? 'Fix report' : 'Answer'}</span>
              {at ? (
                <>
                  <span>·</span>
                  <span>{at}</span>
                </>
              ) : null}
            </div>
          </div>
          <section className="sc-ans__sec">
            {answer ? (
              <AnswerFields data={answer} />
            ) : text ? (
              paragraphs(text).map((p, i) => (
                <p key={i} dir="auto">
                  <Prose text={p} onRef={onRef} />
                </p>
              ))
            ) : (
              <p className="sc-ans__empty">The run finished without a structured answer.</p>
            )}
          </section>
          {answer ? (
            <details className="sc-ans__raw">
              <summary>Raw JSON</summary>
              <pre className="sc-code">{raw}</pre>
            </details>
          ) : null}
        </>
      )}
    </section>
  )
}

/** An answer a later one superseded: one row, folded, so the revision stands alone. */
export function SupersededAnswer({ at, seconds, onOpen }: { at: string; seconds?: number; onOpen?: () => void }): JSX.Element {
  return (
    <button type="button" className="sc-ans sc-ans--prev" onClick={onOpen} disabled={!onOpen} data-testid="answer-prev">
      <span>
        Answer at <span className="sc-mono">{at}</span>
      </span>
      <span className="sc-mono">
        {seconds !== undefined ? `written in ${seconds} s · ` : ''}superseded by the revision below
      </span>
      <ChevronIcon />
    </button>
  )
}
