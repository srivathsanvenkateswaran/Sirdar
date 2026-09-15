import type { ReactNode } from 'react'
import './HeroBand.css'

export interface HeroBandProps {
  /** The first clause, set roman. */
  headline: string
  /**
   * The second clause. Set in the display italic in Latin; Arabic has no
   * italic, so under RTL it is marked with weight instead.
   */
  headlineTail?: string
  /** One line under the headline. Not a paragraph. */
  deck?: ReactNode
  /** The one action. A band with two calls to action has none. */
  action?: ReactNode
  /** The deep indigo band or the ink one. A page has at most one ink band. */
  tone?: 'deep' | 'ink'
  /** Ambient decoration, placed beside the headline: the ring text lives here. */
  aside?: ReactNode
}

/**
 * The full-bleed colour band a page opens on.
 *
 * It runs the whole width of the window with its leading corners rounded to
 * `--sd-radius-band`, so the band reads as a card the width of the page rather
 * than as a stripe. Only band ink is allowed on it: no accent, no status hue,
 * nothing that means something elsewhere.
 *
 * The headline is one roman clause and one italic clause, which is the
 * typographic figure the whole design language is built around. Arabic has no
 * italic and a faux-slanted Naskh is a defect rather than an emphasis, so an
 * Arabic band marks its second clause with weight — 600 against 400 — and the
 * figure survives the translation.
 */
export default function HeroBand({
  headline,
  headlineTail,
  deck,
  action,
  tone = 'deep',
  aside,
}: HeroBandProps): JSX.Element {
  return (
    <section className="sd-hero" data-tone={tone}>
      <div className="sd-hero__inner">
        {aside && <div className="sd-hero__aside">{aside}</div>}
        <div className="sd-hero__content">
          <h1 className="sd-hero__headline">
            <span className="sd-hero__clause">{headline}</span>
            {headlineTail && (
              <>
                {/* A real space between the clauses, so the headline is read
                    and copied as one sentence. */}
                {' '}
                <span className="sd-hero__clause-tail">{headlineTail}</span>
              </>
            )}
          </h1>
          {deck && <p className="sd-hero__deck">{deck}</p>}
          {action && <div className="sd-hero__action">{action}</div>}
        </div>
      </div>
    </section>
  )
}
