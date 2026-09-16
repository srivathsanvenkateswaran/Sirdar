import { fireEvent, render, screen, within } from '@testing-library/react'
import { useState } from 'react'
import { describe, expect, it, vi } from 'vitest'
import ComposerCard, { type ComposerSend } from './ComposerCard'

function Harness({
  send,
  error,
  aside,
}: {
  send: Partial<ComposerSend>
  error?: string
  aside?: string
}): JSX.Element {
  const [text, setText] = useState('')
  return (
    <ComposerCard
      name="Start"
      label="Ticket key or URL"
      value={text}
      onChange={setText}
      placeholder="Paste a ticket key"
      error={error}
      aside={aside}
      chips={
        <>
          <span>Model chip</span>
          <span>Mode chip</span>
          <span>Access chip</span>
        </>
      }
      send={{ label: 'Start', busy: false, disabled: false, onClick: () => {}, ...send }}
    />
  )
}

describe('ComposerCard', () => {
  it('is a form holding the labelled textarea, the chips with hairlines between, and one round primary send', () => {
    render(<Harness send={{ title: 'Start (⌘↵)' }} />)
    const form = screen.getByRole('form', { name: 'Start' })
    const box = within(form).getByRole('textbox', { name: 'Ticket key or URL' })
    expect(box).toHaveAttribute('placeholder', 'Paste a ticket key')
    expect(form.querySelector('.composer-bar__chips')?.children).toHaveLength(3)
    const send = within(form).getByRole('button', { name: 'Start' })
    expect(send).toHaveAttribute('data-variant', 'primary')
    expect(send).toHaveAttribute('data-icon-only', 'true')
    expect(send).toHaveAttribute('title', 'Start (⌘↵)')
    expect(send).toHaveAttribute('aria-keyshortcuts', 'Meta+Enter')
    expect(send.closest('.composer-send')).not.toBeNull()
  })

  it('sends on the button, on submit, and on Cmd or Ctrl with Enter — and Enter alone does not', () => {
    const onClick = vi.fn()
    render(<Harness send={{ onClick }} />)
    const box = screen.getByRole('textbox')
    fireEvent.change(box, { target: { value: 'OMNI-1' } })
    fireEvent.keyDown(box, { key: 'Enter' })
    expect(onClick).not.toHaveBeenCalled()
    fireEvent.keyDown(box, { key: 'Enter', metaKey: true })
    fireEvent.keyDown(box, { key: 'Enter', ctrlKey: true })
    fireEvent.click(screen.getByRole('button', { name: 'Start' }))
    fireEvent.submit(screen.getByRole('form'))
    expect(onClick).toHaveBeenCalledTimes(4)
  })

  it('is off when told so, with the reason as the title, and the keyboard cannot send either', () => {
    const onClick = vi.fn()
    render(<Harness send={{ disabled: true, title: 'Paste a ticket key first', onClick }} />)
    const send = screen.getByRole('button', { name: 'Start' })
    expect(send).toBeDisabled()
    expect(send).toHaveAttribute('title', 'Paste a ticket key first')
    fireEvent.keyDown(screen.getByRole('textbox'), { key: 'Enter', metaKey: true })
    expect(onClick).not.toHaveBeenCalled()
  })

  it('reads the busy word while the send is in flight and refuses a second send', () => {
    const onClick = vi.fn()
    render(<Harness send={{ busy: true, busyLabel: 'Starting…', onClick }} />)
    const send = screen.getByRole('button', { name: 'Starting…' })
    expect(send).toHaveAttribute('aria-disabled', 'true')
    fireEvent.click(send)
    fireEvent.keyDown(screen.getByRole('textbox'), { key: 'Enter', metaKey: true })
    expect(onClick).not.toHaveBeenCalled()
  })

  it('shows the error under the text and the aside beside the chips', () => {
    render(<Harness send={{}} error="OMNI-9 is busy" aside="The run is still working." />)
    expect(screen.getByRole('alert')).toHaveTextContent('OMNI-9 is busy')
    expect(screen.getByText('The run is still working.')).toHaveClass('composer-reason')
  })

  // A blocked run's send is the screen's primary action, and the word is
  // what the reader looks for; `wide` draws it beside the arrow.
  it('draws the send wide, with its word visible, when asked', () => {
    render(<Harness send={{ label: 'Answer', wide: true }} />)
    const button = screen.getByRole('button', { name: 'Answer' })
    expect(button).not.toHaveAttribute('data-icon-only')
    expect(button.querySelector('.sd-button__label')).toHaveTextContent('Answer')
    expect(button.closest('.composer-send')).toHaveAttribute('data-wide', 'true')
    expect(button.querySelector('svg')).not.toBeNull()
  })
})
