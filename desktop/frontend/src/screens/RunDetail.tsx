import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  useSyncExternalStore,
  type JSX,
} from 'react'
import type { NoteKind, RunDetail as RunDetailData, Transport } from '../api/types'
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

/** How long the copy button stays on "Copied" before it says its name again. */
const COPIED_MS = 1500

export default function RunDetail(props: {
  transport: Transport
  workspaceId: string
  runId: string
  onBack: () => void
  onStartRCA: (key: string, opts?: { prUrl?: string; resolution?: string }) => Promise<void> | void
}): JSX.Element {
  const { transport, workspaceId, runId, onBack, onStartRCA } = props
  const [detail, setDetail] = useState<RunDetailData | null>(null)
  const [loadError, setLoadError] = useState('')
  const [events, setEvents] = useState<IndexedEvent[]>([])
  const [tab, setTab] = useState<Tab>('note')
  const [rcaOpen, setRcaOpen] = useState(false)
  const [pending, setPending] = useState('')
  const [actionError, setActionError] = useState('')
  const [copied, setCopied] = useState(false)
  const [now, setNow] = useState(() => Date.now())
  const seen = useRef<Set<number>>(new Set())
  const copyTimer = useRef<ReturnType<typeof setTimeout> | null>(null)
  const jobId = useRunJob(runId)

  const live = LIVE.has(detail?.status ?? '')

  // Subscribe before backfilling so nothing written between the two is lost;
  // the index dedupe absorbs whatever the two deliveries have in common.
  useEffect(() => {
    let cancelled = false
    seen.current = new Set()
    setDetail(null)
    setEvents([])
    setLoadError('')
    setActionError('')
    setRcaOpen(false)

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
      onBack()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [onBack, rcaOpen])

  const noteKinds = useMemo<NoteKind[]>(
    () => (detail?.kind === 'rca' ? ['rca', 'resolution'] : ['triage']),
    [detail?.kind],
  )

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
    async (o: { prUrl: string; resolution: string }) => {
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
          <button type="button" className="run-btn" onClick={onBack}>
            Back
          </button>
        </header>
        <p className={loadError ? 'run-failed' : 'run-loading'}>{loadError || 'Loading run…'}</p>
      </div>
    )
  }

  const question = askedQuestion(detail.reason)
  const canStartRCA = detail.kind === 'triage' && detail.status === 'completed'

  return (
    <div className="run">
      <header className="run-head">
        <h2 className="run-key">{detail.key}</h2>
        <span className="run-badge">{detail.kind}</span>
        <span className="run-badge" data-status={detail.status}>
          {detail.status.replace('_', ' ')}
        </span>
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
          <button type="button" className="run-btn" onClick={onBack}>
            Back
          </button>
          <button
            type="button"
            className="run-btn"
            onClick={cancel}
            disabled={!jobId || pending !== ''}
            title={
              jobId
                ? 'Stop the run this window started'
                : 'Only a run started from this window can be cancelled'
            }
          >
            Cancel
          </button>
          {canStartRCA ? (
            <button
              type="button"
              className="run-btn"
              onClick={() => setRcaOpen((v) => !v)}
              aria-expanded={rcaOpen}
            >
              Start RCA
            </button>
          ) : null}
          <button
            type="button"
            className="run-btn"
            onClick={copyNotePath}
            disabled={!notePath}
            title={notePath || 'This run has written no note'}
          >
            {copied ? 'Copied' : 'Copy note path'}
          </button>
        </div>
      </header>

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
          onStart={startRCA}
          onCancel={() => setRcaOpen(false)}
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
