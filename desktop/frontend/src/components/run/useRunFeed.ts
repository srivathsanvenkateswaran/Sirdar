import { useCallback, useEffect, useRef, useState, type Dispatch, type SetStateAction } from 'react'
import type { RunDetail, RunEvent, RunSummary, Transport } from '../../api/types'
import type { IndexedEvent } from '../../lib/events'
import { parseTime, reasonOf } from '../../lib/format'

/** The states in which the agent is working. */
export const LIVE: ReadonlySet<string> = new Set(['preparing', 'running'])

/**
 * The states a run does not come back from. `blocked` is not one of them: it
 * is waiting for an answer and resumes into `running`, so Cancel stays on
 * offer there and the artefacts are not asked for again.
 */
export const TERMINAL: ReadonlySet<string> = new Set(['completed', 'failed', 'over_budget'])

/**
 * Puts an event where its index belongs. The live stream arrives in order,
 * so the common case is one push at the end; the backfill and a late line
 * that overtook it are the exceptions, and they are placed by a binary
 * search rather than by sorting the whole transcript again for every line.
 */
export function insertByIndex(list: IndexedEvent[], item: IndexedEvent): IndexedEvent[] {
  const last = list[list.length - 1]
  if (!last || item.index > last.index) return [...list, item]
  let lo = 0
  let hi = list.length
  while (lo < hi) {
    const mid = (lo + hi) >> 1
    if (list[mid].index < item.index) lo = mid + 1
    else hi = mid
  }
  return [...list.slice(0, lo), item, ...list.slice(lo)]
}

/**
 * Many events at once — the backfill, or a coalesced burst — placed with one
 * copy of the list rather than one per line. Each item is placed by the same
 * binary search; the in-order case is a push.
 */
export function insertManyByIndex(list: IndexedEvent[], items: IndexedEvent[]): IndexedEvent[] {
  if (items.length === 0) return list
  const next = list.slice()
  for (const item of items) {
    const last = next[next.length - 1]
    if (!last || item.index > last.index) {
      next.push(item)
      continue
    }
    let lo = 0
    let hi = next.length
    while (lo < hi) {
      const mid = (lo + hi) >> 1
      if (next[mid].index < item.index) lo = mid + 1
      else hi = mid
    }
    next.splice(lo, 0, item)
  }
  return next
}

/** True when `update` was written after `detail`; an unreadable stamp on either side counts as yes. */
function newer(update: RunSummary, detail: RunDetail): boolean {
  const a = parseTime(update.updatedAt)
  const b = parseTime(detail.updatedAt)
  if (Number.isNaN(a) || Number.isNaN(b)) return true
  return a >= b
}

export interface RunFeed {
  detail: RunDetail | null
  setDetail: Dispatch<SetStateAction<RunDetail | null>>
  events: IndexedEvent[]
  setEvents: Dispatch<SetStateAction<IndexedEvent[]>>
  /** Why the run or its log could not be read; empty while both are fine. */
  loadError: string
  /**
   * How many times the run has gone from live to ended while this feed was
   * watching it. The note, the fix result and the rest of state.json are
   * written as the run finishes, so a screen opened mid-run asks for its
   * artefacts again each time this moves; the run itself is re-read here.
   */
  finished: number
}

/**
 * What the Session and the Change review both read: the run, its event log,
 * and the two subscriptions that keep them current.
 *
 * It subscribes before backfilling so nothing written between the two is
 * lost, and the index dedupe absorbs whatever the two deliveries have in
 * common. A `run.updated` that lands before the run itself has been read is
 * kept and merged in when it does, rather than dropped for want of
 * something to merge into. A backfill that fails is a run that cannot be
 * read, and says so.
 */
