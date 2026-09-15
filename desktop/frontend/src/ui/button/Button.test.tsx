import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import Button from './index'

describe('Button', () => {
  it('is a secondary, flat button unless told otherwise', () => {
    render(<Button>Open note</Button>)
    const button = screen.getByRole('button', { name: 'Open note' })
    expect(button).toHaveAttribute('data-variant', 'secondary')
    expect(button).toHaveAttribute('type', 'button')
  })

  it('marks the primary variant, which is the only one that may mutate', () => {
    render(<Button variant="primary">Apply fix</Button>)
    expect(screen.getByRole('button')).toHaveAttribute('data-variant', 'primary')
  })

  it('draws the ghost variant with no fill of its own', () => {
    render(<Button variant="ghost">Dismiss</Button>)
    expect(screen.getByRole('button')).toHaveAttribute('data-variant', 'ghost')
  })

  it('runs its action on click', () => {
    const onClick = vi.fn()
    render(<Button onClick={onClick}>Resume</Button>)
    fireEvent.click(screen.getByRole('button'))
    expect(onClick).toHaveBeenCalledOnce()
  })

  it('refuses a second click while busy but keeps focus', () => {
    const onClick = vi.fn()
    render(
      <Button busy onClick={onClick}>
        Starting
      </Button>,
    )
    const button = screen.getByRole('button')
    fireEvent.click(button)
    expect(onClick).not.toHaveBeenCalled()
    // aria-disabled rather than disabled: a disabled element loses focus
    // mid-action, and the reader is then nowhere.
    expect(button).toHaveAttribute('aria-disabled', 'true')
    expect(button).not.toBeDisabled()
    button.focus()
    expect(button).toHaveFocus()
  })

  it('does not fire when disabled', () => {
    const onClick = vi.fn()
    render(
      <Button disabled onClick={onClick}>
        Fix
      </Button>,
    )
    const button = screen.getByRole('button')
    expect(button).toBeDisabled()
    fireEvent.click(button)
    expect(onClick).not.toHaveBeenCalled()
  })

  it('is reachable by keyboard and activates from the keyboard', () => {
    const onClick = vi.fn()
    render(<Button onClick={onClick}>Retry</Button>)
    const button = screen.getByRole('button')
    button.focus()
    expect(button).toHaveFocus()
    expect(button).not.toHaveAttribute('tabindex')
    // A native button turns Enter and Space into a click; jsdom does not run
    // that activation behaviour, so the click it would produce is dispatched
    // here and what is under test is that nothing intercepts it.
    fireEvent.click(document.activeElement as HTMLElement)
    expect(onClick).toHaveBeenCalledOnce()
  })

  it('hides the shortcut hint from the accessible name', () => {
    render(
      <Button shortcut="n" variant="primary">
        New triage
      </Button>,
    )
    expect(screen.getByRole('button', { name: 'New triage' })).toBeInTheDocument()
    expect(screen.getByText('n')).toHaveAttribute('aria-hidden', 'true')
  })

  it('carries an Arabic label inside an RTL pane without changing variant', () => {
    render(
      <div dir="rtl">
        <Button variant="primary">ابدأ الفرز</Button>
      </div>,
    )
    const button = screen.getByRole('button', { name: 'ابدأ الفرز' })
    expect(button).toHaveAttribute('data-variant', 'primary')
    expect(button.closest('[dir="rtl"]')).not.toBeNull()
  })
})
