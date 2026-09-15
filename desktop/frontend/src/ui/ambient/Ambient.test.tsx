import { render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { Marquee, MarqueeItem, RingText } from './index'

/** Answers the reduced-motion query the way a case is about. */
function prefersReduced(matches: boolean): () => void {
  const original = window.matchMedia
  Object.defineProperty(window, 'matchMedia', {
    configurable: true,
    writable: true,
    value: vi.fn(() => ({
      matches,
      media: '(prefers-reduced-motion: reduce)',
      addEventListener: () => {},
      removeEventListener: () => {},
    })),
  })
  return () => {
    Object.defineProperty(window, 'matchMedia', {
      configurable: true,
      writable: true,
      value: original,
    })
  }
}

let restore: (() => void) | null = null
afterEach(() => {
  restore?.()
  restore = null
})

describe('RingText', () => {
  it('sets one element per character', () => {
    restore = prefersReduced(false)
    const { container } = render(<RingText text="triage" />)
    expect(container.querySelectorAll('.sd-ring__glyph')).toHaveLength(6)
  })

  it('is hidden from a screen reader, which would read it letter by letter', () => {
    restore = prefersReduced(false)
    const { container } = render(<RingText text="evidence first" />)
    expect(container.querySelector('.sd-ring')).toHaveAttribute('aria-hidden', 'true')
  })

  it('turns when nothing has been asked of it', () => {
    restore = prefersReduced(false)
    const { container } = render(<RingText text="triage" />)
    const ring = container.querySelector('.sd-ring')
    expect(ring).toHaveClass('sd-motion-ring')
    expect(ring).not.toHaveAttribute('data-still')
  })

  it('freezes at its starting angle when the reader has asked for less motion', () => {
    restore = prefersReduced(true)
    const { container } = render(<RingText text="triage" />)
    const ring = container.querySelector('.sd-ring')
    expect(ring).not.toHaveClass('sd-motion-ring')
    expect(ring).toHaveAttribute('data-still', 'true')
  })

  it('keeps a space as a space rather than collapsing the ring', () => {
    restore = prefersReduced(true)
    const { container } = render(<RingText text="a b" />)
    expect(container.querySelectorAll('.sd-ring__glyph')).toHaveLength(3)
  })
})

describe('Marquee', () => {
  it('says what the row lists', () => {
    restore = prefersReduced(false)
    render(
      <Marquee label="Providers and sources Sirdar reads">
        <MarqueeItem>Claude Code</MarqueeItem>
      </Marquee>,
    )
    expect(screen.getByRole('group', { name: 'Providers and sources Sirdar reads' })).toBeInTheDocument()
  })

  it('doubles the track for a seamless loop and hides the copy', () => {
    restore = prefersReduced(false)
    const { container } = render(
      <Marquee label="Sources">
        <MarqueeItem>Zoho Desk</MarqueeItem>
      </Marquee>,
    )
    const runs = container.querySelectorAll('.sd-marquee__run')
    expect(runs).toHaveLength(2)
    expect(runs[1]).toHaveAttribute('aria-hidden', 'true')
    // The duplicate is what makes the loop seamless; announcing it would read
    // every source twice.
    expect(screen.getAllByText('Zoho Desk')).toHaveLength(2)
  })

  it('becomes one wrapped row, rendered once, under reduced motion', () => {
    restore = prefersReduced(true)
    const { container } = render(
      <Marquee label="Sources">
        <MarqueeItem>Zoho Desk</MarqueeItem>
      </Marquee>,
    )
    expect(container.querySelectorAll('.sd-marquee__run')).toHaveLength(1)
    expect(container.querySelector('.sd-marquee__track')).not.toHaveClass('sd-motion-marquee')
    expect(screen.getAllByText('Zoho Desk')).toHaveLength(1)
  })

  it('carries Arabic items, which the stylesheet reverses with the page', () => {
    restore = prefersReduced(true)
    render(
      <div dir="rtl">
        <Marquee label="المصادر">
          <MarqueeItem>مكتب زوهو</MarqueeItem>
        </Marquee>
      </div>,
    )
    expect(screen.getByText('مكتب زوهو').closest('[dir="rtl"]')).not.toBeNull()
  })
})
