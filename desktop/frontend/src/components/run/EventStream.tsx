import { useCallback, useEffect, useId, useMemo, useRef, useState, type ReactNode } from 'react'
import { classify, groupTurns, type IndexedEvent, type Turn } from '../../lib/events'
import Toggle from '../../ui/toggle'
import TurnGroup from './TurnGroup'

/** How close to the bottom still counts as following the stream, in pixels. */
const STICK_SLACK = 24

/**
 * What the transcript shows until "Show everything" is on: the things the
 * agent did and said. The raw `stream_event` deltas a provider writes by the
 * dozen per turn and the per-turn usage tick are bookkeeping, and the topbar
 * already carries the turn count and the cost.
 */
export function quietTurns(turns: Turn[]): Turn[] {
  const out: Turn[] = []
  for (const turn of turns) {
    const events = turn.events.filter((e) => {
      const family = classify(e.event)
      return family !== 'system' && family !== 'usage'
    })
    if (events.length > 0) out.push({ ...turn, events })
  }
  return out
}

/**
 * The transcript. What the agent did and said, in turns: tool calls with
 * their results, its prose, the policy's decisions, its questions, the note
 * landing, and the operator's own words as bubbles among them. "Show
 * everything" adds the raw stream deltas, folded behind one row per burst,
 * and the per-turn usage ticks. It follows the tail while the engineer is at
 * the bottom and stops the moment they scroll up to read something, offering
 * a pill back.
 *
 * It is pinned `dir="ltr"`. What it shows is tool names, file paths,
 * queries and JSON, and a right-to-left layout moves their leading slashes,
 * brackets and colons to the wrong end — so the one place an Arabic string
 * appears here (a quoted ticket line) is worth less than keeping every path
 * readable.
 */
export default function EventStream({
  events,
  startedAt,
  live,
  head,
}: {
  events: IndexedEvent[]
  startedAt: string | undefined
  live: boolean
  /** Drawn above the scroll and outside it: the banner for the last finished step. */
  head?: ReactNode
}) {
  const [showJump, setShowJump] = useState(false)
  const [everything, setEverything] = useState(false)
  const scrollRef = useRef<HTMLDivElement | null>(null)
  const stick = useRef(true)
  const toggleId = useId()

  const turns = useMemo(() => {
    const all = groupTurns(events)
    return everything ? all : quietTurns(all)
  }, [events, everything])

  const toBottom = useCallback(() => {
    const el = scrollRef.current
    if (el) el.scrollTop = el.scrollHeight
    stick.current = true
    setShowJump(false)
  }, [])

  useEffect(() => {
    if (stick.current) {
      toBottom()
      return
    }
    if (events.length > 0) setShowJump(true)
  }, [events.length, live, toBottom])

  const onScroll = () => {
    const el = scrollRef.current
    if (!el) return
    const near = el.scrollHeight - el.scrollTop - el.clientHeight <= STICK_SLACK
    stick.current = near
    setShowJump(!near && events.length > 0)
  }

  return (
    <div className="stream" dir="ltr">
      {head}
      <div className="stream-head">
        <span className="stream-head__label" id={toggleId}>
          Show everything
        </span>
        <Toggle checked={everything} onChange={setEverything} label="Show everything" labelledBy={toggleId} />
      </div>
      <div
        className="stream-scroll"
        ref={scrollRef}
        onScroll={onScroll}
        data-testid="event-stream"
        role="log"
        aria-live="polite"
        aria-label="Transcript"
      >
        {turns.length === 0 ? (
          <p className="stream-empty">
            {events.length === 0
              ? 'No events yet. They appear here as the agent works.'
              : 'Nothing but stream events yet. Show everything to see them.'}
          </p>
        ) : (
          turns.map((turn) => <TurnGroup key={turn.n} turn={turn} startedAt={startedAt} fold />)
        )}
      </div>
      {showJump ? (
        <button type="button" className="stream-jump" onClick={toBottom}>
          Jump to latest
        </button>
      ) : null}
    </div>
  )
}
