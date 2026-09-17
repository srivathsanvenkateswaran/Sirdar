import { useEffect, useState } from 'react'

/**
 * How long a change may be pending before the window says so.
 *
 * Under it, saying anything is worse than saying nothing: a flicker of
 * "loading" on a switch that was over in 80ms reads as a fault. Past it the
 * reader has noticed the wait, and a window that looks unchanged reads as
 * one that did not hear the click.
 */
export const SETTLE_MS = 120

/**
 * True once `pending` has held for `after` milliseconds, and false the
 * moment it clears.
 *
 * It is what keeps the previous screen painted during a switch: the new one
 * renders in the background while what the reader was looking at stays up,
 * and only a switch slow enough to notice is marked as one in progress.
 */
export function usePendingLonger(pending: boolean, after = SETTLE_MS): boolean {
  const [long, setLong] = useState(false)

  useEffect(() => {
    if (!pending) {
      setLong(false)
      return
    }
    const timer = setTimeout(() => setLong(true), after)
    return () => clearTimeout(timer)
  }, [pending, after])

  return pending && long
}
