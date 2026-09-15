import type { ReactNode } from 'react'
import './Banner.css'

/** The four things a banner reports, in the hue each one wears elsewhere. */
export type BannerTone = 'ok' | 'done' | 'blocked' | 'failed' | 'live'

export interface BannerProps {
  /** `ok` (the triaged hue) is the default: a step finished and nothing is wrong. */
  tone?: BannerTone
  /** The lead, in weight 600: "Tests passed", "Note filed", "The agent asked". */
  title: ReactNode
  /** The rest of the sentence, after a separator. */
  children?: ReactNode
  /** One control at the inline end, when the banner offers something to do. */
  action?: ReactNode
  /** `auto` lets an Arabic question lay itself out from its own first letter. */
  dir?: 'auto' | 'ltr' | 'rtl'
}

/**
 * The tinted line that says what just finished: tests passed, a note was
 * filed, the agent asked a question. One per transcript, for the last
 * finished step, and above the composer so the reader sees it before typing.
 *
 * The fill is a 10% tint of the tone's hue and the text is the hue itself,
 * so the banner reads at a glance and still clears 4.5:1. The lead is bold
 * and the rest is not, which is what makes it a sentence rather than a badge.
 */
export default function Banner({
  tone = 'ok',
  title,
  children,
  action,
  dir,
}: BannerProps): JSX.Element {
  return (
    <div className="sd-banner" data-tone={tone} role="status" dir={dir}>
      <span className="sd-banner__text">
        <b className="sd-banner__title">{title}</b>
        {children !== undefined && children !== null && children !== '' && (
          <>
            <span className="sd-banner__sep" aria-hidden="true">
              ·
            </span>
            <span className="sd-banner__body">{children}</span>
          </>
        )}
      </span>
      {action && <span className="sd-banner__action">{action}</span>}
    </div>
  )
}
