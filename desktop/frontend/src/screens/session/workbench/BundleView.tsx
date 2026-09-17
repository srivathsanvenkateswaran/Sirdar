import { useMemo, useState, type JSX } from 'react'
import type { Transport } from '../../../api/types'
import BundlePane from '../../../components/session/BundlePane'
import type { Bundle } from '../../../components/session/bundleModel'
import Document, { type OutlineItem } from './Document'

/**
 * The Workbench's Bundle page: the shared pane inside this layout's
 * document shell, so the outline rail lists the same blocks the
 * Conversation and Document layouts draw — the ticket, the conversation,
 * the attachments, the playbooks — and nothing else.
 *
 * What it used to draw in their place was the tracker's record and the
 * helpdesk's as two facts tables, the whole thread with a translation under
 * every customer message, the bundle path, and a list of every other fact
 * the prompt carried. Read at pane width that is a wall; read at document
 * width it is still four columns of things nobody opened the tab for.
 */
export interface BundleViewProps {
  transport: Transport
  workspaceId: string
  runId: string
  /** Who the ticket is assigned to, off the run's record. */
  assignee?: string
  /** The helpdesk's number, when the run summary carries it. */
  helpdeskKey?: string
  /** Reveals the run's bundle directory; absent in a browser, which cannot. */
  onOpenFolder?: () => void
  /** Called with the bundle once the prompt is read, for the tab's count. */
  onLoaded?: (doc: Bundle | null) => void
}

export default function BundleView({
  transport,
  workspaceId,
  runId,
  assignee,
  helpdeskKey,
  onOpenFolder,
  onLoaded,
}: BundleViewProps): JSX.Element {
  const [bundle, setBundle] = useState<Bundle | null>(null)

  const onRead = useMemo(
    () => (doc: Bundle | null) => {
      setBundle(doc)
      onLoaded?.(doc)
    },
    [onLoaded],
  )

  const outline: OutlineItem[] = useMemo(() => {
    if (!bundle) return [{ id: 'ticket', title: 'Ticket' }]
    const items: OutlineItem[] = []
    if (bundle.facts.length > 0) items.push({ id: 'ticket', title: 'Ticket' })
    items.push({ id: 'conversation', title: 'Conversation', n: String(bundle.thread.length) })
    items.push({ id: 'attachments', title: 'Attachments', n: String(bundle.files.length) })
    if (bundle.playbooks.length > 0) items.push({ id: 'playbooks', title: 'Playbooks', n: String(bundle.playbooks.length) })
    return items
  }, [bundle])

  return (
    <Document outline={outline} label="Bundle" padTop="tight">
      <BundlePane
        transport={transport}
        workspaceId={workspaceId}
        runId={runId}
        assignee={assignee}
        helpdeskKey={helpdeskKey}
        onOpenFolder={onOpenFolder}
        onLoaded={onRead}
      />
    </Document>
  )
}
