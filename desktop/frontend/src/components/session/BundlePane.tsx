import { useEffect, useMemo, useRef, useState } from 'react'
import ReactMarkdown from 'react-markdown'
import type { Attachment, Transport } from '../../api/types'
import { reasonOf } from '../../lib/format'
import ContextMenu from '../../ui/context-menu'
import AttachmentPreview from './AttachmentPreview'
import {
  fact,
  fileSize,
  idFromURL,
  middleEllipsis,
  parseBundle,
  previewKind,
  type Bundle,
} from './bundleModel'
import { ChevronIcon, ExternalIcon, FileGlyph, MoreIcon } from './inspectorIcons'
import './inspector.css'

/*
 * What the agent was handed, in three blocks: the ticket, the conversation
 * and the files. Playbooks are the fourth, folded.
 *
 * The rule the owner set on 2026-09-17 is that a pane shows three or four
 * things and gives them room. So the identifier is the link and there is no
 * URL beside it; the bundle path is in the pane's menu rather than in a
 * card; the thread is the last six messages with the rest one click away;
 * and an attachment is a row you can open rather than a file name you can
 * only read.
 *
 * Every piece of the customer's own text — the title, their name, what they
 * wrote — carries `dir="auto"` and `.sd-bidi`, and every identifier beside
 * it is in a `<bdi>`, so an Arabic ticket reads correctly inside an English
 * pane and an English one inside an Arabic pane.
 */

/** How many of the thread's messages the pane draws before "Show all N". */
export const MESSAGES_SHOWN = 6

export interface BundlePaneProps {
  transport: Transport
  workspaceId: string
  runId: string
  /** Who the ticket is assigned to, off the run's own record. */
  assignee?: string
  /** The helpdesk number the run recorded, when the prompt's URL has none. */
  helpdeskKey?: string
  /** Reveals the run's bundle directory; absent in a browser, which cannot. */
  onOpenFolder?: () => void
  /** Told what the prompt carried once it is read, for a tab's count or an outline. */
  onLoaded?: (bundle: Bundle | null) => void
}

function Line({ label, children }: { label: string; children: React.ReactNode }): JSX.Element {
  return (
    <p className="si-card__line">
      <span className="si-card__label">{label}</span>
      {children}
    </p>
  )
}

/** The tracker record and, under it, the helpdesk one. The key is the link. */
function TicketBlock({
  bundle,
  assignee,
  helpdeskKey,
}: {
  bundle: Bundle
  assignee?: string
  helpdeskKey?: string
}): JSX.Element | null {
  const key = fact(bundle, 'Key')
  const title = fact(bundle, 'Title')
  const priority = fact(bundle, 'Priority')
  const trackerUrl = fact(bundle, 'Tracker URL')
  const helpdeskUrl = fact(bundle, 'Helpdesk URL')
  const customer = fact(bundle, 'Customer')
  const helpdeskId = helpdeskKey || (helpdeskUrl ? idFromURL(helpdeskUrl) : '')
  const contact = bundle.thread.find((m) => m.role === 'customer')?.author ?? ''
  // The card is the prompt's ticket block. A run whose prompt has not been
  // written yet has no ticket to draw, and the run's own assignee is not
  // one on its own.
  if (bundle.facts.length === 0) return null

  return (
    <section className="si-card" aria-label="Ticket" data-sec="ticket" data-testid="bundle-ticket">
      <div className="si-card__head">
        {key ? (
          trackerUrl ? (
            <a className="si-key" href={trackerUrl} target="_blank" rel="noreferrer noopener">
              <bdi>{key}</bdi>
              <ExternalIcon />
            </a>
          ) : (
            <span className="si-key">
              <bdi>{key}</bdi>
            </span>
          )
        ) : null}
        {priority ? <span className="si-chip">{priority}</span> : null}
      </div>
      {title ? (
        <h3 className="si-card__title sd-bidi" dir="auto">
          {title}
        </h3>
      ) : null}
      {assignee ? <Line label="Assignee">{assignee}</Line> : null}
      {helpdeskId || contact || customer ? (
        <div className="si-card__sub">
          {helpdeskId ? (
            helpdeskUrl ? (
              <a className="si-key si-key--sm" href={helpdeskUrl} target="_blank" rel="noreferrer noopener">
                <bdi>#{helpdeskId}</bdi>
                <ExternalIcon />
              </a>
            ) : (
              <span className="si-key si-key--sm">
                <bdi>#{helpdeskId}</bdi>
              </span>
            )
          ) : null}
          {contact ? (
            <span className="si-card__who sd-bidi" dir="auto">
              {contact}
            </span>
          ) : null}
          {customer ? (
            <span className="si-card__who sd-bidi" dir="auto">
              {customer}
            </span>
          ) : null}
        </div>
      ) : null}
    </section>
  )
}

