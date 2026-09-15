import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  useSyncExternalStore,
  type JSX,
} from 'react'
import type {
  FixStart,
  NoteKind,
  RCAStart,
  RunDetail as RunDetailData,
  Transport,
} from '../api/types'
import { askedQuestion, elapsed, type IndexedEvent } from '../lib/events'
import { costOrUnknown } from '../lib/format'
import { clearRunJob, getRunJob, setRunJob, subscribeRunJobs } from '../lib/jobs'
import EventStream from '../components/run/EventStream'
import NoteView from '../components/run/NoteView'
import PromptView from '../components/run/PromptView'
import BundleView from '../components/run/BundleView'
import StateView from '../components/run/StateView'
import ResumeBox from '../components/run/ResumeBox'
import RCAForm from '../components/run/RCAForm'
import FixForm from '../components/run/FixForm'
import FixPanel from '../components/run/FixPanel'
import Button from '../ui/button'
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

const TABS = [
  { id: 'note', label: 'Note' },
  { id: 'prompt', label: 'Prompt' },
  { id: 'bundle', label: 'Bundle' },
  { id: 'state', label: 'State' },
] as const

type Tab = (typeof TABS)[number]['id']

const LIVE = new Set(['preparing', 'running'])

/**
 * The states a run does not come back from. `blocked` is not one of them: it
 * is waiting for an answer and resumes into `running`, so Cancel stays on
 * offer there and the artefacts are not asked for again.
 */
const TERMINAL = new Set(['completed', 'failed', 'over_budget'])

/** How long the copy button stays on "Copied" before it says its name again. */
const COPIED_MS = 1500

