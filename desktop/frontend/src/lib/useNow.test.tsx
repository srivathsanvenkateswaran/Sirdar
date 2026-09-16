import { act, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import Age from '../components/Age'
import { probeRender, renderCount, resetRenderCounts } from './renderProbe'
import { nowListeners, useNow } from './useNow'

afterEach(() => {
  vi.useRealTimers()
  resetRenderCounts()
})

function Clock({ period = 1000, active = true }: { period?: number; active?: boolean }) {
  probeRender('Clock')
  const now = useNow(period, active)
  return <span data-testid="clock">{now}</span>
}

/** A screen around the clock: it must not render on the tick. */
function Screen({ active = true }: { active?: boolean }) {
  probeRender('Screen')
  return (
    <div>
      <Clock active={active} />
      <Clock active={active} />
    </div>
  )
}

describe('useNow', () => {
  it('ticks the leaf that reads it and nothing above it', () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-09-16T10:00:00Z'))
    render(<Screen />)
    const start = Date.parse('2026-09-16T10:00:00Z')
    expect(screen.getAllByTestId('clock').map((el) => Number(el.textContent))).toEqual([start, start])
    resetRenderCounts()

    act(() => {
      vi.advanceTimersByTime(1000)
    })
    expect(screen.getAllByTestId('clock').map((el) => Number(el.textContent))).toEqual([
      start + 1000,
      start + 1000,
    ])
    expect(renderCount('Clock')).toBe(2)
    expect(renderCount('Screen')).toBe(0)
  })

  it('runs one interval for every reader of a period and stops it with the last one', () => {
    vi.useFakeTimers()
    const view = render(<Screen />)
    expect(nowListeners(1000)).toBe(2)
    expect(vi.getTimerCount()).toBe(1)
    view.unmount()
    expect(nowListeners(1000)).toBe(0)
    expect(vi.getTimerCount()).toBe(0)
  })

  it('holds still when told it is not active', () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-09-16T10:00:00Z'))
    render(<Clock active={false} />)
    const start = Date.parse('2026-09-16T10:00:00Z')
    expect(nowListeners(1000)).toBe(0)
    act(() => {
      vi.advanceTimersByTime(5000)
    })
    expect(Number(screen.getByTestId('clock').textContent)).toBe(start)
  })
})

describe('Age', () => {
  it('prints the formatter for the time now and re-prints it on the tick', () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-09-16T10:00:00Z'))
    const start = Date.parse('2026-09-16T10:00:00Z')
    render(<Age at="2026-09-16T09:59:50Z" format={(now) => `${Math.round((now - start) / 1000 + 10)}s`} />)
    const el = screen.getByText('10s')
    expect(el.tagName).toBe('TIME')
    expect(el).toHaveAttribute('datetime', '2026-09-16T09:59:50Z')
    act(() => {
      vi.advanceTimersByTime(3000)
    })
    expect(screen.getByText('13s')).toBeInTheDocument()
  })

  it('with a clock of its own it is still, for tests and the gallery', () => {
    vi.useFakeTimers()
    render(<Age now={5000} format={(now) => `${now}`} />)
    expect(screen.getByText('5000').tagName).toBe('SPAN')
    expect(nowListeners(1000)).toBe(0)
  })
})
