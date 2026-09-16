import { describe, expect, it } from 'vitest'
import {
  deriveChangeMarkers,
  deriveEvidenceMarkers,
  fileTokens,
  markersForFile,
  markersForRef,
  markersForStep,
  parseRefs,
  type StepLike,
} from './evidence'

/** The SBX-1 triage run's calls, as the path lists them. */
const STEPS: StepLike[] = [
  { index: 1, tool: 'Bash', command: 'ls -la /repos/app' },
  { index: 2, tool: 'Read', path: '/repos/app/ledger.go' },
  { index: 3, tool: 'Read', path: '/repos/app/movement.go' },
  { index: 4, tool: 'Read', path: '/repos/app/ledger_test.go' },
  { index: 5, tool: 'Read', path: '/repos/app/.sirdar/runs/SBX-1/r1/bundle/ticket.json' },
  { index: 6, tool: 'Bash', command: "git -C /repos/app log --stat --format='%h %ad %s' --date=iso" },
  { index: 7, tool: 'Bash', command: "git log --stat --format='%h %ad %s' --date=iso" },
  { index: 8, tool: 'Bash', command: 'rg -n "Return|restock" --type go' },
  { index: 9, tool: 'Bash', command: 'rg -n -i "partial|Quantity" --type go' },
]

const ITEMS = [
  {
    source: 'code',
    query: 'Read ledger.go',
    finding: 'ledger.go:27-34: `case Return:` runs the increment (line 33) and restock (line 34). ledger.go:44-46: restock adds again.',
  },
  {
    source: 'code',
    query: 'Read ledger.go lines 22-37 against movement.go:51-62, case by case',
    finding: 'Purchase +Quantity (ledger.go:24) = Delta. Return +2×Quantity (ledger.go:33-34, 45) ≠ Delta.',
  },
  {
    source: 'code',
    query: 'rg -n "Return|restock" --type go',
    finding: 'restock is defined at ledger.go:44 and its only caller is ledger.go:34.',
  },
  {
    source: 'git',
    query: "git log --stat --format='%h %ad %s' --date=iso",
    finding: 'The repository has one commit: a9b28cd at 2026-09-15 14:06:20 +0530.',
  },
  {
    source: 'helpdesk thread',
    query: 'ticket.json Thread[0], Thread[2]',
    finding: 'The customer says stock matched the report before the return.',
  },
  { source: 'apm', query: 'traces for /returns', finding: 'No APM source is configured for this run.' },
]

describe('parseRefs', () => {
  it('reads file:line and file:from-to, and drops the directories', () => {
    expect(parseRefs('once at ledger.go:33 and again at internal/x/ledger.go:27-34.')).toEqual([
      { file: 'ledger.go', from: 33, to: undefined, text: 'ledger.go:33' },
      { file: 'ledger.go', from: 27, to: 34, text: 'internal/x/ledger.go:27-34' },
    ])
  })

  it('never reads a clock, an address or a URL as a file', () => {
    expect(parseRefs('at 2026-09-12 09:14:00 on 127.0.0.1:47349 via https://sandbox.local/desk/88341')).toEqual([])
    expect(parseRefs('ledger_test.go:82: CurrentStock = 12')).toEqual([
      { file: 'ledger_test.go', from: 82, to: undefined, text: 'ledger_test.go:82' },
    ])
  })

  it('finds the files a query names', () => {
    expect(fileTokens('ticket.json Thread[0], Thread[2]')).toEqual(['ticket.json'])
    expect(fileTokens('Read ledger.go lines 22-37 against movement.go:51-62')).toEqual(['ledger.go', 'movement.go'])
    expect(fileTokens('bundle/thread.md and go test')).toEqual(['thread.md'])
  })
})

describe('deriveEvidenceMarkers', () => {
  const markers = deriveEvidenceMarkers(ITEMS, STEPS)

  it('numbers the items E1…En in order', () => {
    expect(markers.map((m) => m.id)).toEqual(['E1', 'E2', 'E3', 'E4', 'E5', 'E6'])
    expect(markers.every((m) => m.kind === 'E')).toBe(true)
  })

  it('pairs "Read X" with the read of X, and only the first file named', () => {
    expect(markers[0].steps).toEqual([2])
    expect(markers[1].steps).toEqual([2])
    expect(markers[0].refs.map((r) => r.text)).toEqual(['ledger.go:27-34', 'ledger.go:44-46'])
  })

  it('pairs a quoted command with the call that ran it, and not with the denied near-miss', () => {
    expect(markers[2].steps).toEqual([8])
    expect(markers[3].steps).toEqual([7])
  })

  it('pairs a file named in the query with the read of that file', () => {
    expect(markers[4].steps).toEqual([5])
    expect(markers[4].files).toEqual(['ticket.json'])
  })

  it('leaves a marker with no twin rather than guessing', () => {
    expect(markers[5].steps).toEqual([])
  })

  it('answers which markers sit on a step and on a file', () => {
    expect(markersForStep(2, markers).map((m) => m.id)).toEqual(['E1', 'E2'])
    expect(markersForStep(1, markers)).toEqual([])
    expect(markersForFile('/repos/app/ledger.go', markers).map((m) => m.id)).toEqual(['E1', 'E2', 'E3'])
  })

  it('answers which markers a reference in prose belongs to, by overlapping lines, two at most', () => {
    expect(markersForRef(parseRefs('ledger.go:33')[0], markers).map((m) => m.id)).toEqual(['E1', 'E2'])
    expect(markersForRef(parseRefs('ledger.go:44')[0], markers).map((m) => m.id)).toEqual(['E1', 'E3'])
    expect(markersForRef(parseRefs('ledger.go:10')[0], markers)).toEqual([])
    expect(markersForRef(parseRefs('movement.go:51')[0], markers)).toEqual([])
  })
})

describe('deriveChangeMarkers', () => {
  it('numbers hunks in the order the files were edited, and puts every marker of a file on its edits', () => {
    const markers = deriveChangeMarkers(
      [
        { path: 'ledger.go', hunks: 2 },
        { path: 'ledger_test.go', hunks: 1 },
      ],
      [
        { index: 11, path: '/wt/ledger_test.go' },
        { index: 14, path: '/wt/ledger.go' },
      ],
    )
    expect(markers.map((m) => [m.id, m.path, m.hunk, m.steps])).toEqual([
      ['C1', 'ledger_test.go', 0, [11]],
      ['C2', 'ledger.go', 0, [14]],
      ['C3', 'ledger.go', 1, [14]],
    ])
    expect(markersForStep(14, markers).map((m) => m.id)).toEqual(['C2', 'C3'])
  })

  it('puts a file the log never edited last, with no step', () => {
    const markers = deriveChangeMarkers(
      [
        { path: 'a.go', hunks: 1 },
        { path: 'b.go', hunks: 1 },
      ],
      [{ index: 3, path: 'b.go' }],
    )
    expect(markers.map((m) => [m.id, m.path, m.steps])).toEqual([
      ['C1', 'b.go', [3]],
      ['C2', 'a.go', []],
    ])
  })
})
