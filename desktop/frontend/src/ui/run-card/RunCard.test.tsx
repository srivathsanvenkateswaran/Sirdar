import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import RunCard from './index'

const BASE = {
  runKey: 'OMNI-2510',
  kind: 'triage',
  provider: 'claude',
  onOpen: () => {},
} as const

describe('RunCard', () => {
  it('names the run and its ticket in one accessible label', () => {
    render(<RunCard {...BASE} status="completed" title="Statement export times out" />)
    expect(
      screen.getByRole('button', { name: 'OMNI-2510: Statement export times out' }),
    ).toBeInTheDocument()
  })

  it('shows the title first and the key at the foot, with no key in the head', () => {
    const { container } = render(
      <RunCard {...BASE} status="completed" title="Statement export times out" />,
    )
    const card = container.querySelector('.sd-run-card') as HTMLElement
    expect(card.firstElementChild).toHaveClass('sd-run-card__title')
    expect(card.querySelector('.sd-run-card__foot .sd-run-card__key')).toHaveTextContent(
      'OMNI-2510',
    )
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

  it('carries the kind as a chip and the state as a glyph with its word', () => {
    render(<RunCard {...BASE} kind="fix" status="blocked" />)
    expect(screen.getByText('fix')).toHaveClass('sd-kind')
    expect(screen.getByText('blocked')).toBeInTheDocument()
  })

  it('shows the clock only while the run is live or waiting', () => {
    const { rerender } = render(<RunCard {...BASE} status="running" clock="1:47" />)
    expect(screen.getByText('1:47')).toBeInTheDocument()
    rerender(<RunCard {...BASE} status="blocked" clock="4:12" />)
    expect(screen.getByText('4:12')).toBeInTheDocument()
    rerender(<RunCard {...BASE} status="completed" clock="9:02" />)
    expect(screen.queryByText('9:02')).toBeNull()
  })

  it('draws the provider mark as the avatar, named by the vendor', () => {
    render(<RunCard {...BASE} status="done" provider="agy" />)
    expect(screen.getByRole('img', { name: 'Antigravity' })).toBeInTheDocument()
  })

  it('carries neither a reason nor a cost', () => {
    const { container } = render(<RunCard {...BASE} status="failed" />)
    expect(container.querySelector('.sd-run-card__reason')).toBeNull()
    expect(container.querySelector('.sd-run-card__cost')).toBeNull()
  })

  it('keeps the key and the clock left to right inside an Arabic card', () => {
    render(
      <div dir="rtl">
        <RunCard
          {...BASE}
          status="blocked"
          title="العميل لا يستطيع تصدير كشف الحساب"
          clock="4:12"
        />
      </div>,
    )
    expect(screen.getByText('OMNI-2510')).toHaveAttribute('dir', 'ltr')
    expect(screen.getByText('4:12')).toHaveAttribute('dir', 'ltr')
    expect(screen.getByText('العميل لا يستطيع تصدير كشف الحساب')).toHaveAttribute('dir', 'auto')
  })
})
