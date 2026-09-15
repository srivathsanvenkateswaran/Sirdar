import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import StateGlyph, { STATE_WORDS, type GlyphState } from './index'

describe('StateGlyph', () => {
  it.each(Object.keys(STATE_WORDS) as GlyphState[])('always carries the word for %s', (state) => {
    const { container } = render(<StateGlyph state={state} />)
    expect(screen.getByText(STATE_WORDS[state])).toBeInTheDocument()
    expect(container.querySelector('.sd-state')).toHaveAttribute('data-state', state)
    expect(container.querySelector('svg')).toHaveAttribute('aria-hidden', 'true')
  })

  it.each([
    ['queued', 'queued'],
    ['preparing', 'running'],
    ['running', 'running'],
    ['blocked', 'blocked'],
    ['completed', 'check'],
    ['done', 'check'],
    ['failed', 'failed'],
    ['over_budget', 'failed'],
  ] as const)('draws %s with the %s glyph', (state, glyph) => {
    const { container } = render(<StateGlyph state={state} />)
    expect(container.querySelector('.sd-state')).toHaveAttribute('data-glyph', glyph)
  })

  it('shows the clock only when the caller hands one over, left to right', () => {
    const { container, rerender } = render(<StateGlyph state="running" clock="01:47" />)
    expect(screen.getByText('01:47')).toHaveAttribute('dir', 'ltr')
    rerender(<StateGlyph state="running" />)
    expect(container.querySelector('.sd-state__clock')).toBeNull()
  })

  it('takes a translation of the word', () => {
    render(<StateGlyph state="blocked" word="بانتظار ردّك" />)
    expect(screen.getByText('بانتظار ردّك')).toBeInTheDocument()
  })
})
