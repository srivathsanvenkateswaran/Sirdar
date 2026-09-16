import type { MouseEvent, ReactNode } from 'react'
import type { RunDetail } from '../../api/types'
import type { Marker as MarkerModel } from '../../lib/evidence'
import { usd } from '../../lib/format'
import { field, rtlBlock, type NoteModel } from '../../lib/note'
import { stripRTLBlocks } from '../../lib/rtl'
import StatusBadge from '../../ui/status-badge'
import EvidenceList from './Evidence'
import type { SessionStep } from './model'
import { inline, Markdown, RefProse, ReplyLetter, Section, type MarkerHandlers } from './Prose'

export interface NoteDocumentProps {
  note: NoteModel
  detail: RunDetail
  /** The vault path the run recorded, for the sub line. */
  notePath?: string
  markers: MarkerModel[]
  hotMarker?: string
  onMarker?: (id: string, event: MouseEvent<HTMLElement>) => void
  steps: SessionStep[]
  onGoToStep?: (index: number) => void
  /** "written by the agent at 03:13 after your steer" */
  writtenAt?: string
  afterSteer?: boolean
  /** The ticket's title, for the sub line. */
  title?: string
  /** After Copy on the letter: Open desk. */
  replyActions?: ReactNode
  /** The customer's contact, from the bundle, for the metadata strip and the letter. */
  contact?: string
}

/** One cell of the metadata strip. */
export function MetaCell({ k, v, mono, span2 }: { k: string; v: ReactNode; mono?: boolean; span2?: boolean }): JSX.Element {
  return (
    <div className={span2 ? 'sn-meta__sp2' : undefined}>
      <div className="sn-meta__k">{k}</div>
      <div className="sn-meta__v" data-mono={mono ? 'true' : undefined} dir="auto">
        {v}
      </div>
    </div>
  )
}

/**
 * The note as a document: a metadata strip from the frontmatter, the title
 * in the display serif, then the sections the template writes — root cause
 * with `file:line` references carrying their evidence markers, the
 * complaint in Arabic beside its translation, the evidence as callouts,
 * proposed fix, blast radius, repro, conversation, open questions and the
 * reply draft as a letter with Copy. A section the template does not name
 * is rendered as markdown, so an RCA note reads whole here too.
 */
