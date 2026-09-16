/**
 * Counts renders of a component, in tests only.
 *
 * A component that must not render on some store emit — a sessions row when
 * a toast lands, a turn of the transcript when a later turn grows — calls
 * `probeRender('Name')` at the top of its body. Under vitest the call is
 * counted and a test reads the count back; in the built app `import.meta.env`
 * is replaced at build time and the branch is dropped, so the probe costs one
 * comparison of a constant.
 */

const counts = new Map<string, number>()

/** True under vitest; false in the built app and the dev server. */
export const probing: boolean = import.meta.env.MODE === 'test'

export function probeRender(name: string): void {
  if (!probing) return
  counts.set(name, (counts.get(name) ?? 0) + 1)
}

/** How many times `name` has rendered since the last reset. */
export function renderCount(name: string): number {
  return counts.get(name) ?? 0
}

export function resetRenderCounts(): void {
  counts.clear()
}
