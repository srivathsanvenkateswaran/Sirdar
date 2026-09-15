import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import KanbanColumn, { type LaneId } from './index'

const LANES: LaneId[] = ['queue', 'gathering', 'blocked', 'triaged', 'done', 'failed']

describe('KanbanColumn', () => {
  it.each(LANES)('carries the %s hue on the rail', (lane) => {
    const { container } = render(
      <KanbanColumn lane={lane} title={lane} count={0} empty="Nothing here." />,
    )
    expect(container.querySelector('.sd-lane')).toHaveAttribute('data-lane', lane)
  })

  it('names itself with its count, so the board can be heard as well as seen', () => {
    render(<KanbanColumn lane="blocked" title="Blocked" count={3} empty="Nothing waiting." />)
    expect(screen.getByRole('region', { name: 'Blocked (3)' })).toBeInTheDocument()
  })

  it('says in prose what an empty column means', () => {
    render(
      <KanbanColumn
        lane="queue"
        title="Queue"
        count={0}
        empty="No ticket is waiting. New ones arrive from the tracker."
      />,
    )
    expect(
      screen.getByText('No ticket is waiting. New ones arrive from the tracker.'),
    ).toBeInTheDocument()
  })

  it('drops the empty prose once it holds a card', () => {
    render(
      <KanbanColumn lane="done" title="Done" count={1} empty="Nothing resolved yet.">
        <article>OMNI-2510</article>
      </KanbanColumn>,
    )
    expect(screen.queryByText('Nothing resolved yet.')).toBeNull()
    expect(screen.getByText('OMNI-2510')).toBeInTheDocument()
  })

  it('shows a zero count rather than hiding the column', () => {
    render(<KanbanColumn lane="failed" title="Failed" count={0} empty="No failures." />)
    expect(screen.getByText('0')).toBeInTheDocument()
  })

  it('says after the count what narrows the lane, inside the heading', () => {
    const { container } = render(
      <KanbanColumn lane="queue" title="Queue" count={6} note="assigned to you" empty="Nothing." />,
    )
    const head = container.querySelector('.sd-lane__head') as HTMLElement
    expect([...head.children].map((el) => el.textContent)).toEqual([
      'Queue',
      '6',
      '· assigned to you',
    ])
    expect(head.querySelector('.sd-lane__note')).not.toBeNull()
  })

  it('draws no note for a lane that is narrowed by nothing', () => {
    const { container } = render(
      <KanbanColumn lane="done" title="Done" count={2} empty="Nothing resolved yet." />,
    )
    expect(container.querySelector('.sd-lane__note')).toBeNull()
  })

  it('reads right to left with an Arabic heading', () => {
    const { container } = render(
      <div dir="rtl">
        <KanbanColumn lane="triaged" title="تم الفرز" count={2} empty="لا يوجد شيء بعد." />
      </div>,
    )
    expect(screen.getByText('تم الفرز')).toBeInTheDocument()
    expect(container.querySelector('.sd-lane')?.closest('[dir="rtl"]')).not.toBeNull()
  })
})
