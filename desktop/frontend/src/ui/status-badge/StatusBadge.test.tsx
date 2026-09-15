import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import StatusBadge, { PriorityBadge, STATUS_WORDS, type SdStatus } from './index'

const EVERY: SdStatus[] = [
  'queued',
  'preparing',
  'running',
  'blocked',
  'completed',
  'failed',
  'over_budget',
]

describe('StatusBadge', () => {
  it.each(EVERY)('shows %s as a word, never as colour alone', (status) => {
    const { container } = render(<StatusBadge status={status} />)
    const badge = container.querySelector('.sd-badge')
    expect(badge).toHaveAttribute('data-status', status)
    expect(badge).toHaveTextContent(STATUS_WORDS[status])
  })

  it('says what blocked means rather than saying blocked', () => {
    render(<StatusBadge status="blocked" />)
    expect(screen.getByText('Needs input')).toBeInTheDocument()
  })

  it('takes a word the CLI reports under another name', () => {
    render(<StatusBadge status="completed">Triaged</StatusBadge>)
    expect(screen.getByText('Triaged')).toBeInTheDocument()
  })

  it('renders an Arabic word without losing the hue', () => {
    const { container } = render(
      <div dir="rtl">
        <StatusBadge status="failed">فشل</StatusBadge>
      </div>,
    )
    expect(container.querySelector('.sd-badge')).toHaveAttribute('data-status', 'failed')
    expect(screen.getByText('فشل')).toBeInTheDocument()
  })
})

describe('PriorityBadge', () => {
  it('shows the tracker string verbatim', () => {
    render(<PriorityBadge priority="Blocker" />)
    const badge = screen.getByText('Blocker')
    expect(badge).toHaveAttribute('data-priority', 'blocker')
  })

  it('renders nothing when the ticket states no priority', () => {
    const { container } = render(<PriorityBadge priority="   " />)
    expect(container.firstChild).toBeNull()
  })
})
