import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  useSyncExternalStore,
  type JSX,
  type KeyboardEvent as ReactKeyboardEvent,
} from 'react'
import type { FixStart, NoteKind, RunDiff, SourcesSummary, Transport } from '../../api/types'
import type { ComposerMode } from '../../components/run/Composer'
import { LIVE, useRunFeed } from '../../components/run/useRunFeed'
import { useProvidePrimaryAction } from '../../components/shell/primaryAction'
import { parseTime, reasonOf } from '../../lib/format'
import { clearRunJob, getRunJob, setRunJob, subscribeRunJobs } from '../../lib/jobs'
import { readStoredFlag, writeStoredFlag } from '../../lib/storedFlag'
import { stateWord } from '../../ui/status-badge'
import ActivityRail from './workbench/ActivityRail'
import AnswerCard from './workbench/AnswerCard'
import type { Bundle } from './workbench/bundle'
import BundleView from './workbench/BundleView'
import ChangesView from './workbench/ChangesView'
import ComposerCard from './workbench/ComposerCard'
import Console, { CONSOLE_COLLAPSED_KEY, CONSOLE_DEFAULT_HEIGHT, CONSOLE_HEIGHT_KEY, CONSOLE_MIN_HEIGHT } from './workbench/Console'
import { CollapseIcon } from './workbench/icons'
import {
  buildRows,
  closingRow,
  consoleCounts,
  countsLabel,
  isRailCell,
  modelOf,
  pendingQuestion,
  railItems,
  type ConsoleFilter,
  type ConsoleRow,
  type Permissions,
} from './workbench/model'
import NoteDocument from './workbench/NoteDocument'
import RunHeader from '../../components/session/RunHeader'
import { useSessionModel } from './model'
import { evidenceOf } from '../../components/session/model'
import { deriveEvidenceMarkers, stepLikeOf } from '../../lib/evidence'
import './session-workbench.css'

/**
 * Layout C, the Workbench: the session window as an IDE rather than a chat.
 * The run is a program the reader attaches to, and the window shows its
 * state, its documents and its log at once — a 60px run header with the
 * budget gauges, a 56px activity rail of the turns, a document area with
 * tabs (Answer, Note, Diff, Bundle) and an outline, a collapsible and
 * resizable console rendering events.jsonl as a structured log, and the
 * composer as one command bar at the very bottom.
 *
 * Reached through `screens/Session.tsx` when the `sirdar.sessionLayout`
 * preference says `workbench`. It reads the run the way the other layouts
 * do (`useRunFeed`) and builds its own model from the events in
 * `./workbench/model`; the blocks under `./workbench` are local adapters
 * carrying the shared blocks' names, to be swapped for
 * `src/components/session/*` when those land.
 */

type Tab = 'answer' | 'note' | 'diff' | 'bundle'

function useRunJob(runId: string): string | undefined {
  return useSyncExternalStore(
    subscribeRunJobs,
    () => getRunJob(runId),
    () => undefined,
  )
}

function isTyping(target: EventTarget | null): boolean {
  const el = target as HTMLElement | null
  if (!el || !el.tagName) return false
  const tag = el.tagName.toLowerCase()
  return tag === 'input' || tag === 'textarea' || tag === 'select' || el.isContentEditable
}

function withoutCode(err: unknown): string {
  return reasonOf(err).replace(/^(?:conflict|not_found|no_diff|forbidden|internal|unsupported):\s*/, '')
}

function readHeight(): number {
  try {
    const n = Number(globalThis.localStorage?.getItem(CONSOLE_HEIGHT_KEY))
    return Number.isFinite(n) && n >= CONSOLE_MIN_HEIGHT ? n : CONSOLE_DEFAULT_HEIGHT
  } catch {
    return CONSOLE_DEFAULT_HEIGHT
  }
}

function writeHeight(h: number): void {
  try {
    globalThis.localStorage?.setItem(CONSOLE_HEIGHT_KEY, String(Math.round(h)))
  } catch {
    // Remembered for this window only.
  }
}

