import { useEffect, useMemo, useState, useSyncExternalStore, type JSX } from 'react'
import ReactMarkdown from 'react-markdown'
import type { NoteKind, Transport } from '../../../api/types'
import { notePathFor, splitFrontmatter } from '../../../lib/events'
import { reasonOf } from '../../../lib/format'
import { noteName } from '../../../lib/review'
import { noteDir, stripRTLBlocks, subscribePreferRTL } from '../../../lib/rtl'
import Button from '../../../ui/button'
import { chipsOf } from '../NoteDocument'
import Document, { type OutlineItem } from './Document'

/**
 * The filed note as a page: the frontmatter as a facts strip, the title in
 * the display face, and the body split at its `##` headings into sections
 * the outline lists. The complaint's Arabic block keeps its direction; the
 * reply draft gets a Copy.
 *
 * A local adapter with the shared `NoteDocument`'s name. It reads the note
 * through the transport the way `components/run/NoteView` does — a missing
 * note is the ordinary case while a run is working, and says so.
 */
export interface NoteDocumentProps {
  transport: Transport
  workspaceId: string
  runId: string
  kinds: NoteKind[]
  reload?: number
  notePaths?: string[]
  notesDir?: string
  /** Called with the number of notes found, for the tab's count. */
  onLoaded?: (count: number) => void
}

interface NoteSection {
  id: string
  title: string
  body: string
}

function isMissing(err: unknown): boolean {
  return /^not_found:|^404\b|\bnot found\b|no such/i.test(reasonOf(err))
}

function slug(text: string): string {
  return text
    .toLowerCase()
    .replace(/[^a-z0-9؀-ۿ]+/g, '-')
    .replace(/^-|-$/g, '')
}

/** Splits the body at `## ` headings; the `# ` title comes off the top. */
export function noteSections(body: string): { title: string; sections: NoteSection[] } {
  const lines = body.split('\n')
  let title = ''
  const sections: NoteSection[] = []
  let current: NoteSection = { id: 'intro', title: 'Introduction', body: '' }
  let fenced = false
  for (const line of lines) {
    if (line.trim().startsWith('```')) fenced = !fenced
    if (!fenced && /^# \S/.test(line) && !title) {
      title = line.slice(2).trim()
      continue
    }
    if (!fenced && /^## \S/.test(line)) {
      if (current.body.trim()) sections.push(current)
      const heading = line.slice(3).trim()
      current = { id: slug(heading) || `s${sections.length}`, title: heading, body: '' }
      continue
    }
    current.body += line + '\n'
  }
  if (current.body.trim()) sections.push(current)
  return { title, sections }
}

function CopyText({ text }: { text: string }): JSX.Element {
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
      aria-label="Copy this section"
    >
      {copied ? 'Copied' : 'Copy'}
    </Button>
  )
}

export default function NoteDocument({
  transport,
  workspaceId,
  runId,
  kinds,
  reload = 0,
  notePaths,
  notesDir,
  onLoaded,
}: NoteDocumentProps): JSX.Element {
  const [notes, setNotes] = useState<{ kind: NoteKind; text: string }[] | null>(null)
  const [error, setError] = useState('')
  const [copiedPath, setCopiedPath] = useState(false)
  const wanted = kinds.join(',')
  const dir = useSyncExternalStore(subscribePreferRTL, noteDir, () => 'auto' as const)

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
      onLoaded?.(found.length)
      if (found.length > 0) return
      const failed = all.find((n) => n.failed !== '')
      setError(failed ? failed.failed : 'No note yet. It is written when the run completes.')
    })
    return () => {
      cancelled = true
    }
  }, [transport, workspaceId, runId, wanted, reload, onLoaded])

  const parsed = useMemo(
    () =>
      (notes ?? []).map((note) => {
        const { fields, body } = splitFrontmatter(note.text)
        const { title, sections } = noteSections(stripRTLBlocks(body))
        return { kind: note.kind, chips: chipsOf(fields), title, sections, path: notePathFor(note.kind, notePaths) }
      }),
    [notes, notePaths],
  )

  const outline = useMemo<OutlineItem[]>(() => {
    const out: OutlineItem[] = []
    for (const note of parsed) {
      if (parsed.length > 1) out.push({ id: `note-${note.kind}`, title: note.kind || 'note' })
      for (const s of note.sections) {
        const items = (s.body.match(/^\s*(?:[-*]|\d+\.)\s/gm) ?? []).length
        out.push({ id: `${note.kind}-${s.id}`, title: s.title, n: items > 1 ? String(items) : undefined, sub: parsed.length > 1 })
      }
    }
    return out
  }, [parsed])

  if (notes === null) {
    return (
      <Document outline={[]} label="Note">
        <p className="wb-empty">Loading note…</p>
      </Document>
    )
  }
  if (notes.length === 0) {
    return (
      <Document outline={[]} label="Note">
        <p className="wb-empty">{error}</p>
      </Document>
    )
  }

  return (
    <Document outline={outline} label="Note">
      {parsed.map((note) => (
        <article key={note.kind} className="wb-note" dir={dir}>
          {parsed.length > 1 ? (
            <div className="wb-sec-h" data-sec={`note-${note.kind}`}>
              {note.kind}
            </div>
          ) : null}
          {note.title ? (
            <h2 className="wb-doc-h wb-doc-h--serif" dir="auto">
              {note.title}
            </h2>
          ) : null}
          {note.chips.length > 0 || note.path ? (
            <div className="wb-facts">
              {note.chips.map((c, i) => (
                <span key={`${c.key}-${i}`}>
                  {c.key}{' '}
                  {c.url ? (
                    <a href={c.url} target="_blank" rel="noreferrer" dir="auto">
                      {c.value}
                    </a>
                  ) : (
                    <b dir="auto">{c.value}</b>
                  )}
                </span>
              ))}
              {note.path ? (
                <span className="wb-facts__act">
                  <Button
                    variant="pale"
                    size="sm"
                    title={transport.openNote ? `Open ${note.path}` : note.path}
                    onClick={() => {
                      if (transport.openNote) {
                        void transport.openNote(workspaceId, runId, note.path).catch(() => {})
                        return
                      }
                      void navigator.clipboard?.writeText(note.path).then(
                        () => {
                          setCopiedPath(true)
                          setTimeout(() => setCopiedPath(false), 2000)
                        },
                        () => {},
                      )
                    }}
                  >
                    {transport.openNote ? 'Open file' : copiedPath ? 'Copied' : 'Copy path'}
                  </Button>
                  <span className="wb-mono">{noteName(note.path, notesDir)}</span>
                </span>
              ) : null}
            </div>
          ) : null}
          {note.sections.map((s) => (
            <section key={s.id} className="wb-sec" data-sec={`${note.kind}-${s.id}`}>
              <div className="wb-sec-h">
                {s.title}
                {/reply draft/i.test(s.title) ? <CopyText text={s.body.trim()} /> : null}
              </div>
              <div className="md wb-md" dir="auto">
                <ReactMarkdown>{s.body}</ReactMarkdown>
              </div>
            </section>
          ))}
        </article>
      ))}
    </Document>
  )
}
