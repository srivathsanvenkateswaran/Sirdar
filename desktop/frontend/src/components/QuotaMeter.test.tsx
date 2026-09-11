// @vitest-environment jsdom
import { cleanup, render, screen } from '@testing-library/react'
import '@testing-library/jest-dom/vitest'
import { afterEach, describe, expect, it } from 'vitest'
import type { Quota } from '../api/types'
import QuotaMeter from './QuotaMeter'

afterEach(cleanup)

describe('QuotaMeter', () => {
  it('renders nothing when there is no quota', () => {
    const { container } = render(<QuotaMeter quota={[]} />)
    expect(container).toBeEmptyDOMElement()
  })

  it('renders two bars with percentages for a Claude quota', () => {
    const quota: Quota[] = [
      {
        provider: 'claude',
        observedAt: new Date().toISOString(),
        fiveHour: { utilization: 0.42, resetsAt: new Date(Date.now() + 3600_000).toISOString() },
        sevenDay: { utilization: 0.1, resetsAt: new Date(Date.now() + 7200_000).toISOString() },
      },
    ]
    render(<QuotaMeter quota={quota} />)
    expect(screen.getByText('42%')).toBeInTheDocument()
    expect(screen.getByText('10%')).toBeInTheDocument()
    expect(screen.getByText('5h')).toBeInTheDocument()
    expect(screen.getByText('7d')).toBeInTheDocument()
  })

  it('renders one bar from usedPercent for a Codex quota', () => {
    const quota: Quota[] = [
      { provider: 'codex', observedAt: new Date().toISOString(), usedPercent: 55 },
    ]
    render(<QuotaMeter quota={quota} />)
    expect(screen.getByText('55%')).toBeInTheDocument()
    expect(screen.getByText('used')).toBeInTheDocument()
  })

  it('applies the warning class above 80% and the danger class at 100%', () => {
    const quota: Quota[] = [
      {
        provider: 'claude',
        observedAt: new Date().toISOString(),
        fiveHour: { utilization: 0.85, resetsAt: new Date().toISOString() },
        sevenDay: { utilization: 1, resetsAt: new Date().toISOString() },
      },
    ]
    const { container } = render(<QuotaMeter quota={quota} />)
    const fills = container.querySelectorAll('.quota-bar__fill')
    expect(fills[0]).toHaveClass('quota-bar--warn')
    expect(fills[1]).toHaveClass('quota-bar--danger')
  })
})
