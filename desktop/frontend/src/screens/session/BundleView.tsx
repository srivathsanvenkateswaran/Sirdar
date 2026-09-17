import type { Transport } from '../../api/types'
import BundlePane from '../../components/session/BundlePane'

/*
 * The Conversation layout's Bundle tab. Everything it draws is the shared
 * pane in `components/session/BundlePane`, so the three layouts show the
 * same three blocks: the ticket, the conversation, the attachments, with
 * the playbooks folded under them.
 *
 * The bundle directory is not drawn here at all. It is in the pane's own
 * menu, which the desktop answers with "Open bundle folder" and a browser
 * with "Copy bundle path", so the menu is there either way.
 */

export type { Bundle, BundleMessage, Playbook } from '../../components/session/bundleModel'
export { parseBundle } from '../../components/session/bundleModel'

export interface BundleViewProps {
  transport: Transport
  workspaceId: string
  runId: string
  /** Who the ticket is assigned to, off the run's record. */
  assignee?: string
  /** The helpdesk number the run recorded, when the prompt's URL carries none. */
  helpdeskKey?: string
  /** Reveals the run's bundle directory; absent in a browser, which cannot. */
  onOpenFolder?: () => void
  /** Where the bundle is on disk, for the browser's copy of the same menu item. */
  folderPath?: string
  /** Told what the prompt carried once it is read, for a tab's count or an outline. */
  onLoaded?: (bundle: import('../../components/session/bundleModel').Bundle | null) => void
}

export default function BundleView(props: BundleViewProps): JSX.Element {
  return <BundlePane {...props} />
}
