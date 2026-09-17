import { describe, expect, it } from 'vitest'
import type { RegisterRow, RunSummary } from '../api/types'
import {
  buildLedger,
  computeAccuracy,
  formatHeld,
  groupDate,
  groupRegisterRows,
  groupStatus,
  kindsLine,
  ledgerPerDay,
  minutesOf,
  runsThisWeek,
  spendLine,
  spent,
  sumUsage,
  toCSV,
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
    title: '',
    company: '',
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

  it('keeps the newest non-empty title and company for a key', () => {
    const rows = [
      row({ key: 'OMNI-1', kind: 'triage', title: 'Export job times out', company: 'NEQSA SWEET' }),
      row({ key: 'OMNI-1', kind: 'rca', title: '', company: '' }),
      row({ key: 'OMNI-1', kind: 'resolution', title: 'Export job times out on large orders', company: '' }),
    ]
    const [group] = groupRegisterRows(rows)
    expect(group.title).toBe('Export job times out on large orders')
    expect(group.company).toBe('NEQSA SWEET')
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

describe('groupStatus', () => {
  it('is triaged when only a triage row exists', () => {
    const [group] = groupRegisterRows([row({ key: 'OMNI-1', kind: 'triage', notePath: 'triage.md' })])
    expect(groupStatus(group)).toBe('triaged')
  })

  it('is fix-pushed once a fix row landed but no rca or resolution note was written', () => {
    const [group] = groupRegisterRows([
      row({ key: 'OMNI-1', kind: 'triage', notePath: 'triage.md' }),
      row({ key: 'OMNI-1', kind: 'fix', date: '2026-09-02', notePath: '' }),
    ])
    expect(groupStatus(group)).toBe('fix-pushed')
  })

  it('is resolved once an rca or resolution note exists, even without a fix row', () => {
    const rcaOnly = groupRegisterRows([
      row({ key: 'OMNI-1', kind: 'triage', notePath: 'triage.md' }),
      row({ key: 'OMNI-1', kind: 'rca', notePath: 'rca.md' }),
    ])[0]
    expect(groupStatus(rcaOnly)).toBe('resolved')

    const resolutionOnly = groupRegisterRows([
      row({ key: 'OMNI-2', kind: 'triage', notePath: 'triage.md' }),
      row({ key: 'OMNI-2', kind: 'resolution', notePath: 'res.md' }),
    ])[0]
    expect(groupStatus(resolutionOnly)).toBe('resolved')
  })
})

describe('toMarkdownTable', () => {
  it('matches printRegisterMarkdown column for column: key in Issue, blank Helpdesk/Tracker, status in Status', () => {
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
      '| # | Issue | Title | Company | Helpdesk | Tracker | Triage | RCA | Resolution | Status |',
    )
    expect(lines[2]).toBe('| 1 | OMNI-1 |  |  |  |  |  |  |  | triaged |')
  })

  it('fills Title and Company from the group, the two cells a human used to type in', () => {
    const groups = groupRegisterRows([
      row({
        key: 'OMNI-1',
        kind: 'triage',
        date: '2026-09-01',
        triageVerdict: 'confirmed',
        title: 'Export job times out on large orders',
        company: 'NEQSA SWEET',
      }),
    ])

    const table = toMarkdownTable(groups)
    const lines = table.split('\n')

    expect(lines[2]).toBe(
      '| 1 | OMNI-1 | Export job times out on large orders | NEQSA SWEET |  |  |  |  |  | triaged |',
    )
  })

  it('renders the stage cells as wiki links to the notes that were written', () => {
    const groups = groupRegisterRows([
      row({ key: 'OMNI-1', kind: 'triage', notePath: '/vault/notes/OMNI-1-triage.md' }),
      row({ key: 'OMNI-1', kind: 'rca', notePath: '/vault/notes/OMNI-1-rca.md' }),
      row({ key: 'OMNI-1', kind: 'resolution', notePath: '/vault/notes/OMNI-1-res.md' }),
    ])

    const table = toMarkdownTable(groups)
    const lines = table.split('\n')

    expect(lines[2]).toBe(
      '| 1 | OMNI-1 |  |  |  |  | [[OMNI-1-triage]] | [[OMNI-1-rca]] | [[OMNI-1-res]] | resolved |',
    )
  })

  it('escapes a pipe in the key, title or company so it cannot break the table row', () => {
    const groups = groupRegisterRows([
      row({ key: 'OMNI|1', kind: 'triage', title: 'Times out | fails', company: 'A | B' }),
    ])

    const table = toMarkdownTable(groups)
    const lines = table.split('\n')

    expect(lines[2]).toBe('| 1 | OMNI\\|1 | Times out \\| fails | A \\| B |  |  |  |  |  | triaged |')
  })
})

// ---------------------------------------------------------------------------
// The ledger
// ---------------------------------------------------------------------------

function run(overrides: Partial<RunSummary>): RunSummary {
  return {
    runId: 'run-1',
    key: 'OMNI-1',
    kind: 'triage',
    status: 'completed',
    provider: 'claude',
    model: 'sonnet',
    startedAt: '2026-09-14T09:00:00Z',
    updatedAt: '2026-09-14T09:04:30Z',
    reason: '',
    usage: { turns: 4, inputTokens: 0, outputTokens: 0, costUsd: 0.5 },
    notes: [],
    ...overrides,
  }
}

const NOW = Date.parse('2026-09-15T12:00:00Z')

describe('buildLedger', () => {
  it('joins a register row to its run for the state, the minutes and the reason', () => {
    const ledger = buildLedger(
      [row({ key: 'OMNI-1', runId: 'run-1', date: '2026-09-14', triageVerdict: 'confirmed', notePath: 't.md' })],
      [run({ runId: 'run-1' })],
      NOW,
    )
    expect(ledger).toHaveLength(1)
    expect(ledger[0]).toMatchObject({
      key: 'OMNI-1',
      state: 'completed',
      minutes: 5,
      verdict: 'confirmed',
      confidence: 'high',
      notes: { triage: true, rca: false, resolution: false },
      when: '2026-09-14T09:00:00Z',
      day: '2026-09-14',
    })
  })

  it('lists a run the register has no line for, which is how a failed run is seen', () => {
    const ledger = buildLedger(
      [row({ key: 'OMNI-1', runId: 'run-1', date: '2026-09-14' })],
      [
        run({ runId: 'run-1' }),
        run({
          runId: 'run-2',
          key: 'OMNI-2',
          status: 'failed',
          reason: 'provider refused the prompt',
          startedAt: '2026-09-15T08:00:00Z',
          updatedAt: '2026-09-15T08:01:00Z',
          usage: { turns: 0, inputTokens: 0, outputTokens: 0, costUsd: 0 },
        }),
      ],
      NOW,
    )
    expect(ledger.map((r) => r.runId)).toEqual(['run-2', 'run-1'])
    expect(ledger[0]).toMatchObject({
      state: 'failed',
      reason: 'provider refused the prompt',
      turns: 0,
      costUsd: 0,
      minutes: 1,
      verdict: '',
    })
  })

  it('treats a register row whose run is gone as a completed run on the register date', () => {
    const ledger = buildLedger([row({ runId: 'old', date: '2026-08-01' })], [], NOW)
    expect(ledger[0]).toMatchObject({ state: 'completed', minutes: null, when: '2026-08-01', day: '2026-08-01' })
  })

  it('times a live run against now rather than its last change', () => {
    const live = run({ status: 'running', startedAt: '2026-09-15T11:30:00Z', updatedAt: '2026-09-15T11:31:00Z' })
    expect(minutesOf(live, NOW)).toBe(30)
  })

  it('carries a key’s triage facts onto its other runs', () => {
    const ledger = buildLedger(
      [
        row({ key: 'OMNI-1', kind: 'triage', runId: 'r1', date: '2026-09-10', confidence: 'medium', triageVerdict: 'partial', notePath: 't.md' }),
        row({ key: 'OMNI-1', kind: 'rca', runId: 'r2', date: '2026-09-11', confidence: '', notePath: 'r.md' }),
      ],
      [],
      NOW,
    )
    const rca = ledger.find((r) => r.kind === 'rca')
    expect(rca).toMatchObject({ confidence: 'medium', verdict: 'partial', notes: { triage: true, rca: true, resolution: false } })
  })
})

describe('the three figures', () => {
  const ledger = buildLedger(
    [
      row({ key: 'A', kind: 'triage', runId: 'a', date: '2026-09-15', provider: 'claude', costUsd: 1.0, triageVerdict: 'confirmed' }),
      row({ key: 'B', kind: 'triage', runId: 'b', date: '2026-09-13', provider: 'codex', costUsd: 0.25, triageVerdict: 'wrong' }),
      row({ key: 'A', kind: 'fix', runId: 'c', date: '2026-09-12', provider: 'claude', costUsd: 2.0 }),
      row({ key: 'C', kind: 'triage', runId: 'd', date: '2026-09-01', provider: 'qwen', costUsd: 0.1, triageVerdict: 'partial' }),
      row({ key: 'D', kind: 'rca', runId: 'e', date: '2026-09-09', provider: 'kimi', costUsd: 0.05 }),
    ],
    [],
    NOW,
  )

  it('counts the seven days ending today, by kind', () => {
    const week = runsThisWeek(ledger, NOW)
    expect(week.total).toBe(4)
    expect(kindsLine(week.kinds)).toBe('2 triages · 1 fix · 1 RCA')
  })

  it('sums what was spent and names the two providers that took most of it', () => {
    const s = spent(ledger)
    expect(s.total).toBeCloseTo(3.4)
    expect(spendLine(s.providers, (n) => `$${n.toFixed(2)}`)).toBe('claude $3.00 · codex $0.25 · others $0.15')
  })

  it('names a lone third provider rather than calling it others', () => {
    expect(
      spendLine(
        [
          { provider: 'claude', costUsd: 3 },
          { provider: 'codex', costUsd: 1 },
          { provider: 'qwen', costUsd: 0.5 },
        ],
        (n) => `$${n.toFixed(2)}`,
      ),
    ).toBe('claude $3.00 · codex $1.00 · qwen $0.50')
  })

  it('buckets runs by the day they happened, for the grid', () => {
    expect(ledgerPerDay(ledger)).toEqual([
      { date: '2026-09-01', count: 1 },
      { date: '2026-09-09', count: 1 },
      { date: '2026-09-12', count: 1 },
      { date: '2026-09-13', count: 1 },
      { date: '2026-09-15', count: 1 },
    ])
  })
})

describe('toCSV', () => {
  it('writes a header and one line per run with CRLF ends', () => {
    const ledger = buildLedger(
      [row({ key: 'OMNI-1', runId: 'run-1', date: '2026-09-14', triageVerdict: 'confirmed', notePath: 't.md' })],
      [run({ runId: 'run-1' })],
      NOW,
    )
    const csv = toCSV(ledger)
    const lines = csv.split('\r\n')
    expect(lines[0]).toBe(
      'key,kind,state,provider,model,turns,cost_usd,minutes,confidence,verdict,triage_note,rca_note,resolution_note,when,reason',
    )
    expect(lines[1]).toBe(
      'OMNI-1,triage,completed,claude,sonnet,4,0.50,5,high,confirmed,yes,no,no,2026-09-14T09:00:00Z,',
    )
    expect(csv.endsWith('\r\n')).toBe(true)
  })

  it('quotes a field carrying a comma, a quote or a line break', () => {
    const ledger = buildLedger(
      [],
      [run({ runId: 'x', status: 'failed', reason: 'said "no", then\nstopped' })],
      NOW,
    )
    const line = toCSV(ledger).split('\r\n')[1]
    expect(line.endsWith(',"said ""no"", then\nstopped"')).toBe(true)
  })
})
