import { useCallback, useEffect, useState, useSyncExternalStore } from 'react'
import type { FixStart, SourcesSummary, Transport } from '../api/types'
import type { Decision } from '../components/session/ComposerStrip'
import { useSessionModel, withoutCode } from '../components/session/useSessionModel'
import { LIVE, useRunFeed } from '../components/run/useRunFeed'
import { useProvidePrimaryAction } from '../components/shell/primaryAction'
import { clearRunJob, getRunJob, setRunJob, subscribeRunJobs } from '../lib/jobs'
import { reasonOf } from '../lib/format'
import { sessionLayout, setSessionLayout, subscribeSessionLayout, type SessionLayout } from '../lib/sessionLayout'
import { sessionsShow, subscribeSessionsShow, type SessionsShow } from '../lib/sessionsShow'
import { BELOW_STANDARD, useMediaQuery } from '../lib/useMediaQuery'
import { stateWord } from '../ui/status-badge'
import type { SessionLayoutProps } from './session/layoutProps'
import SessionConversation from './session/SessionConversation'
import SessionDocument from './session/SessionDocument'
import SessionWorkbench from './session/SessionWorkbench'
import '../components/run/run.css'
import '../components/session/session.css'

export { badgeDetail, statsTitle } from '../components/session/RunHeader'

/**
 * Cancel only works for a job this window started. `lib/jobs` holds the run →
 * job pairing the store fills in as the runs appear; until it has one, the
 * Cancel button says why it cannot act rather than failing when pressed.
 */
function useRunJob(runId: string): string | undefined {
  return useSyncExternalStore(subscribeRunJobs, () => getRunJob(runId), () => undefined)
}

/** Keys typed into a field belong to that field, not to the window. */
function isTyping(target: EventTarget | null): boolean {
  const el = target as HTMLElement | null
  if (!el || !el.tagName) return false
  const tag = el.tagName.toLowerCase()
  return tag === 'input' || tag === 'textarea' || tag === 'select' || el.isContentEditable
}

/** The layouts that draw from the shared model, by the preference's name. */
const LAYOUTS: Record<'document', (props: SessionLayoutProps) => JSX.Element> = {
  document: SessionDocument,
}

export interface SessionProps {
  transport: Transport
  workspaceId: string
  runId: string
  /** The ticket's title, when the tracker's queue lists it. */
  title?: string
  /** The workspace's notes directory, so a filed note is named as the vault names it. */
  notesDir?: string
  /** The workspace's tracker and helpdesk, for the mark beside the number. */
  sources?: SourcesSummary
  onBack: () => void
  /** Opens the change review for this run. */
  onOpenReview: () => void
  /** Reruns the fix with the deviation accepted; the shell owns the job. */
  onStartFix?: (key: string, opts?: FixStart) => Promise<void> | void
}

/**

 * The session, reached at `#/runs/<workspace>/<run>`, in the layout the
 * reader chose (`lib/sessionLayout`, the `sirdar.sessionLayout` preference).
 * Conversation — the transcript as a chat with an inspector beside it — is
 * the default and lives in `./session/SessionConversation`; Workbench — the
 * documents over a structured console — is `./session/SessionWorkbench`.
 * Both own their feed and actions. Document — the note as the window with
 * the path beside it — draws from the shared dispatcher below: it reads the
 * run once and subscribes for what follows (`useRunFeed`), builds the shared
 * model (`useSessionModel`), owns the actions — Answer resumes a blocked
 * run, Steer continues a finished one, Cancel stops the job this window
 * started, Accept publishes a deviated fix — and hands them to the layout.
 * The layout's send button is the screen's one filled control; the
 * sidebar's New session steps down.
 */
export default function Session(props: SessionProps): JSX.Element {
  const layout = useSyncExternalStore(subscribeSessionLayout, sessionLayout, () => 'conversation' as SessionLayout)
  if (layout === 'conversation') return <SessionConversation {...props} />
  if (layout === 'workbench') return <SessionWorkbench {...props} />
  return <SessionShared {...props} layout={layout} />
}

