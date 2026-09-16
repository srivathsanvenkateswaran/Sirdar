import { describe, expect, it } from 'vitest'
import {
  bytes,
  commandKind,
  editDelta,
  resultSummary,
  shapeOutput,
  shortCommand,
  shortPath,
  testVerdict,
} from './toolOutput'

const RG = [
  'ledger.go:27:\tcase Return:',
  'ledger.go:33:\t\tl.Stock[m.ProductID] += m.Quantity',
  'ledger.go:34:\t\tl.restock(m.ProductID, m.Quantity)',
  'ledger.go:44:func (l *Ledger) restock(productID string, qty int) {',
].join('\n')

const LS = [
  'total 48',
  'drwxr-xr-x@ 10 srivathsanv  staff   320 15 Sep 14:07 .',
  'drwxr-xr-x@ 20 srivathsanv  staff   640 15 Sep 17:40 ..',
  '-rw-r--r--@  1 srivathsanv  staff  1987 15 Sep 14:06 ledger.go',
].join('\n')

const GIT_STAT = [
  'a9b28cd 2026-09-15 14:06:20 +0530 Add sandbox/ledger: inventory stock tracked from movement history',
  '',
  ' go.mod         |  3 +',
  ' ledger.go      | 65 +++++++++++',
  ' movement.go    | 63 +++++++++++',
  '',
  ' 3 files changed, 131 insertions(+)',
].join('\n')

const GO_FAIL = [
  'Exit code 1',
  '--- FAIL: TestApplyMovementReturnAddsQuantityOnce (0.00s)',
  '    ledger_test.go:82: CurrentStock = 12, want 11',
  'FAIL',
  'FAIL\tsandbox/ledger\t1.425s',
  'FAIL',
].join('\n')

const GO_OK = 'ok  \tsandbox/ledger\t2.070s\n'

const READ = ['1\tpackage ledger', '2\t', '3\t// Ledger tracks stock'].join('\n')

describe('commandKind', () => {
  it('reads the family off the first words, skipping a leading cd', () => {
    expect(commandKind('rg -n "Return|restock" --type go')).toBe('rg')
    expect(commandKind('ls -la /repos/app')).toBe('ls')
    expect(commandKind("git log --stat --format='%h %ad %s' --date=iso")).toBe('git-stat')
    expect(commandKind('git -C /repos/app log --stat')).toBe('git-stat')
    expect(commandKind('cd /repos/app && go test ./...')).toBe('test')
    expect(commandKind('go build ./... && go vet ./... && go test ./...')).toBe('test')
    expect(commandKind('go build ./...')).toBe('build')
    expect(commandKind('date -j -f %Y-%m-%d 2026-09-12 +%A')).toBe('other')
  })
})

describe('shortCommand and shortPath', () => {
  it('elides absolute paths and drops the flags that only widen the line', () => {
    expect(shortCommand('ls -la /Users/me/Documents/sirdar-sandbox/app')).toBe('ls -la …/app')
    expect(shortCommand('rg -n "Return|restock" --type go')).toBe('rg -n "Return|restock"')
    expect(shortCommand("git log --stat --format='%h %ad %s' --date=iso")).toBe('git log --stat --format=…')
    expect(shortCommand('go build ./... && go vet ./... && go test ./...')).toBe('go build && go vet && go test')
    expect(shortCommand('x'.repeat(60), 20)).toHaveLength(20)
  })

  it('names a file by its base name, keeping bundle/ and worktree/ as the place it was read', () => {
    expect(shortPath('/repos/app/ledger.go')).toBe('ledger.go')
    expect(shortPath('/repos/app/.sirdar/runs/SBX-1/r1/bundle/ticket.json')).toBe('bundle/ticket.json')
    expect(shortPath('/repos/app/.sirdar/worktrees/r2/ledger_test.go')).toBe('worktree/ledger_test.go')
  })
})

describe('resultSummary', () => {
  it('counts what each command listed', () => {
    expect(resultSummary('Read', '', READ)).toBe('3 lines')
    expect(resultSummary('Bash', 'rg -n "Return|restock" --type go', RG)).toBe('4 matches')
    expect(resultSummary('Bash', 'ls -la /repos/app', LS)).toBe('3 entries')
    expect(resultSummary('Bash', "git log --stat --format='%h' --date=iso", GIT_STAT)).toBe('1 commit · 3 files')
    expect(resultSummary('Bash', 'go test ./...', GO_FAIL)).toBe('FAIL · 1 test')
    expect(resultSummary('Bash', 'go build ./... && go vet ./... && go test ./...', GO_OK)).toBe('ok · 2.070s')
    expect(resultSummary('Bash', 'date +%A', '')).toBe('no output')
    expect(resultSummary('Bash', 'rg nothing', 'rg: No matches found\n[exit status 1]')).toBe('no matches')
  })

  it('is empty before the call has returned', () => {
    expect(resultSummary('Bash', 'go test ./...', undefined)).toBe('')
  })
})

