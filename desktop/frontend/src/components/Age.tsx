import { useEffect, useRef, useState, type HTMLAttributes } from 'react'
import { useNow } from '../lib/useNow'

export interface AgeProps extends Omit<HTMLAttributes<HTMLElement>, 'children'> {
  /**
   * The words for the time `now`: `shortAge(run.updatedAt, now)`,
   * `elapsed(detail, now)`, whatever the place calls for. Called on every
   * tick, so it should be the formatting and nothing more.
   */
  format: (now: number) => string
  /** The stamp the words are about, for the `<time>` element's `dateTime`. */
  at?: string
  /** How often the words are re-read; 1000ms for a clock, longer for an age. */
  period?: number
  /** False for a clock that has stopped: the words are read once and kept. */
  active?: boolean
  /** A still clock for tests and the gallery; nothing ticks when this is given. */
  now?: number
}

/** Whether the element is on screen; true where it cannot be asked. */
function useOnScreen(ref: React.RefObject<HTMLElement | null>): boolean {
  const [onScreen, setOnScreen] = useState(true)
  useEffect(() => {
    const el = ref.current
    if (!el || typeof IntersectionObserver !== 'function') return
    const observer = new IntersectionObserver(([entry]) => {
      if (entry) setOnScreen(entry.isIntersecting)
    })
    observer.observe(el)
    return () => observer.disconnect()
  }, [ref])
  return onScreen
}

/**
 * A time in words that keeps itself current: the age at the end of a
 * sessions row, the board's "updated 12s ago", a live run's clock.
 *
 * It is the one component that renders on the shared tick (`lib/useNow`), so
 * the screen around it does not have to. A row that has scrolled out of
 * view stops ticking until it is back; a clock told it is not `active` reads
 * the time once and holds it. With `at` it is a `<time>` carrying the stamp;
 * without one, a `<span>`.
 */
export default function Age({
  format,
  at,
  period = 1000,
  active = true,
  now,
  ...rest
}: AgeProps): JSX.Element {
  const ref = useRef<HTMLElement | null>(null)
  const onScreen = useOnScreen(ref)
  const ticking = useNow(period, now === undefined && active && onScreen)
  const text = format(now ?? ticking)
  if (at !== undefined) {
    return (
      <time ref={ref as React.RefObject<HTMLTimeElement>} dateTime={at} {...rest}>
        {text}
      </time>
    )
  }
  return (
    <span ref={ref as React.RefObject<HTMLSpanElement>} {...rest}>
      {text}
    </span>
  )
}
