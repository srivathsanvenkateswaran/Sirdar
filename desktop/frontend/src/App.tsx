import { memo, useCallback, useEffect, useRef, useSyncExternalStore } from 'react'
import type { RunSummary, Ticket } from './api/types'
import Sidebar from './components/shell/Sidebar'
import { PrimaryActionProvider } from './components/shell/primaryAction'
import { PAGE_ENTER_CLASS } from './ui/motion'
import Toasts from './ui/toast'
import Board from './screens/Board'
import Eval from './screens/Eval'
import Library from './screens/Library'
import NewSession, { type SessionMode, type StartOverrides } from './screens/NewSession'
import Register from './screens/Register'
import Review from './screens/Review'
import Session from './screens/Session'
import Settings from './screens/Settings'
import { showLibrary, subscribeShowLibrary } from './lib/library'
import { parseRoute, routeHash, sameScreen } from './lib/routes'
import type { AppState, AppStore, EvalOptions, FixOptions, Screen } from './store/appStore'
import { useAppState, useStore } from './store/useAppStore'
import './components/shell/shell.css'

/**
 * A rejection the store has already toasted. The forms show the reason beside
 * their own button; a start with no form behind it has nothing else to do with
 * it, and an unhandled rejection would only reach the console.
 */
function reported(): void {}

const NO_RUNS: RunSummary[] = []
const NO_TICKETS: Ticket[] = []

/* ---------- the slices the shell reads ---------- */

const selectRuns = (s: AppState): RunSummary[] | undefined => s.runsByWorkspace[s.currentWorkspaceId]
const selectTickets = (s: AppState): Ticket[] | undefined => s.ticketsByWorkspace[s.currentWorkspaceId]
const selectSources = (s: AppState) => s.sourcesByWorkspace[s.currentWorkspaceId]
const selectQueueUnsupported = (s: AppState): boolean => Boolean(s.queueUnsupported[s.currentWorkspaceId])
const selectWorkspace = (s: AppState) => s.workspaces.find((w) => w.id === s.currentWorkspaceId)

/**
 * A window that opened on nothing in particular, in a workspace with no runs
 * yet, opens on New session: there is no board to read and no session to
 * return to, and the one thing to do is start one. It is decided once, from
 * the address the window opened with, so a reader who then goes to the board
 * is not sent back; a link that names a screen is followed as written.
 */
function useFirstLaunch(
  store: AppStore,
  loading: boolean,
  currentWorkspaceId: string,
  runs: RunSummary[] | undefined,
  screen: Screen,
): void {
  const fresh = useRef(typeof window !== 'undefined' && window.location.hash === '')
  useEffect(() => {
    if (!fresh.current || loading || !currentWorkspaceId) return
    if (!runs) return
    fresh.current = false
    if (runs.length === 0 && screen.name === 'board') store.navigate({ name: 'new' })
  }, [store, loading, currentWorkspaceId, runs, screen])
}

/**
 * Keeps the address bar and the store's screen in step, both ways.
 *
 * Reading: whatever the hash names is opened — on load, on Back, and on a
 * pasted link. Nothing is applied until `init()` has answered, because a run
 * link names a workspace and there are none to match it against before then.
 *
 * Writing: every move the window makes — the sidebar's rows, a board card, the
 * store's own navigation — leaves an address that can be linked to. A hash
 * that names nothing opens nothing and is then overwritten by the screen the
 * window is really on, so the address never describes a screen that is not up.
 *
 * Settings is a route like any other even though it is now a modal rather than
 * a screen: `#/settings` opens the modal over whatever was behind it, and
 * closing the modal writes the address of the screen it uncovers.
 */
