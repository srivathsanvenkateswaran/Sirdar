import type { ReactNode } from 'react'
import './SidebarFooterCard.css'

export interface SidebarFooterCardProps {
  /** The workspace switcher row: a name and a chevron that opens a popover. */
  switcher?: ReactNode
  /** One quota chip per provider that has a budget. */
  quotas?: ReactNode
  /** The screen's one primary action, at the full width of the card. */
  action?: ReactNode
  /** Names the block for a screen reader. */
  label?: string
}

/**
 * The block pinned to the foot of the sidebar.
 *
 * The reference this is translated from puts an account card here, with a
 * trial countdown and an upgrade button. Sirdar has no commercial half, so the
 * slot is taken by the two facts a person actually needs pinned — which
 * repository the board is looking at, and how much of each provider's limit is
 * gone — and by the one action that screen can commit.
 *
 * It is `--sd-card-row` on the shell rather than a panel on a sheet: it is
 * chrome, and the sheet beside it is the work.
 */
export default function SidebarFooterCard({
  switcher,
  quotas,
  action,
  label = 'Workspace',
}: SidebarFooterCardProps): JSX.Element {
  return (
    <div className="sd-sidebar-foot" aria-label={label} role="group">
      {switcher && <div className="sd-sidebar-foot__switcher">{switcher}</div>}
      {quotas && <div className="sd-sidebar-foot__quotas">{quotas}</div>}
      {action && <div className="sd-sidebar-foot__action">{action}</div>}
    </div>
  )
}
