import { memo, useCallback, useEffect, useRef, useState, useSyncExternalStore } from 'react'
import type { Quota, RunSummary, SearchHit, SourcesSummary, Workspace } from '../../api/types'
import { showLibrary, subscribeShowLibrary } from '../../lib/library'
import { readStoredFlag, writeStoredFlag } from '../../lib/storedFlag'
import { useDebounced } from '../../lib/useDebounced'
import { BELOW_COMPACT, useMediaQuery } from '../../lib/useMediaQuery'
import type { Screen } from '../../store/appStore'
import BrandMark from '../../ui/brand-mark'
import Button from '../../ui/button'
import PanelToggle from '../../ui/panel-toggle'
import SearchBar from '../../ui/search-bar'
import SidebarFooterCard from '../../ui/sidebar-footer-card'
import SidebarNavItem from '../../ui/sidebar-nav-item'
import QuotaMeter from '../QuotaMeter'
import {
  BoardIcon,
  EvalIcon,
  LibraryIcon,
  PlusIcon,
  RegisterIcon,
  SessionsIcon,
  SettingsIcon,
  SwitcherIcon,
} from './icons'
import { usePrimaryAction } from './primaryAction'
import SessionsList, { recentRuns, type SearchMode, type SessionActions } from './SessionsList'
import WorkspaceSwitcher from './WorkspaceSwitcher'
import './sidebar.css'

export { CARD_CLOSE_MS, CARD_OPEN_MS, SHOWN_LIMIT, recentRuns, shortAge, splitRuns } from './SessionsList'
export type { SessionActions } from './SessionsList'

/** How long the notes search waits after a keystroke before it asks the service. */
export const NOTES_SEARCH_DEBOUNCE_MS = 250

type NavName = 'sessions' | 'board' | 'register' | 'eval' | 'library' | 'settings'

const ROWS: { name: NavName; label: string; icon: JSX.Element }[] = [
  { name: 'sessions', label: 'Sessions', icon: <SessionsIcon /> },
  { name: 'board', label: 'Board', icon: <BoardIcon /> },
  { name: 'register', label: 'Register', icon: <RegisterIcon /> },
  { name: 'eval', label: 'Eval', icon: <EvalIcon /> },
  { name: 'library', label: 'Library', icon: <LibraryIcon /> },
  { name: 'settings', label: 'Settings', icon: <SettingsIcon /> },
]

/**
 * Below this window width the sidebar keeps its icons and drops its labels:
 * the narrow band of `styles/tokens.css`, where the sheet needs every pixel.
 */
export const RAIL_AT = BELOW_COMPACT

/**
 * The reader's own fold: the sidebar as the 56px rail at any width, on the
 * head's toggle or ⌘B, remembered in this browser. The automatic rail under
 * 1024 applies regardless.
 */
export const SIDEBAR_COLLAPSED_KEY = 'sirdar.sidebarCollapsed'

/** The nav row a screen belongs to. A run is reached from Sessions, a review from its run. */
function rowOf(screen: Screen): NavName {
  switch (screen.name) {
    case 'new':
    case 'run':
    case 'review':
      return 'sessions'
    default:
      return screen.name
  }
}

/**
 * The app's left edge: the wordmark, the workspace on its own row under it,
 * the six screens, the sessions list (`SessionsList`) with the search field
 * as its first line, and a footer card with how much of each provider's
 * plan is gone and the one button that starts a session.
 *
 * It replaces the top header. A board that scrolls horizontally has no room to
 * spare above it, and a nav that does not move is one less thing that can
 * cover a lane — which is the reason `docs/design/03-desktop-app.md` section 5
 * gives, and it is the same reason the reference it is read from did it. The
 * 2026-09-15 screens round re-scaled it to the reference's own register:
 * 248 wide, 44-tall rows, 20px icons.
 *
 * Settings is a row here but not a screen: it opens a modal over whatever is
 * behind it, because settings is a place you leave and the board staying
 * painted is what says you are coming back.
 */
