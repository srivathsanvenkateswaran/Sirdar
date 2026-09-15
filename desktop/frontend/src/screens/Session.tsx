import {
  useCallback,
  useEffect,
  useId,
  useMemo,
  useState,
  useSyncExternalStore,
  type JSX,
  type KeyboardEvent as ReactKeyboardEvent,
} from 'react'
import type { FixStart, NoteKind, RunDiff, Transport } from '../api/types'
import { askedQuestion, elapsed } from '../lib/events'
import { costOrUnknown, reasonOf } from '../lib/format'
import { checksFromEvents, describeTests, latestStep, noteName } from '../lib/review'
import { clearRunJob, getRunJob, setRunJob, subscribeRunJobs } from '../lib/jobs'
import BundleView from '../components/run/BundleView'
import ChangesPane, { withoutCode } from '../components/run/ChangesPane'
import Composer, { type ComposerMode } from '../components/run/Composer'
import EventStream from '../components/run/EventStream'
import NoteView from '../components/run/NoteView'
import ToolsPane, { toolCount } from '../components/run/ToolsPane'
import { LIVE, TERMINAL, useRunFeed } from '../components/run/useRunFeed'
import { useProvidePrimaryAction } from '../components/shell/primaryAction'
import Banner from '../ui/banner'
import Button from '../ui/button'
import KindChip from '../ui/kind-chip'
import ProviderMark from '../ui/provider-mark'
import StatusBadge, { stateWord, type SdStatus } from '../ui/status-badge'
import '../components/run/run.css'

/**
 * Cancel only works for a job this window started. `lib/jobs` holds the run →
 * job pairing the store fills in as the runs appear; until it has one, the
 * Cancel button says why it cannot act rather than failing when pressed.
 */
function useRunJob(runId: string): string | undefined {
  return useSyncExternalStore(
    subscribeRunJobs,
    () => getRunJob(runId),
    () => undefined,
  )
}

type Tab = 'changes' | 'note' | 'bundle' | 'tools'

/** The reading direction the tabs are laid out in, from the nearest `dir`. */
function directionOf(node: HTMLElement | null): 'ltr' | 'rtl' {
  const dir = node?.closest('[dir]')?.getAttribute('dir')
  if (dir === 'rtl' || dir === 'ltr') return dir
  return typeof document !== 'undefined' && document.dir === 'rtl' ? 'rtl' : 'ltr'
}

/** Keys typed into a field belong to that field, not to the window. */
function isTyping(target: EventTarget | null): boolean {
  const el = target as HTMLElement | null
  if (!el || !el.tagName) return false
  const tag = el.tagName.toLowerCase()
  return tag === 'input' || tag === 'textarea' || tag === 'select' || el.isContentEditable
}

/**
 * The session: the run's transcript on the left with the composer under it,
 * and the run's artefacts on the right.
 *
 * Reached at `#/runs/<workspace>/<run>`. It reads the run once, subscribes
 * for what happens after, and the banner and the composer follow the run's
 * state from there: Answer while the agent is waiting on a question, Steer
 * once the run has finished, disabled with the reason in between.
 */