/** `17:41:05`, the run's start as a clock in the reader's zone. */
function clockOf(t: string | undefined): string {
  const ms = parseTime(t)
  if (Number.isNaN(ms)) return ''
  return new Date(ms).toLocaleTimeString('en-GB', { hour: '2-digit', minute: '2-digit', second: '2-digit' })
}

export interface SessionWorkbenchProps {
  transport: Transport
  workspaceId: string
  runId: string
  title?: string
  notesDir?: string
  sources?: SourcesSummary
  onBack: () => void
  onOpenReview: () => void
  onStartFix?: (key: string, opts?: FixStart) => Promise<void> | void
}

export default function SessionWorkbench(props: SessionWorkbenchProps): JSX.Element {
  const { transport, workspaceId, runId, title, notesDir, sources, onBack, onOpenReview, onStartFix } = props
  const { detail, setDetail, events, setEvents, loadError, finished } = useRunFeed(transport, workspaceId, runId)
  const jobId = useRunJob(runId)

  // ---- state --------------------------------------------------------------
  const [tab, setTab] = useState<Tab | null>(null)
  const [filter, setFilter] = useState<ConsoleFilter>('all')
  const [query, setQuery] = useState('')
  const [follow, setFollow] = useState<boolean | null>(null)
  const [collapsed, setCollapsedState] = useState(() => readStoredFlag(CONSOLE_COLLAPSED_KEY))
  const [maximised, setMaximised] = useState(false)
  const [height, setHeightState] = useState(readHeight)
  const [expanded, setExpanded] = useState<Set<string>>(() => new Set())
  const [scrollTo, setScrollTo] = useState<{ key: string; n: number } | undefined>()
  const [visibleTurns, setVisibleTurns] = useState<Set<string>>(() => new Set())
  const [pending, setPending] = useState('')
  const [actionError, setActionError] = useState('')
  const [steerRefusal, setSteerRefusal] = useState('')
  const [sent, setSent] = useState(0)
  const [prefill, setPrefill] = useState<{ text: string; n: number } | undefined>()
  const [permissions, setPermissions] = useState<Permissions | undefined>()
  const [noteCount, setNoteCount] = useState<number | null>(null)
  const [diffFiles, setDiffFiles] = useState<number | null>(null)
  const [bundle, setBundle] = useState<Bundle | null>(null)
  const searchRef = useRef<HTMLInputElement | null>(null)

  const status = detail?.status ?? ''
  const live = LIVE.has(status)
  const isFix = detail?.kind === 'fix'

  useEffect(() => {
    setTab(null)
    setQuery('')
    setExpanded(new Set())
    setActionError('')
    setSteerRefusal('')
    setFollow(null)
    setNoteCount(null)
    setDiffFiles(null)
    setBundle(null)
  }, [runId])

  useEffect(() => {
    setSteerRefusal('')
  }, [status])

  // The allow-lists, so a shell row can name the pattern that let it through.
  useEffect(() => {
    let cancelled = false
    transport
      .configSummary(workspaceId)
      .then((c) => {
        if (!cancelled) setPermissions({ bash: c.permissions.bash ?? [], fixBash: c.permissions.fixBash ?? [] })
      })
      .catch(() => {
        // Without the lists the rows still say allow or deny, without the rule.
      })
    return () => {
      cancelled = true
    }
  }, [transport, workspaceId])

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape' && !isTyping(e.target)) {
        onBack()
        return
      }
      // ⌘F searches the transcript rather than the page.
      if (e.key === 'f' && (e.metaKey || e.ctrlKey) && !e.altKey && !e.shiftKey) {
        e.preventDefault()
        setCollapsedState(false)
        searchRef.current?.focus()
        searchRef.current?.select()
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [onBack])

  const setCollapsed = useCallback((v: boolean) => {
    setCollapsedState(v)
    writeStoredFlag(CONSOLE_COLLAPSED_KEY, v)
  }, [])
  const setHeight = useCallback((h: number) => {
    setHeightState(h)
    writeHeight(h)
  }, [])

  // ---- model --------------------------------------------------------------
  // The shared reading of the run — calls with their paths shortened, the
  // policy's word, the answer, the report, the checks — and the console's
  // own rows built on it.
  const session = useSessionModel(events, detail)
  const runEvents = useMemo(() => events.map((e) => e.event), [events])
  const rows = useMemo<ConsoleRow[]>(() => {
    const built = buildRows(events, { startedAt: detail?.startedAt, kind: detail?.kind, permissions, calls: session.calls })
    const closing = detail ? closingRow(detail, notesDir) : undefined
    if (!closing) return built
    // The run ended at updatedAt; a review filed after that follows it.
    const at = built.findIndex((r) => Number.isFinite(closing.atMs) && r.atMs > closing.atMs)
    return at === -1 ? [...built, closing] : [...built.slice(0, at), closing, ...built.slice(at)]
  }, [events, detail, permissions, notesDir, session.calls])
  const rail = useMemo(() => railItems(events, status), [events, status])
  const counts = useMemo(() => consoleCounts(rows), [rows])
  const answerText = useMemo(() => {
    for (let i = runEvents.length - 1; i >= 0; i -= 1) if (runEvents[i].kind === 'final') return runEvents[i].payload?.text ?? ''
    return ''
  }, [runEvents])
  const answer = isFix ? undefined : session.answer
  // E1…En off the answer's evidence, on the calls that produced each item.
  const markers = useMemo(
    () => deriveEvidenceMarkers(evidenceOf(answer), rows.filter((r) => r.call).map((r) => stepLikeOf(r.index, r.call!.started.event))),
    [answer, rows],
  )
  const report = isFix ? session.report : undefined
  const checks = session.checks
  const question = useMemo(() => (detail ? pendingQuestion(detail, events) : undefined), [detail, events])
  const dropped = useMemo(
    () =>
      runEvents
        .filter((e) => e.kind === 'review' && e.payload?.action === 'drop' && e.payload.path)
        .map((e) => ({ path: e.payload.path as string, hunk: e.payload.hunk ?? 0 })),
    [runEvents],
  )
  const noteKinds = useMemo<NoteKind[]>(() => {
    if (detail?.kind === 'rca') return ['rca', 'resolution']
    if (detail?.kind === 'fix') return ['']
    return ['triage']
  }, [detail?.kind])

  const turnOf = useCallback((row: ConsoleRow) => rail.turnOf.get(row.index), [rail])
  const turnLabelOf = useCallback(
    (row: ConsoleRow) => {
      const key = rail.turnOf.get(row.index)
      if (!key) return undefined
      const cell = rail.items.find((i) => isRailCell(i) && i.key === key)
      if (!cell || !isRailCell(cell)) return undefined
      const segment = Number(/^s(\d+)-/.exec(key)?.[1] ?? 0)
      return segment > 0 ? `turn ${cell.n} after steer` : `turn ${cell.n}`
    },
    [rail],
  )

  const shownTab: Tab = tab ?? (isFix ? 'diff' : answer ? 'answer' : 'note')
  // state.json records the model only when the config names one; the
  // provider names it on its init line and on every assistant line.
  const model = detail?.model || session.start?.model || modelOf(events)
  const following = follow ?? live

  // ---- actions ------------------------------------------------------------
  const noteOwnLine = useCallback(
    (text: string) => {
      setEvents((prev) => {
        const last = prev.length > 0 ? prev[prev.length - 1].index : 0
        return [...prev, { index: last + 0.5, event: { t: new Date().toISOString(), kind: 'answer', payload: { text } } }]
      })
    },
    [setEvents],
  )

  const answerRun = useCallback(
    async (text: string) => {
      setPending('answer')
      setActionError('')
      try {
        const started = await transport.resume(workspaceId, runId, text)
        if (started?.jobId) setRunJob(runId, started.jobId)
        if (text) noteOwnLine(text)
        setSent((n) => n + 1)
      } catch (err: unknown) {
        setActionError(withoutCode(err))
      } finally {
        setPending('')
      }
    },
    [transport, workspaceId, runId, noteOwnLine],
  )

  const steer = useCallback(
    async (text: string) => {
      setPending('steer')
      setActionError('')
      try {
        const started = await transport.steer(workspaceId, runId, text)
        if (started?.jobId) setRunJob(runId, started.jobId)
        setSent((n) => n + 1)
        setDetail((prev) => (prev ? { ...prev, status: 'running' } : prev))
        noteOwnLine(text)
        setFollow(true)
      } catch (err: unknown) {
        if (/^conflict:|refused/i.test(reasonOf(err))) setSteerRefusal(withoutCode(err))
        else setActionError(withoutCode(err))
      } finally {
        setPending('')
      }
    },
    [transport, workspaceId, runId, setDetail, noteOwnLine],
  )

  const cancel = useCallback(async () => {
    if (!jobId) return
    setPending('cancel')
    setActionError('')
    try {
      await transport.cancel(jobId)
      clearRunJob(runId)
    } catch (err: unknown) {
      setActionError(withoutCode(err))
    } finally {
      setPending('')
    }
  }, [jobId, transport, runId])

  const acceptDeviation = useCallback(async () => {
    if (!detail || !onStartFix) return
    setPending('accept')
    setActionError('')
    try {
      await onStartFix(detail.key, { acceptDeviation: true })
    } catch (err: unknown) {
      setActionError(withoutCode(err))
    } finally {
      setPending('')
    }
  }, [detail, onStartFix])

  const mode: ComposerMode = useMemo(() => {
    if (!detail) return { kind: 'disabled', reason: 'Loading the run…' }
    if (steerRefusal) return { kind: 'disabled', reason: steerRefusal }
    switch (detail.status) {
      case 'blocked':
        return { kind: 'answer', question: question?.text ?? '' }
      case 'completed':
      case 'failed':
        return { kind: 'steer' }
      case 'over_budget':
        return { kind: 'disabled', reason: 'Over budget: a run at its cap cannot be steered.' }
      default:
        return { kind: 'running' }
    }
  }, [detail, question, steerRefusal])

  const send = mode.kind === 'answer' ? answerRun : steer
  const sendBusy = pending === 'answer' || pending === 'steer'

  // The screen's one filled control, whichever it is: the bar's send, or
  // the Stop that stands in its place while the run works.
  useProvidePrimaryAction(
    detail
      ? {
          label: mode.kind === 'answer' ? 'Answer' : mode.kind === 'running' ? 'Stop' : 'Send',
          onRun: () => {},
          disabled: mode.kind === 'disabled' || (mode.kind === 'running' && !jobId),
          busy: mode.kind === 'running' ? pending === 'cancel' : sendBusy,
          shortcut: mode.kind === 'running' ? undefined : '↵',
          title: mode.kind === 'disabled' ? mode.reason : undefined,
          placement: 'screen',
        }
      : null,
  )

  const toggleRow = useCallback((id: string) => {
    setExpanded((prev) => {
      const next = new Set(prev)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })
  }, [])

  /** A file:line chip: search the console for the calls that read the file. */
  const findInConsole = useCallback((ref: string) => {
    const name = ref.split(':')[0].split(/[\\/]/).pop() ?? ref
    setQuery(name)
    setCollapsedState(false)
    setFilter('all')
  }, [])

  const onDiffLoaded = useCallback((diff: RunDiff | null) => setDiffFiles(diff ? diff.files.length : null), [])
  const onNoteLoaded = useCallback((n: number) => setNoteCount(n), [])
  const onBundleLoaded = useCallback((doc: Bundle | null) => setBundle(doc), [])
  // Only the desktop shell can reveal a folder, so a browser gets no menu
  // item and the pane never shows a path it cannot act on.
  const openBundleFolder = useMemo(
    () => (transport.openRunDir ? () => void transport.openRunDir!(workspaceId, runId) : undefined),
    [transport, workspaceId, runId],
  )

  // ---- render -------------------------------------------------------------
  if (loadError || !detail) {
    return (
      <div className="wb">
        <p className={loadError ? 'wb-failed' : 'wb-loading'}>{loadError || 'Loading run…'}</p>
      </div>
    )
  }

  const turns = detail.usage?.turns ?? 0
  const placeholder =
    mode.kind === 'answer'
      ? `Type an answer${question && question.options.length > 0 ? ', or pick an option above' : ''} — the run resumes from turn ${turns}`
      : `Follow up — the run resumes from turn ${turns}${isFix ? ' in the same worktree' : ' with the note in context'}`

  const tabs: { id: Tab; label: string; n?: string; off?: boolean }[] = [
    { id: 'answer', label: 'Answer', off: !answer && !report && !answerText },
    { id: 'note', label: 'Note', n: noteCount === null ? (isFix ? undefined : noteKinds[0]) : noteCount === 0 ? 'none' : noteCount > 1 ? String(noteCount) : noteKinds[0] || undefined },
    { id: 'diff', label: 'Diff', n: diffFiles !== null ? String(diffFiles) : undefined, off: !isFix },
    { id: 'bundle', label: 'Bundle', n: bundle ? `ticket · ${bundle.thread.length}` : undefined },
  ]
  const tabIndex = Math.max(tabs.findIndex((t) => t.id === shownTab), 0)
  const onTabKey = (e: ReactKeyboardEvent<HTMLDivElement>) => {
    let to: number
    switch (e.key) {
      case 'ArrowRight':
        to = tabIndex + 1
        break
      case 'ArrowLeft':
        to = tabIndex - 1
        break
      case 'Home':
        to = 0
        break
      case 'End':
        to = tabs.length - 1
        break
      default:
        return
    }
    e.preventDefault()
    for (let n = 0; n < tabs.length; n += 1) {
      const at = (((to + n * (to >= tabIndex ? 1 : -1)) % tabs.length) + tabs.length) % tabs.length
      if (!tabs[at].off) {
        setTab(tabs[at].id)
        e.currentTarget.querySelectorAll<HTMLButtonElement>('.wb-dtab')[at]?.focus()
        return
      }
    }
  }

  const aside = isFix
    ? `run ${detail.runId}${detail.fix?.branch ? ` · branch ${detail.fix.branch}` : ''}`
    : `run ${detail.runId} · started ${clockOf(detail.startedAt)}`

  return (
    <div className="wb" data-testid="session-workbench">
      <RunHeader
        variant="workbench"
        detail={detail}
        title={title}
        sources={sources}
        fallbackModel={model}
        transport={transport}
        workspaceId={workspaceId}
      />
      <span className="visually-hidden" aria-live="polite">{`Run ${stateWord(detail.status)}`}</span>
      {/* The bar carries what a send or a stop came back with; this line is
          for the errors no composer is up to hold. */}
      {actionError && mode.kind === 'disabled' ? (
        <p className="wb-failed-line" role="alert">
          {actionError}
        </p>
      ) : null}

      <div className="wb-body">
        <ActivityRail items={rail.items} current={visibleTurns} onPick={(key) => setScrollTo({ key, n: (scrollTo?.n ?? 0) + 1 })} />
        <div className="wb-centre">
          <div className="wb-dtabs" role="tablist" aria-label="Documents" onKeyDown={onTabKey}>
            {tabs.map((t, i) => (
              <button
                key={t.id}
                type="button"
                role="tab"
                className="wb-dtab"
                aria-selected={shownTab === t.id}
                aria-disabled={t.off ? true : undefined}
                data-on={shownTab === t.id ? 'true' : undefined}
                data-off={t.off ? 'true' : undefined}
                tabIndex={i === tabIndex ? 0 : -1}
                title={t.off ? (t.id === 'diff' ? 'Only a fix run has a change' : 'No answer yet') : undefined}
                onClick={() => {
                  if (t.off) return
                  setTab(t.id)
                  setMaximised(false)
                }}
              >
                {t.label}
                {t.n ? <span className="wb-dtab__n">{t.n}</span> : null}
              </button>
            ))}
            <span className="wb-dtabs__aside">
              {maximised ? (
                <button type="button" className="wb-linkbtn wb-linkbtn--icon" onClick={() => setMaximised(false)}>
                  documents collapsed <CollapseIcon />
                </button>
              ) : (
                aside
              )}
            </span>
          </div>

          {maximised ? null : (
            <div className="wb-docarea" role="tabpanel" aria-label={tabs[tabIndex].label}>
              {shownTab === 'answer' ? <AnswerCard variant="document" answer={answer} text={answerText} report={report} onRef={findInConsole} /> : null}
              {shownTab === 'note' ? (
                <NoteDocument
                  transport={transport}
                  workspaceId={workspaceId}
                  runId={runId}
                  kinds={noteKinds}
                  reload={finished}
                  notePaths={detail.notes}
                  notesDir={notesDir}
                  onLoaded={onNoteLoaded}
                />
              ) : null}
              {shownTab === 'diff' && isFix ? (
                <ChangesView
                  transport={transport}
                  workspaceId={workspaceId}
                  runId={runId}
                  detail={detail}
                  checks={checks}
                  report={report}
                  dropped={dropped}
                  reload={finished}
                  onLoaded={onDiffLoaded}
                  onOpenReview={onOpenReview}
                  onAcceptDeviation={onStartFix ? () => void acceptDeviation() : undefined}
                  acceptPending={pending === 'accept'}
                />
              ) : null}
              {shownTab === 'bundle' ? (
                <BundleView
                  transport={transport}
                  workspaceId={workspaceId}
                  runId={runId}
                  assignee={detail.assignee}
                  helpdeskKey={detail.helpdeskKey}
                  onOpenFolder={openBundleFolder}
                  onLoaded={onBundleLoaded}
                />
              ) : null}
            </div>
          )}

          <Console
            rows={rows}
            markers={markers}
            countsLabel={countsLabel(counts)}
            usage={detail.usage}
            filter={filter}
            onFilter={(f) => {
              setFilter(f)
              if (f === 'tools' && !maximised) setCollapsedState(false)
            }}
            query={query}
            onQuery={setQuery}
            searchRef={searchRef}
            follow={following}
            onFollow={setFollow}
            live={live}
            collapsed={collapsed}
            onCollapsed={setCollapsed}
            maximised={maximised}
            onMaximised={setMaximised}
            height={height}
            onHeight={setHeight}
            expanded={expanded}
            onToggleRow={toggleRow}
            question={question}
            startedAt={detail.startedAt}
            startedMs={parseTime(detail.startedAt)}
            onPickOption={(text) => setPrefill({ text, n: (prefill?.n ?? 0) + 1 })}
            turnLabelOf={turnLabelOf}
            turnOf={turnOf}
            scrollTo={scrollTo}
            onVisibleTurns={setVisibleTurns}
          />

          <ComposerCard
            variant="strip"
            mode={mode}
            busy={sendBusy}
            error={mode.kind === 'disabled' ? '' : actionError}
            onSend={(text) => void send(text)}
            provider={detail.provider}
            model={model}
            kind={detail.kind}
            sentCount={sent}
            placeholder={placeholder}
            prefill={prefill}
            onStop={() => void cancel()}
            canStop={Boolean(jobId) && pending === ''}
            stopBusy={pending === 'cancel'}
          />
        </div>
      </div>
    </div>
  )
}
