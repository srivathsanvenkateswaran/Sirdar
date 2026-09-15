import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { groupTurns, type IndexedEvent } from '../../lib/events'
import TurnGroup from './TurnGroup'

/** How close to the bottom still counts as following the stream, in pixels. */
const STICK_SLACK = 24

/**
 * The transcript. Every line the run wrote, in turns, with the raw stream
 * deltas folded behind one row per burst; the operator's own words as
 * bubbles among them. It follows the tail while the engineer is at the
 * bottom and stops the moment they scroll up to read something, offering a
 * pill back.
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
  const scrollRef = useRef<HTMLDivElement | null>(null)
  const stick = useRef(true)

  const turns = useMemo(() => groupTurns(events), [events])

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
      <div className="stream-scroll" ref={scrollRef} onScroll={onScroll} data-testid="event-stream">
        {turns.length === 0 ? (
          <p className="stream-empty">No events yet. They appear here as the agent works.</p>
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
