import type { ReactNode } from 'react'
import './PageHead.css'

export interface PageHeadProps {
  title: ReactNode
  /** One sentence under the title, in the third ink. */
  lede?: ReactNode
  /** The screen's actions, at the inline end. At most one of them is filled. */
  actions?: ReactNode
  /** The heading level. `h1` on a screen, `h2` inside a modal. */
  level?: 1 | 2
  /** Set the title in the display serif: the settings page heading only. */
  serif?: boolean
  id?: string
}

/**
 * The top of a screen: its name at 32px, an optional line under it, and its
 * actions at the inline end.
 *
 * The title is the UI face, not the serif, on every screen but Settings —
 * the reference app keeps its serif for the settings heading and note
 * titles, and so does Sirdar. There is no rule under it: the content below
 * starts at its own margin.
 */
export default function PageHead({
  title,
  lede,
  actions,
  level = 1,
  serif = false,
  id,
}: PageHeadProps): JSX.Element {
  const Heading = level === 1 ? 'h1' : 'h2'
  return (
    <div className="sd-page-head">
      <div className="sd-page-head__text">
        <Heading className="sd-page-head__title" data-serif={serif ? 'true' : undefined} id={id}>
          {title}
        </Heading>
        {lede && <p className="sd-page-head__lede">{lede}</p>}
      </div>
      {actions && <div className="sd-page-head__actions">{actions}</div>}
    </div>
  )
}
