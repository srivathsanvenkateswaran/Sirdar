import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import Button from '../button'
import HeroBand from './index'

describe('HeroBand', () => {
  it('reads as one headline, not two', () => {
    render(<HeroBand headline="Support tickets" headlineTail="answered in your own repo" />)
    expect(
      screen.getByRole('heading', { level: 1, name: 'Support tickets answered in your own repo' }),
    ).toBeInTheDocument()
  })

  it('sets the second clause apart as its own part', () => {
    const { container } = render(<HeroBand headline="Support tickets" headlineTail="answered" />)
    expect(container.querySelector('.sd-hero__clause-tail')).toHaveTextContent('answered')
  })

  it('is the deep band unless the ink one is asked for', () => {
    const { container, rerender } = render(<HeroBand headline="Sirdar" />)
    expect(container.querySelector('.sd-hero')).toHaveAttribute('data-tone', 'deep')
    rerender(<HeroBand headline="Sirdar" tone="ink" />)
    expect(container.querySelector('.sd-hero')).toHaveAttribute('data-tone', 'ink')
  })

  it('carries one deck line and one action', () => {
    render(
      <HeroBand
        headline="Support tickets"
        headlineTail="answered in your own repo"
        deck="A ticket comes in, an agent you already pay for gathers the evidence, you review the note."
        action={<Button variant="primary">Read the quick start</Button>}
      />,
    )
    expect(screen.getByRole('button', { name: 'Read the quick start' })).toBeInTheDocument()
    expect(screen.getByText(/A ticket comes in/)).toBeInTheDocument()
  })

  it('takes ambient decoration beside the headline without announcing it', () => {
    const { container } = render(
      <HeroBand headline="Sirdar" aside={<span data-testid="ring">ring</span>} />,
    )
    expect(container.querySelector('.sd-hero__aside')).toContainElement(screen.getByTestId('ring'))
  })

  it('keeps the action reachable from the keyboard', () => {
    render(<HeroBand headline="Sirdar" action={<Button variant="primary">Start</Button>} />)
    const button = screen.getByRole('button', { name: 'Start' })
    button.focus()
    expect(button).toHaveFocus()
  })

  it('renders an Arabic headline whose second clause is marked by weight', () => {
    render(
      <div dir="rtl">
        <HeroBand
          headline="تذاكر الدعم"
          headlineTail="تُحلّ داخل مستودعك"
          deck="تصل التذكرة، فيجمع الوكيل الأدلة، وتراجع أنت الملاحظة."
        />
      </div>,
    )
    // The weight swap is a stylesheet rule keyed on [dir="rtl"]; what the
    // component owes the test is that the clause is still its own element.
    const tail = screen.getByText('تُحلّ داخل مستودعك')
    expect(tail).toHaveClass('sd-hero__clause-tail')
    expect(tail.closest('[dir="rtl"]')).not.toBeNull()
  })
})
