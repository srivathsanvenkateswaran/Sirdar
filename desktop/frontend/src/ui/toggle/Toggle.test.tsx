import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import Toggle from './index'

describe('Toggle', () => {
  it('is a switch that reports the opposite of what it was', () => {
    const onChange = vi.fn()
    render(<Toggle label="Include the title" checked={false} onChange={onChange} />)
    const toggle = screen.getByRole('switch', { name: 'Include the title' })
    expect(toggle).toHaveAttribute('aria-checked', 'false')
    fireEvent.click(toggle)
    expect(onChange).toHaveBeenCalledWith(true)
  })

  it('reads as on when checked', () => {
    render(<Toggle label="Notify" checked onChange={() => {}} />)
    expect(screen.getByRole('switch')).toHaveAttribute('aria-checked', 'true')
  })

  it('does nothing while disabled', () => {
    const onChange = vi.fn()
    render(<Toggle label="Notify" checked onChange={onChange} disabled />)
    const toggle = screen.getByRole('switch')
    expect(toggle).toBeDisabled()
    fireEvent.click(toggle)
    expect(onChange).not.toHaveBeenCalled()
  })

  it('can be named by a visible label instead', () => {
    render(
      <>
        <span id="lab">إشعارات</span>
        <Toggle label="Notifications" labelledBy="lab" checked={false} onChange={() => {}} />
      </>,
    )
    expect(screen.getByRole('switch', { name: 'إشعارات' })).toBeInTheDocument()
  })
})
