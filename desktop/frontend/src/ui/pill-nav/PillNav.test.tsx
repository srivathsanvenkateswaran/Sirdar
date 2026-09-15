import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import PillNav from './index'

const ITEMS = [
  { id: 'board', label: 'Board' },
  { id: 'register', label: 'Register', count: 12 },
  { id: 'settings', label: 'Settings' },
]

describe('PillNav', () => {
  it('names itself, so two navs on a page are told apart', () => {
    render(<PillNav items={ITEMS} current="board" label="Screens" />)
    expect(screen.getByRole('navigation', { name: 'Screens' })).toBeInTheDocument()
  })

  it('marks exactly one item as the current page', () => {
    render(<PillNav items={ITEMS} current="register" label="Screens" />)
    const current = screen.getAllByRole('button').filter((b) => b.getAttribute('aria-current'))
    expect(current).toHaveLength(1)
    expect(current[0]).toHaveTextContent('Register')
  })

  it('marks nothing when the current item is not in the list', () => {
    render(<PillNav items={ITEMS} current="library" label="Screens" />)
    expect(screen.queryByRole('button', { current: 'page' })).toBeNull()
  })

  it('reports the item that was chosen', () => {
    const onSelect = vi.fn()
    render(<PillNav items={ITEMS} current="board" label="Screens" onSelect={onSelect} />)
    fireEvent.click(screen.getByRole('button', { name: /Settings/ }))
    expect(onSelect).toHaveBeenCalledWith('settings')
  })

  it('shows a zero count rather than hiding it', () => {
    render(<PillNav items={[{ id: 'a', label: 'Inbound', count: 0 }]} label="Screens" />)
    expect(screen.getByText('0')).toBeInTheDocument()
  })

  it('renders links when the items carry a URL', () => {
    render(
      <PillNav
        items={[{ id: 'docs', label: 'Docs', href: '#/docs' }]}
        current="docs"
        label="Site"
      />,
    )
    expect(screen.getByRole('link', { name: 'Docs' })).toHaveAttribute('href', '#/docs')
  })

  it('reaches every item by keyboard in DOM order', () => {
    render(<PillNav items={ITEMS} current="board" label="Screens" />)
    const buttons = screen.getAllByRole('button')
    for (const button of buttons) {
      button.focus()
      expect(button).toHaveFocus()
      expect(button).not.toHaveAttribute('tabindex')
    }
    expect(buttons.map((b) => b.textContent)).toEqual(['Board', 'Register12', 'Settings'])
  })

  it('keeps its order under RTL, where the row itself is mirrored', () => {
    const arabic = [
      { id: 'board', label: 'اللوحة' },
      { id: 'register', label: 'السجل' },
    ]
    render(
      <div dir="rtl">
        <PillNav items={arabic} current="board" label="الشاشات" />
      </div>,
    )
    const buttons = screen.getAllByRole('button')
    expect(buttons.map((b) => b.textContent)).toEqual(['اللوحة', 'السجل'])
    expect(buttons[0]).toHaveAttribute('aria-current', 'page')
  })
})
