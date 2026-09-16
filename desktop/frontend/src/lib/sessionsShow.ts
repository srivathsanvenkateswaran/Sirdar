import type { RunSummary, SourcesSummary, SourceSummary } from '../api/types'

/**
 * Which ticket number the sessions list, the board cards and the session
 * topbar show: the tracker's key (`OMNI-2815`) or the helpdesk's number
 * (`#25312`). A support engineer who lives in the helpdesk looks a session up
 * by the number the customer's ticket has; one who lives in the tracker, by
 * the issue key.
 *
 * It is a preference of this person and this browser, like the theme: it says
 * nothing about the workspace, so it lives in localStorage and never in the
 * config file. Tracker is the default, since the run's own key is the
 * tracker's.
 */

export type SessionsShow = 'tracker' | 'helpdesk'

export const SESSIONS_SHOW: readonly SessionsShow[] = ['tracker', 'helpdesk']

export const DEFAULT_SESSIONS_SHOW: SessionsShow = 'tracker'

const KEY = 'sirdar.sessionsShow'

const watchers = new Set<() => void>()

function isSessionsShow(value: unknown): value is SessionsShow {
  return value === 'tracker' || value === 'helpdesk'
}

/** localStorage is absent in some tests and can throw in a locked-down webview. */
function read(): SessionsShow {
  try {
    const stored = globalThis.localStorage?.getItem(KEY)
    return isSessionsShow(stored) ? stored : DEFAULT_SESSIONS_SHOW
  } catch {
    return DEFAULT_SESSIONS_SHOW
  }
}

let current = read()

/** The number the lists show. */
export function sessionsShow(): SessionsShow {
  return current
}

export function setSessionsShow(value: SessionsShow): void {
  if (current === value) return
  current = value
  try {
    globalThis.localStorage?.setItem(KEY, value)
  } catch {
    // A preference that cannot be remembered still holds this session.
  }
  for (const watcher of [...watchers]) watcher()
}

/** Notifies on every change; the return value unsubscribes. */
export function subscribeSessionsShow(listener: () => void): () => void {
  watchers.add(listener)
  return () => {
    watchers.delete(listener)
  }
}

/** Re-reads storage and drops the cached value, so one test cannot leak into the next. */
export function resetSessionsShow(): void {
  current = read()
  for (const watcher of [...watchers]) watcher()
}

/** A helpdesk number as the lists write it: `#25312`. A key already so written is left alone. */
export function helpdeskNumber(key: string): string {
  const k = key.trim()
  return k.startsWith('#') ? k : `#${k}`
}

/**
 * The number a run is shown by, with the source it belongs to.
 *
 * The preference picks the helpdesk's number when the run has one; a run with
 * no helpdesk number shows its key under the tracker's mark whatever the
 * preference says, since the alternative is a blank. `other` is the number
 * not shown, for a tooltip, and is empty when there is only one.
 */
export interface ShownNumber {
  /** `OMNI-2815` or `#25312`. */
  text: string
  /** Which source the shown number belongs to. */
  role: SessionsShow
  /** The source, when the config summary names it. */
  source?: SourceSummary
  /** The other number, written out with its source's name: `Zoho Desk #25312`. Empty when there is none. */
  other: string
}

export function shownNumber(
  run: Pick<RunSummary, 'key' | 'helpdeskKey'>,
  show: SessionsShow,
  sources?: SourcesSummary,
): ShownNumber {
  const helpdesk = run.helpdeskKey?.trim() ?? ''
  const tracker = run.key
  if (show === 'helpdesk' && helpdesk) {
    return {
      text: helpdeskNumber(helpdesk),
      role: 'helpdesk',
      source: sources?.helpdesk,
      other: withSource(tracker, sources?.tracker),
    }
  }
  return {
    text: tracker,
    role: 'tracker',
    source: sources?.tracker,
    other: helpdesk ? withSource(helpdeskNumber(helpdesk), sources?.helpdesk) : '',
  }
}

/** `Zoho Desk #25312`, or the number alone when the source is not named. */
export function withSource(number: string, source?: SourceSummary): string {
  const name = source?.name?.trim()
  return name ? `${name} ${number}` : number
}
