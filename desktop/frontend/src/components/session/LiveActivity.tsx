import { useEffect, useMemo, useState } from 'react'
import type { IndexedEvent } from '../../lib/events'
import { duringNote, NOTE_LABEL, readActivity, STALL_MS } from '../../lib/activity'
import { duration, tokens } from '../../lib/format'
import './live-activity.css'

export interface LiveActivityProps {
  events: IndexedEvent[]
  /** Only a working run has an activity; any other status draws nothing. */
  working: boolean
  /**
   * The run's phase off its summary. `'note'` while a run that has already
   * replied files its note: the line says so whatever the log's tail reads.
   */
  phase?: string
}

/**
 * The line above the composer while a run works: what the agent is doing
 * this second and for how long. The transcript only grows when a call or a
 * message is complete, so a minute of thinking, or the note's JSON
 * streaming for 66 seconds, used to look exactly like a hang.
 */
export default function LiveActivity({ events, working, phase }: LiveActivityProps): JSX.Element | null {
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
  const read = useMemo(() => (working ? readActivity(events) : undefined), [events, working])
  const filing = working && phase === 'note'
  const parsed = filing && read ? duringNote(read) : read
  if (!parsed) {
    // The summary says the note turn has begun before its first line has
    // landed: there is nothing to time yet, only the fact to say.
    if (!filing) return null
    return (
      <div className="sd-activity" role="status" aria-live="off" data-what="note" data-testid="live-activity">
        <span className="sd-activity__dot" aria-hidden="true" />
        <span className="sd-activity__label">{NOTE_LABEL}</span>
      </div>
    )
  }

  const since = duration(Math.max(0, now - parsed.since))
  const stalled = now - parsed.silentSince > STALL_MS
  const label = stalled && !filing ? `No output for ${duration(Math.max(0, now - parsed.silentSince))}` : parsed.label
  return (
    <div className="sd-activity" role="status" aria-live="off" data-what={parsed.what} data-stalled={stalled ? '' : undefined} data-testid="live-activity">
      <span className="sd-activity__dot" aria-hidden="true" />
      <span className="sd-activity__label">{label}</span>
      {stalled ? (
        <span className="sd-activity__meta">
          {filing ? `· no output for ${duration(Math.max(0, now - parsed.silentSince))}` : `· still ${parsed.label.toLowerCase()}`}
        </span>
      ) : (
        <span className="sd-activity__meta">{since}</span>
      )}
      {parsed.chars ? <span className="sd-activity__meta">· {tokens(parsed.chars)} characters</span> : null}
    </div>
  )
}
