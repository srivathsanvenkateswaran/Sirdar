import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import StatCard from './index'

describe('StatCard', () => {
  it('shows the label, the figure and the line under it', () => {
    render(<StatCard label="Runs this week" value="38" detail="12 more than last week" />)
    expect(screen.getByText('Runs this week')).toBeInTheDocument()
    expect(screen.getByText('38')).toBeInTheDocument()
    expect(screen.getByText('12 more than last week')).toBeInTheDocument()
  })

  it('keeps the figure left to right and carries the exact number as its tooltip', () => {
    render(
      <div dir="rtl">
        <StatCard label="المصروف" value="$12.40" valueTitle="$12.4031" />
      </div>,
    )
    const value = screen.getByText('$12.40')
    expect(value).toHaveAttribute('dir', 'ltr')
    expect(value).toHaveAttribute('title', '$12.4031')
  })

  it('draws no detail line when there is none', () => {
    const { container } = render(<StatCard label="Confirmed" value="71%" />)
    expect(container.querySelector('.sd-stat__detail')).toBeNull()
  })
})
