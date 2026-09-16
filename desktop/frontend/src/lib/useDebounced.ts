import { useEffect, useState } from 'react'

/** How long the board's filter waits after a keystroke before it narrows the lanes. */
export const FILTER_DEBOUNCE_MS = 80

/**
 * `value`, a beat behind: the last value that has held still for `ms`.
 *
 * For a filter field the input itself shows every keystroke at once, and the
 * narrowing — which walks every card — waits for the typist to pause. An
 * empty value is taken at once, so clearing the field is not a beat late.
 */
export function useDebounced<T>(value: T, ms: number): T {
  const [settled, setSettled] = useState(value)
  useEffect(() => {
    if (value === '' || value === undefined || value === null) {
      setSettled(value)
      return
    }
    const id = setTimeout(() => setSettled(value), ms)
    return () => clearTimeout(id)
  }, [value, ms])
  return value === '' ? value : settled
}
