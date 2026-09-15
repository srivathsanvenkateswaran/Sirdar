import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import StatusBadge, { PriorityBadge, STATE_WORDS, stateWord, type SdStatus } from './index'
import { STATE_WORDS as GLYPH_WORDS } from '../state-glyph'

const EVERY: SdStatus[] = [
  'queued',
  'preparing',
  'running',
  'blocked',
  'completed',
  'done',
  'failed',
  'over_budget',
]

describe('StatusBadge', () => {
  it.each(EVERY)('shows %s as a word, never as colour alone', (status) => {
    const { container } = render(<StatusBadge status={status} />)
    const badge = container.querySelector('.sd-badge')
    expect(badge).toHaveAttribute('data-status', status)
    expect(badge).toHaveTextContent(STATE_WORDS[status])
  })

  it('spells every word lowercase, the way the mocks do on every screen', () => {
    for (const word of Object.values(STATE_WORDS)) expect(word).toBe(word.toLowerCase())
    expect(STATE_WORDS.over_budget).toBe('over budget')
  })

  it('shares its words with the state glyph, so a pill and a card footer never disagree', () => {
    expect(GLYPH_WORDS).toBe(STATE_WORDS)
  })

  it('keeps the word when a screen adds what the state means there', () => {
    render(<StatusBadge status="blocked" detail="waiting on you" />)
    expect(screen.getByText('blocked · waiting on you')).toBeInTheDocument()
  })

  it('takes a translation of the word', () => {
    render(<StatusBadge status="completed">تم</StatusBadge>)
    expect(screen.getByText('تم')).toBeInTheDocument()
  })

  it('gives a state off the wire its word, and an unknown one back as it came', () => {
    expect(stateWord('over_budget')).toBe('over budget')
    expect(stateWord('cancelled')).toBe('cancelled')
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
