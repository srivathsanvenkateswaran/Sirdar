import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import Drawer from './index'

describe('Drawer', () => {
  it('draws nothing while closed, and a named dialog over the column when open', () => {
    const onClose = vi.fn()
    const { rerender } = render(
      <Drawer open={false} title="Tools" onClose={onClose}>
        <p>every call</p>
      </Drawer>,
    )
    expect(screen.queryByRole('dialog')).toBeNull()

    rerender(
      <Drawer open title="Tools" meta="15 calls · 2 denied" onClose={onClose}>
        <p>every call</p>
      </Drawer>,
    )
    const dialog = screen.getByRole('dialog', { name: 'Tools' })
    expect(dialog).toHaveAttribute('aria-modal', 'false')
    expect(screen.getByText('15 calls · 2 denied')).toBeInTheDocument()
    expect(screen.getByText('every call')).toBeInTheDocument()
  })

  it('closes on its button and on Escape, and hands focus back where it came from', () => {
    const onClose = vi.fn()
    render(
      <>
        <button type="button">Tools</button>
        <Drawer open title="Tools" onClose={onClose}>
          <p>body</p>
        </Drawer>
      </>,
    )
    const close = screen.getByRole('button', { name: 'Close Tools' })
    expect(document.activeElement).toBe(close)
    fireEvent.click(close)
    expect(onClose).toHaveBeenCalledTimes(1)
    fireEvent.keyDown(document, { key: 'Escape' })
    expect(onClose).toHaveBeenCalledTimes(2)
  })

  it('returns focus to the opener once it is gone', () => {
    const onClose = vi.fn()
    function Host(): JSX.Element {
      return (
        <>
          <button type="button">Bundle</button>
          <Drawer open title="Bundle" onClose={onClose}>
            <p>body</p>
          </Drawer>
        </>
      )
    }
    const opener = document.createElement('button')
    document.body.appendChild(opener)
    opener.focus()
    const { rerender } = render(<Host />)
    expect(document.activeElement).toBe(screen.getByRole('button', { name: 'Close Bundle' }))
    rerender(
      <>
        <button type="button">Bundle</button>
        <Drawer open={false} title="Bundle" onClose={onClose}>
          <p>body</p>
        </Drawer>
      </>,
    )
    expect(document.activeElement).toBe(opener)
    opener.remove()
  })

  it('takes a width when the column is not the path', () => {
    render(
      <Drawer open title="Tools" onClose={() => {}} width={520}>
        <p>body</p>
      </Drawer>,
    )
    expect(screen.getByRole('dialog').getAttribute('style')).toContain('--sd-drawer-w: 520px')
  })
})
