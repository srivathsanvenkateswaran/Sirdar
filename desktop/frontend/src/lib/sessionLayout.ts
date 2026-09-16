/**
 * Which of the three Session layouts the window draws: the Conversation
 * (A), the Document (B) or the Workbench (C). Chosen in Settings › General
 * and by the switcher in the session header; a preference of this person
 * and this browser, like the theme, so it lives in localStorage and never
 * in the config file.
 *
 * The default is the Conversation, as decided after the mock review. This
 * file is the Workbench branch's copy of a preference the session-blocks
 * branch (session-b) defines with the same key; when the two meet, one copy
 * stays.
 */

export type SessionLayout = 'conversation' | 'document' | 'workbench'

export const SESSION_LAYOUTS: readonly SessionLayout[] = ['conversation', 'document', 'workbench']

export const DEFAULT_SESSION_LAYOUT: SessionLayout = 'conversation'

export const SESSION_LAYOUT_KEY = 'sirdar.sessionLayout'

const watchers = new Set<() => void>()

function isLayout(value: unknown): value is SessionLayout {
  return value === 'conversation' || value === 'document' || value === 'workbench'
}

/** localStorage is absent in some tests and can throw in a locked-down webview. */
function read(): SessionLayout {
  try {
    const stored = globalThis.localStorage?.getItem(SESSION_LAYOUT_KEY)
    return isLayout(stored) ? stored : DEFAULT_SESSION_LAYOUT
  } catch {
    return DEFAULT_SESSION_LAYOUT
  }
}

let current = read()

export function sessionLayout(): SessionLayout {
  return current
}

export function setSessionLayout(value: SessionLayout): void {
  if (current === value) return
  current = value
  try {
    globalThis.localStorage?.setItem(SESSION_LAYOUT_KEY, value)
  } catch {
    // A preference that cannot be remembered still holds this session.
  }
  for (const watcher of [...watchers]) watcher()
}

export function subscribeSessionLayout(listener: () => void): () => void {
  watchers.add(listener)
  return () => {
    watchers.delete(listener)
  }
}

/** Re-reads storage, so one test cannot leak into the next. */
export function resetSessionLayout(): void {
  current = read()
  for (const watcher of [...watchers]) watcher()
}
