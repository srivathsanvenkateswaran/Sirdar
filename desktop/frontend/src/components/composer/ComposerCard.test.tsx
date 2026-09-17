import { fireEvent, render, screen, within } from '@testing-library/react'
import { useState } from 'react'
import { describe, expect, it, vi } from 'vitest'
import ComposerCard, { type ComposerSend, type ComposerStop } from './ComposerCard'

function Harness({
  send,
  stop,
  error,
  aside,
  variant,
  maxRows,
}: {
  send: Partial<ComposerSend>
  stop?: ComposerStop
  error?: string
  aside?: string
  variant?: 'card' | 'strip'
  maxRows?: number
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
      variant={variant}
      maxRows={maxRows}
      chips={
        <>
          <span>Model chip</span>
          <span>Mode chip</span>
          <span>Access chip</span>
        </>
      }
      send={{ label: 'Start', busy: false, disabled: false, onClick: () => {}, ...send }}
      stop={stop}
    />
  )
}

describe('ComposerCard', () => {
  it('is a form holding the labelled textarea, the chips with hairlines between, and one round primary send', () => {
    render(<Harness send={{ title: 'Start (↵)' }} />)
    const form = screen.getByRole('form', { name: 'Start' })
    const box = within(form).getByRole('textbox', { name: 'Ticket key or URL' })
    expect(box).toHaveAttribute('placeholder', 'Paste a ticket key')
    expect(form.querySelector('.composer-bar__chips')?.children).toHaveLength(3)
    const send = within(form).getByRole('button', { name: 'Start' })
    expect(send).toHaveAttribute('data-variant', 'primary')
    expect(send).toHaveAttribute('data-icon-only', 'true')
    expect(send).toHaveAttribute('title', 'Start (↵)')
    expect(send).toHaveAttribute('aria-keyshortcuts', 'Enter')
    expect(send.closest('.composer-send')).not.toBeNull()
  })

  it('sends on Enter, on Cmd or Ctrl with Enter, on the button and on submit — and Shift with Enter does not', () => {
    const onClick = vi.fn()
    render(<Harness send={{ onClick }} />)
    const box = screen.getByRole('textbox')
    fireEvent.change(box, { target: { value: 'OMNI-1' } })
    fireEvent.keyDown(box, { key: 'Enter', shiftKey: true })
    expect(onClick).not.toHaveBeenCalled()
    fireEvent.keyDown(box, { key: 'Enter' })
    fireEvent.keyDown(box, { key: 'Enter', metaKey: true })
    fireEvent.keyDown(box, { key: 'Enter', ctrlKey: true })
    fireEvent.click(screen.getByRole('button', { name: 'Start' }))
    fireEvent.submit(screen.getByRole('form'))
    expect(onClick).toHaveBeenCalledTimes(5)
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

  // While the run works the round button stops it. A send that is off says
  // nothing a reader can act on, and the reason for it was being printed
  // twice — in the box and again beside the button.
  describe('stops the run in place of sending', () => {
    it('draws Stop in place of the send and calls it', () => {
      const onStop = vi.fn()
      const onClick = vi.fn()
      render(<Harness send={{ onClick }} stop={{ onStop }} />)
      expect(screen.queryByRole('button', { name: 'Start' })).toBeNull()
      const button = screen.getByRole('button', { name: 'Stop the run' })
      expect(button).toHaveAttribute('data-icon-only', 'true')
      expect(button).toHaveAttribute('title', 'Stop the run')
      expect(button.closest('.composer-send')).toHaveAttribute('data-stop', 'true')
      fireEvent.click(button)
      expect(onStop).toHaveBeenCalledTimes(1)
      expect(onClick).not.toHaveBeenCalled()
    })

    it('has no send for Enter or submit to reach', () => {
      const onClick = vi.fn()
      render(<Harness send={{ onClick }} stop={{ onStop: () => {} }} />)
      fireEvent.change(screen.getByRole('textbox'), { target: { value: 'go on' } })
      fireEvent.keyDown(screen.getByRole('textbox'), { key: 'Enter' })
      fireEvent.submit(screen.getByRole('form'))
      expect(onClick).not.toHaveBeenCalled()
    })

    it('says why it cannot stop, when it cannot', () => {
      const onStop = vi.fn()
      render(
        <Harness
          send={{}}
          stop={{ onStop, disabled: true, title: 'Only a run started from this window can be stopped' }}
        />,
      )
      const button = screen.getByRole('button', { name: 'Stop the run' })
      expect(button).toBeDisabled()
      expect(button).toHaveAttribute('title', 'Only a run started from this window can be stopped')
    })
  })

  // The box is one line at rest and follows the text a row at a time, so a
  // chip bar sits directly under a single line and a pasted paragraph is
  // still all visible — up to eight lines, where it scrolls inside.
  describe('sizes itself to the text', () => {
    it('is one row at rest, grows a row per line, and shrinks back', () => {
      render(<Harness send={{}} />)
      const box = screen.getByRole('textbox') as HTMLTextAreaElement
      expect(box.rows).toBe(1)
      fireEvent.change(box, { target: { value: 'one\ntwo\nthree' } })
      expect(box.rows).toBe(3)
      fireEvent.change(box, { target: { value: 'one\ntwo' } })
      expect(box.rows).toBe(2)
      fireEvent.change(box, { target: { value: '' } })
      expect(box.rows).toBe(1)
    })

    it('stops at eight rows and lets the rest scroll', () => {
      render(<Harness send={{}} />)
      const box = screen.getByRole('textbox') as HTMLTextAreaElement
      fireEvent.change(box, { target: { value: Array.from({ length: 12 }, (_, i) => `line ${i + 1}`).join('\n') } })
      expect(box.rows).toBe(8)
    })

    it('takes a smaller cap when the layout asks for one', () => {
      render(<Harness send={{}} maxRows={4} />)
      const box = screen.getByRole('textbox') as HTMLTextAreaElement
      fireEvent.change(box, { target: { value: 'a\nb\nc\nd\ne\nf' } })
      expect(box.rows).toBe(4)
    })

    it('behaves the same as the Document strip', () => {
      render(<Harness send={{}} variant="strip" />)
      const box = screen.getByRole('textbox') as HTMLTextAreaElement
      expect(box.rows).toBe(1)
      fireEvent.change(box, { target: { value: 'yes\nrun it' } })
      expect(box.rows).toBe(2)
      fireEvent.change(box, { target: { value: 'yes' } })
      expect(box.rows).toBe(1)
    })
  })
})
