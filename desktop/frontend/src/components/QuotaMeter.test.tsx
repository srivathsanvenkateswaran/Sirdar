// @vitest-environment jsdom
import { cleanup, render, screen } from '@testing-library/react'
import '@testing-library/jest-dom/vitest'
import { afterEach, describe, expect, it } from 'vitest'
import type { Quota } from '../api/types'
import QuotaMeter, { formatResetIn, formatSeenAgo } from './QuotaMeter'

afterEach(cleanup)

describe('formatSeenAgo', () => {
  const now = Date.parse('2026-09-10T12:00:00Z')

  it('reads in the shared relative clock', () => {
    expect(formatSeenAgo('2026-09-10T11:59:50Z', now)).toBe('seen just now')
    expect(formatSeenAgo('2026-09-10T11:56:00Z', now)).toBe('seen 4m ago')
    expect(formatSeenAgo('2026-09-10T09:00:00Z', now)).toBe('seen 3h ago')
    expect(formatSeenAgo('not a date', now)).toBe('')
  })
})

describe('formatResetIn', () => {
  it('counts down to the minute', () => {
    const now = Date.parse('2026-09-10T12:00:00Z')
    expect(formatResetIn('2026-09-10T14:14:00Z', now)).toBe('resets in 2h 14m')
    expect(formatResetIn('2026-09-10T11:00:00Z', now)).toBe('resets in 0h 0m')
  })
})

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

  it('warns above 80% and says the words at 100%', () => {
    const quota: Quota[] = [
      {
        provider: 'claude',
        observedAt: new Date().toISOString(),
        fiveHour: { utilization: 0.85, resetsAt: new Date().toISOString() },
        sevenDay: { utilization: 1, resetsAt: new Date().toISOString() },
      },
    ]
    const { container } = render(<QuotaMeter quota={quota} />)
    const chips = container.querySelectorAll('.sd-quota')
    expect(chips[0]).toHaveAttribute('data-level', 'warn')
    expect(chips[1]).toHaveAttribute('data-level', 'over')
    // Over budget is said in words as well as in red, which is the rule the
    // design language states for every status: never colour alone.
    expect(screen.getByText('over budget')).toBeInTheDocument()
  })
})
