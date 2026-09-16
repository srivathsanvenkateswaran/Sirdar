import { describe, expect, it } from 'vitest'
import { buildSessionModel } from '../model'
import { translations } from './bundle'
import {
  BLOCKED_FIX_RUN,
  FIX_RUN,
  TRIAGE_ANSWER,
  TRIAGE_RUN,
  fixEvents,
  indexed,
  triageEvents,
} from './fixtures'
import {
  buildRows,
  closingRow,
  consoleCounts,
  countsLabel,
  gauges,
  isRailCell,
  matchesConsoleFilter,
  offsetTenths,
  patternMatches,
  pendingQuestion,
  railItems,
  ruleFor,
  sizeLabel,
} from './model'
import { shapeOutput, tokenizeJSON } from './shapes'

const PERMS = { bash: ['git log*', 'git show*', 'rg *', 'ls *'], fixBash: ['git status*', 'go test*', 'go build*'] }

describe('the console rows', () => {
  const events = indexed(triageEvents())
  const calls = buildSessionModel(events, TRIAGE_RUN).calls
  const rows = buildRows(events, { startedAt: TRIAGE_RUN.startedAt, kind: 'triage', permissions: PERMS, calls })

  it('folds the system lines into one session row and leaves the deltas out', () => {
    expect(rows[0]).toMatchObject({ kind: 'sys', tool: 'session' })
    expect(rows[0].summary).toContain('init · cwd ~/Documents/Personal/sirdar-sandbox/app')
    expect(rows[0].summary).toContain('5 hooks SessionStart:startup')
    expect(rows.filter((r) => r.tool === 'session')).toHaveLength(2)
    expect(rows.find((r) => r.tool === 'session' && r.summary.startsWith('resume'))?.summary).toContain('4 hooks')
  })

  it('draws a thinking row for a message that only thought', () => {
    const think = rows.filter((r) => r.kind === 'think')
    expect(think.length).toBeGreaterThanOrEqual(3)
    expect(think[0].summary).toBe('thinking · content not returned by the provider')
  })

  it('pairs a call with its result and prints the offset, the description, the duration and the size', () => {
    const ls = rows.find((r) => r.tool === 'Bash' && r.summary.startsWith('ls -la'))!
    expect(ls.at).toBe('00:04.7')
    expect(ls.description).toBe('List repository root files')
    expect(ls.duration).toBe('68 ms')
    expect(ls.output).toBe('3 ln · 87 B')
    expect(ls.decision).toBe('allow')
    expect(ls.rule).toBe('ls *')
  })

  it('names the rule that let a read-only tool or a shell command through', () => {
    const read = rows.find((r) => r.tool === 'Read')!
    expect(read.rule).toBe('read-only tool')
    expect(read.summary).toBe('/repo/ledger.go')
    const rg = rows.find((r) => r.summary.startsWith('rg -n "Return'))!
    expect(rg.rule).toBe('rg *')
  })

  it('marks a denied call with the allow-list it failed and keeps the reason', () => {
    const denied = rows.find((r) => r.kind === 'deny')!
    expect(denied.decision).toBe('deny')
    expect(denied.rule).toBe('not in permissions.bash')
    expect(denied.reason).toContain('passes git "-C"')
    expect(denied.summary).toContain('git -C /repo log')
  })

  it('shows the structured answer as a final row with its title and the result as figures', () => {
    const finals = rows.filter((r) => r.kind === 'final')
    expect(finals).toHaveLength(2)
    expect(finals[0].summary).toBe(TRIAGE_ANSWER.title)
    expect(finals[0].output).toMatch(/ch$/)
    expect(finals[0].rule).toBe('schema-checked')
    const result = rows.find((r) => r.tool === 'result')!
    expect(result.summary).toBe('turns 8 · $0.72 · api 101.7 s · 44,160 cache write · 157,945 cache read · 7,966 out')
  })

  it('draws the steer as a you row carrying how the run continued', () => {
    const steer = rows.find((r) => r.kind === 'steer')!
    expect(steer.tool).toBe('you')
    expect(steer.summary).toMatch(/^Re-check/)
    expect(steer.output).toBe('resume')
  })

  it('counts what the header prints', () => {
    const counts = consoleCounts(rows)
    expect(counts).toMatchObject({ calls: 9, denied: 1, steers: 1 })
    expect(countsLabel(counts)).toBe(`${rows.length} events · 9 calls · 1 denied · 1 steer`)
  })

  it('filters to the calls, the denials or the prose', () => {
    expect(rows.filter((r) => matchesConsoleFilter(r, 'tools')).every((r) => r.call)).toBe(true)
    expect(rows.filter((r) => matchesConsoleFilter(r, 'denied'))).toHaveLength(1)
    const prose = rows.filter((r) => matchesConsoleFilter(r, 'prose'))
    expect(prose.some((r) => r.kind === 'steer')).toBe(true)
    expect(prose.some((r) => r.kind === 'final')).toBe(true)
    expect(prose.some((r) => r.call && r.kind === 'tool')).toBe(false)
  })

  it('closes a completed triage with the note it filed', () => {
    const row = closingRow(TRIAGE_RUN, '/Users/me/Documents/Personal/sirdar-sandbox/notes')!
    expect(row.kind).toBe('done')
    expect(row.summary).toBe(
      'note written → SBX-1 recording-a-customer-return-adds-its-quantity-to-stock-twice.md · 17 turns · $1.02',
    )
  })

  it('closes a completed fix with its branch and commit', () => {
    const row = closingRow(FIX_RUN)!
    expect(row.summary).toBe('branch fix-sbx-1-recording-a-customer-return-adds-its-qua · commit f144936 · local, not pushed')
    expect(closingRow(BLOCKED_FIX_RUN)).toBeUndefined()
  })

  it('reads a fix run: writes take the write kind, a failing test says so in its summary', () => {
    const fixed = indexed(fixEvents())
    const fix = buildRows(fixed, { startedAt: FIX_RUN.startedAt, kind: 'fix', permissions: PERMS, calls: buildSessionModel(fixed, FIX_RUN).calls })
    const edit = fix.find((r) => r.kind === 'write')!
    expect(edit.tool).toBe('Edit')
    // The file, and the lines the replacement adds: five for one.
    expect(edit.summary).toBe('ledger_test.go · +4')
    expect(edit.rule).toBe('fix worktree')
    const test = fix.find((r) => r.test)!
    expect(test.summary).toBe('go test ./...  →  exit 1 · --- FAIL: TestApplyMovementReturnAddsQuantityOnce (0.00s)')
    expect(test.rule).toBe('go test*')
    expect(test.duration).toBe('1,995 ms')
    const review = fix.find((r) => r.kind === 'review')!
    expect(review.summary).toBe('dropped ledger_test.go · hunk 1 in review')
  })
})

