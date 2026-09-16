/**
 * Delivers a burst of events in one go.
 *
 * The event stream is a per-line firehose while a run is working: a provider
 * streaming text writes dozens of `run.event` frames a second, each its own
 * browser task, and each one used to reach every subscriber — and so React —
 * on its own. Queued here and handed over together every `flushMs`, a burst
 * is one task: the store applies each event in turn as before, React batches
 * the state they set into one render, and the transcript grows once a frame
 * rather than once a line. Order is kept, nothing is dropped, and a single
 * event still arrives within the window.
 */
export interface Coalesced<T> {
  /** Queues one event for the next flush. */
  push(event: T): void
  /** Delivers what is queued now and stops; later pushes are dropped. */
  stop(): void
}

export const FLUSH_MS = 16

export function coalesce<T>(deliver: (event: T) => void, flushMs = FLUSH_MS): Coalesced<T> {
  let queue: T[] = []
  let timer: ReturnType<typeof setTimeout> | null = null
  let stopped = false

  function flush(): void {
    timer = null
    const batch = queue
    queue = []
    for (const event of batch) deliver(event)
  }

  return {
    push(event) {
      if (stopped) return
      queue.push(event)
      if (timer === null) timer = setTimeout(flush, flushMs)
    },
    stop() {
      stopped = true
      if (timer !== null) {
        clearTimeout(timer)
        flush()
      }
    },
  }
}
