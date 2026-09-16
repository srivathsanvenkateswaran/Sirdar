import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import Marker, { markerKind } from './index'

describe('Marker', () => {
  it('labels when it goes nowhere, and is a button when it does', () => {
    const { container, rerender } = render(<Marker id="E1" />)
    expect(container.querySelector('span.sd-marker')).toHaveTextContent('E1')
    expect(screen.queryByRole('button')).toBeNull()

    const onClick = vi.fn()
    rerender(<Marker id="E1" onClick={onClick} title="Read ledger.go" />)
    const button = screen.getByRole('button', { name: 'Marker E1' })
    expect(button).toHaveAttribute('title', 'Read ledger.go')
    fireEvent.click(button)
    expect(onClick).toHaveBeenCalledWith('E1', expect.anything())
  })

  it('says which family it belongs to and whether its twin is in view', () => {
    render(<Marker id="C2" hot onClick={() => {}} />)
    const chip = screen.getByRole('button', { name: 'Marker C2' })
    expect(chip).toHaveAttribute('data-kind', 'C')
    expect(chip).toHaveAttribute('data-hot', 'true')
    expect(chip).toHaveAttribute('aria-pressed', 'true')
    expect(markerKind('E9')).toBe('E')
    expect(markerKind('x1')).toBe('other')
  })

  it('has a small size for prose and no size attribute at the default', () => {
    const { container } = render(
      <>
        <Marker id="E1" size="sm" />
        <Marker id="E2" />
      </>,
    )
    const chips = container.querySelectorAll('.sd-marker')
    expect(chips[0]).toHaveAttribute('data-size', 'sm')
    expect(chips[1]).not.toHaveAttribute('data-size')
  })
})
