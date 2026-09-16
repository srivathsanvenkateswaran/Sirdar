import { useEffect, useRef, useState, type ReactNode } from 'react'
import ReactMarkdown, { type Components } from 'react-markdown'
import type { NoteKind, Transport } from '../../api/types'
import { baseName, notePathFor, splitFrontmatter, type Frontmatter } from '../../lib/events'
import { reasonOf } from '../../lib/format'
import { stripRTLBlocks } from '../../lib/rtl'
import Button from '../../ui/button'
import { Prose } from './AnswerCard'
import { ExternalIcon } from './icons'

/*
 * The note as a document: the frontmatter as a strip of chips, the title in
 * the display serif, the sections under small uppercase heads, the
 * customer's Arabic laid out right to left inside an English body, and
 * every `file:line` in the prose live — clicking one scrolls the transcript
 * to the call that read the file.
 *
 * It reads the note through the Transport, once, and again when the run
 * finishes: a note only exists once the run has written it.
 */

/** How long the copy control says "Copied" before it goes back to offering. */
const COPIED_MS = 2000

export interface Chip {
  key: string
  value: string
  url?: string
  /** A tag from the `tags` list: drawn in the highlight. */
  tag?: boolean
}

/**
 * The frontmatter as chips. `tags: ["a", "b"]` becomes one chip per tag;
 * a `*_url` field attaches to the `*_key` or `*_id` beside it as its link
 * rather than taking a chip of its own; everything else is key and value.
 */
