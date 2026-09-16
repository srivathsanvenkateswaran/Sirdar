import { useEffect, useRef, useState, useSyncExternalStore } from 'react'
import ReactMarkdown from 'react-markdown'
import type { NoteKind, Transport } from '../../api/types'
import { baseName, notePathFor, splitFrontmatter } from '../../lib/events'
import { reasonOf } from '../../lib/format'
import { noteDir, stripRTLBlocks, subscribePreferRTL } from '../../lib/rtl'
import Button from '../../ui/button'
import NotePane from '../../ui/note-pane'

interface Note {
  kind: NoteKind
  text: string
}

/** How long the copy control says "Copied" before it goes back to offering. */
const COPIED_MS = 2000

/**
 * The one control over a note: open the file. On the desktop the bridge
 * hands the path to the machine's opener, which for a `.md` in a vault is
 * Obsidian; in a browser nothing can open a file on the operator's machine,
 * so the button copies the path and says so. The path is one the run
 * recorded, never one the screen made up, which is also what the bridge
 * checks before it opens anything.
 */
function NoteOpen({
  transport,
  workspaceId,
  runId,
  path,
}: {
  transport: Transport
  workspaceId: string
  runId: string
  path: string
}) {
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
    <div className="note-open" dir="ltr">
      <Button
        variant="pale"
        size="sm"
        onClick={() => void act()}
        title={canOpen ? `Open ${path} in the app your desktop associates with Markdown` : path}
      >
        {canOpen ? 'Open file' : copied ? 'Copied' : 'Copy path'}
      </Button>
      <span className="note-open__name">{baseName(path)}</span>
      {failure ? (
        <span className="note-open__failure" role="alert">
          {failure}
        </span>
      ) : null}
    </div>
  )
}

/**
 * A note the run has not written is answered with a not-found, and for an
 * RCA run asking for both of its notes, one of the two missing is the
 * ordinary case. Anything else the service says is a reason the reader
 * should see, not a note that does not exist yet.
 */
function isMissing(err: unknown): boolean {
  return /^not_found:|^404\b|\bnot found\b|no such/i.test(reasonOf(err))
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
  reload = 0,
  notePaths,
}: {
  transport: Transport
  workspaceId: string
  runId: string
  kinds: NoteKind[]
  /**
   * Changes when the run finishes. A note only exists once the run has
   * written it, so a screen opened mid-run asks again rather than leaving
   * "No note yet" up for a run that has one.
   */
  reload?: number
  /** The paths the run recorded for its notes, so each can be opened. */
  notePaths?: string[]
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

  // The pane is the scroll container: the library's note article sets a
  // measure and a face but never a height, and a note is longer than the
  // window as a rule.
  if (notes === null) {
    return (
      <div className="pane pane--note">
        <div className="pane-empty">Loading note…</div>
      </div>
    )
  }
  if (notes.length === 0) {
    return (
      <div className="pane pane--note">
        <div className="pane-empty">{error}</div>
      </div>
    )
  }

  return (
    <div className="pane pane--note" data-testid="note-pane">
      <NotePane dir={dir}>
        {notes.map((note) => {
          const { fields, body } = splitFrontmatter(note.text)
          const path = notePathFor(note.kind, notePaths)
          return (
            <section key={note.kind} className="pane-section">
              {notes.length > 1 ? <div className="pane-label">{note.kind}</div> : null}
              {path ? (
                <NoteOpen transport={transport} workspaceId={workspaceId} runId={runId} path={path} />
              ) : null}
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
            </section>
          )
        })}
      </NotePane>
    </div>
  )
}
