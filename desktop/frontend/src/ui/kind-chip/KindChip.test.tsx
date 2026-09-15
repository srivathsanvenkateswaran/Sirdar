import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import KindChip from './index'

describe('KindChip', () => {
  it.each(['triage', 'rca', 'fix'] as const)('carries the word %s and marks the kind', (kind) => {
    render(<KindChip kind={kind} />)
    const chip = screen.getByText(kind)
    expect(chip).toHaveAttribute('data-kind', kind)
    expect(chip).toHaveClass('sd-kind')
  })

  it('shows a kind it does not know verbatim rather than hiding it', () => {
    render(<KindChip kind="eval" />)
    expect(screen.getByText('eval')).toHaveAttribute('data-kind', 'eval')
  })

  it('stays left to right inside an Arabic card', () => {
    render(
      <div dir="rtl">
        <KindChip kind="fix" />
      </div>,
    )
    expect(screen.getByText('fix')).toHaveAttribute('dir', 'ltr')
  })
})