export function useRunFeed(transport: Transport, workspaceId: string, runId: string): RunFeed {
  const [detail, setDetail] = useState<RunDetail | null>(null)
  const [events, setEvents] = useState<IndexedEvent[]>([])
  const [loadError, setLoadError] = useState('')
  const [finished, setFinished] = useState(0)
  const seen = useRef<Set<number>>(new Set())
  /** The last `run.updated` to arrive while `detail` was still null. */
  const pendingUpdate = useRef<RunSummary | null>(null)
  /** The status of the previous render, for spotting the run finishing. */
  const wasStatus = useRef('')

  const status = detail?.status ?? ''

  useEffect(() => {
    let cancelled = false
    seen.current = new Set()
    pendingUpdate.current = null
    wasStatus.current = ''
    setDetail(null)
    setEvents([])
    setLoadError('')
    setFinished(0)

    const append = (index: number, event: RunEvent) => {
      if (seen.current.has(index)) return
      seen.current.add(index)
      setEvents((prev) => insertByIndex(prev, { index, event }))
    }
    const appendMany = (items: IndexedEvent[]) => {
      const fresh = items.filter((item) => !seen.current.has(item.index))
      if (fresh.length === 0) return
      for (const item of fresh) seen.current.add(item.index)
      setEvents((prev) => insertManyByIndex(prev, fresh))
    }

    /**
     * Re-reads the run's log after `from`. The service sends a resync when
     * it could not keep this window supplied; the index dedupe absorbs
     * whatever the re-read has in common with what is already here, so the
     * only new rows are the ones that went missing.
     */
    const refill = (from: number) => {
      transport
        .events(workspaceId, runId, Math.max(0, from))
        .then(({ events: page, next }) => {
          if (cancelled) return
          const first = Math.max(from + 1, next - page.length + 1)
          appendMany(page.map((event, i) => ({ index: first + i, event })))
        })
        .catch(() => {
          // The transcript keeps what it has; the next resync, or the run
          // finishing, asks again.
        })
    }

    const unsubscribe = transport.subscribe((e) => {
      if (cancelled) return
      if (e.kind === 'run.event') {
        if (e.runId !== runId) return
        if (e.workspaceId && e.workspaceId !== workspaceId) return
        append(e.index, e.event)
        return
      }
      if (e.kind === 'run.resync') {
        if (e.runId === runId) refill(e.from)
        return
      }
      if (e.kind === 'run.updated' && e.run?.runId === runId) {
        const update = e.run
        setDetail((prev) => {
          if (prev) return { ...prev, ...update }
          pendingUpdate.current = update
          return prev
        })
      }
    })

    transport
      .run(workspaceId, runId)
      .then((d) => {
        if (cancelled) return
        const update = pendingUpdate.current
        pendingUpdate.current = null
        setDetail(update && newer(update, d) ? { ...d, ...update } : d)
      })
      .catch((err: unknown) => {
        if (!cancelled) setLoadError(reasonOf(err))
      })

    transport
      .events(workspaceId, runId, 0)
      .then(({ events: backfill, next }) => {
        if (cancelled) return
        // The watcher and Service.Events both number events from 1, and
        // `next` is the index of the last line in this page. Numbering the
        // backfill from zero would leave the last line sharing no index with
        // the live event that repeats it, and the stream would show it twice.
        const first = Math.max(1, next - backfill.length + 1)
        appendMany(backfill.map((event, i) => ({ index: first + i, event })))
      })
      .catch((err: unknown) => {
        // A run with no log yet answers with an empty page, not a failure;
        // a rejection here is a run whose log cannot be read.
        if (!cancelled) setLoadError(reasonOf(err))
      })

    return () => {
      cancelled = true
      unsubscribe()
    }
  }, [transport, workspaceId, runId])

  const reread = useCallback(() => {
    let cancelled = false
    transport
      .run(workspaceId, runId)
      .then((d) => {
        if (!cancelled) setDetail(d)
      })
      .catch(() => {
        // The header already carries the finished status from the event; a
        // re-read that fails leaves the screen as it was rather than blanking
        // a run the reader is looking at.
      })
    return () => {
      cancelled = true
    }
  }, [transport, workspaceId, runId])

  useEffect(() => {
    const before = wasStatus.current
    wasStatus.current = status
    if (!LIVE.has(before) || !TERMINAL.has(status)) return
    setFinished((n) => n + 1)
    return reread()
  }, [status, reread])

  return { detail, setDetail, events, setEvents, loadError, finished }
}
