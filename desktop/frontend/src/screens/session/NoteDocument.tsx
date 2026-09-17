import { useEffect, useState, type ReactNode } from 'react'
import ReactMarkdown, { type Components } from 'react-markdown'
import type { NoteKind, Transport } from '../../api/types'
import { notePathFor, splitFrontmatter } from '../../lib/events'
import { reasonOf } from '../../lib/format'
import { stripRTLBlocks } from '../../lib/rtl'
import NoteFooter from '../../components/session/NoteFooter'
import { noteFromFirstHeading } from '../../components/session/noteBody'
import { Prose } from './AnswerCard'

/*
 * The note as a document: the title first, then the sections under small
 * uppercase heads, the customer's Arabic laid out right to left inside an
 * English body, and every `file:line` in the prose live — clicking one
 * scrolls the transcript to the call that read the file.
 *
 * What the pane no longer opens with is the vault's scaffolding: the YAML
 * frontmatter as a strip of chips, the `Register: [[…]] · RCA: [[…]]`
 * wikilink line, the "Working document —" callout. None of it links to
 * anything from inside the app, and it pushed the note's own title below
 * the fold. `noteFromFirstHeading` drops it; the footer row at the bottom
 * says which file this is and opens it in Obsidian.
 *
 * It reads the note through the Transport, once, and again when the run
 * finishes: a note only exists once the run has written it.
 */

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
  /** The workspace's notes directory, so the footer says the vault-relative path. */
  notesDir?: string
  onRef?: (ref: string) => void
}

export default function NoteDocument({ transport, workspaceId, runId, kinds, reload = 0, notePaths, notesDir, onRef }: NoteDocumentProps): JSX.Element {
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
        const { body } = splitFrontmatter(note.text)
        const path = notePathFor(note.kind, notePaths)
        return (
          <article key={note.kind} className="sc-doc__note" aria-label={note.kind ? `${note.kind} note` : 'note'}>
            {notes.length > 1 ? <div className="sc-doc__kind">{note.kind}</div> : null}
            <div className="sc-doc__body sd-bidi" dir="auto">
              <ReactMarkdown components={components(onRef)}>{noteFromFirstHeading(stripRTLBlocks(body))}</ReactMarkdown>
            </div>
            {path ? <NoteFooter transport={transport} workspaceId={workspaceId} runId={runId} path={path} notesDir={notesDir} /> : null}
          </article>
        )
      })}
    </div>
  )
}
