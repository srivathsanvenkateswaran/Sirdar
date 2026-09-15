import type { ReactNode } from 'react'
import './SidebarNavItem.css'

export interface SidebarNavItemProps {
  /** A 16px icon, stroke 1.5, painted in `currentColor`. */
  icon?: ReactNode
  label: string
  /** The row the reader is on. Exactly one row in a sidebar carries it. */
  current?: boolean
  /**
   * A count at the inline end: inbound deliveries waiting on the Board row.
   * Zero is not shown — a badge saying nothing has arrived is noise.
   */
  count?: number
  /** Names what the count is, for a reader who cannot see which row it is on. */
  countLabel?: string
  onSelect: () => void
  /** Shown as a tooltip, and as the accessible name when the rail is collapsed. */
  title?: string
}

/**
 * One row of the app's left sidebar.
 *
 * The current row is a soft neutral pill and carries no accent and no border.
 * In Sirdar the accent means a live run, so a nav row wearing it would compete
 * with the one card on the board that has earned it — which is why the fill is
 * `--sd-nav-active` rather than `--sd-accent-soft`, and why `--sd-ink-3` is
 * never used here: on that fill it clears only 4.12:1.
 *
 * The pill does not slide between rows. Only the fill changes, over
 * `--sd-dur-1`, because a thumb travelling down a five-row list is a longer
 * animation than the click it answers.
 */
export default function SidebarNavItem({
  icon,
  label,
  current = false,
  count,
  countLabel,
  onSelect,
  title,
}: SidebarNavItemProps): JSX.Element {
  const showCount = typeof count === 'number' && count > 0
  return (
    <button
      type="button"
      className="sd-nav-row"
      aria-current={current ? 'page' : undefined}
      title={title}
      onClick={onSelect}
    >
      {icon && (
        <span className="sd-nav-row__icon" aria-hidden="true">
          {icon}
        </span>
      )}
      <span className="sd-nav-row__label">{label}</span>
      {showCount && (
        <span className="sd-nav-row__count">
          <span aria-hidden="true">{count}</span>
          {/* The same number again, with the noun a screen reader needs. */}
          <span className="sd-nav-row__count-name">
            {countLabel ? `${count} ${countLabel}` : `${count} waiting on ${label}`}
          </span>
        </span>
      )}
    </button>
  )
}
