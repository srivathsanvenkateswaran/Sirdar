/**
 * A `matchMedia` for jsdom, which has none: the queries listed match, every
 * other query does not, and a test can flip one and fire its listeners to
 * play a window being resized. Test-only; `useMediaQuery` is what reads it.
 */
export function stubMatchMedia(matching: string[] = []): {
  set: (query: string, matches: boolean) => void
  restore: () => void
} {
  const state = new Map<string, boolean>(matching.map((q) => [q, true]))
  const listeners = new Map<string, Set<(e: MediaQueryListEvent) => void>>()
  const before = globalThis.matchMedia

  function matchMedia(query: string): MediaQueryList {
    const own = listeners.get(query) ?? new Set()
    listeners.set(query, own)
    return {
      get matches() {
        return state.get(query) ?? false
      },
      media: query,
      onchange: null,
      addEventListener: (_type: string, listener: EventListenerOrEventListenerObject) => {
        own.add(listener as (e: MediaQueryListEvent) => void)
      },
      removeEventListener: (_type: string, listener: EventListenerOrEventListenerObject) => {
        own.delete(listener as (e: MediaQueryListEvent) => void)
      },
      addListener: () => {},
      removeListener: () => {},
      dispatchEvent: () => false,
    } as MediaQueryList
  }

  Object.defineProperty(globalThis, 'matchMedia', {
    configurable: true,
    writable: true,
    value: matchMedia,
  })

  return {
    set(query, matches) {
      state.set(query, matches)
      for (const listener of listeners.get(query) ?? []) {
        listener({ matches, media: query } as MediaQueryListEvent)
      }
    },
    restore() {
      Object.defineProperty(globalThis, 'matchMedia', {
        configurable: true,
        writable: true,
        value: before,
      })
    },
  }
}