export default function NoteDocument({
  note,
  detail,
  notePath,
  markers,
  hotMarker,
  onMarker,
  steps,
  onGoToStep,
  writtenAt,
  afterSteer,
  title,
  replyActions,
  contact,
}: NoteDocumentProps): JSX.Element {
  const handlers: MarkerHandlers = { markers, hotMarker, onMarker }
  const rc = note.rootCause
  const customer = field(note, 'customer')
  const helpdesk = field(note, 'helpdesk_id')
  const tracker = field(note, 'tracker_key') || detail.key
  const priority = field(note, 'priority')
  const service = field(note, 'service')
  const status = field(note, 'status')
  const date = field(note, 'date')
  const reported = note.complaint?.translated[0]?.when ?? ''
  const turns = detail.usage?.turns ?? 0
  const complaint = note.complaint
  const drawn = new Set<string>()
  const mark = (role: string) => drawn.add(role)

  return (
    <article className="sn-note" data-testid="note-document">
      <div className="sn-meta" data-cols="3">
        {customer ? <MetaCell k="Customer" v={customer} /> : null}
        {helpdesk ? <MetaCell k="Helpdesk" v={[helpdesk, contact].filter(Boolean).join(' · ')} mono /> : null}
        <MetaCell k="Tracker" v={[tracker, priority].filter(Boolean).join(' · ')} mono />
        {service ? <MetaCell k="Service" v={service} mono /> : null}
        {rc?.classification ? (
          <MetaCell
            k="Classification"
            v={
              <>
                <StatusBadge status="completed">{rc.classification}</StatusBadge>
                {rc.confidence ? ` confidence ${rc.confidence}` : ''}
              </>
            }
          />
        ) : null}
        {status || date ? <MetaCell k="Status" v={[status, date].filter(Boolean).join(' · ')} /> : null}
        {reported ? <MetaCell k="Reported" v={reported.replace(/,.*$/, '')} mono /> : null}
        <MetaCell
          k="Run"
          v={[detail.runId, detail.provider, `${turns} ${turns === 1 ? 'turn' : 'turns'}`, detail.usage?.costUsd ? usd(detail.usage.costUsd) : '']
            .filter(Boolean)
            .join(' · ')}
          mono
          span2
        />
      </div>

      <h1 className="sn-note__h1" dir="auto">
        {note.title || 'Untitled note'}
      </h1>
      <div className="sn-note__sub">
        <span>
          {detail.kind === 'rca' ? 'RCA note' : 'Triage note'}
          {writtenAt ? ` · written by the agent at ${writtenAt}${afterSteer ? ' after your steer' : ''}` : ''}
        </span>
        {notePath ? (
          <>
            <span>·</span>
            <span className="sn-mono" dir="ltr">
              {notePath}
            </span>
          </>
        ) : null}
        {title ? (
          <>
            <span>·</span>
            <span dir="auto">Ticket: {title}</span>
          </>
        ) : null}
      </div>

      {rc ? (
        <Section title="Root cause" tag={[rc.classification, rc.confidence ? `confidence ${rc.confidence}` : ''].filter(Boolean).join(' · ') || undefined}>
          <RefProse text={rc.body} {...handlers} />
        </Section>
      ) : null}
      {rc && mark('root-cause')}

      {complaint && (complaint.translated.length > 0 || complaint.original.length > 0) ? (
        <Section title="Customer complaint" tag={complaint.original.length > 0 ? 'translated · original' : 'translated'} id="complaint">
          <div className="sn-complaint">
            <div className="sn-complaint__en">
              {complaint.translated.map((p, i) => (
                <div key={i}>
                  {p.when ? <span className="sn-complaint__when">{p.when}</span> : null}
                  <p>{p.text}</p>
                </div>
              ))}
            </div>
            {complaint.original.length > 0 ? (
              <div className="sn-complaint__ar" dir="rtl">
                {complaint.original.map((p, i) => (
                  <div key={i}>
                    {p.when ? <span className="sn-complaint__when">{p.when}</span> : null}
                    <p lang="ar">{p.text}</p>
                  </div>
                ))}
              </div>
            ) : null}
          </div>
          {complaint.tone ? <div className="sn-tone">{complaint.tone}</div> : null}
        </Section>
      ) : null}
      {complaint && (mark('complaint'), mark('complaint-original'))}

      {rc && rc.evidence.length > 0 ? (
        <Section title="Evidence" tag={`${rc.evidence.length} items · each traces to a step on the path`}>
          <EvidenceList items={rc.evidence} markers={markers} hotMarker={hotMarker} onMarker={onMarker} steps={steps} onGoToStep={onGoToStep} />
        </Section>
      ) : null}

      {note.proposedFix ? (
        <Section title="Proposed fix" tag={note.proposedFix.files.replace(/,\s*/g, ' · ') || undefined}>
          <RefProse text={note.proposedFix.body} {...handlers} />
          {note.proposedFix.remediationSql ? (
            <p className="sn-p">
              <b>Remediation SQL:</b> {inline(note.proposedFix.remediationSql, handlers)}
            </p>
          ) : null}
          {note.proposedFix.risks ? (
            <p className="sn-p">
              <b>Risks:</b> {inline(note.proposedFix.risks, handlers)}
            </p>
          ) : null}
        </Section>
      ) : null}
      {note.proposedFix && mark('proposed-fix')}

      {rc?.blastRadius ? (
        <Section title="Blast radius">
          <RefProse text={rc.blastRadius} {...handlers} />
        </Section>
      ) : null}

      {note.sections
        .filter((s) => !drawn.has(s.role) && s.role !== 'reply')
        .map((s) => (
          <Section key={s.id} title={s.title.replace(/\s*\(.*?\)\s*$/, '')} id={s.id} tag={s.role === 'repro' ? 'traced through the code' : undefined}>
            <Markdown text={stripRTLBlocks(s.body)} {...handlers} />
          </Section>
        ))}

      {note.reply ? (
        <Section title="Reply to the customer" tag={`draft · ${note.reply.language || 'language unknown'} · not sent`}>
          <ReplyLetter
            language={note.reply.language}
            text={rtlBlock(note.reply.text)}
            note={note.reply.note}
            to={[contact, helpdesk ? `desk ${helpdesk}` : ''].filter(Boolean).join(' · ') || undefined}
            actions={replyActions}
          />
        </Section>
      ) : null}
    </article>
  )
}
