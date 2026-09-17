import {
  useCallback,
  useEffect,
  useId,
  useMemo,
  useRef,
  useState,
  useSyncExternalStore,
  type JSX,
  type KeyboardEvent as ReactKeyboardEvent,
} from 'react'
import ReactMarkdown from 'react-markdown'
import type { FixStart, NoteKind, RunDiff, SourcesSummary, Transport } from '../../api/types'
import ChangesView, { withoutCode } from '../../components/run/ChangesPane'
import Composer, { type ComposerMode } from '../../components/run/Composer'
import { BundleIcon, ChangesIcon, NoteIcon, ToolsIcon } from '../../components/run/paneIcons'
import { LIVE, TERMINAL, useRunFeed } from '../../components/run/useRunFeed'
import { useProvidePrimaryAction } from '../../components/shell/primaryAction'
import ModelLimitBanner from '../../components/session/ModelLimitBanner'
import { askedQuestion, modelLimited, notePathFor } from '../../lib/events'
import { deriveEvidenceMarkers, stepLikeOf } from '../../lib/evidence'
import { evidenceOf } from '../../components/session/model'
import { reasonOf, tokens, usd } from '../../lib/format'
import { clearRunJob, getRunJob, setRunJob, subscribeRunJobs } from '../../lib/jobs'
import { probeRender } from '../../lib/renderProbe'
import { readStoredFlag, writeStoredFlag } from '../../lib/storedFlag'
import PanelToggle from '../../ui/panel-toggle'
import ProviderMark from '../../ui/provider-mark'
import { stateWord } from '../../ui/status-badge'
import AnswerCard, { SupersededAnswer } from './AnswerCard'
import AskCard from './AskCard'
import BundleView from './BundleView'
import { BracesIcon, ThinkIcon } from './icons'
import { callForRef, useSessionModel, type ChatItem, type StepCall } from './model'
import NoteDocument from './NoteDocument'
import RunHeader from './RunHeader'
import { clock } from './shape'
import { ToolStack } from './ToolStep'
import ToolsTable from './ToolsTable'
import './session-conversation.css'

/*
 * Session, layout A — Conversation first.
 *
 * The transcript is a chat: the operator's steers and answers as bubbles at
 * the inline end, the model's prose as plain blocks at the inline start,
 * and what the agent did to the machine as stacks of one-line tool cards
 * that expand in place. The conclusion is a rich card at the end, with the
 * note linked from its footer, and a finished run opens scrolled to it.
 * When the agent is blocked, its question is a highlighted message in the
 * same flow and the composer under it is already focused to answer.
 *
 * The pane beside it is an inspector, not a second transcript: the note as
 * a document, the bundle as the ticket it is, the calls as a sortable
 * table, the change as the diff with its checks. Clicking a row in Tools,
 * or a `file:line` in the answer or the note, scrolls the transcript to the
 * card that produced it and tints the pair.
 */

function useRunJob(runId: string): string | undefined {
  return useSyncExternalStore(subscribeRunJobs, () => getRunJob(runId), () => undefined)
}

type Tab = 'changes' | 'note' | 'bundle' | 'tools'

const TAB_ICONS: Record<Tab, JSX.Element> = {
  changes: <ChangesIcon />,
  note: <NoteIcon />,
  bundle: <BundleIcon />,
  tools: <ToolsIcon />,
}

/** Remembered per screen, not per run — the same key the Document layout reads. */
export const PANE_COLLAPSED_KEY = 'sirdar.sessionPaneCollapsed'

/** How close to the bottom still counts as following a live run, in pixels. */
const STICK_SLACK = 24

function directionOf(node: HTMLElement | null): 'ltr' | 'rtl' {
  const dir = node?.closest('[dir]')?.getAttribute('dir')
  if (dir === 'rtl' || dir === 'ltr') return dir
  return typeof document !== 'undefined' && document.dir === 'rtl' ? 'rtl' : 'ltr'
}

function isTyping(target: EventTarget | null): boolean {
  const el = target as HTMLElement | null
  if (!el || !el.tagName) return false
  const tag = el.tagName.toLowerCase()
  return tag === 'input' || tag === 'textarea' || tag === 'select' || el.isContentEditable
}

/** The words a steer's continuation is shown by. */
function continuationWord(continuation: string | undefined): string {
  if (continuation === 'primed') return 'continued in a new session'
  return continuation ?? ''
}

