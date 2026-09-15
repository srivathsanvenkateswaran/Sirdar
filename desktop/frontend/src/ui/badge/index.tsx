import type { ReactNode } from 'react'
import './Badge.css'

export interface BadgeProps {
  children: ReactNode
  /** Names what the fact is about, when the word alone does not say. */
  title?: string
}

/**
 * A fact about the account or the workspace: the plan, the billing mode, the
 * tracker a workspace reads from.
 *
 * It is not a run state. Run state is the Status badge, with its six hues and
 * its word, and anything that could be either is the Status badge — a chip
 * that sometimes means "pro" and sometimes means "failed" is a chip nobody can
 * read at a glance.
 *
 * Sentence case, not uppercase. A tracked-out capital label above a value is
 * the commonest piece of template chrome there is, and this badge carries a
 * word a person wrote.
 */
export default function Badge({ children, title }: BadgeProps): JSX.Element {
  return (
    <span className="sd-fact-badge" title={title}>
      {children}
    </span>
  )
}
