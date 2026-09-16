import { afterEach, describe, expect, it, vi } from 'vitest'
import { coalesce, FLUSH_MS } from './coalesce'

afterEach(() => {
  vi.useRealTimers()
})

describe('coalesce', () => {
  it('hands a burst over together, in order, after one window', () => {
    vi.useFakeTimers()
    const seen: number[] = []
    const out = coalesce<number>((n) => seen.push(n))
    out.push(1)
    out.push(2)
    out.push(3)
    expect(seen).toEqual([])
    vi.advanceTimersByTime(FLUSH_MS - 1)
    expect(seen).toEqual([])
    vi.advanceTimersByTime(1)
    expect(seen).toEqual([1, 2, 3])
    expect(vi.getTimerCount()).toBe(0)
  })

  it('a lone event still arrives within the window, and the next burst starts a new one', () => {
    vi.useFakeTimers()
    const seen: number[] = []
    const out = coalesce<number>((n) => seen.push(n), 10)
    out.push(1)
    vi.advanceTimersByTime(10)
    expect(seen).toEqual([1])
    out.push(2)
    out.push(3)
    vi.advanceTimersByTime(10)
    expect(seen).toEqual([1, 2, 3])
  })

  it('stopping delivers what is queued and drops what comes after', () => {
    vi.useFakeTimers()
    const seen: number[] = []
    const out = coalesce<number>((n) => seen.push(n))
    out.push(1)
    out.stop()
    expect(seen).toEqual([1])
    out.push(2)
    vi.advanceTimersByTime(FLUSH_MS * 2)
    expect(seen).toEqual([1])
    expect(vi.getTimerCount()).toBe(0)
  })
})
