import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import Heatmap, { HeatmapLegend, bucketOf, cellName, monthLabels } from './index'

describe('monthLabels', () => {
  it('names the column whose Monday starts a month, and never the first column', () => {
    const cells = []
    for (let i = 0; i < 28; i += 1) {
      const d = new Date(Date.UTC(2026, 7, 24 + i)) // Monday 24 August 2026 onwards
      cells.push({ date: d.toISOString().slice(0, 10) })
    }
    // Columns start 24 Aug, 31 Aug, 7 Sep, 14 Sep: September begins on the third.
    expect(monthLabels(cells)).toEqual([{ column: 2, label: 'Sep' }])
  })
})

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

  it('runs its weeks Monday to Sunday and letters the rows', () => {
    // 14 September 2026 is a Monday; the grid closes on Sunday the 20th.
    render(<Heatmap days={days} weeks={1} endDate="2026-09-14" />)
    const cells = screen.getAllByRole('button')
    expect(cells[0]).toHaveAccessibleName('Monday 14 September, 6 runs')
    expect(cells[6]).toHaveAccessibleName('Sunday 20 September, 0 runs')
    expect(screen.getAllByText(/^[MTWFS]$/).map((el) => el.textContent)).toEqual([
      'M',
      'T',
      'W',
      'T',
      'F',
      'S',
      'S',
    ])
  })

  it('names the months along the top of the grid', () => {
    render(<Heatmap days={days} weeks={6} endDate="2026-09-14" />)
    expect(screen.getByText('Sep')).toBeInTheDocument()
  })

  it('names the grid and gives it a scroll container a keyboard can reach', () => {
    render(<Heatmap days={days} weeks={2} endDate="2026-09-14" label="Runs per day" />)
    const group = screen.getByRole('group', { name: 'Runs per day' })
    expect(group).toHaveAttribute('tabindex', '0')
  })

  it('draws at the default size unless told otherwise, and on 10px columns when compact', () => {
    const { container, rerender } = render(<Heatmap days={days} weeks={2} endDate="2026-09-14" />)
    expect(container.querySelector('.sd-heatmap')).toHaveAttribute('data-size', 'default')
    expect(container.querySelector<HTMLElement>('.sd-heatmap__grid')?.style.gridTemplateColumns).toBe(
      'repeat(2, 14px)',
    )

    rerender(<Heatmap days={days} weeks={2} endDate="2026-09-14" size="compact" />)
    expect(container.querySelector('.sd-heatmap')).toHaveAttribute('data-size', 'compact')
    expect(container.querySelector<HTMLElement>('.sd-heatmap__grid')?.style.gridTemplateColumns).toBe(
      'repeat(2, 10px)',
    )
    expect(container.querySelector('.sd-heatmap__legend')).toHaveAttribute('data-size', 'compact')
  })

  it('leaves the legend out when the screen places its own', () => {
    const { container } = render(
      <>
        <Heatmap days={days} weeks={2} endDate="2026-09-14" legend={false} />
        <HeatmapLegend size="compact" />
      </>,
    )
    expect(container.querySelector('.sd-heatmap .sd-heatmap__legend')).toBeNull()
    const legend = container.querySelector('.sd-heatmap__legend')
    expect(legend).toHaveAttribute('data-size', 'compact')
    for (const label of ['0', '1', '2-4', '5-9', '10+']) {
      expect(screen.getByText(label)).toBeInTheDocument()
    }
  })
})
