import { useEffect, useState } from 'react'
import ReactMarkdown from 'react-markdown'
import type { NoteKind, Transport } from '../../api/types'
import { splitFrontmatter } from '../../lib/events'

interface Note {
  kind: NoteKind
  text: string
}

/**
 * The notes a run produced. Frontmatter is lifted into a compact key/value
 * table — those fields are the verdict, and burying them in raw YAML above the
 * prose makes the reader parse a serialisation format to find them.
 */
export default function NoteView({
  transport,
  workspaceId,
  runId,
  kinds,
}: {
  transport: Transport
  workspaceId: string
  runId: string
  kinds: NoteKind[]
}) {
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
          return { kind: kind as NoteKind, text }
        } catch {
          return { kind: kind as NoteKind, text: '' }
        }
      }),
    ).then((all) => {
      if (cancelled) return
      const found = all.filter((n) => n.text.trim() !== '')
      setNotes(found)
      if (found.length === 0) setError('No note yet. It is written when the run completes.')
    })
    return () => {
      cancelled = true
    }
  }, [transport, workspaceId, runId, wanted])

  if (notes === null) return <div className="pane pane-empty">Loading note…</div>
  if (notes.length === 0) return <div className="pane pane-empty">{error}</div>

  return (
    <div className="pane">
      {notes.map((note) => {
        const { fields, body } = splitFrontmatter(note.text)
        return (
          <article key={note.kind} className="pane-section">
            {notes.length > 1 ? <div className="pane-label">{note.kind}</div> : null}
            {fields.length > 0 ? (
              <table className="fm">
                <tbody>
                  {fields.map((f) => (
                    <tr key={f.key}>
                      <td>{f.key}</td>
                      <td>{f.value}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            ) : null}
            <div className="md">
              <ReactMarkdown>{body}</ReactMarkdown>
            </div>
          </article>
        )
      })}
    </div>
  )
}