export default function RunDetail(props: {
  transport: Transport
  workspaceId: string
  runId: string
  /** The workspace's configured provider, named on the override selects. */
  defaultProvider?: string
  onBack: () => void
  onStartRCA: (key: string, opts?: RCAStart) => Promise<void> | void
  onStartFix?: (key: string, opts?: FixStart) => Promise<void> | void
}): JSX.Element {
  const { transport, workspaceId, runId, defaultProvider, onBack, onStartRCA, onStartFix } = props
  const [detail, setDetail] = useState<RunDetailData | null>(null)
  const [loadError, setLoadError] = useState('')
  const [events, setEvents] = useState<IndexedEvent[]>([])
  const [tab, setTab] = useState<Tab>('note')
  const [rcaOpen, setRcaOpen] = useState(false)
  const [fixOpen, setFixOpen] = useState(false)
  const [pending, setPending] = useState('')
  const [actionError, setActionError] = useState('')
  /** What the last "Add to golden set" did, shown until the run changes. */
  const [golden, setGolden] = useState('')
  const [copied, setCopied] = useState(false)
  const [now, setNow] = useState(() => Date.now())
  /** Bumped when the run finishes, to re-ask for artefacts written at the end. */
  const [artefacts, setArtefacts] = useState(0)
  const seen = useRef<Set<number>>(new Set())
  /** The status of the previous render, for spotting the run finishing. */
  const wasStatus = useRef('')
  const copyTimer = useRef<ReturnType<typeof setTimeout> | null>(null)
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
    setGolden('')
    setRcaOpen(false)
    setFixOpen(false)

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
        if (!cancelled) setLoadError(err instanceof Error ? err.message : String(err))
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

  useEffect(() => {
    if (!live) return
    const id = setInterval(() => setNow(Date.now()), 1000)
    return () => clearInterval(id)
  }, [live])

  // The "Copied" label resets itself on a timer; a screen that closes first
  // must not leave that timer behind to fire into an unmounted component.
  useEffect(
    () => () => {
      if (copyTimer.current) clearTimeout(copyTimer.current)
      copyTimer.current = null
    },
    [],
  )

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== 'Escape') return
      if (rcaOpen) {
        setRcaOpen(false)
        return
      }
      if (fixOpen) {
        setFixOpen(false)
        return
      }
      onBack()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [onBack, rcaOpen, fixOpen])

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

  const resume = useCallback(
    async (answer: string) => {
      setPending('resume')
      setActionError('')
      try {
        const started = await transport.resume(workspaceId, runId, answer)
        if (started?.jobId) setRunJob(runId, started.jobId)
      } catch (err: unknown) {
        setActionError(err instanceof Error ? err.message : String(err))
      } finally {
        setPending('')
      }
    },
    [transport, workspaceId, runId],
  )

  const startRCA = useCallback(
    async (o: { prUrl: string; resolution: string; provider: string; model: string }) => {
      if (!detail) return
      setPending('rca')
      setActionError('')
      try {
        // The shell starts the run, so the job id it comes back with is
        // recorded where Cancel can find it. Starting it here as well would
        // triage the same ticket twice.
        await onStartRCA(detail.key, {
          prUrl: o.prUrl || undefined,
          resolution: o.resolution || undefined,
          provider: o.provider || undefined,
          model: o.model || undefined,
        })
        setRcaOpen(false)
      } catch (err: unknown) {
        setActionError(err instanceof Error ? err.message : String(err))
      } finally {
        setPending('')
      }
    },
    [detail, onStartRCA],
  )

  const startFix = useCallback(
    async (o: FixStart, what: 'fix' | 'accept') => {
      if (!detail || !onStartFix) return
      setPending(what)
      setActionError('')
      try {
        await onStartFix(detail.key, o)
        setFixOpen(false)
      } catch (err: unknown) {
        setActionError(err instanceof Error ? err.message : String(err))
      } finally {
        setPending('')
      }
    },
    [detail, onStartFix],
  )

  /*
   * Copy this run's bundle into the golden set, which is what
   * `sirdar golden add` does. The bundle is a real customer's conversation,
   * so nothing of it is shown here and nothing about where it went is
   * reported beyond the directory the service chose.
   */
  const addToGolden = useCallback(async () => {
    setPending('golden')
    setActionError('')
    setGolden('')
    try {
      const entry = await transport.addGolden(workspaceId, { runId })
      setGolden(`Added to the golden set as ${entry.key}.`)
    } catch (err: unknown) {
      setActionError(err instanceof Error ? err.message : String(err))
    } finally {
      setPending('')
    }
  }, [transport, workspaceId, runId])

  const cancel = useCallback(async () => {
    if (!jobId) return
    setPending('cancel')
    setActionError('')
    try {
      await transport.cancel(jobId)
      clearRunJob(runId)
    } catch (err: unknown) {
      setActionError(err instanceof Error ? err.message : String(err))
    } finally {
      setPending('')
    }
  }, [jobId, transport, runId])

  const copyNotePath = useCallback(async () => {
    if (!notePath) return
    try {
      await navigator.clipboard?.writeText(notePath)
      setCopied(true)
      if (copyTimer.current) clearTimeout(copyTimer.current)
      copyTimer.current = setTimeout(() => setCopied(false), COPIED_MS)
    } catch {
      setActionError('Could not reach the clipboard. The path is under State.')
    }
  }, [notePath])

  if (loadError || !detail) {
    return (
      <div className="run">
        <header className="run-head">
          <Button onClick={onBack}>Back</Button>
        </header>
        <p className={loadError ? 'run-failed' : 'run-loading'}>{loadError || 'Loading run…'}</p>
      </div>
    )
  }

  const question = askedQuestion(detail.reason)
  const canStartRCA = detail.kind === 'triage' && detail.status === 'completed'
  // A fix runs from a completed triage note, which is the same gate the CLI
  // applies; the core refuses one whose note says otherwise.
  const canStartFix = Boolean(onStartFix) && canStartRCA

  return (
    <div className="run">
      <header className="run-head">
        <h2 className="run-key">{detail.key}</h2>
        <span className="run-kind">{detail.kind}</span>
        <StatusBadge status={detail.status as SdStatus} />
        <div className="run-stats">
          <span className="run-stat">
            {detail.provider} {detail.model}
          </span>
          <span className="run-stat">{elapsed(detail, now)}</span>
          <span className="run-stat">
            {costOrUnknown(detail.usage?.costUsd, detail.status === 'running' || detail.status === 'preparing')}
          </span>
          <span className="run-stat">
            {detail.usage?.turns ?? 0} {detail.usage?.turns === 1 ? 'turn' : 'turns'}
          </span>
        </div>
        <div className="run-actions">
          <Button onClick={onBack}>Back</Button>
          {/* Nothing is left to stop once the run has ended. */}
          {terminal ? null : (
            <Button
              onClick={cancel}
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
          {canStartRCA ? (
            <Button onClick={() => setRcaOpen((v) => !v)} aria-expanded={rcaOpen}>
              Start RCA
            </Button>
          ) : null}
          {canStartFix ? (
            <Button onClick={() => setFixOpen((v) => !v)} aria-expanded={fixOpen}>
              Start fix
            </Button>
          ) : null}
          {canStartRCA ? (
            <Button
              onClick={() => void addToGolden()}
              disabled={pending !== ''}
              title="Copy this run's bundle into the golden set the eval replays"
            >
              {pending === 'golden' ? 'Adding…' : 'Add to golden set'}
            </Button>
          ) : null}
          <Button
            onClick={copyNotePath}
            disabled={!notePath}
            title={notePath || 'This run has written no note'}
          >
            {copied ? 'Copied' : 'Copy note path'}
          </Button>
        </div>
      </header>

      {golden ? <p className="run-said">{golden}</p> : null}
      {actionError && !rcaOpen && !fixOpen && detail.status !== 'blocked' && !detail.fix ? (
        <p className="run-failed-line">{actionError}</p>
      ) : null}

      {detail.status === 'blocked' ? (
        <ResumeBox
          question={question}
          reason={detail.reason}
          pending={pending === 'resume'}
          error={actionError}
          onResume={resume}
        />
      ) : null}

      {rcaOpen ? (
        <RCAForm
          runKey={detail.key}
          pending={pending === 'rca'}
          error={actionError}
          defaultProvider={defaultProvider}
          onStart={startRCA}
          onCancel={() => setRcaOpen(false)}
        />
      ) : null}

      {fixOpen ? (
        <FixForm
          runKey={detail.key}
          pending={pending === 'fix'}
          error={actionError}
          defaultProvider={defaultProvider}
          onStart={(o) =>
            void startFix(
              {
                dryRun: o.dryRun || undefined,
                noPr: o.noPr || undefined,
                base: o.base || undefined,
                provider: o.provider || undefined,
                model: o.model || undefined,
              },
              'fix',
            )
          }
          onCancel={() => setFixOpen(false)}
        />
      ) : null}

      {detail.fix ? (
        <FixPanel
          fix={detail.fix}
          pending={pending === 'accept'}
          error={actionError}
          onAccept={() => void startFix({ acceptDeviation: true }, 'accept')}
        />
      ) : null}

      <div className="run-body">
        <div className="run-left">
          <EventStream events={events} startedAt={detail.startedAt} live={live} />
        </div>
        <div className="run-right">
          <div className="run-tabs" role="tablist" aria-label="Run artefacts">
            {TABS.map((t) => (
              <button
                key={t.id}
                type="button"
                role="tab"
                className="run-tab"
                aria-selected={tab === t.id}
                onClick={() => setTab(t.id)}
              >
                {t.label}
              </button>
            ))}
          </div>
          {tab === 'note' ? (
            <NoteView
              transport={transport}
              workspaceId={workspaceId}
              runId={runId}
              kinds={noteKinds}
              reload={artefacts}
            />
          ) : null}
          {tab === 'prompt' ? (
            <PromptView
              transport={transport}
              workspaceId={workspaceId}
              runId={runId}
              path={detail.promptPath}
            />
          ) : null}
          {tab === 'bundle' ? (
            <BundleView
              transport={transport}
              workspaceId={workspaceId}
              runId={runId}
              bundleDir={detail.bundleDir}
            />
          ) : null}
          {tab === 'state' ? <StateView detail={detail} /> : null}
        </div>
      </div>
    </div>
  )
}
