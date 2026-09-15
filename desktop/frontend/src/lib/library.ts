/**
 * Whether the window offers the design library.
 *
 * `#/library` is the living asset library: every component in the `ui/` folder,
 * in every state, in both themes and both directions. It is for whoever is
 * building or reviewing the interface, not for an engineer working a ticket,
 * so it is behind a switch that is on in a development build and off in a
 * release unless a person turns it on in Settings.
 *
 * The preference is a browser one, like the note pane's reading direction:
 * it says what this person wants to see, not what the workspace is, so it does
 * not belong in the workspace config.
 */

const KEY = 'sirdar.showLibrary'

const watchers = new Set<() => void>()

/** A development build is any build that is not a production bundle. */
function byDefault(): boolean {
  try {
    return Boolean(import.meta.env?.DEV)
  } catch {
    return false
  }
}

/** localStorage is absent in tests and can throw in a locked-down webview. */
function read(): boolean {
  try {
    const stored = globalThis.localStorage?.getItem(KEY)
    if (stored === '1') return true
    if (stored === '0') return false
  } catch {
    // Fall through to the build's default.
  }
  return byDefault()
}

let enabled = read()

/** Whether the Library tab and the `#/library` route are available. */
export function showLibrary(): boolean {
  return enabled
}

export function setShowLibrary(value: boolean): void {
  if (enabled === value) return
  enabled = value
  try {
    // Written either way round: a release build defaults to off and a dev
    // build to on, so "off" has to be storable as a choice rather than as an
    // absence.
    globalThis.localStorage?.setItem(KEY, value ? '1' : '0')
  } catch {
    // A preference that cannot be remembered still holds this session.
  }
  for (const watcher of [...watchers]) watcher()
}

/** Notifies on every change; the return value unsubscribes. */
export function subscribeShowLibrary(listener: () => void): () => void {
  watchers.add(listener)
  return () => {
    watchers.delete(listener)
  }
}

/** Re-reads storage and drops the cached value, so one test cannot leak into the next. */
export function resetShowLibrary(): void {
  enabled = read()
  for (const watcher of [...watchers]) watcher()
}