export default function Session(props: {
  transport: Transport
  workspaceId: string
  runId: string
  /** The ticket's title, when the tracker's queue lists it. */
  title?: string
  /** The workspace's notes directory, so a filed note is named as the vault names it. */
  notesDir?: string
  onBack: () => void
  /** Opens the change review for this run. */
  onOpenReview: () => void
  /** Reruns the fix with the deviation accepted; the shell owns the job. */
  onStartFix?: (key: string, opts?: FixStart) => Promise<void> | void
}): JSX.Element {
  const { transport, workspaceId, runId, title, notesDir, onBack, onOpenReview, onStartFix } = props
  const { detail, setDetail, events, setEvents, loadError, finished } = useRunFeed(
    transport,
    workspaceId,
    runId,
  )
  const [tab, setTab] = useState<Tab | null>(null)
  const [pending, setPending] = useState('')
  const [actionError, setActionError] = useState('')
  /** Why the provider refused the last steer; the composer stays disabled with it. */
  const [steerRefusal, setSteerRefusal] = useState('')
  /** How many sends went through, so the composer knows to clear. */
  const [sent, setSent] = useState(0)
  const [changed, setChanged] = useState<number | null>(null)
  const [now, setNow] = useState(() => Date.now())
  const jobId = useRunJob(runId)
  const tabsId = useId()

  const status = detail?.status ?? ''
  const live = LIVE.has(status)
  const terminal = TERMINAL.has(status)

  // What this screen holds about a run is about that run alone.
  useEffect(() => {
    setActionError('')
    setSteerRefusal('')
    setTab(null)
    setChanged(null)
  }, [runId])

  // A refusal is about the run as it was; once the run moves it no longer holds.
  useEffect(() => {
    setSteerRefusal('')
  }, [status])

  useEffect(() => {
    if (!live) return
    const id = setInterval(() => setNow(Date.now()), 1000)
    return () => clearInterval(id)
  }, [live])

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== 'Escape' || isTyping(e.target)) return
      onBack()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [onBack])

  /*
   * Which notes the Note tab asks for. A fix run has no triage note of its
   * own — asking for one is a mismatch the service refuses with a 404 — so it
   * asks for the empty kind, which is whatever note.md the run itself wrote.
   */
  const noteKinds = useMemo<NoteKind[]>(() => {
    if (detail?.kind === 'rca') return ['rca', 'resolution']
    if (detail?.kind === 'fix') return ['']
    return ['triage']
  }, [detail?.kind])

  const notePath = detail?.notes?.[0] ?? ''
  const isFix = detail?.kind === 'fix'
  const shownTab: Tab = tab ?? (isFix ? 'changes' : 'note')

  const runEvents = useMemo(() => events.map((e) => e.event), [events])
  const checks = useMemo(() => checksFromEvents(runEvents), [runEvents])
  const step = useMemo(() => latestStep(runEvents), [runEvents])
  const tools = useMemo(() => toolCount(events), [events])

  const onDiffLoaded = useCallback((diff: RunDiff | null) => {
    setChanged(diff ? diff.files.length : null)
  }, [])

  /** Adds the operator's own answer to the transcript; the log does not record it. */
  const noteAnswer = useCallback((text: string) => {
    if (!text) return
    setEvents((prev) => {
      const last = prev.length > 0 ? prev[prev.length - 1].index : 0
      return [
        ...prev,
        {
          index: last + 0.5,
          event: { t: new Date().toISOString(), kind: 'answer', payload: { text } },
        },
      ]
    })
  }, [])

  const answer = useCallback(
    async (text: string) => {
      setPending('answer')
      setActionError('')
      try {
        const started = await transport.resume(workspaceId, runId, text)
        if (started?.jobId) setRunJob(runId, started.jobId)
        noteAnswer(text)
        setSent((n) => n + 1)
      } catch (err: unknown) {
        setActionError(withoutCode(err))
      } finally {
        setPending('')
      }
    },
    [transport, workspaceId, runId, noteAnswer],
  )

  const steer = useCallback(
    async (text: string) => {
      setPending('steer')
      setActionError('')
      try {
        const started = await transport.steer(workspaceId, runId, text)
        if (started?.jobId) setRunJob(runId, started.jobId)
        setSent((n) => n + 1)
        // The service moved state.json to running before answering; the
        // run.updated that says so is on its way, and the screen need not
        // wait for it to stop offering a second steer.
        setDetail((prev) => (prev ? { ...prev, status: 'running' } : prev))
        setEvents((prev) => {
          const last = prev.length > 0 ? prev[prev.length - 1].index : 0
          return [
            ...prev,
            {
              index: last + 0.5,
              event: { t: new Date().toISOString(), kind: 'answer', payload: { text } },
            },
          ]
        })
      } catch (err: unknown) {
        // The run's own state refuses it — 409 — or the provider cannot
        // continue at all. Either way the button says so and stays down
        // until the run moves.
        if (/^conflict:|refused/i.test(reasonOf(err))) setSteerRefusal(withoutCode(err))
        else setActionError(withoutCode(err))
      } finally {
        setPending('')
      }
    },
    [transport, workspaceId, runId],
  )

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

  const question = askedQuestion(detail?.reason)

  const mode: ComposerMode = useMemo(() => {
    if (!detail) return { kind: 'disabled', reason: 'Loading the run…' }
    if (steerRefusal) return { kind: 'disabled', reason: steerRefusal }
    switch (detail.status) {
      case 'blocked':
        return { kind: 'answer', question }
      case 'completed':
      case 'failed':
        return { kind: 'steer' }
      case 'over_budget':
        return { kind: 'disabled', reason: 'Over budget: a run at its cap cannot be steered.' }
      default:
        return { kind: 'disabled', reason: 'The run is still working. Wait for it, or cancel it.' }
    }
  }, [detail, question, steerRefusal])

  const send = mode.kind === 'answer' ? answer : steer
  const sendBusy = pending === 'answer' || pending === 'steer'

  // The composer's button is this screen's one filled control, drawn beside
  // the text it sends; the sidebar's New session steps down while it is up.
  useProvidePrimaryAction(
    detail
      ? {
          label: mode.kind === 'answer' ? 'Answer' : 'Steer',
          onRun: () => {},
          disabled: mode.kind === 'disabled',
          busy: sendBusy,
          shortcut: '⌘↵',
          title: mode.kind === 'disabled' ? mode.reason : undefined,
          placement: 'screen',
        }
      : null,
  )

  if (loadError || !detail) {
    return (
      <div className="session">
        <p className={loadError ? 'session-failed' : 'session-loading'}>
          {loadError || 'Loading run…'}
        </p>
      </div>
    )
  }

  const tabs: { id: Tab; label: string; count?: number }[] = [
    ...(isFix ? [{ id: 'changes' as const, label: 'Changes', count: changed ?? undefined }] : []),
    { id: 'note', label: 'Note' },
    { id: 'bundle', label: 'Bundle' },
    { id: 'tools', label: 'Tools', count: tools },
  ]

  const shownIndex = Math.max(
    tabs.findIndex((t) => t.id === shownTab),
    0,
  )

  /**
   * The tab list is one stop; the arrows move between tabs and select as
   * they go, the way a tab list is expected to. The arrows are read in the
   * reader's own direction, so in an Arabic pane the right arrow moves to
   * the tab on the right.
   */
  const onTabKey = (e: ReactKeyboardEvent<HTMLDivElement>) => {
    const rtl = directionOf(e.currentTarget) === 'rtl'
    let to: number
    switch (e.key) {
      case 'ArrowRight':
        to = rtl ? shownIndex - 1 : shownIndex + 1
        break
      case 'ArrowLeft':
        to = rtl ? shownIndex + 1 : shownIndex - 1
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
    const next = tabs[((to % tabs.length) + tabs.length) % tabs.length]
    setTab(next.id)
    e.currentTarget.querySelectorAll<HTMLButtonElement>('.session-tab')[tabs.indexOf(next)]?.focus()
  }

  let banner: JSX.Element | null = null
  if (step?.kind === 'tests') {
    banner = step.ok ? (
      <Banner tone="done" title="Tests passed">
        {describeTests(step, changed ?? step.filesChanged)}
      </Banner>
    ) : (
      <Banner tone="failed" title="Tests failed">
        {step.detail}
      </Banner>
    )
  } else if (step?.kind === 'note') {
    // The run's final event. A fix run ends in a commit, not a note; say
    // which, and on which branch. A triage or RCA run ends in a filed note.
    if (isFix) {
      banner = detail.fix?.commit ? (
        <Banner tone="ok" title="Fix committed">
          <span className="banner-path">{detail.fix.branch || detail.fix.commit.slice(0, 12)}</span>
        </Banner>
      ) : (
        <Banner tone="ok" title="Run finished" />
      )
    } else {
      banner = (
        <Banner tone="ok" title="Note filed">
          {notePath ? <span className="banner-path">{noteName(notePath, notesDir)}</span> : null}
        </Banner>
      )
    }
  }

  return (
    <div className="session">
      <header className="session-topbar">
        <h1 className="session-key">{detail.key}</h1>
        <KindChip kind={detail.kind} />
        <StatusBadge
          status={detail.status as SdStatus}
          // On this screen the reader is the one being waited on.
          detail={detail.status === 'blocked' ? 'waiting on you' : undefined}
        />
        <span className="session-title" title={title} dir="auto">
          {title ?? ''}
        </span>
        <div className="session-stats">
          <span className="session-stat session-provider">
            <ProviderMark provider={detail.provider} size="sm" />
            <span className="session-provider-name">{detail.provider}</span>
            {/* Never blank: a run that has not reported its model yet says so. */}
            <span className="session-provider-model" dir="ltr">
              {detail.model || 'model unknown'}
            </span>
          </span>
          <span className="session-stat">
            <b>{elapsed(detail, now)}</b>
          </span>
          <span className="session-stat">
            <b>{detail.usage?.turns ?? 0}</b> {detail.usage?.turns === 1 ? 'turn' : 'turns'}
          </span>
          <span className="session-stat">
            <b>{costOrUnknown(detail.usage?.costUsd, live)}</b>
          </span>
        </div>
        {/* Nothing is left to stop once the run has ended. */}
        {terminal ? null : (
          <Button
            variant="ghost"
            onClick={() => void cancel()}
            disabled={!jobId || pending !== ''}
            title={
              jobId
                ? 'Stop the run this window started'
                : 'Only a run started from this window can be cancelled'
            }
          >
            Cancel
          </Button>
        )}
      </header>

      {/* The state, for a screen reader, as it moves; the badge is what a sighted reader watches. */}
      <span className="visually-hidden" aria-live="polite">
        {`Run ${stateWord(detail.status)}`}
      </span>

      {actionError && pending !== 'accept' && mode.kind !== 'answer' && mode.kind !== 'steer' ? (
        <p className="session-failed-line" role="alert">
          {actionError}
        </p>
      ) : null}

      <div className="session-body">
        <div className="session-left">
          <EventStream
            events={events}
            startedAt={detail.startedAt}
            live={live}
            head={banner ? <div className="session-banner">{banner}</div> : null}
          />
          <Composer
            mode={mode}
            busy={sendBusy}
            error={mode.kind === 'disabled' ? '' : actionError}
            onSend={(text) => void send(text)}
            provider={detail.provider}
            model={detail.model}
            sentCount={sent}
          />
        </div>
        <div className="session-right">
          <div className="session-tabs" role="tablist" aria-label="Run artefacts" onKeyDown={onTabKey}>
            {tabs.map((t, i) => (
              <button
                key={t.id}
                type="button"
                role="tab"
                id={`${tabsId}-tab-${t.id}`}
                className="session-tab"
                aria-selected={shownTab === t.id}
                aria-controls={`${tabsId}-panel`}
                tabIndex={i === shownIndex ? 0 : -1}
                onClick={() => setTab(t.id)}
              >
                {t.label}
                {t.count !== undefined ? <span className="session-tab-n">{t.count}</span> : null}
              </button>
            ))}
          </div>
          <div
            className="session-panel"
            role="tabpanel"
            id={`${tabsId}-panel`}
            aria-labelledby={`${tabsId}-tab-${shownTab}`}
          >
            {shownTab === 'changes' && isFix ? (
              <ChangesPane
                transport={transport}
                workspaceId={workspaceId}
                runId={runId}
                checks={checks}
                fix={detail.fix}
                reload={finished}
                onLoaded={onDiffLoaded}
                onOpenReview={onOpenReview}
                onAcceptDeviation={onStartFix ? () => void acceptDeviation() : undefined}
                acceptPending={pending === 'accept'}
                acceptError={pending === 'accept' ? '' : actionError}
              />
            ) : null}
            {shownTab === 'note' ? (
              <NoteView
                transport={transport}
                workspaceId={workspaceId}
                runId={runId}
                kinds={noteKinds}
                reload={finished}
              />
            ) : null}
            {shownTab === 'bundle' ? (
              <BundleView
                transport={transport}
                workspaceId={workspaceId}
                runId={runId}
                bundleDir={detail.bundleDir}
                promptPath={detail.promptPath}
              />
            ) : null}
            {shownTab === 'tools' ? <ToolsPane events={events} startedAt={detail.startedAt} /> : null}
          </div>
        </div>
      </div>
    </div>
  )
}
