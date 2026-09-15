import type { ReactNode } from 'react'
import './StatCard.css'

export interface StatCardProps {
  /** What the figure counts: "Runs this week", "Spent", "Confirmed". */
  label: ReactNode
  /** Already formatted: "38", "$12.40", "71%". This component does no arithmetic. */
  value: string
  /** One line under the figure: the comparison, the period, the denominator. */
  detail?: ReactNode
  /** The figure's tooltip: the exact number behind a rounded one. */
  valueTitle?: string
}

/**
 * One big figure with a small grey label over it and one line under it. The
 * Register's three of these sit beside the heatmap.
 *
 * The figure is tabular so a column of cards lines up on the digit, and it is
 * `dir="ltr"` because a number in an Arabic window is still read left to
 * right. Everything here is a string the screen formatted: a card that did
 * its own rounding would disagree with the table under it.
 */
export default function StatCard({ label, value, detail, valueTitle }: StatCardProps): JSX.Element {
  return (
    <div className="sd-stat">
      <span className="sd-stat__label">{label}</span>
      <span className="sd-stat__value" title={valueTitle} dir="ltr">
        {value}
      </span>
      {detail && <span className="sd-stat__detail">{detail}</span>}
    </div>
  )
}
