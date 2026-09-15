import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import QuotaChip from './index'

describe('QuotaChip', () => {
  it('states the provider, the window and the share used', () => {
    const { container } = render(<QuotaChip provider="claude" window="5h" percent={42.4} />)
    expect(screen.getByText('claude')).toBeInTheDocument()
    expect(screen.getByText('5h')).toBeInTheDocument()
    expect(screen.getByText('42%')).toBeInTheDocument()
    expect(container.querySelector('.sd-quota')).toHaveAttribute('data-level', 'ok')
  })

  it('reads the bar out as a sentence rather than as a picture', () => {
    render(<QuotaChip provider="codex" window="7d" percent={91} />)
    expect(screen.getByRole('img', { name: 'codex 7d: 91% used' })).toBeInTheDocument()
  })

  it('warns past four fifths', () => {
    const { container } = render(<QuotaChip provider="claude" window="5h" percent={80} />)
    expect(container.querySelector('.sd-quota')).toHaveAttribute('data-level', 'warn')
  })

  it('says over budget in words, not only in red', () => {
    render(<QuotaChip provider="claude" window="5h" percent={100} />)
    expect(screen.getByText('over budget')).toBeInTheDocument()
  })

  it('takes over budget as a fact the workspace reports, whatever the bar says', () => {
    render(<QuotaChip provider="claude" window="5h" percent={62} overBudget />)
    expect(screen.getByText('over budget')).toBeInTheDocument()
  })

  it('clamps a nonsense percentage instead of drawing past the end', () => {
    const { container } = render(<QuotaChip provider="claude" window="5h" percent={Number.NaN} />)
    expect(container.querySelector('.sd-quota__fill')).toHaveStyle({ inlineSize: '0%' })
    render(<QuotaChip provider="codex" window="7d" percent={480} />)
    expect(screen.getByText('100%')).toBeInTheDocument()
  })

  it('keeps its numbers left to right inside an Arabic header', () => {
    render(
      <div dir="rtl">
        <QuotaChip provider="claude" window="5h" percent={12} resetsIn="2h 14m" />
      </div>,
    )
    expect(screen.getByText('12%')).toHaveAttribute('dir', 'ltr')
    expect(screen.getByText('2h 14m')).toHaveAttribute('dir', 'ltr')
  })

  it('says the reset in full in its title and the bar, and the countdown alone on the line', () => {
    const { container } = render(
      <QuotaChip provider="claude" window="7d" percent={88} resetsIn="1h 58m" />,
    )
    expect(container.querySelector('.sd-quota')).toHaveAttribute('title', 'resets in 1h 58m')
    expect(
      screen.getByRole('img', { name: 'claude 7d: 88% used, resets in 1h 58m' }),
    ).toBeInTheDocument()
    expect(screen.getByText('1h 58m')).toHaveClass('sd-quota__reset')
  })

  it('carries no title when there is no reset to say', () => {
    const { container } = render(<QuotaChip provider="codex" window="used" percent={55} />)
    expect(container.querySelector('.sd-quota')).not.toHaveAttribute('title')
  })
})
