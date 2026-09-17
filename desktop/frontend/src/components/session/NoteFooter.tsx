import type { Transport } from '../../api/types'
import { usePathAction } from '../../lib/pathAction'
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
  const note = usePathAction({
    path,
    open: transport.openNote ? () => transport.openNote!(workspaceId, runId, path) : undefined,
    openLabel: 'Open',
    copyLabel: 'Copy path',
  })

  return (
    <div className="si-notefoot" data-testid="note-footer">
      <span>In Obsidian</span>
      <span className="si-notefoot__path" title={path} dir="ltr">
        <bdi>{noteLabel(path, notesDir)}</bdi>
      </span>
      <Button variant="pale" size="sm" onClick={note.run} title={note.canOpen ? `Open ${path}` : path}>
        {note.label}
      </Button>
      {note.failure ? (
        <span role="alert" className="si-notefoot__path">
          {note.failure}
        </span>
      ) : null}
    </div>
  )
}
