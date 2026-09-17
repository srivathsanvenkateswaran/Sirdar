import type { Transport } from '../../api/types'
import BundlePane from '../../components/session/BundlePane'

/*
 * The Conversation layout's Bundle tab. Everything it draws is the shared
 * pane in `components/session/BundlePane`, so the three layouts show the
 * same three blocks: the ticket, the conversation, the attachments, with
 * the playbooks folded under them.
 *
 * The bundle directory is not drawn here at all. It is in the pane's own
 * menu as "Open bundle folder", which the desktop shell answers and a
 * browser leaves out.
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
}

export default function BundleView(props: BundleViewProps): JSX.Element {
  return <BundlePane {...props} />
}
