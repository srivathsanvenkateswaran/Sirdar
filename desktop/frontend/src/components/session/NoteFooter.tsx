import { useEffect, useRef, useState } from 'react'
import type { Transport } from '../../api/types'
import { reasonOf } from '../../lib/format'
import Button from '../../ui/button'
import { noteLabel } from './noteBody'
import './inspector.css'

/*
 * Where the note actually lives, at the bottom of the pane rather than at
 * the top of it.
 *
 * The pane's first line should be the note's own title. The path is a fact
 * about the file, not about the issue, so it goes in one footer row —
 * shortened to the notes directory, with the whole thing on the row's title
 * — beside the one control that matters, which opens it in whatever the
 * desktop associates with Markdown. In a browser served by `sirdar serve`
 * nothing can open a file on the operator's machine, so the row copies the
 * path instead.
 */

/** How long the control says "Copied" before it goes back to offering. */
const COPIED_MS = 2000

export interface NoteFooterProps {
  transport: Transport
  workspaceId: string
  runId: string
  /** The path the run itself recorded, which is what the bridge checks. */
  path: string
  /** The notes directory, when the caller knows it; the label is relative to it. */
  notesDir?: string
}

export default function NoteFooter({ transport, workspaceId, runId, path, notesDir }: NoteFooterProps): JSX.Element {
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
    <div className="si-notefoot" data-testid="note-footer">
      <span>In Obsidian</span>
      <span className="si-notefoot__path" title={path} dir="ltr">
        <bdi>{noteLabel(path, notesDir)}</bdi>
      </span>
      <Button variant="pale" size="sm" onClick={() => void act()} title={canOpen ? `Open ${path}` : path}>
        {canOpen ? 'Open' : copied ? 'Copied' : 'Copy path'}
      </Button>
      {failure ? (
        <span role="alert" className="si-notefoot__path">
          {failure}
        </span>
      ) : null}
    </div>
  )
}
