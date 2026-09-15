import { useCallback, useEffect, useRef, useSyncExternalStore } from 'react'
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
import RunDetail from './screens/RunDetail'
import Settings from './screens/Settings'
import { showLibrary, subscribeShowLibrary } from './lib/library'
import { parseRoute, routeHash, sameScreen } from './lib/routes'
import type { AppState, AppStore, EvalOptions, FixOptions, RCAOptions, Screen } from './store/appStore'
import { useAppState, useStore } from './store/useAppStore'
import './components/shell/shell.css'

/**
 * A rejection the store has already toasted. The forms show the reason beside
 * their own button; a start with no form behind it has nothing else to do with
 * it, and an unhandled rejection would only reach the console.
 */
function reported(): void {}

/**
 * A window that opened on nothing in particular, in a workspace with no runs
 * yet, opens on New session: there is no board to read and no session to
 * return to, and the one thing to do is start one. It is decided once, from
 * the address the window opened with, so a reader who then goes to the board
 * is not sent back; a link that names a screen is followed as written.
 */
function useFirstLaunch(store: AppStore, state: AppState): void {
  const fresh = useRef(typeof window !== 'undefined' && window.location.hash === '')
  const { loading, currentWorkspaceId, runsByWorkspace, screen } = state
  useEffect(() => {
    if (!fresh.current || loading || !currentWorkspaceId) return
    const runs = runsByWorkspace[currentWorkspaceId]
    if (!runs) return
    fresh.current = false
    if (runs.length === 0 && screen.name === 'board') store.navigate({ name: 'new' })
  }, [store, loading, currentWorkspaceId, runsByWorkspace, screen])
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
function useHashRoute(store: AppStore, state: AppState): void {
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

function Shell(): JSX.Element {
  const store = useStore()
  const state = useAppState()
  const libraryOn = useSyncExternalStore(subscribeShowLibrary, showLibrary, () => false)
  const filterRef = useRef<HTMLInputElement | null>(null)

  useEffect(() => {
    void store.init()
  }, [store])

  useHashRoute(store, state)
  useFirstLaunch(store, state)

  useEffect(() => {
    if (state.screen.name === 'library' && !libraryOn) store.navigate({ name: 'board' })
  }, [state.screen, libraryOn, store])

  /*
   * Settings is a modal over the screen it was opened from, not a screen of
   * its own: it is a place you leave, and keeping the board painted behind the
   * scrim is what says you are coming back. The store still holds `settings`
   * as a screen so the route, the address bar and the nav row all keep
   * working; this is the screen that stays painted underneath, and the one
   * closing the modal returns to.
   */
  const behind = useRef<Screen>({ name: 'board' })
  if (state.screen.name !== 'settings') behind.current = state.screen
  const settingsOpen = state.screen.name === 'settings'
  const settingsPage = state.screen.name === 'settings' ? state.screen.page : undefined
  const shown = settingsOpen ? behind.current : state.screen

  const workspaceId = state.currentWorkspaceId
  const currentWorkspace = state.workspaces.find((w) => w.id === workspaceId)
  const runs = state.runsByWorkspace[workspaceId] ?? []
  const tickets = state.ticketsByWorkspace[workspaceId] ?? []

  const navigate = useCallback((screen: Screen) => store.navigate(screen), [store])
  const dismissToast = useCallback((id: number) => store.dismissToast(id), [store])
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
        if (state.workspaces.length === 0) {
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
  }, [navigate, shown.name, settingsOpen, state.workspaces.length, store])

  // Every start goes through the store, which keeps the job id the run detail
  // screen's Cancel button needs. A start that fails rejects as well as
  // toasting, so the form that asked can show the reason beside its button;
  // the two call sites with no form behind them swallow it here instead, and
  // the toast is what the reader sees.
  const startRCA = useCallback(
    async (key: string, opts?: RCAOptions) => {
      await store.startRCA(key, opts)
    },
    [store],
  )
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

  let screen
  switch (shown.name) {
    case 'new':
      screen = (
        <NewSession
          transport={state.transport}
          workspaceId={workspaceId}
          workspace={currentWorkspace}
          runs={runs}
          onStart={startSession}
          onOpenRun={(runId) => navigate({ name: 'run', runId })}
        />
      )
      break
    case 'run':
      screen = (
        <RunDetail
          transport={state.transport}
          workspaceId={workspaceId}
          runId={shown.runId}
          defaultProvider={currentWorkspace?.provider}
          onBack={() => navigate({ name: 'board' })}
          onStartRCA={startRCA}
          onStartFix={startFix}
        />
      )
      break
    case 'review':
      screen = (
        <Review
          transport={state.transport}
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
      screen = (
        <Register
          transport={state.transport}
          workspaceId={workspaceId}
          onOpenRun={(runId) => navigate({ name: 'run', runId })}
        />
      )
      break
    case 'eval':
      screen = (
        <Eval
          transport={state.transport}
          workspaceId={workspaceId}
          defaultProvider={currentWorkspace?.provider}
          defaultModel={currentWorkspace?.model}
          quota={state.quota}
          jobs={state.keylessJobs.filter((j) => j.workspaceId === workspaceId)}
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
      screen = libraryOn ? <Library /> : null
      break
    default:
      screen = (
        <Board
          transport={state.transport}
          workspaceId={workspaceId}
          provider={currentWorkspace?.provider}
          tickets={tickets}
          runs={runs}
          queueUnsupported={Boolean(state.queueUnsupported[workspaceId])}
          loading={state.loading}
          inbound={state.inbound}
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
      <Sidebar
        workspaces={state.workspaces}
        currentWorkspaceId={workspaceId}
        quota={state.quota}
        screen={state.screen}
        runs={runs}
        inboundCount={state.inbound?.length ?? 0}
        onSelectWorkspace={(id) => store.setWorkspace(id)}
        onAddWorkspace={() => navigate({ name: 'settings', page: 'workspaces' })}
        onNavigate={navigate}
      />
      <main className="main">
        <div className={`sd-page ${PAGE_ENTER_CLASS}`} key={pageKey}>
          {state.workspaces.length === 0 && !state.loading && shown.name !== 'library' ? (
            <p className="app-empty">
              No workspace yet. Open Settings and add the path to a repository that has a{' '}
              <code>.sirdar</code> config.
            </p>
          ) : (
            screen
          )}
        </div>
      </main>
      <Settings
        open={settingsOpen}
        page={settingsPage}
        onSelectPage={selectSettingsPage}
        transport={state.transport}
        workspaces={state.workspaces}
        currentWorkspaceId={workspaceId}
        onClose={closeSettings}
        onWorkspacesChanged={() => void store.refresh()}
      />
      <Toasts toasts={state.toasts} onDismiss={dismissToast} />
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