function Sidebar(props: {
  workspaces: Workspace[]
  currentWorkspaceId: string
  quota: Quota[]
  screen: Screen
  /** The current workspace's runs, listed under the nav. */
  runs?: RunSummary[]
  /** The current workspace's tracker and helpdesk, for the marks beside each number. */
  sources?: SourcesSummary
  /** The clock the ages are read against; tests hold it still. */
  now?: number
  /** Inbound deliveries waiting to be read, badged on the Board row. */
  inboundCount?: number
  /** What a session row's menu can reach: delete, open, copy links, settings. */
  sessionActions?: SessionActions
  /** The "Search notes" mode's question to the service. Without it the toggle is not offered. */
  onSearchNotes?: (q: string) => Promise<SearchHit[]>
  onSelectWorkspace: (id: string) => void
  onAddWorkspace: () => void
  onNavigate: (screen: Screen) => void
}): JSX.Element {
  const {
    workspaces,
    currentWorkspaceId,
    quota,
    screen,
    runs = [],
    sources,
    now,
    inboundCount = 0,
    sessionActions,
    onSearchNotes,
    onSelectWorkspace,
    onAddWorkspace,
    onNavigate,
  } = props
  const library = useSyncExternalStore(subscribeShowLibrary, showLibrary, () => false)
  const primary = usePrimaryAction()
  const auto = useMediaQuery(RAIL_AT)
  const [folded, setFolded] = useState(() => readStoredFlag(SIDEBAR_COLLAPSED_KEY))
  const rail = auto || folded

  function toggleFold(): void {
    const next = !folded
    setFolded(next)
    writeStoredFlag(SIDEBAR_COLLAPSED_KEY, next)
  }

  // ⌘B, from anywhere in the window. Not while the width has already made
  // the choice: a chord that appears to do nothing is worse than none.
  useEffect(() => {
    if (auto) return
    function onKey(e: KeyboardEvent): void {
      if (!(e.metaKey || e.ctrlKey) || e.altKey || e.shiftKey) return
      if (e.key !== 'b' && e.key !== 'B') return
      e.preventDefault()
      const next = !folded
      setFolded(next)
      writeStoredFlag(SIDEBAR_COLLAPSED_KEY, next)
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [auto, folded])

  // The search: the field's text filters the list as it is typed; in the
  // notes mode the text, once still for a beat, goes to the service.
  const [query, setQuery] = useState('')
  const [mode, setMode] = useState<SearchMode>('sessions')
  const [hits, setHits] = useState<SearchHit[]>([])
  const [hitsState, setHitsState] = useState<'idle' | 'loading' | 'error'>('idle')
  const [hitsError, setHitsError] = useState('')
  const searchInput = useRef<HTMLInputElement | null>(null)
  const askedNotes = useDebounced(mode === 'notes' ? query.trim() : '', NOTES_SEARCH_DEBOUNCE_MS)
  const searchSeq = useRef(0)

  useEffect(() => {
    if (mode !== 'notes' || !onSearchNotes || !askedNotes) {
      setHits([])
      setHitsState('idle')
      return
    }
    const seq = (searchSeq.current += 1)
    setHitsState('loading')
    void onSearchNotes(askedNotes).then(
      (got) => {
        if (searchSeq.current !== seq) return
        setHits(got)
        setHitsState('idle')
      },
      (err: unknown) => {
        if (searchSeq.current !== seq) return
        setHitsError(err instanceof Error ? err.message : String(err))
        setHitsState('error')
      },
    )
  }, [mode, askedNotes, onSearchNotes, currentWorkspaceId])

  // ⌘K puts the cursor in the search field, from anywhere in the window.
  // In the rail the field is not drawn: a fold this person made is undone
  // first; a fold the width made is not, and the chord does nothing.
  const focusSearch = useCallback(() => {
    const el = searchInput.current
    if (!el) return
    el.focus()
    el.select()
  }, [])
  const wantFocus = useRef(false)
  useEffect(() => {
    if (wantFocus.current && !rail) {
      wantFocus.current = false
      focusSearch()
    }
  }, [rail, focusSearch])
  useEffect(() => {
    function onKey(e: KeyboardEvent): void {
      if (!(e.metaKey || e.ctrlKey) || e.altKey || e.shiftKey) return
      if (e.key !== 'k' && e.key !== 'K') return
      if (auto) return
      e.preventDefault()
      if (folded) {
        wantFocus.current = true
        setFolded(false)
        writeStoredFlag(SIDEBAR_COLLAPSED_KEY, false)
        return
      }
      focusSearch()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [auto, folded, focusSearch])

  function onSearchKey(e: React.KeyboardEvent<HTMLInputElement>): void {
    if (e.key !== 'Escape') return
    e.preventDefault()
    e.stopPropagation()
    if (query) setQuery('')
    else e.currentTarget.blur()
  }

  const openRun = useCallback((runId: string) => onNavigate({ name: 'run', runId }), [onNavigate])

  const current = rowOf(screen)
  const rows = library ? ROWS : ROWS.filter((row) => row.name !== 'library')
  const currentRunId =
    screen.name === 'run' || screen.name === 'review' ? screen.runId : undefined
  const newest = recentRuns(runs, 1)[0]
  const workspaceName = workspaces.find((w) => w.id === currentWorkspaceId)?.name

  function open(name: NavName): void {
    if (name === 'sessions') {
      // Sessions is the session that changed last, or a new one when the
      // workspace has none yet.
      onNavigate(newest ? { name: 'run', runId: newest.runId } : { name: 'new' })
      return
    }
    onNavigate({ name })
  }

  return (
    <div className="sd-sidebar" data-collapsed={rail ? 'true' : undefined}>
      <div className="sd-sidebar__brand">
        {/* The mark is decoration here: the word beside it already says the
            product, and a second accessible name would read it twice. */}
        <BrandMark decorative />
        <span className="brand">Sirdar</span>
        <PanelToggle
          side="start"
          open={!rail}
          hideLabel="Hide sidebar"
          showLabel="Show sidebar"
          shortcut="⌘B"
          disabled={auto}
          disabledReason="The sidebar is a rail at this width"
          onToggle={toggleFold}
        />
      </div>

      {/* The workspace on its own row, the sidebar's width, so its name is
          never cut to make room for the wordmark. */}
      <div className="sd-sidebar__workspace">
        <WorkspaceSwitcher
          workspaces={workspaces}
          currentId={currentWorkspaceId}
          onSelect={onSelectWorkspace}
          onAdd={onAddWorkspace}
        />
      </div>

      <nav className="sd-sidebar__nav" aria-label="Screens">
        {rows.map((row) => (
          <SidebarNavItem
            key={row.name}
            icon={row.icon}
            label={row.label}
            current={row.name === current}
            count={row.name === 'board' ? inboundCount : undefined}
            countLabel={row.name === 'board' ? 'inbound deliveries' : undefined}
            title={rail ? row.label : undefined}
            onSelect={() => open(row.name)}
          />
        ))}
      </nav>

      <SessionsList
        runs={runs}
        now={now}
        sources={sources}
        workspaceName={workspaceName}
        workspaceId={currentWorkspaceId}
        currentRunId={currentRunId}
        rail={rail}
        onOpen={openRun}
        actions={sessionActions}
        head={
          // The search is the sessions section's first line, pinned there
          // while the rows scroll under it. It goes with the list: no runs,
          // no field.
          <div className="sd-sidebar__search">
            <SearchBar
              variant="well"
              label={mode === 'notes' ? 'Search notes' : 'Search sessions'}
              placeholder={mode === 'notes' ? 'Search notes' : 'Search sessions'}
              value={query}
              onChange={setQuery}
              inputRef={searchInput}
              onKeyDown={onSearchKey}
              aside={
                <>
                  {onSearchNotes ? (
                    <button
                      type="button"
                      className="sd-sidebar__search-mode"
                      aria-pressed={mode === 'notes'}
                      title={mode === 'notes' ? 'Back to filtering the sessions' : 'Search inside the notes and answers'}
                      onClick={() => setMode((m) => (m === 'notes' ? 'sessions' : 'notes'))}
                    >
                      Notes
                    </button>
                  ) : null}
                  <kbd className="sd-sidebar__kbd" aria-hidden="true">
                    ⌘K
                  </kbd>
                </>
              }
            />
          </div>
        }
        query={query}
        mode={mode}
        hits={hits}
        hitsState={hitsState}
        hitsError={hitsError}
      />

      <SidebarFooterCard
        label="Plan usage"
        title={
          <>
            <span>Plan usage</span>
            <SwitcherIcon />
          </>
        }
        quotas={quota.length > 0 ? <QuotaMeter quota={quota} /> : undefined}
        action={
          primary && primary.placement !== 'screen' ? (
            <Button
              variant="primary"
              busy={primary.busy}
              shortcut={primary.shortcut}
              disabled={primary.disabled}
              title={primary.title}
              onClick={primary.onRun}
            >
              {primary.label}
            </Button>
          ) : (
            // Demoted to the bordered style while a screen draws its own
            // filled button, so the window never has two. In the rail the
            // words go and a plus stays, named the same.
            <Button
              variant={primary ? 'secondary' : 'primary'}
              size={rail ? 'sm' : 'md'}
              iconOnly={rail}
              icon={rail ? <PlusIcon /> : undefined}
              title={rail ? 'New session' : undefined}
              onClick={() => onNavigate({ name: 'new' })}
            >
              New session
            </Button>
          )
        }
      />
    </div>
  )
}

/**
 * Memoised: the shell re-renders for every slice a screen reads, and the
 * sidebar's inputs — the runs, the quota, the screen — change far less often
 * than that. The callbacks it is given are stable, so a render of the shell
 * that changed none of them costs the sidebar nothing.
 */
export default memo(Sidebar)
