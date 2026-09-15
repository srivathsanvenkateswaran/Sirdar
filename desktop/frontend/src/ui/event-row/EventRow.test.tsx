import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import EventRow, { EVENT_GLYPHS, type EventVariant } from './index'

const VARIANTS = Object.keys(EVENT_GLYPHS) as EventVariant[]

describe('EventRow', () => {
  it.each(VARIANTS)('marks a %s row with its own glyph', (variant) => {
    const { container } = render(
      <EventRow at="0m 04s" variant={variant}>
        something happened
      </EventRow>,
    )
    const row = container.querySelector('.sd-event')
    expect(row).toHaveAttribute('data-variant', variant)
    expect(container.querySelector('.sd-event__mark')).toHaveTextContent(EVENT_GLYPHS[variant])
  })

  it('hides the glyph from a screen reader, which reads the content instead', () => {
    const { container } = render(
      <EventRow at="0m 04s" variant="deny">
        Denied: Bash(rm -rf /)
      </EventRow>,
    )
    expect(container.querySelector('.sd-event__mark')).toHaveAttribute('aria-hidden', 'true')
    expect(screen.getByText('Denied: Bash(rm -rf /)')).toBeInTheDocument()
  })

  it('keeps the offset in the gutter', () => {
    const { container } = render(
      <EventRow at="12m 41s" variant="usage">
        41,204 tokens
      </EventRow>,
    )
    expect(container.querySelector('.sd-event__at')).toHaveTextContent('12m 41s')
  })

  it('stays left to right inside a pane laid out right to left', () => {
    const { container } = render(
      <div dir="rtl">
        <EventRow at="0m 09s" variant="tool">
          Read(/srv/api/statements/export.go)
        </EventRow>
      </div>,
    )
    // Paths and tool names are unreadable mirrored, so the row opts out of the
    // pane's direction rather than inheriting it.
    expect(container.querySelector('.sd-event')).toHaveAttribute('dir', 'ltr')
  })

  it('takes a glyph of its own when one is asked for', () => {
    const { container } = render(
      <EventRow at="0m 00s" variant="state" glyph="→">
        preparing
      </EventRow>,
    )
    expect(container.querySelector('.sd-event__mark')).toHaveTextContent('→')
  })
})
