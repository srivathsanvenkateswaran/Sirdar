import { act, renderHook } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { useState } from 'react'
import type { RunDetail, RunEvent } from '../../api/types'
import type { IndexedEvent } from '../../lib/events'
import { FIX_DETAIL, TRIAGE_DETAIL, fixEvents, triageEvents } from './fixtures'
import { buildSessionModel, useSessionModel } from './model'

function indexed(events: RunEvent[]): IndexedEvent[] {
  return events.map((event, i) => ({ index: i + 1, event }))
}

/**
 * The hook under test, driven the way `useRunFeed` drives it: each line
 * appends to the array, so every render hands it the array from the render
 * before with one more row on the end.
 */
function useStreamed(all: IndexedEvent[], detail: RunDetail | null) {
  const [n, setN] = useState(0)
  const [events, setEvents] = useState<IndexedEvent[]>([])
  const model = useSessionModel(events, detail)
  return {
    model,
    n,
    next: () => {
      setEvents((prev) => [...prev, all[prev.length]])
      setN((k) => k + 1)
    },
  }
}

describe('the session model built a line at a time', () => {
  /**
   * The property: after any prefix of a run's log, the model the streaming
   * builder holds is the model a whole-log build of that same prefix gives.
   * Every prefix of both fixture runs is checked, so a result that pairs
   * with a call ten rows back, a steer that supersedes an answer, a stack
   * that closes late and a thinking burst still open are all covered.
   */
  for (const [name, events, detail] of [
    ['the triage run', triageEvents(), TRIAGE_DETAIL],
    ['the fix run', fixEvents(), FIX_DETAIL],
  ] as const) {
    it(`matches the whole-log build at every prefix of ${name}`, () => {
      const all = indexed(events as RunEvent[])
      const { result } = renderHook(() => useStreamed(all, detail))

      for (let n = 1; n <= all.length; n += 1) {
        act(() => {
          result.current.next()
        })
        const whole = buildSessionModel(all.slice(0, n), detail)
        expect({ at: n, model: result.current.model }).toEqual({ at: n, model: whole })
      }
    })
  }

  it('keeps the rows it already drew when a line is appended', () => {
    const all = indexed(triageEvents())
    const { result } = renderHook(() => useStreamed(all, TRIAGE_DETAIL))

    for (let n = 1; n < all.length; n += 1) {
      act(() => {
        result.current.next()
      })
    }
    const before = result.current.model
    act(() => {
      result.current.next()
    })
    const after = result.current.model

    // Every row but the ones the last line touched is the very object the
    // layout drew a moment ago, which is what lets a memoised card sit
    // still while the transcript grows under it.
    const kept = after.items.filter((item, i) => item === before.items[i]).length
    expect(kept).toBeGreaterThanOrEqual(before.items.length - 1)
    expect(after.items.length).toBeGreaterThanOrEqual(before.items.length)
  })

  it('hands back the very same model when the appended lines draw nothing', () => {
    const noise: RunEvent = {
      t: '2026-09-15T12:14:00Z',
      kind: 'system',
      payload: { text: 'stream_event', raw: { type: 'stream_event', event: { type: 'content_block_delta' } } },
    }
    const all = indexed([...triageEvents(), noise, noise, noise, noise, noise])
    const quiet = all.length - 5
    const { result } = renderHook(() => useStreamed(all, TRIAGE_DETAIL))

    for (let n = 1; n <= quiet; n += 1) {
      act(() => {
        result.current.next()
      })
    }
    const settled = result.current.model

    for (let n = 0; n < 5; n += 1) {
      act(() => {
        result.current.next()
      })
      expect(result.current.model.items).toBe(settled.items)
      expect(result.current.model.calls).toBe(settled.calls)
      expect(result.current.model.checks).toBe(settled.checks)
    }
  })

  it('rebuilds when the array is not the one it had with lines added', () => {
    const all = indexed(triageEvents())
    const { result, rerender } = renderHook(
      ({ events }: { events: IndexedEvent[] }) => useSessionModel(events, TRIAGE_DETAIL),
      { initialProps: { events: all.slice(0, 20) } },
    )
    expect(result.current).toEqual(buildSessionModel(all.slice(0, 20), TRIAGE_DETAIL))

    // A backfill that places a late line in the middle is not an append.
    const late = [...all.slice(0, 10), ...all.slice(12, 20)]
    rerender({ events: late })
    expect(result.current).toEqual(buildSessionModel(late, TRIAGE_DETAIL))

    // Nor is a shorter array: a different run opened in the same window.
    rerender({ events: all.slice(0, 5) })
    expect(result.current).toEqual(buildSessionModel(all.slice(0, 5), TRIAGE_DETAIL))
  })
})
