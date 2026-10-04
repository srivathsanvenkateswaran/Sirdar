import { useEffect, useMemo, useState } from 'react'
import type { IndexedEvent } from '../../lib/events'
import { currentActivity } from '../../lib/activity'
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

  // The log changes far less often than the clock; read it once per change.
  const read = useMemo(() => (working ? (at: number) => currentActivity(events, at) : undefined), [events, working])
  const activity = read?.(now)
  if (!activity) return null

  const since = duration(Math.max(0, now - activity.since))
  const label = activity.stalled ? `No output for ${since}` : activity.label
  return (
    <div className="sd-activity" role="status" aria-live="off" data-what={activity.what} data-stalled={activity.stalled ? '' : undefined} data-testid="live-activity">
      <span className="sd-activity__dot" aria-hidden="true" />
      <span className="sd-activity__label">{label}</span>
      {activity.stalled ? <span className="sd-activity__meta">· still {activity.label.toLowerCase()}</span> : <span className="sd-activity__meta">{since}</span>}
      {activity.chars ? <span className="sd-activity__meta">· {tokens(activity.chars)} characters</span> : null}
    </div>
  )
}
