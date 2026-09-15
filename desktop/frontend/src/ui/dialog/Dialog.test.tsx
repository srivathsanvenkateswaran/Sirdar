import { fireEvent, render, screen } from '@testing-library/react'
import { useState } from 'react'
import { describe, expect, it, vi } from 'vitest'
import Dialog from './index'
import Button from '../button'

function Harness() {
  const [open, setOpen] = useState(false)
  return (
    <>
      <button type="button" onClick={() => setOpen(true)}>
        New triage
      </button>
      <Dialog
        open={open}
        title="Start a triage"
        onClose={() => setOpen(false)}
        actions={
          <>
            <Button onClick={() => setOpen(false)}>Cancel</Button>
            <Button variant="primary" onClick={() => setOpen(false)}>
              Start triage
            </Button>
          </>
        }
      >
        <label>
          Ticket keys
          <input aria-label="Ticket keys" />
        </label>
      </Dialog>
    </>
  )
}

describe('Dialog', () => {
  it('renders nothing while it is closed', () => {
    render(<Dialog open={false} title="Start a triage" onClose={() => {}} children={null} />)
    expect(screen.queryByRole('dialog')).toBeNull()
  })

  it('is a modal that names itself by its heading', () => {
    render(<Dialog open title="Start a triage" onClose={() => {}} children={<p>Keys</p>} />)
    const dialog = screen.getByRole('dialog', { name: 'Start a triage' })
    expect(dialog).toHaveAttribute('aria-modal', 'true')
  })

  it('moves focus to the first control when it opens', () => {
    render(<Harness />)
    fireEvent.click(screen.getByRole('button', { name: 'New triage' }))
    expect(screen.getByLabelText('Ticket keys')).toHaveFocus()
  })

  it('returns focus to the control that opened it', () => {
    render(<Harness />)
    const opener = screen.getByRole('button', { name: 'New triage' })
    // jsdom does not focus a button on a dispatched click the way a browser
    // does, and the dialog remembers whatever had focus when it opened.
    opener.focus()
    fireEvent.click(opener)
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }))
    expect(opener).toHaveFocus()
  })

  it('closes the rest of the window to clicks and focus while it is open, and reopens it after', () => {
    const outside = document.createElement('nav')
    outside.innerHTML = '<button type="button">Board</button>'
    document.body.append(outside)
    render(<Harness />)
    expect(outside).not.toHaveAttribute('inert')

    fireEvent.click(screen.getByRole('button', { name: 'New triage' }))
    expect(outside).toHaveAttribute('inert')
    // The opener is a sibling of the dialog inside the render container, so it is closed off too.
    expect(screen.getByRole('button', { name: 'New triage' })).toHaveAttribute('inert')
    expect(screen.getByRole('dialog')).not.toHaveAttribute('inert')

    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }))
    expect(outside).not.toHaveAttribute('inert')
    expect(screen.getByRole('button', { name: 'New triage' })).not.toHaveAttribute('inert')
    outside.remove()
  })

  it('closes on Escape', () => {
    const onClose = vi.fn()
    render(<Dialog open title="Start a triage" onClose={onClose} children={<p>Keys</p>} />)
    fireEvent.keyDown(window, { key: 'Escape' })
    expect(onClose).toHaveBeenCalledOnce()
  })

  it('closes on a click outside, but not on a click inside', () => {
    const onClose = vi.fn()
    const { container } = render(
      <Dialog open title="Start a triage" onClose={onClose} children={<p>Keys</p>} />,
    )
    fireEvent.mouseDown(screen.getByRole('dialog'))
    expect(onClose).not.toHaveBeenCalled()
    fireEvent.mouseDown(container.querySelector('.sd-scrim') as HTMLElement)
    expect(onClose).toHaveBeenCalledOnce()
  })

  it('keeps Tab inside the dialog, both ways round', () => {
    render(<Harness />)
    fireEvent.click(screen.getByRole('button', { name: 'New triage' }))
    const input = screen.getByLabelText('Ticket keys')
    const commit = screen.getByRole('button', { name: 'Start triage' })
    commit.focus()
    fireEvent.keyDown(window, { key: 'Tab' })
    expect(input).toHaveFocus()
    fireEvent.keyDown(window, { key: 'Tab', shiftKey: true })
    expect(commit).toHaveFocus()
  })

  it('carries an Arabic question and an Arabic commit label', () => {
    render(
      <div dir="rtl">
        <Dialog
          open
          title="بدء الفرز"
          onClose={() => {}}
          actions={<Button variant="primary">ابدأ</Button>}
        >
          <p>أدخل مفاتيح التذاكر.</p>
        </Dialog>
      </div>,
    )
    expect(screen.getByRole('dialog', { name: 'بدء الفرز' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'ابدأ' })).toBeInTheDocument()
  })
})
