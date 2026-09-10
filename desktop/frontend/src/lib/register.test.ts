import { describe, expect, it } from 'vitest'
import type { RegisterRow } from '../api/types'
import {
  computeAccuracy,
  formatHeld,
  groupDate,
  groupRegisterRows,
  sumUsage,
  toMarkdownTable,
} from './register'

function row(overrides: Partial<RegisterRow>): RegisterRow {
  return {
    key: 'OMNI-1',
    kind: 'triage',
    runId: 'run-1',
    date: '2026-09-01',
    provider: 'claude',
    model: 'sonnet',
    service: 'oxo-api',
    classification: 'null-pointer',
    confidence: 'high',
    severity: 'sev2',
    turns: 4,
    costUsd: 0.5,
    triageVerdict: '',
    notePath: '',
    ...overrides,
  }
}

describe('groupRegisterRows', () => {
  it('folds triage, rca and resolution rows sharing a key into one group', () => {
    const rows = [
      row({ key: 'OMNI-1', kind: 'triage', date: '2026-09-01', triageVerdict: 'confirmed', notePath: 'triage.md' }),
      row({ key: 'OMNI-1', kind: 'rca', date: '2026-09-02', notePath: 'rca.md' }),
      row({ key: 'OMNI-1', kind: 'resolution', date: '2026-09-03', classification: 'patched', notePath: 'res.md' }),
      row({ key: 'OMNI-2', kind: 'triage', date: '2026-09-04', triageVerdict: 'wrong' }),
    ]

    const groups = groupRegisterRows(rows)

    expect(groups).toHaveLength(2)
    const first = groups[0]
    expect(first.key).toBe('OMNI-1')
    expect(first.rows).toHaveLength(3)
    expect(first.triage?.triageVerdict).toBe('confirmed')
    expect(first.rca?.date).toBe('2026-09-02')
    expect(first.resolution?.classification).toBe('patched')
  })

  it('keeps the first non-empty service seen for a key', () => {
    const rows = [row({ key: 'OMNI-1', service: '' }), row({ key: 'OMNI-1', service: 'oxo-api' })]
    const [group] = groupRegisterRows(rows)
    expect(group.service).toBe('oxo-api')
  })
})

describe('groupDate', () => {
  it('prefers the triage date, then rca, then resolution', () => {
    const withTriage = groupRegisterRows([row({ kind: 'triage', date: '2026-09-01' })])[0]
    expect(groupDate(withTriage)).toBe('2026-09-01')

    const rcaOnly = groupRegisterRows([row({ kind: 'rca', date: '2026-09-05' })])[0]
    expect(groupDate(rcaOnly)).toBe('2026-09-05')

    const resolutionOnly = groupRegisterRows([row({ kind: 'resolution', date: '2026-09-06' })])[0]
    expect(groupDate(resolutionOnly)).toBe('2026-09-06')
  })
})

describe('computeAccuracy', () => {
  it('weighs confirmed as held, partial as half, wrong as not held', () => {
    const groups = groupRegisterRows([
      row({ key: 'A', kind: 'triage', triageVerdict: 'confirmed' }),
      row({ key: 'B', kind: 'triage', triageVerdict: 'partial' }),
      row({ key: 'C', kind: 'triage', triageVerdict: 'wrong' }),
      row({ key: 'D', kind: 'triage', triageVerdict: '' }),
    ])

    const accuracy = computeAccuracy(groups)

    expect(accuracy.reviewed).toBe(3)
    expect(accuracy.held).toBe(1.5)
    expect(accuracy.percent).toBe(50)
  })

  it('returns 0 percent when nothing has been reviewed', () => {
    const groups = groupRegisterRows([row({ key: 'A', kind: 'triage', triageVerdict: '' })])
    expect(computeAccuracy(groups)).toEqual({ held: 0, reviewed: 0, percent: 0 })
  })
})

describe('formatHeld', () => {
  it('drops the trailing .0 for whole numbers but keeps one decimal otherwise', () => {
    expect(formatHeld(3)).toBe('3')
    expect(formatHeld(2.5)).toBe('2.5')
  })
})

describe('sumUsage', () => {
  it('sums cost and turns across every row in every group', () => {
    const groups = groupRegisterRows([
      row({ key: 'A', kind: 'triage', turns: 4, costUsd: 0.5 }),
      row({ key: 'A', kind: 'rca', turns: 6, costUsd: 1.25 }),
      row({ key: 'B', kind: 'triage', turns: 2, costUsd: 0.1 }),
    ])
    expect(sumUsage(groups)).toEqual({ costUsd: 1.85, turns: 12 })
  })
})

describe('toMarkdownTable', () => {
  it('produces the fixed header and blanks unknown columns', () => {
    const groups = groupRegisterRows([
      row({
        key: 'OMNI-1',
        kind: 'triage',
        date: '2026-09-01',
        triageVerdict: 'confirmed',
      }),
    ])

    const table = toMarkdownTable(groups)
    const lines = table.split('\n')

    expect(lines[0]).toBe(
      '| # | Issue | Company | Helpdesk | Tracker | Triage | RCA | Resolution | Status |',
    )
    expect(lines[2]).toBe('| 1 |  |  |  | OMNI-1 | 2026-09-01 |  |  | confirmed |')
  })
})
