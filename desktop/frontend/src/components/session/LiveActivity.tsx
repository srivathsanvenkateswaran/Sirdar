import { useEffect, useMemo, useState } from 'react'
import type { IndexedEvent } from '../../lib/events'
import { readActivity, STALL_MS } from '../../lib/activity'
import { duration, tokens } from '../../lib/format'
import './live-activity.css'

export interface LiveActivityProps {
  events: IndexedEvent[]
  /** Only a working run has an activity; any other status draws nothing. */
  working: boolean
}

/**
 * The line above the composer while a run works: what the agent is doing
 * this second and for how long. The transcript only grows when a call or a
 * message is complete, so a minute of thinking, or the note's JSON
 * streaming for 66 seconds, used to look exactly like a hang.
 */
export default function LiveActivity({ events, working }: LiveActivityProps): JSX.Element | null {
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    if (!working) return
    const tick = setInterval(() => setNow(Date.now()), 1000)
    return () => clearInterval(tick)
  }, [working])

  // The log changes far less often than the clock; parsing it is the
  // expensive part, so it only reruns when `events` itself changes. `now`
  // ticking every second recomputes just the two numbers below it, not the
  // whole tail-walk `readActivity` does.
  const parsed = useMemo(() => (working ? readActivity(events) : undefined), [events, working])
  if (!parsed) return null

  const since = duration(Math.max(0, now - parsed.since))
  const stalled = now - parsed.silentSince > STALL_MS
  const label = stalled ? `No output for ${duration(Math.max(0, now - parsed.silentSince))}` : parsed.label
  return (
    <div className="sd-activity" role="status" aria-live="off" data-what={parsed.what} data-stalled={stalled ? '' : undefined} data-testid="live-activity">
      <span className="sd-activity__dot" aria-hidden="true" />
      <span className="sd-activity__label">{label}</span>
      {stalled ? <span className="sd-activity__meta">· still {parsed.label.toLowerCase()}</span> : <span className="sd-activity__meta">{since}</span>}
      {parsed.chars ? <span className="sd-activity__meta">· {tokens(parsed.chars)} characters</span> : null}
    </div>
  )
}
