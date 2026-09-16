import { useMemo, useState, type JSX, type ReactNode } from 'react'
import { AnswerFields } from '../../../components/run/AnswerCard'
import { fieldLabel, isBlank } from '../../../lib/events'
import type { FixReport } from '../../../lib/review'
import Button from '../../../ui/button'
import { Prose as Inline } from '../AnswerCard'
import Document, { type OutlineItem } from './Document'

/**
 * The final JSON as a document: title, a facts strip (classification,
 * confidence, service, priority, customer, helpdesk), then the sections a
 * triage answer has — root cause with its file:line references, proposed
 * fix, evidence as a source/query/finding table, blast radius, the complaint
 * in both languages, timeline, repro steps, open questions, the reply draft
 * as a letter with Copy — and Raw JSON at the end. A fix run's report is the
 * same page with its own sections. A document the schema does not know is
 * drawn field by field.
 *
 * A local adapter with the shared `AnswerCard`'s name; `variant="document"`
 * is the prop the shared block is expected to grow. `onRef` is what a
 * file:line chip does when clicked: the Workbench searches the console for
 * the calls that read that file, which is the evidence marker's job until
 * `EvidenceMarkers` lands.
 */
export interface AnswerCardProps {
  variant: 'document'
  answer?: Record<string, unknown>
  /** The final event's text, for Raw JSON and for a run that answered in prose. */
  text: string
  report?: FixReport
  /** A `file:line` reference or a file the reader clicked, as written. */
  onRef?: (ref: string) => void
}

function asRecord(v: unknown): Record<string, unknown> | undefined {
  return typeof v === 'object' && v !== null && !Array.isArray(v) ? (v as Record<string, unknown>) : undefined
}
function str(v: unknown): string {
  return typeof v === 'string' ? v : ''
}
function list(v: unknown): unknown[] {
  return Array.isArray(v) ? v : []
}

/**
 * Paragraphs of prose, each with its `file:line` references live and its
 * backticked spans as code — the Conversation layout's `Prose` does the
 * inline work; this splits the paragraphs and gives each the page's measure.
 * `onRef` gets the reference as written (`ledger.go:27-34`).
 */
export function Prose({ text, onRef }: { text: string; onRef?: (ref: string) => void }): JSX.Element {
  return (
    <>
      {text
        .split(/\n{2,}/)
        .map((p) => p.trim())
        .filter(Boolean)
        .map((para, i) => (
          <p key={i} className="wb-prose" dir="auto">
            <Inline text={para} onRef={onRef} />
          </p>
        ))}
    </>
  )
}

function Section({ id, title, aside, children }: { id: string; title: string; aside?: string; children: ReactNode }): JSX.Element {
  return (
    <section className="wb-sec" data-sec={id} aria-labelledby={`wb-sec-${id}`}>
      <div className="wb-sec-h" id={`wb-sec-${id}`}>
        {title}
        {aside ? <span className="wb-mono">{aside}</span> : null}
      </div>
      {children}
    </section>
  )
}

function CopyButton({ text, label }: { text: string; label: string }): JSX.Element {
  const [copied, setCopied] = useState(false)
  return (
    <Button
      variant="pale"
      size="sm"
      onClick={() => {
        void navigator.clipboard?.writeText(text).then(
          () => {
            setCopied(true)
            setTimeout(() => setCopied(false), 2000)
          },
          () => {},
        )
      }}
      aria-label={label}
    >
      {copied ? 'Copied' : 'Copy'}
    </Button>
  )
}

