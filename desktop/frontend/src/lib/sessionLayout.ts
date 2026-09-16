/**
 * Which of the three Session layouts the window draws: Conversation (the
 * transcript as a chat with an inspector beside it), Document (the note in
 * the centre with the path beside it) or Workbench (documents over a
 * console). Conversation is the default.
 *
 * It is a preference of this person and this browser, like the theme: it
 * says nothing about the workspace, so it lives in localStorage under
 * `sirdar.sessionLayout` and never in the config file. Settings › General
 * and the switcher in the session header both write it. Every choice is
 * stored, so a stored one always wins over the default.
 */

export type SessionLayout = 'conversation' | 'document' | 'workbench'

export const SESSION_LAYOUTS: readonly SessionLayout[] = ['conversation', 'document', 'workbench']

export const DEFAULT_SESSION_LAYOUT: SessionLayout = 'conversation'

export const SESSION_LAYOUT_KEY = 'sirdar.sessionLayout'

/** The layouts in the order the Settings row and the header switcher list them: the default first. */
export const SESSION_LAYOUT_OPTIONS: { id: SessionLayout; label: string; note: string }[] = [
  {
    id: 'conversation',
    label: 'Conversation',
    note: 'The transcript as a chat, the answer as a card at its end, an inspector on the right.',
  },
  {
    id: 'document',
    label: 'Document',
    note: 'The note or the change as the window, the path the agent took beside it.',
  },
  {
    id: 'workbench',
    label: 'Workbench',
    note: 'Documents in tabs over a console of every call, with gauges in the header.',
  },
]

const watchers = new Set<() => void>()

export function isSessionLayout(value: unknown): value is SessionLayout {
  return value === 'conversation' || value === 'document' || value === 'workbench'
}

/** localStorage is absent in tests and can throw in a locked-down webview. */
function read(): SessionLayout {
  try {
    const stored = globalThis.localStorage?.getItem(SESSION_LAYOUT_KEY)
    return isSessionLayout(stored) ? stored : DEFAULT_SESSION_LAYOUT
  } catch {
    return DEFAULT_SESSION_LAYOUT
  }
}

let current = read()

/** The layout this window draws sessions in. */
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

/** Notifies on every change; the return value unsubscribes. */
export function subscribeSessionLayout(listener: () => void): () => void {
  watchers.add(listener)
  return () => {
    watchers.delete(listener)
  }
}

/** Re-reads storage and drops the cached value, so one test cannot leak into the next. */
export function resetSessionLayout(): void {
  current = read()
  for (const watcher of [...watchers]) watcher()
}
