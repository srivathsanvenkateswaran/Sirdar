import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  useSyncExternalStore,
  type JSX,
} from 'react'
import type { FixStart, NoteKind, RunDetail, RunDiff, Transport } from '../api/types'
import { askedQuestion, elapsed, type IndexedEvent } from '../lib/events'
import { costOrUnknown, reasonOf } from '../lib/format'
import { checksFromEvents, describeTests, latestStep } from '../lib/review'
import { clearRunJob, getRunJob, setRunJob, subscribeRunJobs } from '../lib/jobs'
import BundleView from '../components/run/BundleView'
import ChangesPane, { withoutCode } from '../components/run/ChangesPane'
import Composer, { type ComposerMode } from '../components/run/Composer'
import EventStream from '../components/run/EventStream'
import NoteView from '../components/run/NoteView'
import ToolsPane, { toolCount } from '../components/run/ToolsPane'
import { useProvidePrimaryAction } from '../components/shell/primaryAction'
import Banner from '../ui/banner'
import Button from '../ui/button'
import KindChip from '../ui/kind-chip'
import ProviderMark from '../ui/provider-mark'
import StatusBadge, { type SdStatus } from '../ui/status-badge'
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

const LIVE = new Set(['preparing', 'running'])

/**
 * The states a run does not come back from. `blocked` is not one of them: it
 * is waiting for an answer and resumes into `running`, so Cancel stays on
 * offer there and the artefacts are not asked for again.
 */
const TERMINAL = new Set(['completed', 'failed', 'over_budget'])

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
  onBack: () => void
  /** Opens the change review for this run. */
  onOpenReview: () => void
  /** Reruns the fix with the deviation accepted; the shell owns the job. */
  onStartFix?: (key: string, opts?: FixStart) => Promise<void> | void
}): JSX.Element {
  const { transport, workspaceId, runId, title, onBack, onOpenReview, onStartFix } = props
  const [detail, setDetail] = useState<RunDetail | null>(null)
  const [loadError, setLoadError] = useState('')
  const [events, setEvents] = useState<IndexedEvent[]>([])
  const [tab, setTab] = useState<Tab | null>(null)
  const [pending, setPending] = useState('')
  const [actionError, setActionError] = useState('')
  /** Why the provider refused the last steer; the composer stays disabled with it. */
  const [steerRefusal, setSteerRefusal] = useState('')
  /** How many sends went through, so the composer knows to clear. */
  const [sent, setSent] = useState(0)
  const [changed, setChanged] = useState<number | null>(null)
  const [now, setNow] = useState(() => Date.now())
  /** Bumped when the run finishes, to re-ask for artefacts written at the end. */
  const [artefacts, setArtefacts] = useState(0)
  const seen = useRef<Set<number>>(new Set())
  /** The status of the previous render, for spotting the run finishing. */
  const wasStatus = useRef('')
  const jobId = useRunJob(runId)

  const status = detail?.status ?? ''
  const live = LIVE.has(status)
  const terminal = TERMINAL.has(status)

  // Subscribe before backfilling so nothing written between the two is lost;
  // the index dedupe absorbs whatever the two deliveries have in common.
  useEffect(() => {
    let cancelled = false
    seen.current = new Set()
    wasStatus.current = ''
    setDetail(null)
    setEvents([])
    setLoadError('')
    setActionError('')
    setSteerRefusal('')
    setTab(null)
    setChanged(null)

    const append = (index: number, event: IndexedEvent['event']) => {
      if (seen.current.has(index)) return
      seen.current.add(index)
      setEvents((prev) => [...prev, { index, event }].sort((a, b) => a.index - b.index))
    }

    const unsubscribe = transport.subscribe((e) => {
      if (cancelled) return
      if (e.kind === 'run.event') {
        if (e.runId !== runId) return
        if (e.workspaceId && e.workspaceId !== workspaceId) return
        append(e.index, e.event)
        return
      }
      if (e.kind === 'run.updated' && e.run?.runId === runId) {
        setDetail((prev) => (prev ? { ...prev, ...e.run } : prev))
      }
    })

    transport
      .run(workspaceId, runId)
      .then((d) => {
        if (!cancelled) setDetail(d)
      })
      .catch((err: unknown) => {
        if (!cancelled) setLoadError(reasonOf(err))
      })

    transport
      .events(workspaceId, runId, 0)
      .then(({ events: backfill, next }) => {
        if (cancelled) return
        // The watcher and Service.Events both number events from 1, and
        // `next` is the index of the last line in this page. Numbering the
        // backfill from zero would leave the last line sharing no index with
        // the live event that repeats it, and the stream would show it twice.
        const first = Math.max(1, next - backfill.length + 1)
        backfill.forEach((event, i) => append(first + i, event))
      })
      .catch(() => {
        // The detail request already reports an unreadable run; an empty event
        // log is normal for one that has not written a line yet.
      })

    return () => {
      cancelled = true
      unsubscribe()
    }
  }, [transport, workspaceId, runId])

  /*
   * A run opened while it was still working keeps whatever it had at the time.
   * The note, the fix result and the rest of state.json are written as the run
   * finishes, so a screen that only asked on mount went on saying "No note yet"
   * for a run that had one, and the reader had to leave and come back.
   *
   * `run.updated` patches the status into `detail` as the store sees it move,
   * so the moment it turns terminal is visible here: ask for the run again,
   * and bump the counter the artefact panes read so they ask too.
   */
  useEffect(() => {
    const before = wasStatus.current
    wasStatus.current = status
    if (!LIVE.has(before) || !TERMINAL.has(status)) return

    let cancelled = false
    setArtefacts((n) => n + 1)
    transport
      .run(workspaceId, runId)
      .then((d) => {
        if (!cancelled) setDetail(d)
      })
      .catch(() => {
        // The header already carries the finished status from the event; a
        // re-read that fails leaves the screen as it was rather than blanking
        // a run the reader is looking at.
      })
    return () => {
      cancelled = true
    }
  }, [status, transport, workspaceId, runId])

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
    banner = (
      <Banner tone="ok" title="Note filed">
        {notePath ? <span className="banner-path">{notePath}</span> : 'the note is under Note'}
      </Banner>
    )
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
            {detail.model ? <span className="session-provider-model">{detail.model}</span> : null}
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
          <div className="session-tabs" role="tablist" aria-label="Run artefacts">
            {tabs.map((t) => (
              <button
                key={t.id}
                type="button"
                role="tab"
                className="session-tab"
                aria-selected={shownTab === t.id}
                onClick={() => setTab(t.id)}
              >
                {t.label}
                {t.count !== undefined ? <span className="session-tab-n">{t.count}</span> : null}
              </button>
            ))}
          </div>
          {shownTab === 'changes' && isFix ? (
            <ChangesPane
              transport={transport}
              workspaceId={workspaceId}
              runId={runId}
              checks={checks}
              fix={detail.fix}
              reload={artefacts}
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
              reload={artefacts}
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
  )
}
