import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import RunCard from './index'

const BASE = {
  runKey: 'OMNI-2510',
  kind: 'triage',
  onOpen: () => {},
} as const

describe('RunCard', () => {
  it('names the run and its ticket in one accessible label', () => {
    render(<RunCard {...BASE} status="completed" title="Statement export times out" />)
    expect(
      screen.getByRole('button', { name: 'OMNI-2510: Statement export times out' }),
    ).toBeInTheDocument()
  })

  it('falls back to the key when the tracker has no title', () => {
    render(<RunCard {...BASE} status="queued" />)
    expect(screen.getByRole('button', { name: 'OMNI-2510: OMNI-2510' })).toBeInTheDocument()
  })

  it('opens the run on click and from the keyboard', () => {
    const onOpen = vi.fn()
    render(<RunCard {...BASE} status="running" onOpen={onOpen} />)
    const card = screen.getByRole('button')
    card.focus()
    expect(card).toHaveFocus()
    fireEvent.click(card)
    expect(onOpen).toHaveBeenCalledOnce()
  })

  it.each(['preparing', 'running'] as const)('marks a %s run as the live one', (status) => {
    const { container } = render(<RunCard {...BASE} status={status} />)
    expect(container.querySelector('.sd-run-card')).toHaveAttribute('data-live', 'true')
  })

  it('leaves a finished run flat', () => {
    const { container } = render(<RunCard {...BASE} status="completed" />)
    expect(container.querySelector('.sd-run-card')).not.toHaveAttribute('data-live')
  })

  it('shows a reason only for a run that stopped on something', () => {
    const { rerender } = render(
      <RunCard {...BASE} status="blocked" reason="The agent asked which account to use." />,
    )
    expect(screen.getByText('The agent asked which account to use.')).toBeInTheDocument()
    rerender(<RunCard {...BASE} status="running" reason="The agent asked which account to use." />)
    expect(screen.queryByText('The agent asked which account to use.')).toBeNull()
  })

  it('keeps the key and the clock left to right inside an Arabic card', () => {
    render(
      <div dir="rtl">
        <RunCard
          {...BASE}
          status="blocked"
          title="العميل لا يستطيع تصدير كشف الحساب"
          reason="طلب الوكيل تحديد رقم الحساب"
          elapsed="4m 12s"
          cost="$0.42"
        />
      </div>,
    )
    expect(screen.getByText('OMNI-2510')).toHaveAttribute('dir', 'ltr')
    expect(screen.getByText('4m 12s')).toHaveAttribute('dir', 'ltr')
    expect(screen.getByText('العميل لا يستطيع تصدير كشف الحساب')).toHaveAttribute('dir', 'auto')
    expect(screen.getByText('طلب الوكيل تحديد رقم الحساب')).toHaveAttribute('dir', 'auto')
  })

  it('shows the tracker priority beside the key', () => {
    render(<RunCard {...BASE} status="failed" priority="P1" />)
    expect(screen.getByText('P1')).toHaveAttribute('data-priority', 'p1')
    expect(screen.getByText('Failed')).toBeInTheDocument()
  })
})
