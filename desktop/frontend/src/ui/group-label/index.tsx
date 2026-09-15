import type { ReactNode } from 'react'
import './GroupLabel.css'

export interface GroupLabelProps {
  children: ReactNode
  /** The dashed rule after the words. Off for a label inside a narrow column. */
  rule?: boolean
  /** The heading level, when the label heads a section. A `p` otherwise. */
  as?: 'p' | 'h2' | 'h3'
  id?: string
}

/**
 * A small tracked label over a group of things — "Landed today", "Workspace",
 * "This app" — with a dashed rule running out to the edge.
 *
 * It is the one tracked-capitals label in the app besides the kind chip, and
 * the dashed rule is what keeps it from being a heading: it names a group
 * rather than starting a section.
 */
export default function GroupLabel({
  children,
  rule = true,
  as: Tag = 'p',
  id,
}: GroupLabelProps): JSX.Element {
  return (
    <Tag className="sd-group-label" data-rule={rule ? 'true' : undefined} id={id}>
      {children}
    </Tag>
  )
}