/** The known triage fields, in the order the page prints them. */
const TRIAGE_KEYS = new Set([
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

interface Built {
  title: string
  facts: { label: string; value: string; mono?: boolean }[]
  sections: { item: OutlineItem; body: ReactNode }[]
}

function buildTriage(a: Record<string, unknown>, onRef?: (ref: string) => void): Built {
  const ticket = asRecord(a.ticket) ?? {}
  const root = asRecord(a.rootCause) ?? {}
  const fix = asRecord(a.proposedFix) ?? {}
  const evidence = list(root.evidence).map(asRecord).filter(Boolean) as Record<string, unknown>[]
  const refs = list(root.codeRefs).map(str).filter(Boolean)
  const timeline = list(a.timeline).map(asRecord).filter(Boolean) as Record<string, unknown>[]
  const steps = list(a.reproSteps).map(str).filter(Boolean)
  const questions = list(a.openQuestions).map(str).filter(Boolean)
  const draft = asRecord(a.customerReplyDraft)
  const files = list(fix.files).map(str).filter(Boolean)

  const facts: Built['facts'] = []
  if (str(a.classification)) facts.push({ label: 'classification', value: str(a.classification) })
  if (str(root.confidence)) facts.push({ label: 'confidence', value: str(root.confidence) })
  if (str(ticket.service)) facts.push({ label: 'service', value: str(ticket.service), mono: true })
  if (str(ticket.priority)) facts.push({ label: 'priority', value: str(ticket.priority) })
  if (str(ticket.customer)) facts.push({ label: 'customer', value: `${str(ticket.customer)}${str(ticket.customerId) ? ` ${str(ticket.customerId)}` : ''}` })
  if (str(ticket.helpdeskId)) facts.push({ label: 'helpdesk', value: str(ticket.helpdeskId), mono: true })

  const sections: Built['sections'] = []
  if (str(root.hypothesis) || refs.length > 0) {
    sections.push({
      item: { id: 'root-cause', title: 'Root cause' },
      body: (
        <Section id="root-cause" title="Root cause hypothesis">
          <Prose text={str(root.hypothesis)} onRef={onRef} />
          {refs.length > 0 ? (
            <div className="wb-refs" aria-label="Code references">
              {refs.map((r) => (
                <button key={r} type="button" className="wb-ref" onClick={() => onRef?.(r)} title={`Find ${r.split(':')[0]} in the console`}>
                  {r}
                </button>
              ))}
            </div>
          ) : null}
        </Section>
      ),
    })
  }
  if (str(fix.description)) {
    const sql = str(fix.remediationSql)
    sections.push({
      item: { id: 'proposed-fix', title: 'Proposed fix', n: files.length > 0 ? `${files.length} ${files.length === 1 ? 'file' : 'files'}` : undefined },
      body: (
        <Section id="proposed-fix" title="Proposed fix" aside={files.join(' · ') || undefined}>
          <Prose text={str(fix.description)} onRef={onRef} />
          {str(fix.risks) ? (
            <>
              <div className="wb-sec-h wb-sec-h--sub">Risks</div>
              <Prose text={str(fix.risks)} onRef={onRef} />
            </>
          ) : null}
          {sql && !/^none\b/i.test(sql) ? (
            <>
              <div className="wb-sec-h wb-sec-h--sub">Remediation SQL</div>
              <pre className="wb-pre">{sql}</pre>
            </>
          ) : null}
        </Section>
      ),
    })
  }
  if (evidence.length > 0) {
    const sources = new Map<string, number>()
    for (const e of evidence) sources.set(str(e.source) || 'other', (sources.get(str(e.source) || 'other') ?? 0) + 1)
    const aside = `${evidence.length} items · ${[...sources].map(([s, n]) => `${n} ${s}`).join(' · ')}`
    sections.push({
      item: { id: 'evidence', title: 'Evidence', n: String(evidence.length) },
      body: (
        <Section id="evidence" title="Evidence" aside={aside}>
          <div className="wb-ev" role="table" aria-label="Evidence">
            <span className="wb-ev__hd" role="columnheader">source</span>
            <span className="wb-ev__hd" role="columnheader">query</span>
            <span className="wb-ev__hd" role="columnheader">finding</span>
            {evidence.map((e, i) => (
              <div key={i} className="wb-ev__row" role="row">
                <span className="wb-ev__src" role="cell">{str(e.source)}</span>
                <span className="wb-ev__q" role="cell" title={str(e.query)}>
                  {onRef && str(e.query) ? (
                    <button type="button" className="wb-refbtn" onClick={() => onRef(str(e.query))} title="Find this call in the console">
                      {str(e.query)}
                    </button>
                  ) : (
                    str(e.query)
                  )}
                </span>
                <span role="cell">
                  <Prose text={str(e.finding)} onRef={onRef} />
                </span>
              </div>
            ))}
          </div>
        </Section>
      ),
    })
  }
  if (str(a.blastRadius)) {
    sections.push({
      item: { id: 'blast-radius', title: 'Blast radius' },
      body: (
        <Section id="blast-radius" title="Blast radius">
          <Prose text={str(a.blastRadius)} onRef={onRef} />
        </Section>
      ),
    })
  }
  if (str(a.complaint) || str(a.complaintOriginal)) {
    const both = str(a.complaint) && str(a.complaintOriginal)
    sections.push({
      item: { id: 'complaint', title: 'Complaint', n: both ? 'ar · en' : undefined },
      body: (
        <Section id="complaint" title="Customer complaint" aside={both ? 'translated · original' : undefined}>
          <div className="wb-two">
            {str(a.complaint) ? <Prose text={str(a.complaint)} /> : null}
            {str(a.complaintOriginal) ? (
              <div className="wb-original" dir="auto">
                <Prose text={str(a.complaintOriginal)} />
              </div>
            ) : null}
          </div>
        </Section>
      ),
    })
  }
  if (timeline.length > 0) {
    sections.push({
      item: { id: 'timeline', title: 'Timeline', n: String(timeline.length) },
      body: (
        <Section id="timeline" title="Timeline">
          <div className="wb-ev wb-ev--timeline" role="table" aria-label="Timeline">
            {timeline.map((t, i) => (
              <div key={i} className="wb-ev__row" role="row">
                <span className="wb-ev__src" role="cell">{str(t.role)}</span>
                <span className="wb-ev__q" role="cell" title={str(t.at)}>{str(t.at)}</span>
                <span role="cell">
                  <Prose text={str(t.summary)} onRef={onRef} />
                </span>
              </div>
            ))}
          </div>
        </Section>
      ),
    })
  }
  if (steps.length > 0) {
    sections.push({
      item: { id: 'repro', title: 'Repro steps', n: String(steps.length) },
      body: (
        <Section id="repro" title="Repro steps">
          <ol className="wb-list">
            {steps.map((s, i) => (
              <li key={i}>
                <Prose text={s} onRef={onRef} />
              </li>
            ))}
          </ol>
        </Section>
      ),
    })
  }
  if (questions.length > 0) {
    sections.push({
      item: { id: 'open-questions', title: 'Open questions', n: String(questions.length) },
      body: (
        <Section id="open-questions" title="Open questions">
          <ul className="wb-list">
            {questions.map((q, i) => (
              <li key={i}>
                <Prose text={q} onRef={onRef} />
              </li>
            ))}
          </ul>
        </Section>
      ),
    })
  }
  if (draft && str(draft.text)) {
    sections.push({
      item: { id: 'reply-draft', title: 'Reply draft', n: str(draft.language) || undefined },
      body: (
        <Section id="reply-draft" title="Reply draft" aside={str(draft.language) ? `in ${str(draft.language)}` : undefined}>
          <div className="wb-letter" dir="auto">
            <Prose text={str(draft.text)} />
            <div className="wb-letter__acts">
              <CopyButton text={str(draft.text)} label="Copy the reply draft" />
            </div>
          </div>
        </Section>
      ),
    })
  }
  // Whatever the schema grew that this page does not know.
  const rest = Object.fromEntries(Object.entries(a).filter(([k, v]) => !TRIAGE_KEYS.has(k) && !isBlank(v)))
  if (Object.keys(rest).length > 0) {
    sections.push({
      item: { id: 'more', title: 'More fields', n: String(Object.keys(rest).length) },
      body: (
        <Section id="more" title="More fields">
          <AnswerFields data={rest} />
        </Section>
      ),
    })
  }
  return { title: str(a.title) || 'Answer', facts, sections }
}

function buildFix(r: FixReport, onRef?: (ref: string) => void): Built {
  const [subject, ...body] = r.summary.split('\n')
  const sections: Built['sections'] = []
  if (body.join('\n').trim()) {
    sections.push({
      item: { id: 'summary', title: 'Summary' },
      body: (
        <Section id="summary" title="Summary">
          <Prose text={body.join('\n').trim()} onRef={onRef} />
        </Section>
      ),
    })
  }
  if (r.filesChanged.length > 0) {
    sections.push({
      item: { id: 'files', title: 'Files changed', n: String(r.filesChanged.length) },
      body: (
        <Section id="files" title="Files changed">
          <div className="wb-refs">
            {r.filesChanged.map((f) => (
              <button key={f} type="button" className="wb-ref" onClick={() => onRef?.(f)} title={`Find ${f} in the console`}>
                {f}
              </button>
            ))}
          </div>
        </Section>
      ),
    })
  }
  if (r.testsRun.length > 0) {
    sections.push({
      item: { id: 'tests', title: 'Tests run', n: String(r.testsRun.length) },
      body: (
        <Section id="tests" title="Tests run">
          <div className="wb-ev wb-ev--tests" role="table" aria-label="Tests run">
            {r.testsRun.map((t, i) => (
              <div key={i} className="wb-ev__row" role="row">
                <span className="wb-ev__q" role="cell" title={t.command}>{t.command}</span>
                <span role="cell" data-failed={/fail/i.test(t.result) ? 'true' : undefined}>{t.result}</span>
              </div>
            ))}
          </div>
        </Section>
      ),
    })
  }
  if (r.risks) {
    sections.push({
      item: { id: 'risks', title: 'Risks' },
      body: (
        <Section id="risks" title="Risks">
          <Prose text={r.risks} onRef={onRef} />
        </Section>
      ),
    })
  }
  sections.push({
    item: { id: 'deviation', title: 'Deviation from note' },
    body: (
      <Section id="deviation" title="Deviation from note">
        <Prose text={r.deviationFromNote || 'None: the change follows the note’s proposed fix.'} onRef={onRef} />
      </Section>
    ),
  })
  return { title: subject || 'Fix report', facts: [{ label: 'files', value: String(r.filesChanged.length) }], sections }
}

function buildGeneric(a: Record<string, unknown>): Built {
  const entries = Object.entries(a).filter(([, v]) => !isBlank(v))
  return {
    title: str(a.title) || str(a.summary).split('\n')[0] || 'Answer',
    facts: [],
    sections: entries.map(([k, v]) => ({
      item: { id: k, title: fieldLabel(k) },
      body: (
        <Section id={k} title={fieldLabel(k)}>
          {typeof v === 'string' ? <Prose text={v} /> : <AnswerFields data={{ [k]: v }} />}
        </Section>
      ),
    })),
  }
}

/** The outline the Workbench shows for an answer, for the tab count and tests. */
export function answerOutline(answer: Record<string, unknown> | undefined, report?: FixReport): OutlineItem[] {
  if (report) return buildFix(report).sections.map((s) => s.item)
  if (!answer) return []
  const built = 'rootCause' in answer || 'ticket' in answer ? buildTriage(answer) : buildGeneric(answer)
  return [...built.sections.map((s) => s.item), { id: 'raw', title: 'Raw JSON' }]
}

export default function AnswerCard({ answer, text, report, onRef }: AnswerCardProps): JSX.Element {
  const built = useMemo<Built | undefined>(() => {
    if (report) return buildFix(report, onRef)
    if (!answer) return undefined
    return 'rootCause' in answer || 'ticket' in answer ? buildTriage(answer, onRef) : buildGeneric(answer)
  }, [answer, report, onRef])

  const pretty = useMemo(() => {
    if (!answer) return text
    try {
      return JSON.stringify(answer, null, 2)
    } catch {
      return text
    }
  }, [answer, text])

  const outline = useMemo<OutlineItem[]>(
    () => (built ? [...built.sections.map((s) => s.item), ...(answer ? [{ id: 'raw', title: 'Raw JSON' }] : [])] : []),
    [built, answer],
  )

  if (!built) {
    return (
      <Document outline={[]} label="Answer">
        {text ? (
          <section data-sec="prose">
            <Prose text={text} onRef={onRef} />
          </section>
        ) : (
          <p className="wb-empty">No answer yet. The run writes one when it completes.</p>
        )}
      </Document>
    )
  }

  return (
    <Document outline={outline} label="Answer">
      <h2 className="wb-doc-h" dir="auto">
        {built.title}
      </h2>
      {built.facts.length > 0 ? (
        <div className="wb-facts">
          {built.facts.map((f) => (
            <span key={f.label}>
              {f.label} {f.mono ? <span className="wb-mono">{f.value}</span> : <b dir="auto">{f.value}</b>}
            </span>
          ))}
        </div>
      ) : null}
      {built.sections.map((s) => (
        <div key={s.item.id}>{s.body}</div>
      ))}
      {answer ? (
        <section className="wb-sec" data-sec="raw">
          <details className="wb-raw">
            <summary>Raw JSON</summary>
            <pre className="wb-pre">{pretty}</pre>
          </details>
        </section>
      ) : null}
    </Document>
  )
}
