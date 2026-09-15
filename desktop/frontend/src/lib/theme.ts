/**
 * The window's theme: the system's, or light or dark by choice.
 *
 * `styles/tokens.css` answers `prefers-color-scheme` on its own, so "system"
 * is the absence of a choice: no attribute on the root, and the media query
 * decides. A choice stamps `data-theme` on the root element, which the same
 * file reads ahead of the media query in either direction.
 *
 * It is a preference of this person and this browser, like the reading
 * direction and the design-library switch: it says nothing about the
 * workspace, so it lives in localStorage and never in the config file.
 */

export type Theme = 'system' | 'light' | 'dark'

export const THEMES: readonly Theme[] = ['system', 'light', 'dark']

const KEY = 'sirdar.theme'

const watchers = new Set<() => void>()

function isTheme(value: unknown): value is Theme {
  return value === 'system' || value === 'light' || value === 'dark'
}

/** localStorage is absent in tests and can throw in a locked-down webview. */
function read(): Theme {
  try {
    const stored = globalThis.localStorage?.getItem(KEY)
    return isTheme(stored) ? stored : 'system'
  } catch {
    return 'system'
  }
}

let current = read()

/** The theme this window is on. */
export function theme(): Theme {
  return current
}

/**
 * Stamps the choice on the root element, or clears it for "system" so the
 * tokens' own media query takes over again.
 */
export function applyTheme(): void {
  if (typeof document === 'undefined') return
  const root = document.documentElement
  if (current === 'system') delete root.dataset.theme
  else root.dataset.theme = current
}

export function setTheme(value: Theme): void {
  if (current === value) return
  current = value
  try {
    if (value === 'system') globalThis.localStorage?.removeItem(KEY)
    else globalThis.localStorage?.setItem(KEY, value)
  } catch {
    // A preference that cannot be remembered still holds this session.
  }
  applyTheme()
  for (const watcher of [...watchers]) watcher()
}

/** Notifies on every change; the return value unsubscribes. */
export function subscribeTheme(listener: () => void): () => void {
  watchers.add(listener)
  return () => {
    watchers.delete(listener)
  }
}

/** Re-reads storage and drops the cached value, so one test cannot leak into the next. */
export function resetTheme(): void {
  current = read()
  applyTheme()
  for (const watcher of [...watchers]) watcher()
}