export interface SessionConversationProps {
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

export default function SessionConversation(props: SessionConversationProps): JSX.Element {
  probeRender('SessionConversation')
  const { transport, workspaceId, runId, title, notesDir, sources, onBack, onOpenReview, onStartFix } = props
  const { detail, setDetail, events, setEvents, loadError, finished } = useRunFeed(transport, workspaceId, runId)
  const model = useSessionModel(events, detail)
  // E1…En off the answer's evidence, on the calls that produced each item:
  // the same derivation the Document layout draws its markers from.
  const markers = useMemo(
    () => deriveEvidenceMarkers(evidenceOf(model.answer), model.calls.map((c) => stepLikeOf(c.index, c.call.started.event))),
    [model.answer, model.calls],
  )

  const [tab, setTab] = useState<Tab | null>(null)
  const [paneOpen, setPaneOpen] = useState(() => !readStoredFlag(PANE_COLLAPSED_KEY))
  const [pending, setPending] = useState('')
  const [actionError, setActionError] = useState('')
  const [steerRefusal, setSteerRefusal] = useState('')
  const [sent, setSent] = useState(0)
  /**
   * The model the next answer or steer asks for, when the reader has picked
   * one that is not the run's. Empty is the run's own, and it is cleared
   * whenever the run moves: a choice is about the send it was made for.
   */
  const [pickedModel, setPickedModel] = useState('')
  const [changed, setChanged] = useState<number | null>(null)
  /** The one open card, by its event index. */
  const [openCall, setOpenCall] = useState(-1)
  /** The card picked from the Tools table or a reference, tinted with its twin. */
  const [highlighted, setHighlighted] = useState(-1)
  const [showJump, setShowJump] = useState(false)
  /*
   * The pill's state, mirrored, so asking for what it already shows is not a
   * render. React renders a component once more even when the value it is
   * handed back is the one it holds, and both the scroll handler and the
   * effect that follows a live tail ask on every line and every wheel event:
   * a streamed line that draws nothing was costing two renders of this
   * screen, one for the line and one for this.
   */
  const jumpShown = useRef(false)
  const jobId = useRunJob(runId)
  const tabsId = useId()
  const paneId = `${tabsId}-pane`
  const streamRef = useRef<HTMLDivElement | null>(null)
  const stick = useRef(true)
  /** The run whose answer the stream has already been scrolled to. */
  const openedOn = useRef('')

  const status = detail?.status ?? ''
  const live = LIVE.has(status)
  const terminal = TERMINAL.has(status)
  const blocked = status === 'blocked'
  const isFix = detail?.kind === 'fix'

  useEffect(() => {
    setActionError('')
    setSteerRefusal('')
    setTab(null)
    setChanged(null)
    setOpenCall(-1)
    setHighlighted(-1)
    stick.current = true
    openedOn.current = ''
  }, [runId])

  useEffect(() => {
    setSteerRefusal('')
    setPickedModel('')
  }, [status])

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== 'Escape' || isTyping(e.target)) return
      onBack()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [onBack])

  const setPane = useCallback((open: boolean) => {
    setPaneOpen(open)
    writeStoredFlag(PANE_COLLAPSED_KEY, !open)
  }, [])

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== '\\' || !(e.metaKey || e.ctrlKey) || e.altKey || e.shiftKey) return
      e.preventDefault()
      setPane(!paneOpen)
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [paneOpen, setPane])

  const showJumpPill = useCallback((on: boolean) => {
    if (jumpShown.current === on) return
    jumpShown.current = on
    setShowJump(on)
  }, [])

  /*
   * Where the stream sits. A finished run opens scrolled to its answer,
   * once; a live one follows the tail while the reader is at the bottom
   * and stops the moment they scroll up, offering a pill back.
   */
  useEffect(() => {
    const el = streamRef.current
    if (!el || !detail) return
    if (terminal && model.answerIndex >= 0 && openedOn.current !== runId) {
      openedOn.current = runId
      const card = el.querySelector<HTMLElement>(`[data-item="${model.answerIndex}"]`)
      if (card && typeof card.scrollIntoView === 'function') card.scrollIntoView({ block: 'start' })
      else el.scrollTop = el.scrollHeight
      stick.current = false
      showJumpPill(false)
      return
    }
    if (!terminal && stick.current) {
      el.scrollTop = el.scrollHeight
      showJumpPill(false)
    } else if (!terminal && events.length > 0) {
      showJumpPill(true)
    }
  }, [events.length, terminal, detail, model.answerIndex, runId])

  const onScroll = () => {
    const el = streamRef.current
    if (!el) return
    const near = el.scrollHeight - el.scrollTop - el.clientHeight <= STICK_SLACK
    stick.current = near
    showJumpPill(!near && !terminal && events.length > 0)
  }

  const toBottom = useCallback(() => {
    const el = streamRef.current
    if (el) el.scrollTop = el.scrollHeight
    stick.current = true
    showJumpPill(false)
  }, [])

  /** Scrolls the transcript to a call's card and tints the pair. */
  const locate = useCallback((step: StepCall) => {
    setHighlighted(step.index)
    const card = streamRef.current?.querySelector<HTMLElement>(`[data-call="${step.index}"]`)
    if (card && typeof card.scrollIntoView === 'function') card.scrollIntoView({ block: 'center' })
    stick.current = false
  }, [])

  /*
   * A `file:line` in the answer or the note, taken to the card that produced
   * it. The calls are read through a ref rather than closed over, so this
   * handler is one object for the life of the screen: a transcript row is
   * held on the handlers it was drawn with, and a handler rebuilt for every
   * call that lands would redraw every row on every call.
   */
  const callsRef = useRef(model.calls)
  useEffect(() => {
    callsRef.current = model.calls
  }, [model.calls])
  const onRef = useCallback(
    (ref: string) => {
      const step = callForRef(callsRef.current, ref)
      if (step) locate(step)
    },
    [locate],
  )

  /** One handler for every card in every stack, so a held card stays held. */
  const toggleCall = useCallback((index: number) => {
    setOpenCall((prev) => (prev === index ? -1 : index))
  }, [])

  const openInTools = useCallback(
    (step: StepCall) => {
      setTab('tools')
      setPane(true)
      setHighlighted(step.index)
    },
    [setPane],
  )

  const noteKinds = useMemo<NoteKind[]>(() => {
    if (detail?.kind === 'rca') return ['rca', 'resolution']
    if (detail?.kind === 'fix') return ['']
    return ['triage']
  }, [detail?.kind])
  const notePath = notePathFor(noteKinds[0], detail?.notes)
  const shownTab: Tab = tab ?? (isFix ? 'changes' : 'note')

  const onDiffLoaded = useCallback((diff: RunDiff | null) => {
    setChanged(diff ? diff.files.length : null)
  }, [])

  const noteOwnWords = useCallback(
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
    async (text: string, model = pickedModel) => {
      setPending('answer')
      setActionError('')
      try {
        const started = await transport.resume(workspaceId, runId, text, model)
        if (started?.jobId) setRunJob(runId, started.jobId)
        noteOwnWords(text)
        setSent((n) => n + 1)
        stick.current = true
      } catch (err: unknown) {
        setActionError(withoutCode(err))
      } finally {
        setPending('')
      }
    },
    [transport, workspaceId, runId, noteOwnWords, pickedModel],
  )

  const steer = useCallback(
    async (text: string) => {
      setPending('steer')
      setActionError('')
      try {
        const started = await transport.steer(workspaceId, runId, text, pickedModel)
        if (started?.jobId) setRunJob(runId, started.jobId)
        setSent((n) => n + 1)
        setDetail((prev) => (prev ? { ...prev, status: 'running' } : prev))
        noteOwnWords(text)
        stick.current = true
      } catch (err: unknown) {
        if (/^conflict:|refused/i.test(reasonOf(err))) setSteerRefusal(withoutCode(err))
        else setActionError(withoutCode(err))
      } finally {
        setPending('')
      }
    },
    [transport, workspaceId, runId, noteOwnWords, setDetail, pickedModel],
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
  /**
   * The model the login has no room for, when that is why the run stopped.
   * It is a choice rather than a wait — every other model on the account is
   * answering — so the banner over the composer offers the choice.
   */
  const limitedModel = blocked ? modelLimited(detail?.reason) : ''

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
        return { kind: 'running' }
    }
  }, [detail, question, steerRefusal])

  const send = mode.kind === 'answer' ? answer : steer
  const sendBusy = pending === 'answer' || pending === 'steer'

  // The screen's one filled control, whichever it is: the send, or the Stop
  // that stands in its place while the run works.
  useProvidePrimaryAction(
    detail
      ? {
          label: mode.kind === 'answer' ? 'Answer' : mode.kind === 'running' ? 'Stop' : 'Steer',
          onRun: () => {},
          disabled: mode.kind === 'disabled' || (mode.kind === 'running' && !jobId),
          busy: mode.kind === 'running' ? pending === 'cancel' : sendBusy,
          shortcut: mode.kind === 'running' ? undefined : '↵',
          title: mode.kind === 'disabled' ? mode.reason : undefined,
          placement: 'screen',
        }
      : null,
  )

  const renderItem = (item: ChatItem): JSX.Element | null => {
    if (!detail) return null
    switch (item.kind) {
      case 'sys':
        return (
          <div key={item.index} className="sc-sys" data-item={item.index}>
            <span>{item.parts.map((p, i) => (typeof p === 'string' ? p : <b key={i}>{p.b}</b>))}</span>
          </div>
        )
      case 'stack':
        return (
          <div key={item.index} data-item={item.index}>
            <ToolStack
              calls={item.calls}
              head={item.head}
              openIndex={openCall}
              onToggle={toggleCall}
              highlighted={highlighted}
              live={live}
              blocked={blocked}
              onOpenInTools={openInTools}
            />
          </div>
        )
      case 'think':
        return (
          <div key={item.index} className="sc-think" data-item={item.index} data-testid="think-stamp">
            <ThinkIcon />
            <span>Thought for {item.seconds} s</span>
            {item.tokens ? <span className="sc-mono">~{item.tokens.toLocaleString('en-US')} tokens</span> : null}
            <span>·</span>
            <span className="sc-mono">{item.at}</span>
          </div>
        )
      case 'wrote':
        return (
          <div key={item.index} className="sc-think" data-item={item.index} data-testid="wrote-stamp">
            <BracesIcon />
            <span>{item.what}</span>
            <span className="sc-mono">
              {item.seconds} s · {item.from} – {item.to}
            </span>
          </div>
        )
      case 'you':
        return (
          <div key={item.index} className="sc-you" data-item={item.index} data-testid="you-bubble">
            <div className="sc-you__meta">
              <span>You</span>
              {item.at ? (
                <>
                  <span>·</span>
                  <span>{item.at}</span>
                </>
              ) : null}
              {item.continuation ? (
                <>
                  <span>·</span>
                  <span>{continuationWord(item.continuation)}</span>
                </>
              ) : null}
            </div>
            <div dir="auto">{item.text}</div>
          </div>
        )
      case 'say':
        return (
          <div key={item.index} className="sc-agent" data-item={item.index} data-testid="assistant-message">
            <ProviderMark provider={detail.provider} size="sm" />
            <div className="sc-agent__say" dir="auto">
              <ReactMarkdown>{item.text}</ReactMarkdown>
            </div>
          </div>
        )
      case 'answer':
        if (item.superseded) {
          return (
            <div key={item.index} data-item={item.index}>
              <SupersededAnswer at={item.at} seconds={item.seconds} />
            </div>
          )
        }
        return (
          <div key={item.index} data-item={item.index}>
            <AnswerCard
              event={item.event}
              at={item.at}
              revised={item.revised}
              kind={detail.kind}
              notePath={notePath}
              notesDir={notesDir}
              fix={detail.fix}
              onRef={onRef}
              onOpenNote={
                isFix
                  ? undefined
                  : () => {
                      setTab('note')
                      setPane(true)
                    }
              }
              onOpenChanges={
                isFix
                  ? () => {
                      setTab('changes')
                      setPane(true)
                    }
                  : undefined
              }
            />
          </div>
        )
      case 'error':
        return (
          <div key={item.index} className="sc-sys sc-sys--error" data-item={item.index} role="alert">
            <span>
              Failed <b>{item.at}</b> · {item.text}
            </span>
          </div>
        )
      case 'callout':
        if (!item.rateLimited && blocked) return null
        return (
          <div key={item.index} className="sc-sys" data-item={item.index}>
            <span>
              {item.rateLimited ? 'Rate limited' : 'The agent asked'} <b>{item.at}</b>
              {item.text ? ` · ${item.text}` : ''}
            </span>
          </div>
        )
      default:
        return null
    }
  }

  /*
   * The transcript's rows, each one held until something it draws moves.
   *
   * The model keeps a row's object across a streamed line, so a line that
   * changes one row leaves the others alone — but the rows are drawn here,
   * and drawing them all again throws that away: a fresh element for a row
   * React already has is a re-render of that row's subtree. So a row whose
   * item is the very object of the render before is handed back its own
   * element, and React, seeing the same element, leaves the subtree where
   * it is. A line that draws nothing costs nothing; a line that does costs
   * the rows it touched.
   *
   * Everything `renderItem` reads besides the item itself is in `drawnWith`,
   * and any of those moving redraws every row: a row drawn against a stale
   * value is a worse fault than a re-render.
   */
  const drawnWith = [detail, openCall, highlighted, live, blocked, isFix, notePath, notesDir, onRef, openInTools, toggleCall, setPane]
  const drawn = useRef<{ with: unknown[]; items: ChatItem[]; rows: (JSX.Element | null)[] }>({ with: [], items: [], rows: [] })
  const rows = useMemo(
    () => {
      const last = drawn.current
      const same = last.with.length === drawnWith.length && last.with.every((v, i) => v === drawnWith[i])
      const out = model.items.map((item, i) => (same && last.items[i] === item ? last.rows[i] : renderItem(item)))
      drawn.current = { with: drawnWith, items: model.items, rows: out }
      return out
    },
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [model.items, ...drawnWith],
  )

  if (loadError || !detail) {
    return (
      <div className="sc">
        <p className={loadError ? 'sc-failed' : 'sc-loading'}>{loadError || 'Loading run…'}</p>
      </div>
    )
  }

  const tabs: { id: Tab; label: string; count?: number }[] = [
    ...(isFix ? [{ id: 'changes' as const, label: 'Changes', count: changed ?? undefined }] : []),
    { id: 'note', label: 'Note' },
    { id: 'bundle', label: 'Bundle' },
    { id: 'tools', label: 'Tools', count: model.calls.length },
  ]
  const shownIndex = Math.max(
    tabs.findIndex((t) => t.id === shownTab),
    0,
  )

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
    e.currentTarget.querySelectorAll<HTMLButtonElement>('.sc-tab')[tabs.indexOf(next)]?.focus()
  }

  const placeholder =
    mode.kind === 'answer'
      ? 'Answer the question — the run resumes with your message'
      : isFix
        ? 'Ask for a change to the fix — it resumes in the same worktree'
        : 'Steer the run or ask a follow-up — it resumes with the note and the transcript in context'

  /** The closing line: when, how many turns, what it cost, what it left. */
  const finishLine = (): JSX.Element | null => {
    if (!terminal) return null
    const u = detail.usage
    const parts: (string | JSX.Element)[] = []
    if (u?.turns) parts.push(`${u.turns} ${u.turns === 1 ? 'turn' : 'turns'}`)
    if (u?.costUsd) parts.push(usd(u.costUsd))
    if (u?.inputTokens) parts.push(`${tokens(u.inputTokens)} in`)
    if (u?.outputTokens) parts.push(`${tokens(u.outputTokens)} out`)
    if (status === 'completed') {
      if (isFix) parts.push(detail.fix?.pushed ? 'branch pushed' : detail.fix?.commit ? 'local branch, not pushed' : 'no commit')
      else if (notePath) parts.push('note written')
    } else if (detail.reason) {
      parts.push(detail.reason)
    }
    const word = status === 'completed' ? 'Finished' : status === 'failed' ? 'Failed' : 'Stopped'
    return (
      <div className={`sc-sys${status === 'failed' ? ' sc-sys--error' : ''}`} data-testid="finish-line">
        <span>
          {word} <b>{clock(detail.updatedAt, detail.startedAt)}</b>
          {parts.length > 0 ? ` · ${parts.join(' · ')}` : ''}
        </span>
      </div>
    )
  }

  return (
    <div className="sc" data-layout="conversation">
      <RunHeader
        detail={detail}
        title={title}
        sources={sources}
        live={live}
        fallbackModel={model.start?.model}
      />

      <span className="visually-hidden" aria-live="polite">
        {`Run ${stateWord(detail.status)}`}
      </span>

      {/* The composer carries what a send or a stop came back with; the
          line above it is for the errors no composer is up to hold. */}
      {actionError && pending !== 'accept' && mode.kind === 'disabled' ? (
        <p className="sc-failed-line" role="alert">
          {actionError}
        </p>
      ) : null}

      <div className="sc-body" data-pane={paneOpen ? undefined : 'collapsed'}>
        <div className="sc-left">
          <div
            className="sc-stream"
            ref={streamRef}
            onScroll={onScroll}
            role="log"
            aria-live="polite"
            aria-label="Transcript"
            data-testid="conversation"
            dir="ltr"
          >
            <div className="sc-flow">
              {model.items.length === 0 ? (
                <p className="sc-empty-flow">
                  {events.length === 0 ? 'No events yet. They appear here as the agent works.' : 'Nothing the agent did or said yet.'}
                </p>
              ) : (
                rows
              )}
              {blocked ? (
                <AskCard
                  question={question}
                  reason={detail.reason}
                  at={clock(detail.updatedAt, detail.startedAt)}
                  pendingCall={model.pendingCall}
                />
              ) : null}
              {finishLine()}
            </div>
          </div>
          {showJump ? (
            <button type="button" className="sc-jump" onClick={toBottom}>
              Jump to latest
            </button>
          ) : null}
          <div className="sc-composer" data-mode={mode.kind}>
            {limitedModel ? (
              <ModelLimitBanner
                transport={transport}
                workspaceId={workspaceId}
                provider={detail.provider}
                limited={limitedModel}
                busy={sendBusy}
                onContinue={(model) => {
                  setPickedModel(model)
                  void answer('', model)
                }}
              />
            ) : null}
            <Composer
              mode={mode}
              busy={sendBusy}
              error={mode.kind === 'disabled' ? '' : actionError}
              onSend={(text) => void send(text)}
              provider={detail.provider}
              model={detail.model || model.start?.model || ''}
              kind={detail.kind}
              sentCount={sent}
              autoFocus={mode.kind === 'answer'}
              placeholder={placeholder}
              wideWhenAnswering
              onCancel={() => void cancel()}
              canCancel={Boolean(jobId) && pending === ''}
              cancelBusy={pending === 'cancel'}
              pickedModel={pickedModel}
              onPickModel={terminal || blocked ? setPickedModel : undefined}
            />
          </div>
        </div>

        {paneOpen ? (
          <div className="sc-right" id={paneId}>
            <div className="sc-pane-head">
              <div className="sc-tabs" role="tablist" aria-label="Run artefacts" onKeyDown={onTabKey}>
                {tabs.map((t, i) => (
                  <button
                    key={t.id}
                    type="button"
                    role="tab"
                    id={`${tabsId}-tab-${t.id}`}
                    className="sc-tab"
                    aria-selected={shownTab === t.id}
                    aria-controls={`${tabsId}-panel`}
                    tabIndex={i === shownIndex ? 0 : -1}
                    onClick={() => setTab(t.id)}
                  >
                    {t.label}
                    {t.count !== undefined ? <span className="sc-tab__n">{t.count}</span> : null}
                  </button>
                ))}
              </div>
              <PanelToggle
                open
                side="end"
                hideLabel="Hide panel"
                showLabel="Show panel"
                shortcut="⌘\"
                controls={paneId}
                onToggle={() => setPane(false)}
              />
            </div>
            <div className="sc-panel" role="tabpanel" id={`${tabsId}-panel`} aria-labelledby={`${tabsId}-tab-${shownTab}`}>
              {shownTab === 'changes' && isFix ? (
                <ChangesView
                  transport={transport}
                  workspaceId={workspaceId}
                  runId={runId}
                  checks={model.checks}
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
                <div className="sc-pane">
                  <NoteDocument
                    transport={transport}
                    workspaceId={workspaceId}
                    runId={runId}
                    kinds={noteKinds}
                    reload={finished}
                    notePaths={detail.notes}
                    onRef={onRef}
                  />
                </div>
              ) : null}
              {shownTab === 'bundle' ? (
                <div className="sc-pane">
                  <BundleView
                    transport={transport}
                    workspaceId={workspaceId}
                    runId={runId}
                    bundleDir={detail.bundleDir}
                    promptPath={detail.promptPath}
                  />
                </div>
              ) : null}
              {shownTab === 'tools' ? (
                <div className="sc-pane sc-pane--tools">
                  <ToolsTable
                    calls={model.calls}
                    highlighted={highlighted}
                    onLocate={locate}
                    markers={markers}
                    onMarker={(id) => {
                      const step = model.calls.find((c) => markers.find((m) => m.id === id)?.steps.includes(c.index))
                      if (step) locate(step)
                    }}
                  />
                </div>
              ) : null}
            </div>
          </div>
        ) : (
          <div className="sc-rail" aria-label="Run artefacts, folded">
            <PanelToggle
              open={false}
              side="end"
              hideLabel="Hide panel"
              showLabel="Show panel"
              shortcut="⌘\"
              controls={paneId}
              onToggle={() => setPane(true)}
            />
            {tabs.map((t) => (
              <button
                key={t.id}
                type="button"
                className="sc-rail__tab"
                aria-label={t.label}
                title={t.count !== undefined ? `${t.label} (${t.count})` : t.label}
                onClick={() => {
                  setTab(t.id)
                  setPane(true)
                }}
              >
                {TAB_ICONS[t.id]}
              </button>
            ))}
          </div>
        )}
      </div>
    </div>
  )
}
