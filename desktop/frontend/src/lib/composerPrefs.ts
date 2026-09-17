/**
 * What this person has settled about the New session composer, per
 * workspace: whether an ambiguous line may be read by the model, and what a
 * fix should do once it has made its commit.
 *
 * Both are per-workspace and neither is about the workspace itself, so they
 * live where the session-list arrangements live — `localStorage`, one record
 * under `sirdar.composer.<workspaceId>` — rather than in config.yaml. A
 * repository where fixes are opened as pull requests and one where they are
 * kept local are two different habits on the same machine, which is exactly
 * what per-workspace means here.
 *
 * The module is a store the way `sessionPrefs` is: a reader, mutators that
 * write and notify, and a subscribe for `useSyncExternalStore`.
 */

/** What a fix does once the commit is made. */
export type FixThen = 'pr' | 'noPr' | 'local'

/** The Then control's options, in the order it draws them. */
export const FIX_THEN_OPTIONS: { id: FixThen; label: string }[] = [
  { id: 'pr', label: 'Open a pull request' },
  { id: 'noPr', label: 'Push the branch only' },
  { id: 'local', label: 'Keep it local' },
]

/** What each choice means, under the control. */
export const FIX_THEN_NOTE: Record<FixThen, string> = {
  pr: 'The branch is pushed and a pull request is opened.',
  noPr: 'The branch is pushed; no pull request is opened.',
  local: 'The commit stays in the worktree — nothing is pushed, and the change is read in Change review.',
}

export interface ComposerPrefs {
  /**
   * Whether a line the parser cannot settle may be read by one short
   * provider call. On by default: the call is one turn with no tools, it
   * is made only when the line is genuinely ambiguous, and it starts
   * nothing on its own.
   */
  intentAssist: boolean
  /** What a fix does once the commit is made. */
  fixThen: FixThen
}

const KEY_PREFIX = 'sirdar.composer.'

function key(workspaceId: string): string {
  return KEY_PREFIX + workspaceId
}

function empty(): ComposerPrefs {
  return { intentAssist: true, fixThen: 'pr' }
}

function isFixThen(value: unknown): value is FixThen {
  return value === 'pr' || value === 'noPr' || value === 'local'
}

/** Takes whatever the stored JSON holds and answers with a record of the current shape. */
function coerce(raw: unknown): ComposerPrefs {
  const out = empty()
  if (typeof raw !== 'object' || raw === null) return out
  const record = raw as Record<string, unknown>
  if (typeof record.intentAssist === 'boolean') out.intentAssist = record.intentAssist
  if (isFixThen(record.fixThen)) out.fixThen = record.fixThen
  return out
}

/** localStorage is absent in some tests and can throw in a locked-down webview. */
function read(workspaceId: string): ComposerPrefs {
  try {
    const stored = globalThis.localStorage?.getItem(key(workspaceId))
    if (!stored) return empty()
    return coerce(JSON.parse(stored))
  } catch {
    return empty()
  }
}

function write(workspaceId: string, prefs: ComposerPrefs): void {
  try {
    globalThis.localStorage?.setItem(key(workspaceId), JSON.stringify(prefs))
  } catch {
    // A choice that cannot be remembered still holds this session.
  }
}

const cache = new Map<string, ComposerPrefs>()
const watchers = new Set<() => void>()

/** The workspace's record, the same object until something in it changes. */
export function composerPrefs(workspaceId: string): ComposerPrefs {
  let prefs = cache.get(workspaceId)
  if (!prefs) {
    prefs = read(workspaceId)
    cache.set(workspaceId, prefs)
  }
  return prefs
}

export function subscribeComposerPrefs(listener: () => void): () => void {
  watchers.add(listener)
  return () => {
    watchers.delete(listener)
  }
}

/** Drops the cache, so one test cannot leak into the next. */
export function resetComposerPrefs(): void {
  cache.clear()
  for (const watcher of [...watchers]) watcher()
}

function update(workspaceId: string, next: ComposerPrefs): void {
  const before = composerPrefs(workspaceId)
  if (before.intentAssist === next.intentAssist && before.fixThen === next.fixThen) return
  cache.set(workspaceId, next)
  write(workspaceId, next)
  for (const watcher of [...watchers]) watcher()
}

export function setIntentAssist(workspaceId: string, on: boolean): void {
  update(workspaceId, { ...composerPrefs(workspaceId), intentAssist: on })
}

export function setFixThen(workspaceId: string, then: FixThen): void {
  update(workspaceId, { ...composerPrefs(workspaceId), fixThen: then })
}
