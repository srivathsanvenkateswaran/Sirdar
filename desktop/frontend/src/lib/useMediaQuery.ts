import { useEffect, useState } from 'react'

/**
 * Whether a media query matches the window now.
 *
 * `matchMedia` is guarded rather than assumed: the Wails webview has it, jsdom
 * does not always, and a shell that throws on mount in a test environment is a
 * shell nobody can test. Without it the answer is `false`, which is the wide
 * layout — the one every screen is designed for first.
 */
export function useMediaQuery(query: string): boolean {
  const [matches, setMatches] = useState(false)
  useEffect(() => {
    const media = globalThis.matchMedia?.(query)
    if (!media) return
    setMatches(media.matches)
    const listen = (e: MediaQueryListEvent) => setMatches(e.matches)
    media.addEventListener?.('change', listen)
    return () => media.removeEventListener?.('change', listen)
  }, [query])
  return matches
}

/*
 * The breakpoints of src/styles/tokens.css, as queries a component can ask
 * about. A stylesheet answers most of them; these are for the few changes
 * that are DOM rather than layout — a stat row that collapses into a title,
 * a file rail that becomes a select.
 */

/** Under 1200: the compact band. */
export const BELOW_STANDARD = '(max-width: 1199px)'
/** Under 1024: the narrow band, where the sidebar is an icon rail. */
export const BELOW_COMPACT = '(max-width: 1023px)'
