import { useEffect, useState } from 'react'
import { elapsedSince, relativeTime } from '../../lib/format'

/**
 * Time on a card. A live run counts up once a second — the only thing on the
 * board that moves on its own, because it is the only thing that is still
 * happening. A finished run shows when it last changed instead.
 */
export default function ElapsedTime(props: {
  startedAt: string
  live: boolean
  updatedAt?: string
}): JSX.Element | null {
  const { startedAt, live, updatedAt } = props
  const [now, setNow] = useState(() => Date.now())

  useEffect(() => {
    if (!live) return
    const id = setInterval(() => setNow(Date.now()), 1000)
    return () => clearInterval(id)
  }, [live])

  if (live) {
    const text = elapsedSince(startedAt, now)
    if (!text) return null
    return (
      <time className="elapsed elapsed--live" dateTime={startedAt} title="Running for">
        {text}
      </time>
    )
  }

  const stamp = updatedAt || startedAt
  const text = relativeTime(stamp, now)
  if (!text) return null
  return (
    <time className="elapsed" dateTime={stamp} title={stamp}>
      {text}
    </time>
  )
}