function useHashRoute(
  store: AppStore,
  state: Pick<AppState, 'screen' | 'currentWorkspaceId' | 'workspaces' | 'loading'>,
): void {
  const { screen, currentWorkspaceId, workspaces, loading } = state

  useEffect(() => {
    function apply(): void {
      const route = parseRoute(window.location.hash)
      if (!route) return
      const now = store.getState()
      if (now.loading) return
      const wants = route.workspaceId
      if (
        sameScreen(now.screen, route.screen) &&
        (!wants || wants === now.currentWorkspaceId || !workspaces.some((w) => w.id === wants))
      ) {
        return
      }
      store.openRoute(route.screen, wants)
    }
    apply()
    window.addEventListener('hashchange', apply)
    return () => window.removeEventListener('hashchange', apply)
  }, [store, loading, workspaces])

  useEffect(() => {
    if (loading) return
    const want = routeHash(screen, currentWorkspaceId)
    if (window.location.hash === want) return
    // A window that opened with no hash at all gets one without a history
    // entry, so Back still leads out of the app rather than to the board.
    if (window.location.hash === '') {
      window.history.replaceState(null, '', want)
      return
    }
    window.location.hash = want
  }, [screen, currentWorkspaceId, loading])
}

/** Keys typed into a field belong to that field, not to the window. */
function isTyping(target: EventTarget | null): boolean {
  const el = target as HTMLElement | null
  if (!el || !el.tagName) return false
  const tag = el.tagName.toLowerCase()
  return tag === 'input' || tag === 'textarea' || tag === 'select' || el.isContentEditable
}

/**
 * The sidebar on its own subscriptions. It reads what it draws — the
 * workspaces, the current one's runs and sources, the quota, the screen, and
 * how many deliveries wait — and nothing else, so the sheet re-rendering for
 * a toast or a filter does not re-draw the sessions list. Memoised with no
 * props so the shell above it can render freely.
 */
const ConnectedSidebar = memo(function ConnectedSidebar({
  onNavigate,
}: {
  onNavigate: (screen: Screen) => void
}): JSX.Element {
  const store = useStore()
  const workspaces = useAppState((s) => s.workspaces)
  const currentWorkspaceId = useAppState((s) => s.currentWorkspaceId)
  const quota = useAppState((s) => s.quota)
  const screen = useAppState((s) => s.screen)
  const runs = useAppState(selectRuns)
  const sources = useAppState(selectSources)
  const inboundCount = useAppState((s) => s.inbound.length)
  const onSelectWorkspace = useCallback((id: string) => store.setWorkspace(id), [store])
  const onAddWorkspace = useCallback(
    () => store.navigate({ name: 'settings', page: 'general' }),
    [store],
  )
  return (
    <Sidebar
      workspaces={workspaces}
      currentWorkspaceId={currentWorkspaceId}
      quota={quota}
      screen={screen}
      runs={runs ?? NO_RUNS}
      sources={sources}
      inboundCount={inboundCount}
      onSelectWorkspace={onSelectWorkspace}
      onAddWorkspace={onAddWorkspace}
      onNavigate={onNavigate}
    />
  )
})

/** The toasts, on their own subscription: a toast landing re-draws the toasts. */
const ConnectedToasts = memo(function ConnectedToasts(): JSX.Element {
  const store = useStore()
  const toasts = useAppState((s) => s.toasts)
  const dismissToast = useCallback((id: number) => store.dismissToast(id), [store])
  return <Toasts toasts={toasts} onDismiss={dismissToast} />
})

