/**
 * A yes-or-no the browser remembers: a folded pane, a collapsed sidebar.
 *
 * Browser preferences, like the theme and the library switch: they say how
 * this person wants this window, not what the workspace is, so they never
 * go near the config file. `localStorage` is absent in some tests and can
 * throw in a locked-down webview, so a flag that cannot be read is `false`
 * and one that cannot be written still holds for the session.
 */

export function readStoredFlag(key: string): boolean {
  try {
    return globalThis.localStorage?.getItem(key) === '1'
  } catch {
    return false
  }
}

export function writeStoredFlag(key: string, value: boolean): void {
  try {
    globalThis.localStorage?.setItem(key, value ? '1' : '0')
  } catch {
    // Remembered for this window only.
  }
}
