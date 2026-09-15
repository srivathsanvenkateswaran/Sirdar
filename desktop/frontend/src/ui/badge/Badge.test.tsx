import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import Badge from './index'
import StatusBadge from '../status-badge'

describe('Badge', () => {
  it('draws the word it was given', () => {
    render(<Badge>pro</Badge>)
    expect(screen.getByText('pro')).toBeInTheDocument()
  })

  it('says what the word is about when it is given a title', () => {
    render(<Badge title="Billing mode">api</Badge>)
    expect(screen.getByText('api')).toHaveAttribute('title', 'Billing mode')
  })

  it('does not share a class with the status badge, so no status hue reaches it', () => {
    const { container } = render(
      <>
        <Badge>pro</Badge>
        <StatusBadge status="failed" />
      </>,
    )
    const fact = container.querySelector('.sd-fact-badge') as HTMLElement
    expect(fact.className).toBe('sd-fact-badge')
    expect(fact.classList.contains('sd-badge')).toBe(false)
  })

  it('is not a control', () => {
    render(<Badge>pro</Badge>)
    expect(screen.queryByRole('button')).toBeNull()
  })
})
