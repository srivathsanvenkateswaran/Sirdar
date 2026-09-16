import { useMemo } from 'react'
import { classify, filterTurns, groupTurns, lastCallIndex, type IndexedEvent } from '../../lib/events'
import TurnGroup from './TurnGroup'

/** How many of the run's events are tool calls or the policy's answers to them. */
export function toolCount(events: IndexedEvent[]): number {
  let n = 0
  for (const { event } of events) {
    const family = classify(event)
    if (family === 'tool' || family === 'permission') n += 1
  }
  return n
}

/**
 * The tool ledger: every call the agent made and what the policy said to
 * it, in turns, with the prose and the deltas left out. It is the transcript
 * under the "Tools" filter it used to open on, moved to its own tab so the
 * transcript can show everything and this can show only what was done.
 */
export default function ToolsPane({
  events,
  startedAt,
  provider = '',
  live = false,
}: {
  events: IndexedEvent[]
  startedAt: string | undefined
  provider?: string
  live?: boolean
}) {
  const turns = useMemo(() => filterTurns(groupTurns(events), 'tools'), [events])
  const lastCall = useMemo(() => lastCallIndex(events), [events])

  return (
    <div className="tools" dir="ltr">
      {turns.length === 0 ? (
        <p className="pane-empty changes-note">No tool has been called yet.</p>
      ) : (
        turns.map((turn) => (
          <TurnGroup
            key={turn.n}
            turn={turn}
            startedAt={startedAt}
            provider={provider}
            lastCall={lastCall}
            live={live}
          />
        ))
      )}
    </div>
  )
}
