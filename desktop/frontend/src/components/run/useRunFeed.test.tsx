import { act, renderHook, waitFor } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import type { RunDetail, RunEvent } from '../../api/types'
import { createFakeTransport } from '../../store/fakeTransport'
import { insertByIndex, useRunFeed } from './useRunFeed'

const RUN: RunDetail = {
  runId: 'r1',
  key: 'OMNI-1',
  kind: 'triage',
  status: 'running',
  provider: 'claude',
  model: 'claude-haiku-4-5',
  startedAt: '2026-09-10T10:00:00Z',
  updatedAt: '2026-09-10T10:00:10Z',
  reason: '',
  usage: { turns: 1, inputTokens: 10, outputTokens: 5, costUsd: 0.01 },
  notes: [],
  promptPath: '/w/prompt.md',
  bundleDir: '/w/bundle',
  warnings: [],
  handle: 'ab01',
  budget: { maxTurns: 40, maxMinutes: 20, maxUsd: 5 },
}

function ev(text: string): RunEvent {
  return { t: '2026-09-10T10:00:05Z', kind: 'assistant_text', payload: { text } }
}

/** A promise the test resolves by hand, so it can order what lands when. */
function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (reason: unknown) => void
  const promise = new Promise<T>((res, rej) => {
    resolve = res
    reject = rej
  })
  return { promise, resolve, reject }
}

describe('insertByIndex', () => {
  it('appends in the common case and places a late line where it belongs', () => {
    const a = { index: 1, event: ev('one') }
    const b = { index: 2, event: ev('two') }
    const c = { index: 3, event: ev('three') }
    expect(insertByIndex([], a)).toEqual([a])
    expect(insertByIndex([a, b], c)).toEqual([a, b, c])
    expect(insertByIndex([a, c], b)).toEqual([a, b, c])
    expect(insertByIndex([b, c], a)).toEqual([a, b, c])
  })
})

describe('useRunFeed', () => {
  it('keeps a run.updated that arrives before the run has been read, and merges it in', async () => {
    const transport = createFakeTransport({})
    const read = deferred<RunDetail>()
    transport.run = vi.fn(() => read.promise)
    transport.events = vi.fn(async () => ({ events: [], next: 0 }))

    const { result } = renderHook(() => useRunFeed(transport, 'ws1', 'r1'))
    act(() => {
      transport.emit({
        kind: 'run.updated',
        workspaceId: 'ws1',
        run: { ...RUN, status: 'completed', updatedAt: '2026-09-10T10:01:00Z' },
      })
    })
    expect(result.current.detail).toBeNull()

    await act(async () => {
      read.resolve(RUN)
      await read.promise
    })
    await waitFor(() => expect(result.current.detail?.status).toBe('completed'))
    // The detail-only fields came from the read; the state from the update.
    expect(result.current.detail?.bundleDir).toBe('/w/bundle')
  })

  it('lets the read win over an update that is older than it', async () => {
    const transport = createFakeTransport({})
    const read = deferred<RunDetail>()
    transport.run = vi.fn(() => read.promise)
    transport.events = vi.fn(async () => ({ events: [], next: 0 }))

    const { result } = renderHook(() => useRunFeed(transport, 'ws1', 'r1'))
    act(() => {
      transport.emit({
        kind: 'run.updated',
        workspaceId: 'ws1',
        run: { ...RUN, status: 'blocked', updatedAt: '2026-09-10T09:59:00Z' },
      })
    })
    await act(async () => {
      read.resolve({ ...RUN, status: 'completed' })
      await read.promise
    })
    await waitFor(() => expect(result.current.detail?.status).toBe('completed'))
  })

  it('shows events in index order however they arrive, without repeating one', async () => {
    const transport = createFakeTransport({})
    transport.run = vi.fn(async () => RUN)
    transport.events = vi.fn(async () => ({ events: [ev('one'), ev('two')], next: 2 }))

    const { result } = renderHook(() => useRunFeed(transport, 'ws1', 'r1'))
    await waitFor(() => expect(result.current.events).toHaveLength(2))
    act(() => {
      transport.emit({ kind: 'run.event', workspaceId: 'ws1', runId: 'r1', index: 4, event: ev('four') })
      transport.emit({ kind: 'run.event', workspaceId: 'ws1', runId: 'r1', index: 3, event: ev('three') })
      transport.emit({ kind: 'run.event', workspaceId: 'ws1', runId: 'r1', index: 2, event: ev('two again') })
    })
    expect(result.current.events.map((e) => e.event.payload.text)).toEqual(['one', 'two', 'three', 'four'])
  })

  it('says why the log could not be read', async () => {
    const transport = createFakeTransport({})
    transport.run = vi.fn(async () => RUN)
    transport.events = vi.fn(async () => {
      throw new Error('internal: events.jsonl is not readable')
    })
    const { result } = renderHook(() => useRunFeed(transport, 'ws1', 'r1'))
    await waitFor(() => expect(result.current.loadError).toBe('internal: events.jsonl is not readable'))
  })

  it('counts the run finishing and reads it again, once per finish', async () => {
    const transport = createFakeTransport({})
    let current = RUN
    transport.run = vi.fn(async () => current)
    transport.events = vi.fn(async () => ({ events: [], next: 0 }))

    const { result } = renderHook(() => useRunFeed(transport, 'ws1', 'r1'))
    await waitFor(() => expect(result.current.detail?.status).toBe('running'))
    expect(result.current.finished).toBe(0)

    current = { ...RUN, status: 'completed', notes: ['/notes/OMNI-1.md'] }
    act(() => {
      transport.emit({ kind: 'run.updated', workspaceId: 'ws1', run: { ...RUN, status: 'completed' } })
    })
    await waitFor(() => expect(result.current.finished).toBe(1))
    await waitFor(() => expect(result.current.detail?.notes).toEqual(['/notes/OMNI-1.md']))
    expect(transport.run).toHaveBeenCalledTimes(2)

    // Blocked is not the end of a run; nothing is re-read for it.
    act(() => {
      transport.emit({ kind: 'run.updated', workspaceId: 'ws1', run: { ...RUN, status: 'blocked' } })
    })
    expect(result.current.finished).toBe(1)
  })

  it('unsubscribes and forgets the run when it is pointed elsewhere', async () => {
    const transport = createFakeTransport({})
    transport.run = vi.fn(async ({}, runId) => ({ ...RUN, runId }))
    transport.events = vi.fn(async () => ({ events: [ev('one')], next: 1 }))

    const { result, rerender, unmount } = renderHook(({ runId }) => useRunFeed(transport, 'ws1', runId), {
      initialProps: { runId: 'r1' },
    })
    await waitFor(() => expect(result.current.detail?.runId).toBe('r1'))
    rerender({ runId: 'r2' })
    expect(result.current.detail).toBeNull()
    expect(result.current.events).toEqual([])
    await waitFor(() => expect(result.current.detail?.runId).toBe('r2'))
    expect(transport.subscriberCount()).toBe(1)
    unmount()
    expect(transport.subscriberCount()).toBe(0)
  })
})