/** The Document and Workbench layouts: one feed, one model, one set of actions, the chosen layout drawing them. */
function SessionShared(props: SessionProps & { layout: 'document' }): JSX.Element {
  const { transport, workspaceId, runId, title, notesDir, sources, onBack, onOpenReview, onStartFix, layout } = props
  const show = useSyncExternalStore(subscribeSessionsShow, sessionsShow, () => 'tracker' as SessionsShow)
  const { detail, setDetail, events, setEvents, loadError, finished } = useRunFeed(transport, workspaceId, runId)
  const data = useSessionModel(transport, workspaceId, runId, detail, events, finished)
  const [pending, setPending] = useState<SessionLayoutProps['pending']>('')
  const [actionError, setActionError] = useState('')
  const [steerRefusal, setSteerRefusal] = useState('')
  const [sent, setSent] = useState(0)
  const jobId = useRunJob(runId)
  const narrow = useMediaQuery(BELOW_STANDARD)

  const status = detail?.status ?? ''
  const live = LIVE.has(status)

  useEffect(() => {
    setActionError('')
    setSteerRefusal('')
  }, [runId])

  // A refusal is about the run as it was; once the run moves it no longer holds.
  useEffect(() => {
    setSteerRefusal('')
  }, [status])

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== 'Escape' || isTyping(e.target)) return
      // A drawer takes Escape first; the layout stops the event when it does.
      if (document.querySelector('.sd-drawer')) return
      onBack()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [onBack])

  /** Adds the operator's own answer to the log; the service does not record it. */
  const noteAnswer = useCallback(
    (text: string) => {
      if (!text) return
      setEvents((prev) => {
        const last = prev.length > 0 ? prev[prev.length - 1].index : 0
        return [...prev, { index: last + 0.5, event: { t: new Date().toISOString(), kind: 'answer', payload: { text } } }]
      })
    },
    [setEvents],
  )

  const answer = useCallback(
    async (text: string, _decision?: Decision) => {
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
        // run.updated that says so is on its way.
        setDetail((prev) => (prev ? { ...prev, status: 'running' } : prev))
        setEvents((prev) => {
          const last = prev.length > 0 ? prev[prev.length - 1].index : 0
          return [...prev, { index: last + 0.5, event: { t: new Date().toISOString(), kind: 'steer', payload: { text, continuation: 'resume' } } }]
        })
      } catch (err: unknown) {
        if (/^conflict:|refused/i.test(reasonOf(err))) setSteerRefusal(withoutCode(err))
        else setActionError(withoutCode(err))
      } finally {
        setPending('')
      }
    },
    [transport, workspaceId, runId, setDetail, setEvents],
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

  const composer = steerRefusal ? ({ kind: 'disabled', reason: steerRefusal } as const) : data.model.composer
  const sendBusy = pending === 'answer' || pending === 'steer'
  // The screen's one filled control, whichever it is: the strip's send, or
  // the Stop that stands in its place while the run works.
  useProvidePrimaryAction(
    detail
      ? {
          label: composer.kind === 'reply' ? 'Answer' : composer.kind === 'running' ? 'Stop' : 'Steer',
          onRun: () => {},
          disabled: composer.kind === 'disabled' || (composer.kind === 'running' && !jobId),
          busy: composer.kind === 'running' ? pending === 'cancel' : sendBusy,
          shortcut: composer.kind === 'running' ? undefined : '↵',
          title: composer.kind === 'disabled' ? composer.reason : undefined,
          placement: 'screen',
        }
      : null,
  )

  if (loadError || !detail) {
    return (
      <div className="sn">
        <p className={loadError ? 'sn-failed' : 'sn-loading'}>{loadError || 'Loading run…'}</p>
      </div>
    )
  }

  const Layout = LAYOUTS[layout]
  return (
    <>
      <span className="visually-hidden" aria-live="polite">
        {`Run ${stateWord(detail.status)}`}
      </span>
      <Layout
        transport={transport}
        workspaceId={workspaceId}
        runId={runId}
        detail={detail}
        data={data}
        title={title}
        notesDir={notesDir}
        sources={sources}
        show={show}
        narrow={narrow}
        live={live}
        actions={{
          answer: (text, decision) => void answer(text, decision),
          steer: (text) => void steer(text),
          cancel: () => void cancel(),
          acceptDeviation: onStartFix ? () => void acceptDeviation() : undefined,
          openReview: onOpenReview,
        }}
        pending={pending}
        actionError={actionError}
        steerRefusal={steerRefusal}
        sent={sent}
        canCancel={Boolean(jobId)}
        layout={layout}
        onLayout={setSessionLayout}
        fixInfo={detail.fix}
      />
    </>
  )
}