describe('editDelta and testVerdict', () => {
  it('reads an edit as the lines it added and removed', () => {
    expect(editDelta('func Old() {', 'func New() {\n\tl := 1\n}\n\nfunc Old() {')).toBe('+4')
    expect(editDelta('a\nb\nc\nd', 'a\nx')).toBe('−3 +1')
    expect(editDelta(undefined, 'one\ntwo')).toBe('+2')
    expect(editDelta('same', 'same')).toBe('±0')
  })

  it('reads a go test run', () => {
    expect(testVerdict(GO_FAIL)).toEqual({ failed: true, tests: 1, seconds: undefined })
    expect(testVerdict(GO_OK)).toEqual({ failed: false, tests: undefined, seconds: '2.070s' })
  })
})

describe('shapeOutput', () => {
  it('turns rg output into file:line rows, each carrying its reference', () => {
    const shape = shapeOutput('Bash', 'rg -n "Return|restock" --type go', RG)
    expect(shape.kind).toBe('table')
    if (shape.kind !== 'table') return
    expect(shape.columns).toEqual(['file:line', 'match'])
    expect(shape.rows[1]).toEqual({
      cells: ['ledger.go:33', 'l.Stock[m.ProductID] += m.Quantity'],
      ref: 'ledger.go:33',
    })
  })

  it('turns a long listing into name, size and date, dropping the total line', () => {
    const shape = shapeOutput('Bash', 'ls -la /repos/app', LS)
    expect(shape.kind).toBe('table')
    if (shape.kind !== 'table') return
    expect(shape.rows.map((r) => r.cells[0])).toEqual(['.', '..', 'ledger.go'])
    expect(shape.rows[2].cells[1]).toBe('1987')
  })

  it('turns git log --stat into a commit head row and a row per file', () => {
    const shape = shapeOutput('Bash', "git log --stat --format='%h %ad %s' --date=iso", GIT_STAT)
    expect(shape.kind).toBe('table')
    if (shape.kind !== 'table') return
    expect(shape.rows[0].head).toBe(true)
    expect(shape.rows[0].cells[0]).toBe('a9b28cd')
    expect(shape.rows[2]).toEqual({ cells: ['ledger.go', '65', '+++++++++++'] })
    expect(shape.rows[shape.rows.length - 1].cells[0]).toBe('3 files changed, 131 insertions(+)')
  })

  it('keeps a test run as a block with the failing lines in the failed tone', () => {
    const shape = shapeOutput('Bash', 'go test ./...', GO_FAIL)
    expect(shape.kind).toBe('pre')
    if (shape.kind !== 'pre') return
    expect(shape.lines[1].tone).toBe('bad')
    expect(shape.lines[2].tone).toBe('bad')
    const ok = shapeOutput('Bash', 'go test ./...', GO_OK)
    if (ok.kind !== 'pre') throw new Error('expected a block')
    expect(ok.lines[0].tone).toBe('good')
  })

  it('keeps a file read numbered by the numbers the tool printed', () => {
    const shape = shapeOutput('Read', '', READ)
    expect(shape).toEqual({
      kind: 'lines',
      rows: [
        { n: 1, text: 'package ledger' },
        { n: 2, text: '' },
        { n: 3, text: '// Ledger tracks stock' },
      ],
    })
  })

  it('says when there is nothing, and falls back to a block for anything else', () => {
    expect(shapeOutput('Bash', 'date', '  \n')).toEqual({ kind: 'empty' })
    expect(shapeOutput('Bash', 'date', 'Saturday')).toEqual({ kind: 'pre', lines: [{ text: 'Saturday' }] })
    expect(shapeOutput('StructuredOutput', '', 'Structured output provided successfully').kind).toBe('pre')
  })
})

describe('bytes', () => {
  it('reads as the mock does', () => {
    expect(bytes(624)).toBe('624 B')
    expect(bytes(1104)).toBe('1.1 kB')
    expect(bytes(12_600)).toBe('13 kB')
    expect(bytes(1_500_000)).toBe('1.5 MB')
  })
})