describe('the rail', () => {
  it('numbers the turns as the provider counted them and breaks at the steer', () => {
    const { items, turnOf } = railItems(indexed(triageEvents()))
    const labels = items.map((i) => (isRailCell(i) ? i.n : `—${i.sep}`))
    expect(labels).toEqual(['1', '2', '3', '4', '5', '6', '7', '—steer', '1', '2'])
    const cells = items.filter(isRailCell)
    expect(cells[0].kind).toBe('tool')
    expect(cells[3].kind).toBe('deny')
    expect(cells[6].kind).toBe('final')
    // Turn 3 was numbered twice — the Read, then a message that only thought — and is one cell.
    expect(cells[2].kind).toBe('tool')
    // Every counted event lands in a cell.
    expect(turnOf.size).toBeGreaterThan(20)
  })

  it('ends a blocked run on a question cell and a reviewed fix on a you cell', () => {
    const blocked = railItems(indexed(fixEvents({ untilBlocked: true })), 'blocked').items
    const last = blocked[blocked.length - 1]
    expect(isRailCell(last) && last.n === '?' && last.kind === 'ask').toBe(true)

    const reviewed = railItems(indexed(fixEvents()), 'completed').items
    const tail = reviewed.slice(-2)
    expect(tail[0]).toMatchObject({ sep: 'review' })
    expect(isRailCell(tail[1]) && tail[1].n === 'you').toBe(true)
    const writes = reviewed.filter((i) => isRailCell(i) && i.kind === 'write')
    expect(writes.length).toBeGreaterThanOrEqual(2)
  })
})