function Shell(): JSX.Element {
  const store = useStore()
  const transport = useAppState((s) => s.transport)
  const screen = useAppState((s) => s.screen)
  const workspaces = useAppState((s) => s.workspaces)
  const workspaceId = useAppState((s) => s.currentWorkspaceId)
  const loading = useAppState((s) => s.loading)
  const currentWorkspace = useAppState(selectWorkspace)
  const runsOrNone = useAppState(selectRuns)
  const ticketsOrNone = useAppState(selectTickets)
  const sources = useAppState(selectSources)
  const queueUnsupported = useAppState(selectQueueUnsupported)
  const inbound = useAppState((s) => s.inbound)
  const quota = useAppState((s) => s.quota)
  const keylessJobs = useAppState((s) => s.keylessJobs)
  const libraryOn = useSyncExternalStore(subscribeShowLibrary, showLibrary, () => false)
  const filterRef = useRef<HTMLInputElement | null>(null)

  useEffect(() => {
    void store.init()
  }, [store])

  useHashRoute(store, { screen, currentWorkspaceId: workspaceId, workspaces, loading })
  useFirstLaunch(store, loading, workspaceId, runsOrNone, screen)

  useEffect(() => {
    if (screen.name === 'library' && !libraryOn) store.navigate({ name: 'board' })
  }, [screen, libraryOn, store])

  /*
   * Settings is a modal over the screen it was opened from, not a screen of
   * its own: it is a place you leave, and keeping the board painted behind the
   * scrim is what says you are coming back. The store still holds `settings`
   * as a screen so the route, the address bar and the nav row all keep
   * working; this is the screen that stays painted underneath, and the one
   * closing the modal returns to.
   */
  const behind = useRef<Screen>({ name: 'board' })
  if (screen.name !== 'settings') behind.current = screen
  const settingsOpen = screen.name === 'settings'
  const settingsPage = screen.name === 'settings' ? screen.page : undefined
  const shown = settingsOpen ? behind.current : screen

  const runs = runsOrNone ?? NO_RUNS
  const tickets = ticketsOrNone ?? NO_TICKETS

  const navigate = useCallback((screen: Screen) => store.navigate(screen), [store])
  const closeSettings = useCallback(() => store.navigate(behind.current), [store])
  const selectSettingsPage = useCallback(
    (page: string) => store.navigate({ name: 'settings', page }),
    [store],
  )

  useEffect(() => {
    function onKeyDown(e: KeyboardEvent): void {
      if (e.metaKey || e.ctrlKey || e.altKey) return
      // The settings modal owns the keyboard while it is open.
      if (settingsOpen || isTyping(e.target)) return
      if (e.key === 'n') {
        e.preventDefault()
        if (workspaces.length === 0) {
          store.toast('Add a workspace before starting a run.', 'error')
          return
        }
        navigate({ name: 'new' })
        return
      }
      if (e.key === '/' && shown.name === 'board') {
        e.preventDefault()
        filterRef.current?.focus()
      }
    }
    window.addEventListener('keydown', onKeyDown)
    return () => window.removeEventListener('keydown', onKeyDown)
  }, [navigate, shown.name, settingsOpen, workspaces.length, store])

  // Every start goes through the store, which keeps the job id the session
  // screen's Cancel button needs. A start that fails rejects as well as
  // toasting, so the form that asked can show the reason beside its button;
  // the two call sites with no form behind them swallow it here instead, and
  // the toast is what the reader sees.
  const startFix = useCallback(
    async (key: string, opts?: FixOptions) => {
      await store.startFix(key, opts)
    },
    [store],
  )
  // New session's Start: the mode picks the store method, and the job id
  // comes back so the screen can open the run the job produces.
  const startSession = useCallback(
    (mode: SessionMode, key: string, o: StartOverrides) => {
      switch (mode) {
        case 'rca':
          return store.startRCA(key, { provider: o.provider, model: o.model })
        case 'fix':
          return store.startFix(key, { provider: o.provider, model: o.model, dryRun: o.dryRun })
        default:
          return store.startTriage([key], { provider: o.provider, model: o.model, dryRun: o.dryRun })
      }
    },
    [store],
  )
  const startEval = useCallback(
    (keys?: string[], opts?: EvalOptions) => store.startEval(keys, opts),
    [store],
  )
  const cancelJob = useCallback((jobId: string) => store.cancelJob(jobId), [store])

  let page
  switch (shown.name) {
    case 'new':
      page = (
        <NewSession
          transport={transport}
          workspaceId={workspaceId}
          workspace={currentWorkspace}
          workspaces={workspaces}
          onSelectWorkspace={(id) => store.setWorkspace(id)}
          onAddWorkspace={() => navigate({ name: 'settings', page: 'general' })}
          runs={runs}
          onStart={startSession}
          onOpenRun={(runId) => navigate({ name: 'run', runId })}
        />
      )
      break
    case 'run': {
      // The tracker's title for the run's ticket, when the queue lists it;
      // the run itself records only the key.
      const runId = shown.runId
      const key = runs.find((r) => r.runId === runId)?.key
      const title = key ? tickets.find((t) => t.key === key)?.title : undefined
      page = (
        <Session
          transport={transport}
          workspaceId={workspaceId}
          runId={runId}
          title={title}
          notesDir={currentWorkspace?.notesDir}
          sources={sources}
          onBack={() => navigate({ name: 'board' })}
          onOpenReview={() => navigate({ name: 'review', runId })}
          onStartFix={startFix}
        />
      )
      break
    }
    case 'review':
      page = (
        <Review
          transport={transport}
          workspaceId={workspaceId}
          runId={shown.runId}
          tickets={tickets}
          onBack={() => navigate({ name: 'run', runId: shown.runId })}
          // The session opens on its note. Its route carries no tab yet, so
          // this is the session itself; the tab is the session screen's to
          // take from the address once it has one.
          onOpenNote={() => navigate({ name: 'run', runId: shown.runId })}
        />
      )
      break
    case 'register':
      page = (
        <Register
          transport={transport}
          workspaceId={workspaceId}
          onOpenRun={(runId) => navigate({ name: 'run', runId })}
        />
      )
      break
    case 'eval':
      page = (
        <Eval
          transport={transport}
          workspaceId={workspaceId}
          defaultProvider={currentWorkspace?.provider}
          defaultModel={currentWorkspace?.model}
          quota={quota}
          jobs={keylessJobs.filter((j) => j.workspaceId === workspaceId)}
          onStartEval={startEval}
          onCancelJob={cancelJob}
        />
      )
      break
    case 'library':
      // The switch can be turned off while the gallery is open, and a link to
      // it can be pasted into a window that has it off. Either way the window
      // shows the board and the address catches up, rather than showing a
      // screen the reader has said they do not want.
      page = libraryOn ? <Library /> : null
      break
    default:
      page = (
        <Board
          transport={transport}
          workspaceId={workspaceId}
          provider={currentWorkspace?.provider}
          tickets={tickets}
          runs={runs}
          sources={sources}
          queueUnsupported={queueUnsupported}
          loading={loading}
          inbound={inbound}
          filterRef={filterRef}
          onOpenRun={(runId) => navigate({ name: 'run', runId })}
          onTriage={(keys) => void store.startTriage(keys).catch(reported)}
        />
      )
  }

  // The page's key is its address, so every navigation mounts a fresh page
  // and the enter motion runs again; a settings modal opening over it does
  // not, because the page underneath has not moved.
  const pageKey = routeHash(shown, workspaceId)

  return (
    <div className="app">
      <ConnectedSidebar onNavigate={navigate} />
      <main className="main">
        <div className={`sd-page ${PAGE_ENTER_CLASS}`} key={pageKey}>
          {workspaces.length === 0 && !loading && shown.name !== 'library' ? (
            <p className="app-empty">
              No workspace yet. Open Settings and add the path to a repository that has a{' '}
              <code>.sirdar</code> config.
            </p>
          ) : (
            page
          )}
        </div>
      </main>
      <Settings
        open={settingsOpen}
        page={settingsPage}
        onSelectPage={selectSettingsPage}
        transport={transport}
        workspaces={workspaces}
        currentWorkspaceId={workspaceId}
        onClose={closeSettings}
        onWorkspacesChanged={() => void store.refresh()}
      />
      <ConnectedToasts />
    </div>
  )
}

export default function App(): JSX.Element {
  return (
    <PrimaryActionProvider>
      <Shell />
    </PrimaryActionProvider>
  )
}
