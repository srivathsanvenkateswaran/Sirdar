import type { ReactNode } from 'react'
import './StatCard.css'

export type StatCardSize = 'default' | 'compact'

export interface StatCardProps {
  /** What the figure counts: "Runs this week", "Spend". */
  label: ReactNode
  /** Already formatted: "38", "$12.40", "71%". This component does no arithmetic. */
  value: string
  /** One line under the figure: the comparison, the period, the denominator. */
  detail?: ReactNode
  /** The figure's tooltip: the exact number behind a rounded one. */
  valueTitle?: string
  /**
   * `compact` puts the figure and its label on one line with the detail
   * under them, at about 72px tall, for a strip of stats that shares a row
   * with something else. The default stacks the three at 36px.
   */
  size?: StatCardSize
}

/**
 * One big figure with a small grey label over it and one line under it, or,
 * compact, the figure and the label on one line with the detail under them.
 * The Register's two of these sit beside the heatmap.
 *
 * The figure is tabular so a column of cards lines up on the digit, and it is
 * `dir="ltr"` because a number in an Arabic window is still read left to
 * right. Everything here is a string the screen formatted: a card that did
 * its own rounding would disagree with the table under it.
 */
export default function StatCard({
  label,
  value,
  detail,
  valueTitle,
  size = 'default',
}: StatCardProps): JSX.Element {
  return (
    <div className="sd-stat" data-size={size}>
      <span className="sd-stat__label">{label}</span>
      <span className="sd-stat__value" title={valueTitle} dir="ltr">
        {value}
      </span>
      {detail && <span className="sd-stat__detail">{detail}</span>}
    </div>
  )
}
