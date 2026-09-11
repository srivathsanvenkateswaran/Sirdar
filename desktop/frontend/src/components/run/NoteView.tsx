import { useEffect, useState, useSyncExternalStore } from 'react'
import ReactMarkdown from 'react-markdown'
import type { NoteKind, Transport } from '../../api/types'
import { splitFrontmatter } from '../../lib/events'
import { noteDir, stripRTLBlocks, subscribePreferRTL } from '../../lib/rtl'



interface Note {
  kind: NoteKind
  text: string
}

/**
 * The notes a run produced. Frontmatter is lifted into a compact key/value
 * table — those fields are the verdict, and burying them in raw YAML above the
 * prose makes the reader parse a serialisation format to find them.
 *
 * A note is bilingual: an English body with the customer's Arabic complaint and
 * reply draft inside it. `dir="auto"` on the markdown container lets the
 * browser resolve each block from its own first strong character, so an Arabic
 * paragraph wraps and punctuates correctly without dragging the English around
 * it to the right. An engineer who would rather read the whole pane right to
 * left says so in Settings, and that turns the container's `dir` to "rtl".
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
  const dir = useSyncExternalStore(subscribePreferRTL, noteDir, () => 'auto' as const)


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
    <div className="pane" dir={dir} data-testid="note-pane">
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
                      <td dir="auto">{f.value}</td>

                    </tr>
                  ))}
                </tbody>
              </table>
            ) : null}
            <div className="md" dir={dir} data-testid="note-markdown">
              <ReactMarkdown>{stripRTLBlocks(body)}</ReactMarkdown>

            </div>

          </article>
        )
      })}
    </div>
  )
}
