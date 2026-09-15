import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import SidebarNavItem from './index'

describe('SidebarNavItem', () => {
  it('marks the row the reader is on, and only that one', () => {
    render(
      <>
        <SidebarNavItem label="Board" current onSelect={() => {}} />
        <SidebarNavItem label="Register" onSelect={() => {}} />
      </>,
    )
    expect(screen.getByRole('button', { name: 'Board' })).toHaveAttribute('aria-current', 'page')
    expect(screen.getByRole('button', { name: 'Register' })).not.toHaveAttribute('aria-current')
  })

  it('calls back when the row is chosen', () => {
    const onSelect = vi.fn()
    render(<SidebarNavItem label="Eval" onSelect={onSelect} />)
    fireEvent.click(screen.getByRole('button', { name: 'Eval' }))
    expect(onSelect).toHaveBeenCalledTimes(1)
  })

  it('names the count rather than drawing a bare number', () => {
    render(<SidebarNavItem label="Board" count={3} onSelect={() => {}} />)
    expect(screen.getByRole('button', { name: /3 waiting on Board/ })).toBeInTheDocument()
  })

  it('draws no count when nothing is waiting', () => {
    const { container } = render(<SidebarNavItem label="Board" count={0} onSelect={() => {}} />)
    expect(container.querySelector('.sd-nav-row__count')).toBeNull()
  })

  it('hides the icon from the accessible name', () => {
    render(
      <SidebarNavItem
        label="Settings"
        icon={<svg data-testid="icon" />}
        onSelect={() => {}}
      />,
    )
    expect(screen.getByRole('button', { name: 'Settings' })).toBeInTheDocument()
    expect(screen.getByTestId('icon').parentElement).toHaveAttribute('aria-hidden', 'true')
  })
})
