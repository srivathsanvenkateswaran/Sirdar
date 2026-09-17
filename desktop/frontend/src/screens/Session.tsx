import { useCallback, useDeferredValue, useEffect, useMemo, useState, useSyncExternalStore } from 'react'
import type { FixStart, NoteKind, SourcesSummary, Transport } from '../api/types'
import type { Decision } from '../components/session/ComposerStrip'
import { modelOf } from '../components/session/model'
import RunHeader from '../components/session/RunHeader'
import { useSessionModel, withoutCode } from '../components/session/useSessionModel'
import { LIVE, useRunFeed, type RunFeed } from '../components/run/useRunFeed'
import { useProvidePrimaryAction } from '../components/shell/primaryAction'
import { notePathFor } from '../lib/events'
import { clearRunJob, getRunJob, setRunJob, subscribeRunJobs } from '../lib/jobs'
import { reasonOf } from '../lib/format'
import { usePendingLonger } from '../lib/pending'
import { sessionLayout, setSessionLayout, subscribeSessionLayout, type SessionLayout } from '../lib/sessionLayout'
import { stateWord } from '../ui/status-badge'
import type { SessionLayoutProps } from './session/layoutProps'
import SessionConversation from './session/SessionConversation'
import SessionDocument from './session/SessionDocument'
import SessionWorkbench from './session/SessionWorkbench'
import '../components/run/run.css'
import '../components/session/session.css'

export { badgeDetail } from '../components/session/RunHeader'

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

/**
 * Which note the header's state chip means by `note saved`: an RCA run's
 * own RCA note, a fix run's note.md, a triage run's triage note. The header
 * asks for it once here so all three layouts say the same thing about the
 * same run.
 */
function headNoteKind(kind: string | undefined): NoteKind {
  if (kind === 'rca') return 'rca'
  if (kind === 'fix') return ''
  return 'triage'
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
  const { transport, workspaceId, runId, title, sources } = props
  const wanted = useSyncExternalStore(subscribeSessionLayout, sessionLayout, () => 'conversation' as SessionLayout)
  /*
   * The switcher used to unmount one layout and mount another whole tree
   * with nothing on screen in between: 191 to 601ms of empty window on a
   * long run. Deferring the choice renders the new layout in the background
   * while the one the reader is looking at stays painted, and the wait is
   * marked only once it is long enough to notice. See lib/pending.
   */
  const layout = useDeferredValue(wanted)
  const busy = usePendingLonger(layout !== wanted)
  /*
   * The feed is the window's, not the layout's. Each layout used to open
   * its own — which meant switching layout re-read the run and rebuilt the
   * header from nothing, and the owner watched the header bar blink on
   * every switch. Read once here and handed down, the run is the same
   * object across a switch and the header above never unmounts.
   */
  const feed = useRunFeed(transport, workspaceId, runId)
  const { detail, events } = feed

  // The model the log named, for a run whose record carries none.
  const fallbackModel = useMemo(() => modelOf(events), [events])
  const notePath = useMemo(
    () => notePathFor(headNoteKind(detail?.kind), detail?.notes),
    [detail?.kind, detail?.notes],
  )

  // The frame is always here, switch or no switch: adding it only while the
  // switch is on would remount the layout it is meant to keep on screen.
  return (
    <div className="sn-frame" data-switching={busy || undefined} aria-busy={busy || undefined}>
      {detail ? (
        <RunHeader
          detail={detail}
          title={title}
          sources={sources}
          variant={layout === 'workbench' ? 'workbench' : 'default'}
          notePath={notePath}
          fallbackModel={fallbackModel}
          transport={transport}
          workspaceId={workspaceId}
        />
      ) : null}
      {layout === 'conversation' ? (
        <SessionConversation {...props} feed={feed} />
      ) : layout === 'workbench' ? (
        <SessionWorkbench {...props} feed={feed} />
      ) : (
        <SessionShared {...props} feed={feed} layout={layout} />
      )}
    </div>
  )
}

/** The Document layout: the window's feed, one model, one set of actions, the layout drawing them. */
function SessionShared(props: SessionProps & { feed: RunFeed; layout: 'document' }): JSX.Element {
  const { transport, workspaceId, runId, title, notesDir, sources, onBack, onOpenReview, onStartFix, layout } = props
  const { detail, setDetail, events, setEvents, loadError, finished } = props.feed
  const data = useSessionModel(transport, workspaceId, runId, detail, events, finished)
  const [pending, setPending] = useState<SessionLayoutProps['pending']>('')
  const [actionError, setActionError] = useState('')
  const [steerRefusal, setSteerRefusal] = useState('')
  const [sent, setSent] = useState(0)
  const jobId = useRunJob(runId)

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