describe('the gauges', () => {
  it('reads turns, minutes and cost against the caps', () => {
    const g = gauges(TRIAGE_RUN)
    expect(g[0]).toMatchObject({ label: 'turns', value: '17', cap: '/ 60', pct: 28 })
    expect(g[1]).toMatchObject({ label: 'minutes', value: '3:15', cap: '/ 20:00', pct: 16 })
    expect(g[2]).toMatchObject({ label: 'cost', value: '$1.02', cap: '/ $5.00', pct: 20 })
  })

  it('says the cost is not known yet while the run is blocked or running', () => {
    const g = gauges(BLOCKED_FIX_RUN, Date.parse(BLOCKED_FIX_RUN.updatedAt))
    expect(g[2]).toMatchObject({ label: 'cost · at result', value: '—', pct: 0, na: true })
    expect(g[0].value).toBe('4')
  })
})

describe('the pending question', () => {
  it('lifts the numbered options out of the reason', () => {
    const q = pendingQuestion(BLOCKED_FIX_RUN, [])!
    expect(q.text).toBe('The smallest change is to delete one of the two increments in the Return case. Which should I keep?')
    expect(q.options).toEqual([
      'remove line 33 and keep restock',
      'remove the restock call at line 34 and the restock helper',
      'fold every movement type with l.Stock[m.ProductID] += m.Delta()',
    ])
    expect(pendingQuestion(TRIAGE_RUN, [])).toBeUndefined()
  })
})

describe('the small helpers', () => {
  it('formats offsets to tenths and sizes to lines and bytes', () => {
    expect(offsetTenths('2026-09-15T12:11:46.884Z', '2026-09-15T12:11:05.184Z')).toBe('00:41.7')
    expect(offsetTenths('2026-09-15T12:14:19.732Z', '2026-09-15T12:11:05.184Z')).toBe('03:14.5')
    expect(sizeLabel('a\nb\nc')).toBe('3 ln · 5 B')
    expect(sizeLabel('x'.repeat(1104))).toBe('1 ln · 1.1 KB')
  })

  it('matches allow-list patterns with their stars', () => {
    expect(patternMatches('git log*', "git log --stat --format='%h'")).toBe(true)
    expect(patternMatches('rg *', 'rg -n foo')).toBe(true)
    expect(patternMatches('rg *', 'rgx')).toBe(false)
    expect(ruleFor('Bash', 'go test ./... && go vet ./...', '', '', 'fix', PERMS)).toBe('go test*')
    expect(ruleFor('Bash', 'date +%A', '', '', 'triage', PERMS)).toBe('')
  })
})

describe('the output shapes', () => {
  it('reads rg output as file:line:match', () => {
    const shape = shapeOutput('Bash', 'rg -n foo', 'a.go:3:\tfoo\nb.go:12:\tbar\n')
    expect(shape.kind).toBe('grep')
    if (shape.kind === 'grep') expect(shape.rows[1]).toEqual({ file: 'b.go', line: '12', text: '\tbar' })
  })

  it('reads numbered lines, JSON, tables and test output', () => {
    expect(shapeOutput('Read', '', '     1\tpackage x\n     2\t\n     3\tfunc f() {}').kind).toBe('lines')
    expect(shapeOutput('mcp__zoho__get_ticket', '', '{"a": 1}').kind).toBe('json')
    expect(shapeOutput('Bash', 'psql -c "select 1"', 'a | b\n1 | 2\n3 | 4').kind).toBe('table')
    const test = shapeOutput('Bash', 'go test ./...', '--- FAIL: TestX (0.00s)\n    x_test.go:8: boom\nok')
    expect(test.kind).toBe('test')
    if (test.kind === 'test') expect(test.lines.map((l) => l.failed)).toEqual([true, true, false])
  })

  it('colours JSON keys and strings apart', () => {
    const tokens = tokenizeJSON('{\n  "command": "rg -n",\n  "n": 3\n}')
    expect(tokens.filter((t) => t.kind === 'key').map((t) => t.text)).toEqual(['"command"', '"n"'])
    expect(tokens.filter((t) => t.kind === 'string').map((t) => t.text)).toEqual(['"rg -n"'])
    expect(tokens.map((t) => t.text).join('')).toBe('{\n  "command": "rg -n",\n  "n": 3\n}')
  })
})

describe('the bundle read off the prompt', () => {
  it('lifts the translated quotes out of the complaint in order', () => {
    expect(translations(TRIAGE_ANSWER.complaint)).toHaveLength(2)
    expect(translations(TRIAGE_ANSWER.complaint)[1]).toMatch(/^The return was last Tuesday/)
  })
})
