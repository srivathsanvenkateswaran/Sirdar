import type { Screen } from '../store/appStore'

/**
 * The window's address bar, as a hash.
 *
 * Navigation is state the store holds; this is a second spelling of the same
 * thing, so a screen can be linked to — pasted into a chat, bookmarked, opened
 * by `sirdar serve` behind a reverse proxy — and so the browser's Back button
 * does what it looks as though it does. A hash is used rather than a path
 * because the same bundle is served from a Wails asset server and from the Go
 * binary's embedded FS, neither of which rewrites unknown paths to index.html.
 *
 *   #/                        the board
 *   #/new                     a new session
 *   #/register  #/eval  #/library
 *   #/settings  #/settings/<page>
 *   #/runs/<workspaceId>/<runId>
 *   #/runs/<workspaceId>/<runId>/review
 *
 * A run link names its workspace because a run id means nothing without one:
 * following the link into the wrong repository would show a run that is not
 * there.
 */
export interface Route {
  screen: Screen
  /** Only a run link names one; every other screen follows the current one. */
  workspaceId?: string
}

function decode(segment: string): string {
  try {
    return decodeURIComponent(segment)
  } catch {
    // A half-typed or mangled escape is not a route; leave it as written and
    // let the caller fail to match it.
    return segment
  }
}

/**
 * Reads a hash into a route, or answers null when it names nothing this window
 * has. A null is not an error: the caller stays where it is, and the address
 * then catches up with the screen actually on show rather than the other way
 * round, so what the window displays and what it says are never two things.
 */
export function parseRoute(hash: string): Route | null {
  const parts = hash
    .replace(/^#/, '')
    .split('/')
    .filter((s) => s !== '')
    .map(decode)

  if (parts.length === 0) return { screen: { name: 'board' } }

  switch (parts[0]) {
    case 'board':
      return parts.length === 1 ? { screen: { name: 'board' } } : null
    case 'new':
    case 'register':
    case 'eval':
    case 'library':
      // A library link parses whether or not the switch is on. The window
      // decides what to do with it: a route is an address, and refusing to
      // read one here would mean the address bar and the screen disagree.
      return parts.length === 1 ? { screen: { name: parts[0] } } : null
    case 'settings': {
      // `#/settings/<page>` opens the modal on that page. A page the modal
      // does not have is the modal's problem, not the address bar's: it
      // falls back to its first page, and the address then catches up.
      if (parts.length === 1) return { screen: { name: 'settings' } }
      if (parts.length === 2 && parts[1]) return { screen: { name: 'settings', page: parts[1] } }
      return null
    }
    case 'runs': {
      if (parts.length !== 3 && parts.length !== 4) return null
      const [, workspaceId, runId, tail] = parts
      if (!workspaceId || !runId) return null
      if (parts.length === 4) {
        if (tail !== 'review') return null
        return { screen: { name: 'review', runId }, workspaceId }
      }
      return { screen: { name: 'run', runId }, workspaceId }
    }
    default:
      return null
  }
}

/** The hash a screen is reached by. Always starts with `#/`. */
export function routeHash(screen: Screen, workspaceId: string): string {
  switch (screen.name) {
    case 'run':
    case 'review': {
      // A run with no workspace behind it yet is not linkable; the board is.
      if (!workspaceId) return '#/'
      const base = `#/runs/${encodeURIComponent(workspaceId)}/${encodeURIComponent(screen.runId)}`
      return screen.name === 'review' ? `${base}/review` : base
    }
    case 'settings':
      return screen.page ? `#/settings/${encodeURIComponent(screen.page)}` : '#/settings'
    case 'board':
      return '#/'
    default:
      return `#/${screen.name}`
  }
}

/** True when two screens are the same place, so a sync can stand still. */
export function sameScreen(a: Screen, b: Screen): boolean {
  if (a.name !== b.name) return false
  if ((a.name === 'run' || a.name === 'review') && (b.name === 'run' || b.name === 'review')) {
    return a.runId === b.runId
  }
  if (a.name === 'settings' && b.name === 'settings') return (a.page ?? '') === (b.page ?? '')
  return true
}
