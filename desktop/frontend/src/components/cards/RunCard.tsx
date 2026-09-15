import { useEffect, useState } from 'react'
import type { RunSummary } from '../../api/types'
import { costOrUnknown, elapsedSince, relativeTime, tokenFlow } from '../../lib/format'
import SdRunCard from '../../ui/run-card'
import type { SdStatus } from '../../ui/status-badge'

const LIVE = new Set(['preparing', 'running'])
const NEEDS_REASON = new Set(['blocked', 'failed', 'over_budget'])

/**
 * A run in any lane past the queue, drawn by the library's run card.
 *
 * What stays here is the arithmetic the library component refuses to do: a
 * live run's clock counts up once a second — the only thing on the board that
 * moves on its own, because it is the only thing still happening — and a
 * finished one shows when it last changed instead. The card is handed two
 * strings and lays them out.
 */
export default function RunCard(props: {
  run: RunSummary
  title?: string
  priority?: string
  onOpen: (runId: string) => void
}): JSX.Element {
  const { run, title, priority, onOpen } = props
  const live = LIVE.has(run.status)
  const [now, setNow] = useState(() => Date.now())

  useEffect(() => {
    if (!live) return
    const id = setInterval(() => setNow(Date.now()), 1000)
    return () => clearInterval(id)
  }, [live])

  const stamp = run.updatedAt || run.startedAt
  const clock = live ? elapsedSince(run.startedAt, now) : relativeTime(stamp, now)

  const usage = run.usage
  const cost = usage?.costUsd ?? 0
  const turns = usage?.turns ?? 0
  const spent = usage && (cost !== 0 || turns !== 0) ? costOrUnknown(cost, live) : ''

  return (
    <SdRunCard
      runKey={run.key}
      kind={run.kind}
      status={run.status as SdStatus}
      title={title}
      reason={NEEDS_REASON.has(run.status) ? run.reason : undefined}
      priority={priority}
      elapsed={clock || undefined}
      elapsedTitle={live ? 'Running for' : stamp}
      cost={spent || undefined}
      costTitle={
        usage
          ? `${turns} ${turns === 1 ? 'turn' : 'turns'}, ${tokenFlow(usage.inputTokens, usage.outputTokens)}${
              live && cost === 0 ? '; cost is reported when the session ends' : ''
            }`
          : undefined
      }
      onOpen={() => onOpen(run.runId)}
    />
  )
}