export function chipsOf(fields: Frontmatter['fields']): Chip[] {
  const out: Chip[] = []
  const urls = new Map<string, string>()
  for (const f of fields) {
    const m = /^(.*)_url$/.exec(f.key)
    if (m) urls.set(m[1], f.value)
  }
  for (const f of fields) {
    if (/_url$/.test(f.key)) continue
    if (f.key === 'tags') {
      const inner = f.value.replace(/^\[|\]$/g, '')
      for (const tag of inner.split(',')) {
        const clean = tag.trim().replace(/^["']|["']$/g, '')
        if (clean) out.push({ key: 'tag', value: clean, tag: true })
      }
      continue
    }
    // `tracker_key` with a `tracker_url` beside it is one chip, "tracker",
    // linked; `customer_id` with no url is the customer's id, "id".
    const m = /^(.*)_(?:key|id)$/.exec(f.key)
    const url = m ? urls.get(m[1]) : undefined
    const key = m && url ? m[1] : /_id$/.test(f.key) ? 'id' : f.key
    out.push({ key, value: f.value, url })
  }
  // A url with no key beside it still gets its chip.
  for (const [stem, url] of urls) {
    if (!fields.some((f) => f.key === `${stem}_key` || f.key === `${stem}_id`)) {
      out.push({ key: stem, value: url.replace(/^https?:\/\//, ''), url })
    }
  }
  return out
}

/** Text children with their `file:line` references live; other nodes as they are. */
function withRefs(children: ReactNode, onRef?: (ref: string) => void): ReactNode {
  if (typeof children === 'string') return <Prose text={children} onRef={onRef} />
  if (Array.isArray(children)) {
    return children.map((child, i) => (typeof child === 'string' ? <Prose key={i} text={child} onRef={onRef} /> : child))
  }
  return children
}

function components(onRef?: (ref: string) => void): Components {
  return {
    h1: ({ children }) => <h1 dir="auto">{children}</h1>,
    h2: ({ children }) => <h2 dir="auto">{children}</h2>,
    h3: ({ children }) => <h3 dir="auto">{children}</h3>,
    p: ({ children }) => <p dir="auto">{withRefs(children, onRef)}</p>,
    li: ({ children }) => <li dir="auto">{withRefs(children, onRef)}</li>,
    blockquote: ({ children }) => <blockquote dir="auto">{children}</blockquote>,
  }
}

/**
 * Open the file, or copy its path where nothing can open one. The path is
 * one the run recorded, which is what the bridge checks before it opens
 * anything.
 */
function NoteOpen({ transport, workspaceId, runId, path }: { transport: Transport; workspaceId: string; runId: string; path: string }): JSX.Element {
  const [copied, setCopied] = useState(false)
  const [failure, setFailure] = useState('')
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null)
  useEffect(
    () => () => {
      if (timer.current) clearTimeout(timer.current)
    },
    [],
  )
  const canOpen = Boolean(transport.openNote)

  async function act(): Promise<void> {
    setFailure('')
    if (canOpen) {
      try {
        await transport.openNote!(workspaceId, runId, path)
      } catch (err) {
        setFailure(reasonOf(err))
      }
      return
    }
    try {
      await navigator.clipboard?.writeText(path)
      setCopied(true)
      if (timer.current) clearTimeout(timer.current)
      timer.current = setTimeout(() => setCopied(false), COPIED_MS)
    } catch {
      setFailure('The path could not be copied')
    }
  }

  return (
    <div className="sc-doc__open" dir="ltr">
      <Button variant="pale" size="sm" onClick={() => void act()} title={canOpen ? `Open ${path} in the app your desktop associates with Markdown` : path}>
        {canOpen ? 'Open file' : copied ? 'Copied' : 'Copy path'}
      </Button>
      <span className="sc-doc__name">{baseName(path)}</span>
      {failure ? (
        <span className="sc-doc__failure" role="alert">
          {failure}
        </span>
      ) : null}
    </div>
  )
}

function isMissing(err: unknown): boolean {
  return /^not_found:|^404\b|\bnot found\b|no such/i.test(reasonOf(err))
}

interface Note {
  kind: NoteKind
  text: string
}

export interface NoteDocumentProps {
  transport: Transport
  workspaceId: string
  runId: string
  kinds: NoteKind[]
  /** Bumped when the run finishes, so the note written at the end is read. */
  reload?: number
  notePaths?: string[]
  onRef?: (ref: string) => void
}

export default function NoteDocument({ transport, workspaceId, runId, kinds, reload = 0, notePaths, onRef }: NoteDocumentProps): JSX.Element {
  const [notes, setNotes] = useState<Note[] | null>(null)
  const [error, setError] = useState('')
  const wanted = kinds.join(',')

  useEffect(() => {
    let cancelled = false
    setNotes(null)
    setError('')
    Promise.all(
      wanted.split(',').map(async (kind) => {
        try {
          const text = await transport.note(workspaceId, runId, kind as NoteKind)
          return { kind: kind as NoteKind, text, failed: '' }
        } catch (err: unknown) {
          return { kind: kind as NoteKind, text: '', failed: isMissing(err) ? '' : reasonOf(err) }
        }
      }),
    ).then((all) => {
      if (cancelled) return
      const found = all.filter((n) => n.text.trim() !== '').map(({ kind, text }) => ({ kind, text }))
      setNotes(found)
      if (found.length > 0) return
      const failed = all.find((n) => n.failed !== '')
      setError(failed ? failed.failed : 'No note yet. It is written when the run completes.')
    })
    return () => {
      cancelled = true
    }
  }, [transport, workspaceId, runId, wanted, reload])

  if (notes === null) return <div className="sc-pane-empty">Loading note…</div>
  if (notes.length === 0) return <div className="sc-pane-empty">{error}</div>

  return (
    <div className="sc-doc" data-testid="note-document">
      {notes.map((note) => {
        const { fields, body } = splitFrontmatter(note.text)
        const path = notePathFor(note.kind, notePaths)
        const chips = chipsOf(fields)
        return (
          <article key={note.kind} className="sc-doc__note" aria-label={note.kind ? `${note.kind} note` : 'note'}>
            {notes.length > 1 ? <div className="sc-doc__kind">{note.kind}</div> : null}
            {chips.length > 0 ? (
              <div className="sc-fm" role="list" aria-label="Note metadata">
                {chips.map((c, i) => (
                  <span key={i} role="listitem" className="sc-fm__k" data-tag={c.tag ? 'true' : undefined} dir="auto">
                    {c.tag ? null : <i>{c.key}</i>}
                    {c.url ? (
                      <a href={c.url} target="_blank" rel="noreferrer noopener" dir="ltr">
                        {c.value} <ExternalIcon />
                      </a>
                    ) : (
                      c.value
                    )}
                  </span>
                ))}
              </div>
            ) : null}
            {path ? <NoteOpen transport={transport} workspaceId={workspaceId} runId={runId} path={path} /> : null}
            <div className="sc-doc__body" dir="auto">
              <ReactMarkdown components={components(onRef)}>{stripRTLBlocks(body)}</ReactMarkdown>
            </div>
          </article>
        )
      })}
    </div>
  )
}
