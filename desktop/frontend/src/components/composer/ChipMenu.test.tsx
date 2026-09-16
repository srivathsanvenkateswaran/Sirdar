import { fireEvent, render, screen, within } from '@testing-library/react'
import { useState } from 'react'
import { describe, expect, it, vi } from 'vitest'
import ChipMenu, { type ChipMenuItem } from './ChipMenu'

const ITEMS: ChipMenuItem[] = [
  { id: 'triage', label: 'Triage', note: 'Read the ticket and the code' },
  { id: 'rca', label: 'RCA', note: 'A root-cause note' },
  { id: 'fix', label: 'Fix', note: 'A fix on a branch', disabled: 'Needs a triage note first' },
]

function Harness({ onSelect }: { onSelect?: (id: string) => void }): JSX.Element {
  const [value, setValue] = useState('triage')
  return (
    <ChipMenu
      label="Mode"
      value={value}
      items={ITEMS}
      onSelect={(id) => {
        setValue(id)
        onSelect?.(id)
      }}
    />
  )
}

const chip = () => screen.getByRole('button', { name: /^Mode/ })

describe('ChipMenu', () => {
  it('is a chip reading the word and the chosen value, and opens a menu of the items', () => {
    render(<Harness />)
    expect(chip()).toHaveTextContent('ModeTriage')
    expect(chip()).toHaveAccessibleName('Mode: Triage')
    expect(chip()).toHaveAttribute('aria-haspopup', 'menu')
    fireEvent.click(chip())
    const menu = screen.getByRole('menu', { name: 'Mode' })
    expect(menu.style.position).toBe('fixed')
    const rows = within(menu).getAllByRole('menuitemradio')
    expect(rows.map((r) => r.querySelector('.composer-menu__label')?.textContent)).toEqual([
      'Triage',
      'RCA',
      'Fix',
    ])
    expect(rows[0]).toHaveAttribute('aria-checked', 'true')
    expect(rows[0]).toHaveFocus()
    expect(rows[0]).toHaveTextContent('Read the ticket and the code')
    // An item that cannot be chosen says why, and stays in the list.
    expect(rows[2]).toHaveAttribute('aria-disabled', 'true')
    expect(rows[2]).toHaveAttribute('title', 'Needs a triage note first')
    expect(rows[2]).toHaveTextContent('Needs a triage note first')
  })

  it('picks with a click, closes, and returns focus to the chip', () => {
    const onSelect = vi.fn()
    render(<Harness onSelect={onSelect} />)
    fireEvent.click(chip())
    fireEvent.click(screen.getByRole('menuitemradio', { name: /RCA/ }))
    expect(onSelect).toHaveBeenCalledWith('rca')
    expect(screen.queryByRole('menu')).toBeNull()
    expect(chip()).toHaveFocus()
    expect(chip()).toHaveAccessibleName('Mode: RCA')
  })

  it('refuses a disabled item', () => {
    const onSelect = vi.fn()
    render(<Harness onSelect={onSelect} />)
    fireEvent.click(chip())
    fireEvent.click(screen.getByRole('menuitemradio', { name: /Fix/ }))
    expect(onSelect).not.toHaveBeenCalled()
    expect(screen.getByRole('menu')).toBeInTheDocument()
  })

  it('walks with the arrows, wrapping, and Enter picks', () => {
    const onSelect = vi.fn()
    render(<Harness onSelect={onSelect} />)
    fireEvent.click(chip())
    const menu = screen.getByRole('menu')
    fireEvent.keyDown(menu, { key: 'ArrowDown' })
    expect(screen.getByRole('menuitemradio', { name: /RCA/ })).toHaveFocus()
    fireEvent.keyDown(menu, { key: 'ArrowUp' })
    fireEvent.keyDown(menu, { key: 'ArrowUp' })
    expect(screen.getByRole('menuitemradio', { name: /Fix/ })).toHaveFocus()
    fireEvent.keyDown(menu, { key: 'Home' })
    fireEvent.keyDown(menu, { key: 'ArrowDown' })
    fireEvent.keyDown(menu, { key: 'Enter' })
    expect(onSelect).toHaveBeenCalledWith('rca')
    expect(screen.queryByRole('menu')).toBeNull()
  })

  it('closes on Escape and on a click outside, keeping the value', () => {
    render(<Harness />)
    fireEvent.click(chip())
    fireEvent.keyDown(screen.getByRole('menu'), { key: 'Escape' })
    expect(screen.queryByRole('menu')).toBeNull()
    expect(chip()).toHaveFocus()
    fireEvent.click(chip())
    fireEvent.mouseDown(document.body)
    expect(screen.queryByRole('menu')).toBeNull()
    expect(chip()).toHaveAccessibleName('Mode: Triage')
  })

  it('only explains when nothing can be chosen: a dialog describing the items, the current one marked', () => {
    render(<ChipMenu label="Access" value="worktree" items={ITEMS.map((i) => ({ ...i, disabled: undefined }))} />)
    const access = screen.getByRole('button', { name: 'Access: worktree' })
    expect(access).toHaveAttribute('aria-haspopup', 'dialog')
    fireEvent.click(access)
    const dialog = screen.getByRole('dialog', { name: 'Access' })
    expect(within(dialog).queryByRole('menuitemradio')).toBeNull()
    expect(dialog).toHaveTextContent('A root-cause note')
    expect(dialog).toHaveFocus()
    fireEvent.keyDown(dialog, { key: 'Escape' })
    expect(screen.queryByRole('dialog')).toBeNull()
    expect(access).toHaveFocus()
  })

  it('is a fact rather than a control when read-only, and says why', () => {
    render(<ChipMenu label="Mode" value="fix" items={ITEMS} readOnly="The run's kind does not change" />)
    const fact = screen.getByRole('button', { name: /^Mode: Fix/ })
    expect(fact).toBeDisabled()
    expect(fact).toHaveAttribute('title', "The run's kind does not change")
    expect(fact).toHaveAccessibleName("Mode: Fix. The run's kind does not change")
    expect(fact.querySelector('.composer-chip__chevron')).toBeNull()
  })
})
