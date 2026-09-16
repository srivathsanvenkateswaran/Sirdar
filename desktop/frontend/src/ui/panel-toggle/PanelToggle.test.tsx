import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import PanelToggle, { keyShortcut } from './index'

describe('PanelToggle', () => {
  it('is named for what a press will do, and says whether the pane is open', () => {
    const { rerender } = render(
      <PanelToggle open side="end" hideLabel="Hide panel" showLabel="Show panel" controls="pane" onToggle={() => {}} />,
    )
    const button = screen.getByRole('button', { name: 'Hide panel' })
    expect(button).toHaveAttribute('aria-expanded', 'true')
    expect(button).toHaveAttribute('aria-controls', 'pane')
    rerender(
      <PanelToggle open={false} side="end" hideLabel="Hide panel" showLabel="Show panel" controls="pane" onToggle={() => {}} />,
    )
    expect(screen.getByRole('button', { name: 'Show panel' })).toHaveAttribute('aria-expanded', 'false')
  })

  it('carries the shortcut in the tooltip and in aria-keyshortcuts', () => {
    render(
      <PanelToggle open side="start" hideLabel="Hide sidebar" showLabel="Show sidebar" shortcut="⌘B" onToggle={() => {}} />,
    )
    const button = screen.getByRole('button', { name: 'Hide sidebar' })
    expect(button).toHaveAttribute('title', 'Hide sidebar (⌘B)')
    expect(button).toHaveAttribute('aria-keyshortcuts', 'Meta+B')
    expect(keyShortcut('⌘\\')).toBe('Meta+\\')
    expect(keyShortcut('⇧⌘B')).toBe('Shift+Meta+B')
  })

  it('toggles on click and not while disabled, saying why instead', () => {
    const onToggle = vi.fn()
    const { rerender } = render(
      <PanelToggle open side="end" hideLabel="Hide panel" showLabel="Show panel" onToggle={onToggle} />,
    )
    fireEvent.click(screen.getByRole('button'))
    expect(onToggle).toHaveBeenCalledOnce()
    rerender(
      <PanelToggle
        open={false}
        side="start"
        hideLabel="Hide sidebar"
        showLabel="Show sidebar"
        disabled
        disabledReason="The sidebar is a rail at this width"
        onToggle={onToggle}
      />,
    )
    const button = screen.getByRole('button', { name: 'Show sidebar' })
    expect(button).toBeDisabled()
    expect(button).toHaveAttribute('title', 'The sidebar is a rail at this width')
    fireEvent.click(button)
    expect(onToggle).toHaveBeenCalledOnce()
  })

  it('draws the glyph for the edge and hides it from the name', () => {
    const { container, rerender } = render(
      <PanelToggle open side="start" hideLabel="Hide sidebar" showLabel="Show sidebar" onToggle={() => {}} />,
    )
    expect(container.querySelector('svg')).toHaveAttribute('aria-hidden', 'true')
    expect(container.querySelector('path')).toHaveAttribute('d', 'M9 3v18')
    rerender(<PanelToggle open side="end" hideLabel="Hide panel" showLabel="Show panel" onToggle={() => {}} />)
    expect(container.querySelector('path')).toHaveAttribute('d', 'M15 3v18')
    expect(screen.getByRole('button')).toHaveAttribute('data-side', 'end')
  })
})
