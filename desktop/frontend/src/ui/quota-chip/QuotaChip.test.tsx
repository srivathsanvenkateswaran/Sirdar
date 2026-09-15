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

  // The countdown is the only part of the chip that a narrow sidebar can
  // drop, and it has to go whole: the middot lives on the countdown's own
  // ::before, so hiding the countdown hides the separator with it, rather
  // than leaving "57% · …" pointing at nothing. Everything that is always
  // drawn is grouped so that it cannot be what wraps away.
  it('puts everything but the countdown in one group, with the countdown beside it', () => {
    const { container } = render(
      <QuotaChip provider="claude" window="5h" percent={57} resetsIn="1h 58m" />,
    )
    const chip = container.querySelector('.sd-quota')!
    const line = container.querySelector('.sd-quota__line')!
    for (const part of ['__provider', '__window', '__bar', '__pct']) {
      expect(line.querySelector(`.sd-quota${part}`)).not.toBeNull()
    }
    expect(line.querySelector('.sd-quota__reset')).toBeNull()
    expect(chip.children).toHaveLength(2)
    expect(chip.children[1]).toHaveClass('sd-quota__reset')
  })

  it('keeps the over-budget words out of the countdown group, so they are never what drops', () => {
    const { container } = render(
      <QuotaChip provider="codex" window="used" percent={100} resetsIn="4h 0m" />,
    )
    expect(container.querySelector('.sd-quota__line .sd-quota__word')).toHaveTextContent(
      'over budget',
    )
  })

  it('draws no countdown element at all when there is none to draw', () => {
    const { container } = render(<QuotaChip provider="claude" window="5h" percent={12} />)
    expect(container.querySelector('.sd-quota__reset')).toBeNull()
    expect(container.querySelector('.sd-quota')!.children).toHaveLength(1)
  })

  it('carries no title when there is no reset to say', () => {
    const { container } = render(<QuotaChip provider="codex" window="used" percent={55} />)
    expect(container.querySelector('.sd-quota')).not.toHaveAttribute('title')
  })
})