/** Strips the markdown a message body carries, leaving the words. */
function plain(text: string): string {
  return text
    .replace(/^\s{0,3}#{1,6}\s+/gm, '')
    .replace(/\*\*([^*]+)\*\*/g, '$1')
    .replace(/(^|[^*])\*([^*\n]+)\*/g, '$1$2')
    .replace(/`([^`]+)`/g, '$1')
    .replace(/^\s{0,3}[-*+]\s+/gm, '• ')
}

function ConversationBlock({ bundle }: { bundle: Bundle }): JSX.Element {
  const [all, setAll] = useState(false)
  const shown = all ? bundle.thread : bundle.thread.slice(-MESSAGES_SHOWN)
  const hidden = bundle.thread.length - shown.length

  return (
    <section className="si-block" aria-label="Conversation" data-sec="conversation" data-testid="bundle-conversation">
      <h3 className="si-block__h">
        Conversation
        {bundle.thread.length > 0 ? (
          <span className="si-block__n">
            <bdi>{bundle.thread.length}</bdi> {bundle.thread.length === 1 ? 'message' : 'messages'} · original language
          </span>
        ) : null}
      </h3>
      {bundle.thread.length === 0 ? (
        <p className="si-empty">The prompt carried no conversation.</p>
      ) : (
        <>
          {hidden > 0 ? (
            <button type="button" className="si-more" onClick={() => setAll(true)}>
              Show all <bdi>{bundle.thread.length}</bdi>
            </button>
          ) : null}
          <ol className="si-msgs">
            {shown.map((m, i) => (
              <li key={`${m.at}-${i}`} className="si-msg" data-role={m.role}>
                <p className="si-msg__who sd-bidi" dir="auto">
                  {m.author}
                  <span className="si-msg__at">
                    <bdi>{m.at}</bdi>
                  </span>
                </p>
                <div className="si-msg__body sd-bidi" dir="auto">
                  {plain(m.text)}
                </div>
              </li>
            ))}
          </ol>
        </>
      )}
    </section>
  )
}

function AttachmentsBlock({
  files,
  attachments,
  onOpen,
}: {
  files: string[]
  attachments: Attachment[]
  onOpen: (a: Attachment) => void
}): JSX.Element {
  // The bundle's own record is the truth about sizes and types; the prompt's
  // Files block is the fallback for a run whose bundle predates the record.
  const rows: Attachment[] =
    attachments.length > 0
      ? attachments
      : files.map((path) => ({ name: path.split('/').pop() ?? path, path, mime: '', size: 0 }))

  return (
    <section className="si-block" aria-label="Attachments" data-sec="attachments" data-testid="bundle-attachments">
      <h3 className="si-block__h">
        Attachments
        {rows.length > 0 ? (
          <span className="si-block__n">
            <bdi>{rows.length}</bdi>
          </span>
        ) : null}
      </h3>
      {rows.length === 0 ? (
        <p className="si-empty">None in this bundle.</p>
      ) : (
        <ul className="si-files">
          {rows.map((a) => {
            const kind = previewKind(a.mime, a.name)
            const meta = [fileSize(a.size), a.modified ? a.modified.slice(0, 10) : ''].filter(Boolean).join(' · ')
            return (
              <li key={a.path}>
                <button type="button" className="si-file" onClick={() => onOpen(a)} title={a.name}>
                  <span className="si-file__glyph" aria-hidden="true">
                    <FileGlyph kind={kind} />
                  </span>
                  <span className="si-file__name sd-bidi" dir="auto">
                    {middleEllipsis(a.name)}
                  </span>
                  {meta ? (
                    <span className="si-file__meta">
                      <bdi>{meta}</bdi>
                    </span>
                  ) : null}
                </button>
              </li>
            )
          })}
        </ul>
      )}
    </section>
  )
}

function PlaybooksBlock({ bundle }: { bundle: Bundle }): JSX.Element | null {
  const [open, setOpen] = useState(false)
  const [shown, setShown] = useState('')
  if (bundle.playbooks.length === 0) return null

  return (
    <section className="si-block" aria-label="Playbooks" data-sec="playbooks" data-testid="bundle-playbooks">
      <button type="button" className="si-fold" aria-expanded={open} onClick={() => setOpen((v) => !v)}>
        <ChevronIcon />
        <span>
          <bdi>{bundle.playbooks.length}</bdi> {bundle.playbooks.length === 1 ? 'playbook' : 'playbooks'} in the prompt
        </span>
      </button>
      {open ? (
        <ul className="si-pbs">
          {bundle.playbooks.map((p) => (
            <li key={p.name}>
              <button
                type="button"
                className="si-pb"
                aria-expanded={shown === p.name}
                onClick={() => setShown((v) => (v === p.name ? '' : p.name))}
              >
                <ChevronIcon />
                <bdi>{p.name}</bdi>
              </button>
              {shown === p.name ? (
                <div className="si-pb__body sd-bidi" dir="auto">
                  <ReactMarkdown>{p.body || '_Nothing but headings._'}</ReactMarkdown>
                </div>
              ) : null}
            </li>
          ))}
        </ul>
      ) : null}
    </section>
  )
}

/**
 * The Bundle pane, the same in all three layouts. It reads the prompt for
 * the ticket, the thread and the playbooks, and the bundle's own record for
 * the attachments' sizes and types.
 */
export default function BundlePane({
  transport,
  workspaceId,
  runId,
  assignee,
  helpdeskKey,
  onOpenFolder,
  onLoaded,
}: BundlePaneProps): JSX.Element {
  const [prompt, setPrompt] = useState<string | null>(null)
  const [error, setError] = useState('')
  const [attachments, setAttachments] = useState<Attachment[]>([])
  const [preview, setPreview] = useState<Attachment | null>(null)
  const [menu, setMenu] = useState(false)
  const menuButton = useRef<HTMLButtonElement | null>(null)

  useEffect(() => {
    let cancelled = false
    setPrompt(null)
    setError('')
    transport
      .prompt(workspaceId, runId)
      .then((text) => {
        if (!cancelled) setPrompt(text)
      })
      .catch((e: unknown) => {
        if (cancelled) return
        setPrompt('')
        setError(reasonOf(e))
      })
    return () => {
      cancelled = true
    }
  }, [transport, workspaceId, runId])

  useEffect(() => {
    let cancelled = false
    setAttachments([])
    // A run whose bundle has no record of its files is not an error here:
    // the prompt's Files block still names them, and the pane falls back.
    transport
      .attachments(workspaceId, runId)
      .then((list) => {
        if (!cancelled) setAttachments(list)
      })
      .catch(() => {})
    return () => {
      cancelled = true
    }
  }, [transport, workspaceId, runId])

  const bundle = useMemo(() => parseBundle(prompt ?? ''), [prompt])

  useEffect(() => {
    onLoaded?.(prompt === null ? null : bundle)
  }, [onLoaded, prompt, bundle])

  if (prompt === null) return <p className="si-empty">Reading the bundle…</p>
  if (error) return <p className="si-empty si-empty--error">{error}</p>

  return (
    <div className="si-bundle" data-testid="bundle-view">
      {onOpenFolder ? (
        <div className="si-bundle__menu">
          <button
            ref={menuButton}
            type="button"
            className="si-iconbtn"
            aria-label="Bundle options"
            aria-haspopup="menu"
            aria-expanded={menu}
            onClick={() => setMenu((v) => !v)}
          >
            <MoreIcon />
          </button>
          <ContextMenu
            open={menu}
            anchor={menuButton}
            label="Bundle options"
            items={[{ id: 'folder', label: 'Open bundle folder', onSelect: onOpenFolder }]}
            onClose={() => setMenu(false)}
          />
        </div>
      ) : null}
      <TicketBlock bundle={bundle} assignee={assignee} helpdeskKey={helpdeskKey} />
      <ConversationBlock bundle={bundle} />
      <AttachmentsBlock files={bundle.files} attachments={attachments} onOpen={setPreview} />
      <PlaybooksBlock bundle={bundle} />
      {preview ? (
        <AttachmentPreview
          attachment={preview}
          transport={transport}
          workspaceId={workspaceId}
          runId={runId}
          onClose={() => setPreview(null)}
          onOpenFolder={onOpenFolder}
        />
      ) : null}
    </div>
  )
}
