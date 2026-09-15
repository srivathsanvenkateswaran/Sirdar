import type { ReactNode } from 'react'
import './SidebarFooterCard.css'

export interface SidebarFooterCardProps {
  /**
   * The card's title row: "Plan usage" over the quota chips in the app. Any
   * node, so the shell can put a chevron beside the words.
   */
  title?: ReactNode
  /**
   * The workspace switcher row: a name and a chevron that opens a popover.
   * The app moved its switcher up beside the wordmark on 2026-09-15; the slot
   * stays for a shell that wants it here.
   */
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
  title,
  switcher,
  quotas,
  action,
  label = 'Workspace',
}: SidebarFooterCardProps): JSX.Element {
  return (
    <div className="sd-sidebar-foot" aria-label={label} role="group">
      {title && <div className="sd-sidebar-foot__title">{title}</div>}
      {switcher && <div className="sd-sidebar-foot__switcher">{switcher}</div>}
      {quotas && <div className="sd-sidebar-foot__quotas">{quotas}</div>}
      {action && <div className="sd-sidebar-foot__action">{action}</div>}
    </div>
  )
}
