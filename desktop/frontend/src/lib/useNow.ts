import { useCallback, useRef, useSyncExternalStore } from 'react'

/**
 * The clock the ages and elapsed counters read, as one shared tick.
 *
 * Every "4m ago" and every live run's "0:42" needs the current time and needs
 * it again a moment later. Each screen used to keep its own `now` in state
 * and a `setInterval` that set it, so the tick re-rendered the whole screen —
 * the board with every card, the session with its whole transcript — once a
 * second, to change a few digits. Here there is one interval per period,
 * started when the first component asks for it and stopped when the last
 * one leaves, and each component subscribes for itself: the components that
 * render on a tick are the leaves that print a time (`components/Age`), and
 * nothing above them.
 *
 * `active` false gives a still clock — the time when it was last read — and
 * no subscription, for a counter whose run has finished or a row that has
 * scrolled out of view. The value comes back live when it turns true again.
 */

interface Ticker {
  period: number
  listeners: Set<() => void>
  timer: ReturnType<typeof setInterval> | null
  /** The time of the last tick, or of the last read while idle. */
  now: number
}

const tickers = new Map<number, Ticker>()

function ticker(period: number): Ticker {
  let t = tickers.get(period)
  if (!t) {
    t = { period, listeners: new Set(), timer: null, now: Date.now() }
    tickers.set(period, t)
  }
  return t
}

/**
 * Registers `listener` on the shared tick of `period` ms; the return value
 * unregisters it. The interval runs only while something is listening, and
 * a ticker that was idle reads the clock again as it starts, so a component
 * mounting after a quiet spell is not told the time the spell began.
 */
export function subscribeNow(period: number, listener: () => void): () => void {
  const t = ticker(period)
  t.listeners.add(listener)
  if (!t.timer) {
    t.now = Date.now()
    t.timer = setInterval(() => {
      t.now = Date.now()
      for (const l of t.listeners) l()
    }, period)
  }
  return () => {
    t.listeners.delete(listener)
    if (t.listeners.size === 0 && t.timer) {
      clearInterval(t.timer)
      t.timer = null
    }
  }
}

/** The shared tick's reading for `period`, without subscribing to it. */
export function readNow(period: number): number {
  return ticker(period).now
}

/** How many listeners the tick of `period` has; for tests. */
export function nowListeners(period: number): number {
  return tickers.get(period)?.listeners.size ?? 0
}

const still = (): (() => void) => () => {}

/**
 * The current time, re-read every `period` ms while `active`.
 *
 * Use it in the leaf that prints the time and nowhere higher: the component
 * that calls this renders on every tick.
 */
export function useNow(period = 1000, active = true): number {
  const frozen = useRef(0)
  const subscribe = useCallback(
    (listener: () => void) => (active ? subscribeNow(period, listener) : still()),
    [period, active],
  )
  const read = () => {
    if (active) {
      frozen.current = readNow(period)
      return frozen.current
    }
    // Still: the time last read, or the time now for a clock that has never
    // run. Read once and kept, so two reads in one render agree.
    if (frozen.current === 0) frozen.current = Date.now()
    return frozen.current
  }
  return useSyncExternalStore(subscribe, read, read)
}
