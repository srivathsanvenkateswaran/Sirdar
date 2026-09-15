import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import Heatmap, { bucketOf, cellName } from './index'

describe('bucketOf', () => {
  it('puts each count in the step the design language names', () => {
    expect(bucketOf(0)).toBe(0)
    expect(bucketOf(1)).toBe(1)
    expect([2, 3, 4].map(bucketOf)).toEqual([2, 2, 2])
    expect([5, 7, 9].map(bucketOf)).toEqual([3, 3, 3])
    expect([10, 40].map(bucketOf)).toEqual([4, 4])
  })

  it('treats a nonsense count as no runs rather than as a colour', () => {
    expect(bucketOf(Number.NaN)).toBe(0)
    expect(bucketOf(-3)).toBe(0)
  })
})

describe('cellName', () => {
  it('reads as a sentence, and counts one run in the singular', () => {
    expect(cellName('2026-09-14', 6)).toBe('14 September, 6 runs')
    expect(cellName('2026-09-14', 1)).toBe('14 September, 1 run')
    expect(cellName('2026-09-14', 0)).toBe('14 September, 0 runs')
  })
})

describe('Heatmap', () => {
  const days = [
    { date: '2026-09-14', count: 6 },
    { date: '2026-09-10', count: 1 },
  ]

  it('draws a whole grid of weeks, each cell a named button', () => {
    render(<Heatmap days={days} weeks={2} endDate="2026-09-14" />)
    expect(screen.getByRole('button', { name: /14 September, 6 runs/ })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /10 September, 1 run$/ })).toBeInTheDocument()
    // Two weeks is fourteen cells, plus none in the legend, which is aria-hidden.
    expect(screen.getAllByRole('button')).toHaveLength(14)
  })

  it('paints a day with no runs at the empty step rather than leaving a hole', () => {
    render(<Heatmap days={days} weeks={2} endDate="2026-09-14" />)
    const quiet = screen.getByRole('button', { name: /11 September, 0 runs/ })
    expect(quiet).toHaveAttribute('data-heat', '0')
  })

  it('prints the bucket boundaries as numbers, not More and Less', () => {
    render(<Heatmap days={days} weeks={2} endDate="2026-09-14" />)
    for (const label of ['0', '1', '2-4', '5-9', '10+']) {
      expect(screen.getByText(label)).toBeInTheDocument()
    }
    expect(screen.queryByText('More')).toBeNull()
    expect(screen.queryByText('Less')).toBeNull()
  })

  it('hands back the day that was chosen', () => {
    const onSelect = vi.fn()
    render(<Heatmap days={days} weeks={2} endDate="2026-09-14" onSelect={onSelect} />)
    fireEvent.click(screen.getByRole('button', { name: /14 September, 6 runs/ }))
    expect(onSelect).toHaveBeenCalledWith('2026-09-14')
  })

  it('names the grid and gives it a scroll container a keyboard can reach', () => {
    render(<Heatmap days={days} weeks={2} endDate="2026-09-14" label="Runs per day" />)
    const group = screen.getByRole('group', { name: 'Runs per day' })
    expect(group).toHaveAttribute('tabindex', '0')
  })
})
