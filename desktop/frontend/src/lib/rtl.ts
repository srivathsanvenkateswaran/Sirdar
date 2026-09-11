/**
 * The reading-direction preference for note text.
 *
 * Sirdar's notes are bilingual: the body is English, the customer's own
 * complaint and the reply draft are usually Arabic. `dir="auto"` on each block
 * handles the mixture paragraph by paragraph, which is what most engineers
 * want. An engineer who reads mostly Arabic wants the whole note pane laid out
 * right to left instead — the margin, the list bullets, the scrollbar — and
 * that is a per-person choice, not a workspace one, so it lives in this
 * browser rather than in the workspace config.
 *
 * The event stream is deliberately not covered: it is tool names, paths and
 * JSON, and laying those out right to left makes them unreadable.
 */

const KEY = 'sirdar.preferRTL'

const watchers = new Set<() => void>()

/** localStorage is absent in tests and can throw in a locked-down webview. */
function read(): boolean {
  try {
    return globalThis.localStorage?.getItem(KEY) === '1'
  } catch {
    return false
  }
}

let preferred = read()

/** Whether the note pane should be laid out right to left. */
export function prefersRTL(): boolean {
  return preferred
}

/** The `dir` a note pane should carry: "rtl" when preferred, else "auto". */
export function noteDir(): 'rtl' | 'auto' {
  return preferred ? 'rtl' : 'auto'
}

export function setPreferRTL(value: boolean): void {
  if (preferred === value) return
  preferred = value
  try {
    if (value) globalThis.localStorage?.setItem(KEY, '1')
    else globalThis.localStorage?.removeItem(KEY)
  } catch {
    // A preference that cannot be remembered still holds this session.
  }
  for (const watcher of [...watchers]) watcher()
}

/** Notifies on every change; the return value unsubscribes. */
export function subscribePreferRTL(listener: () => void): () => void {
  watchers.add(listener)
  return () => {
    watchers.delete(listener)
  }
}

/**
 * Re-reads localStorage and drops the cached value. Tests use it so one case
 * cannot leak its preference into the next.
 */
export function resetPreferRTL(): void {
  preferred = read()
  for (const watcher of [...watchers]) watcher()
}

/**
 * Removes the `<div dir="rtl">` wrappers the note templates put around an
 * Arabic paragraph.
 *
 * Those wrappers are for Obsidian, which renders HTML in a markdown note.
 * react-markdown does not, and shows the tags as literal text, so the note
 * pane would read `<div dir="rtl">` above every Arabic paragraph. Dropping
 * them costs nothing here: `dir="auto"` on the container already resolves each
 * block from its own first strong character.
 *
 * Only a line that is exactly the opening tag counts, and only a `</div>` that
 * closes one — any other HTML in the note is left alone to render as it did.
 */
export function stripRTLBlocks(markdown: string): string {
  let depth = 0
  return markdown
    .split('\n')
    .filter((line) => {
      const trimmed = line.trim()
      if (trimmed === '<div dir="rtl">') {
        depth += 1
        return false
      }
      if (depth > 0 && trimmed === '</div>') {
        depth -= 1
        return false
      }
      return true
    })
    .join('\n')
}
