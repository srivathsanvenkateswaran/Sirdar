import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import ModalSheet, { type ModalNavGroup } from './index'

const GROUPS: ModalNavGroup[] = [
  { label: 'Workspace', items: [{ id: 'general', label: 'General' }, { id: 'providers', label: 'Providers' }] },
  { label: 'Account', items: [{ id: 'identity', label: 'Identity' }] },
]

function open(props: Partial<React.ComponentProps<typeof ModalSheet>> = {}) {
  const onClose = vi.fn()
  const onSelect = vi.fn()
  render(
    <ModalSheet
      open
      title="Settings"
      groups={GROUPS}
      current="general"
      onSelect={onSelect}
      onClose={onClose}
      {...props}
    >
      <p>General settings</p>
    </ModalSheet>,
  )
  return { onClose, onSelect }
}

describe('ModalSheet', () => {
  it('renders nothing while it is closed', () => {
    const { container } = render(
      <ModalSheet
        open={false}
        title="Settings"
        groups={GROUPS}
        current="general"
        onSelect={() => {}}
        onClose={() => {}}
      >
        <p>hidden</p>
      </ModalSheet>,
    )
    expect(container).toBeEmptyDOMElement()
  })

  it('is a modal dialog named by its page heading', () => {
    open()
    expect(screen.getByRole('dialog', { name: 'Settings' })).toHaveAttribute('aria-modal', 'true')
  })

  it('names the secondary nav and marks the page the reader is on', () => {
    open()
    const nav = screen.getByRole('navigation', { name: 'Settings sections' })
    expect(nav).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'General' })).toHaveAttribute('aria-current', 'page')
    expect(screen.getByRole('button', { name: 'Providers' })).not.toHaveAttribute('aria-current')
  })

  // The icon is decoration: the row is still named by its label alone, so a
  // screen reader hears "General" and not an unlabelled graphic before it.
  it('draws an item icon before the label without changing the row name', () => {
    open({
      groups: [
        {
          label: 'Workspace',
          items: [
            {
              id: 'general',
              label: 'General',
              icon: <svg viewBox="0 0 24 24" data-testid="general-icon" />,
            },
          ],
        },
      ],
    })
    const row = screen.getByRole('button', { name: 'General' })
    expect(row.querySelector('.sd-modal__nav-icon')).toHaveAttribute('aria-hidden', 'true')
    expect(screen.getByTestId('general-icon')).toBeInTheDocument()
  })

  it('closes on Escape', () => {
    const { onClose } = open()
    fireEvent.keyDown(window, { key: 'Escape' })
    expect(onClose).toHaveBeenCalled()
  })

  it('closes on a click on the scrim itself, not on the sheet', () => {
    const { onClose } = open()
    fireEvent.mouseDown(screen.getByRole('dialog'))
    expect(onClose).not.toHaveBeenCalled()
    fireEvent.mouseDown(document.querySelector('.sd-modal-scrim') as HTMLElement)
    expect(onClose).toHaveBeenCalled()
  })

  it('moves within the secondary nav with the arrow keys, and wraps', () => {
    const { onSelect } = open()
    const nav = screen.getByRole('navigation', { name: 'Settings sections' })
    fireEvent.keyDown(nav, { key: 'ArrowDown' })
    expect(onSelect).toHaveBeenLastCalledWith('providers')
    fireEvent.keyDown(nav, { key: 'ArrowUp' })
    expect(onSelect).toHaveBeenLastCalledWith('identity')
  })

  it('moves focus to the page heading, closes the window behind it, and gives focus back on close', () => {
    const opener = document.createElement('button')
    document.body.append(opener)
    opener.focus()

    const { unmount } = render(
      <ModalSheet
        open
        title="Settings"
        groups={GROUPS}
        current="general"
        onSelect={() => {}}
        onClose={() => {}}
      >
        <p>General settings</p>
      </ModalSheet>,
    )
    expect(document.activeElement).toBe(screen.getByRole('heading', { name: 'Settings' }))
    expect(opener).toHaveAttribute('inert')
    expect(screen.getByRole('dialog')).not.toHaveAttribute('inert')
    unmount()
    expect(opener).not.toHaveAttribute('inert')
    expect(document.activeElement).toBe(opener)
    opener.remove()
  })

  it('draws the footer and the nav footer when it is given them', () => {
    open({
      footer: <button type="button">Save</button>,
      navFooter: <span>Sirdar desktop v1.2.3</span>,
    })
    expect(screen.getByRole('button', { name: 'Save' })).toBeInTheDocument()
    expect(screen.getByText('Sirdar desktop v1.2.3')).toBeInTheDocument()
  })
})
