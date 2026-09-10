import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import {
  FILTERS,
  filterTurns,
  groupTurns,
  type Filter,
  type IndexedEvent,
} from '../../lib/events'
import TurnGroup from './TurnGroup'

/** How close to the bottom still counts as following the stream, in pixels. */
const STICK_SLACK = 24

/**
 * The live log. It follows the tail while the engineer is at the bottom and
 * stops the moment they scroll up to read something, offering a pill back.
 */
export default function EventStream({
  events,
  startedAt,
  live,
}: {
  events: IndexedEvent[]
  startedAt: string | undefined
  live: boolean
}) {
  const [filter, setFilter] = useState<Filter>('all')
  const [showJump, setShowJump] = useState(false)
  const scrollRef = useRef<HTMLDivElement | null>(null)
  const stick = useRef(true)

  const turns = useMemo(() => groupTurns(events), [events])
  const shown = useMemo(() => filterTurns(turns, filter), [turns, filter])
  const shownCount = useMemo(
    () => shown.reduce((n, t) => n + t.events.length, 0),
    [shown],
  )

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
  }, [events.length, filter, live, toBottom])

  const onScroll = () => {
    const el = scrollRef.current
    if (!el) return
    const near = el.scrollHeight - el.scrollTop - el.clientHeight <= STICK_SLACK
    stick.current = near
    setShowJump(!near && events.length > 0)
  }

  return (
    <>
      <div className="stream-bar" role="group" aria-label="Filter events">
        {FILTERS.map((f) => (
          <button
            key={f.id}
            type="button"
            className="run-chip"
            aria-pressed={filter === f.id}
            onClick={() => setFilter(f.id)}
          >
            {f.label}
          </button>
        ))}
        <span className="stream-count">
          {filter === 'all'
            ? `${events.length} ${events.length === 1 ? 'event' : 'events'}`
            : `${shownCount} of ${events.length}`}
        </span>
      </div>
      <div className="stream">
        <div className="stream-scroll" ref={scrollRef} onScroll={onScroll}>
          {shown.length === 0 ? (
            <p className="stream-empty">
              {events.length === 0
                ? 'No events yet. They appear here as the agent works.'
                : 'Nothing matches this filter.'}
            </p>
          ) : (
            shown.map((turn) => (
              <TurnGroup key={turn.n} turn={turn} startedAt={startedAt} />
            ))
          )}
        </div>
        {showJump ? (
          <button type="button" className="stream-jump" onClick={toBottom}>
            Jump to latest
          </button>
        ) : null}
      </div>
    </>
  )
}
