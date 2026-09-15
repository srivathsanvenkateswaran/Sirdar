import { useEffect, useState } from 'react'
import type { RunSummary } from '../../api/types'
import { elapsedSince } from '../../lib/format'
import SdRunCard from '../../ui/run-card'
import type { GlyphState } from '../../ui/state-glyph'

const LIVE = new Set(['preparing', 'running'])

/**
 * A run in any lane past the queue, drawn by the library's run card.
 *
 * What stays here is the arithmetic the library component refuses to do: a
 * live run's clock counts up from its start once a second — the only thing on
 * the board that moves on its own, because it is the only thing still
 * happening — and a blocked run's clock counts how long a person has been
 * asked. Any other state gets no clock. The card is handed a string and lays
 * it out.
 *
 * A key whose RCA is written is `done` on the board; the CLI reports the run
 * itself as `completed`, so the lane says which word the card gets.
 */
export default function RunCard(props: {
  run: RunSummary
  title?: string
  /** The board's word for a completed RCA, in the Done lane. */
  done?: boolean
  onOpen: (runId: string) => void
}): JSX.Element {
  const { run, title, done = false, onOpen } = props
  const live = LIVE.has(run.status)
  const blocked = run.status === 'blocked'
  const [now, setNow] = useState(() => Date.now())

  useEffect(() => {
    if (!live && !blocked) return
    const id = setInterval(() => setNow(Date.now()), 1000)
    return () => clearInterval(id)
  }, [live, blocked])

  const clock = live
    ? elapsedSince(run.startedAt, now)
    : blocked
      ? elapsedSince(run.updatedAt || run.startedAt, now)
      : ''

  const status: GlyphState = done && run.status === 'completed' ? 'done' : (run.status as GlyphState)

  return (
    <SdRunCard
      runKey={run.key}
      kind={run.kind}
      status={status}
      title={title}
      provider={run.provider}
      assignee={run.assignee}
      clock={clock || undefined}
      clockTitle={live ? 'Running for' : 'Waiting for an answer for'}
      onOpen={() => onOpen(run.runId)}
    />
  )
}
