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
import { askedQuestion, elapsed, formatCost, type IndexedEvent } from '../lib/events'
import EventStream from '../components/run/EventStream'
import NoteView from '../components/run/NoteView'
import PromptView from '../components/run/PromptView'
import BundleView from '../components/run/BundleView'
import StateView from '../components/run/StateView'
import ResumeBox from '../components/run/ResumeBox'
import RCAForm from '../components/run/RCAForm'
import '../components/run/run.css'

/**
 * Cancel only works for jobs this process started, and only the shell knows
 * which run a `startTriage` turned into. It records the pairing here as the
 * `job.finished` outcomes arrive; until it does, the Cancel button says why it
 * cannot act rather than failing when pressed.
 */
const jobs = new Map<string, string>()
const jobWatchers = new Set<() => void>()

export function setRunJob(runId: string, jobId: string): void {
  jobs.set(runId, jobId)
  for (const notify of jobWatchers) notify()
}

export function clearRunJob(runId: string): void {
  if (!jobs.delete(runId)) return
  for (const notify of jobWatchers) notify()
}

export function getRunJob(runId: string): string | undefined {
  return jobs.get(runId)
}

function useRunJob(runId: string): string | undefined {
  return useSyncExternalStore(
    (notify) => {
      jobWatchers.add(notify)
      return () => {
        jobWatchers.delete(notify)
      }
    },
    () => jobs.get(runId),
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

export default function RunDetail(props: {
  transport: Transport
  workspaceId: string
  runId: string
  onBack: () => void
  onStartRCA: (key: string) => void
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
      .then(({ events: backfill }) => {
        if (cancelled) return
        backfill.forEach((event, i) => append(i, event))
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
        await transport.startRCA(workspaceId, detail.key, {
          prUrl: o.prUrl || undefined,
          resolution: o.resolution || undefined,
        })
        setRcaOpen(false)
        onStartRCA(detail.key)
      } catch (err: unknown) {
        setActionError(err instanceof Error ? err.message : String(err))
      } finally {
        setPending('')
      }
    },
    [detail, transport, workspaceId, onStartRCA],
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
      setTimeout(() => setCopied(false), 1500)
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
          <span className="run-stat">{formatCost(detail.usage?.costUsd)}</span>
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
