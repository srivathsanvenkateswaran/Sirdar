import { describe, expect, it } from 'vitest'
import type { IndexedEvent } from '../../lib/events'
import { FIX_DETAIL, TRIAGE_DETAIL, fixEvents, triageEvents } from './fixtures'
import { buildSessionModel, callForRef, relativeTo, shortenPaths } from './model'
import { clock, exitCodeOf, formatBytes, formatMs, shapeOutput, sizeOf } from './shape'

function indexed(events: ReturnType<typeof triageEvents>): IndexedEvent[] {
  return events.map((event, i) => ({ index: i + 1, event }))
}

describe('the session model', () => {
  const triage = buildSessionModel(indexed(triageEvents()), TRIAGE_DETAIL)

  it('opens with the run started line read off the init event', () => {
    const first = triage.items[0]
    expect(first.kind).toBe('sys')
    const text = first.kind === 'sys' ? first.parts.map((p) => (typeof p === 'string' ? p : p.b)).join('') : ''
    expect(text).toBe('Run started 00:01 · claude · claude-opus-5 · triage · read-only · budget 60 turns / 20 min / $5')
    expect(triage.start?.model).toBe('claude-opus-5')
    expect(triage.root).toBe('/Users/srivathsanv/Documents/Personal/sirdar-sandbox/app')
  })

  it('stacks consecutive calls and heads a long stack with its span', () => {
    const stacks = triage.items.filter((i) => i.kind === 'stack')
    expect(stacks[0].kind === 'stack' && stacks[0].calls).toHaveLength(8)
    expect(stacks[0].kind === 'stack' && stacks[0].head).toBe('8 calls · 00:03 – 00:10 · all within policy')
    // Two denials and two more calls: no "all within policy".
    expect(stacks[1].kind === 'stack' && stacks[1].calls).toHaveLength(4)
    expect(stacks[1].kind === 'stack' && stacks[1].head).toBe('4 calls · 00:19 – 00:23')
    // The one search after the steer stands alone, without a head.
    expect(stacks[2].kind === 'stack' && stacks[2].calls).toHaveLength(1)
    expect(stacks[2].kind === 'stack' && stacks[2].head).toBeUndefined()
  })

  it('numbers every call across the run and leaves the StructuredOutput calls out', () => {
    expect(triage.calls).toHaveLength(13)
    expect(triage.calls.map((c) => c.n)).toEqual([1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13])
    expect(triage.calls.some((c) => /structured/i.test(c.tool))).toBe(false)
  })

  it('reads a call: description, the input in one line relative to the root, the size and the time it took', () => {
    const ls = triage.calls[0]
    expect(ls.tool).toBe('Bash')
    expect(ls.description).toBe('List repository root files')
    expect(ls.summary).toBe('ls -la …/sirdar-sandbox/app')
    expect(ls.decision).toBe('policy')
    expect(ls.size.lines).toBe(4)
    expect(ls.tookMs).toBe(0)

    const read = triage.calls[2]
    expect(read.tool).toBe('Read')
    expect(read.summary).toBe('ledger.go')
    expect(read.size.lines).toBe(7)
    expect(read.at).toBe('00:06')

    const bundle = triage.calls[7]
    expect(bundle.summary).toBe('.sirdar/runs/SBX-1/20260915T121105Z-076d/bundle/ticket.json')
  })

  it('stamps a denied call with the policy reason, without the Sirdar prefix', () => {
    const denied = triage.calls.filter((c) => c.decision === 'denied')
    expect(denied).toHaveLength(2)
    expect(denied[0].summary).toBe("git -C …/sirdar-sandbox/app log --stat --format='%h %ad %s' --date=iso")
    expect(denied[0].reason).toMatch(/^not permitted by permissions\.bash/)
    expect(denied[0].failed).toBe(false)
    expect(triage.denied).toBe(2)
  })

  it('turns a thinking burst into one stamp with its tokens and its clock', () => {
    const thinks = triage.items.filter((i) => i.kind === 'think')
    expect(thinks).toHaveLength(3)
    expect(thinks[0]).toMatchObject({ kind: 'think', tokens: 440, at: '00:19', seconds: 8 })
    expect(thinks[1]).toMatchObject({ kind: 'think', tokens: 1100, at: '00:41' })
    expect(thinks[2]).toMatchObject({ kind: 'think', tokens: 300, at: '02:10' })
  })

  it('draws the StructuredOutput call as a Wrote-the-answer stamp, then the answer', () => {
    const wrote = triage.items.filter((i) => i.kind === 'wrote')
    expect(wrote).toHaveLength(2)
    expect(wrote[1]).toMatchObject({ kind: 'wrote', what: 'Wrote the answer', from: '02:10', to: '03:13', seconds: 63 })
    const answers = triage.items.filter((i) => i.kind === 'answer')
    expect(answers).toHaveLength(2)
    expect(answers[0]).toMatchObject({ superseded: true, revised: false, at: '01:41' })
    expect(answers[1]).toMatchObject({ superseded: false, revised: true, at: '03:13', seconds: 63 })
    expect(triage.answerIndex).toBe(answers[1].index)
    expect(triage.answer?.title).toMatch(/^Recording a customer return/)
  })

  it('puts the steer between the two answers as the operator’s words', () => {
    const kinds = triage.items.map((i) => i.kind)
    const you = kinds.indexOf('you')
    expect(you).toBeGreaterThan(kinds.indexOf('answer'))
    expect(you).toBeLessThan(kinds.lastIndexOf('answer'))
    expect(triage.items[you]).toMatchObject({ kind: 'you', at: '02:02', continuation: 'resume' })
  })

  it('keeps the provider’s bookkeeping out of the flow', () => {
    expect(triage.items.some((i) => i.kind === 'sys' && i.parts.join('').includes('stream_event'))).toBe(false)
    expect(triage.items.filter((i) => i.kind === 'sys')).toHaveLength(1)
  })

  it('finds the call a file:line reference points at', () => {
    expect(callForRef(triage.calls, 'ledger.go:33')?.n).toBe(3)
    expect(callForRef(triage.calls, 'movement.go:51-62')?.n).toBe(4)
    expect(callForRef(triage.calls, 'nowhere.go:1')).toBeUndefined()
  })

  describe('the fix run', () => {
    it('leaves the unanswered go test call pending while the run is blocked', () => {
      const blocked = buildSessionModel(indexed(fixEvents('blocked')), { ...FIX_DETAIL, status: 'blocked', reason: 'agent asked: Sirdar wants to run a command that requires approval.' })
      expect(blocked.pendingCall?.summary).toBe('go test ./...')
      expect(blocked.pendingCall?.description).toBe('Run tests before the fix')
      expect(blocked.pendingCall?.pending).toBe(true)
      const edit = blocked.calls[2]
      expect(edit).toMatchObject({ tool: 'Edit', decision: 'accepted', reason: 'acceptEdits', summary: 'ledger_test.go' })
      expect(edit.edit).toEqual({ file: 'ledger_test.go', added: 14, removed: 0 })
    })

    it('reads the report, the failing test and the checks once it is done', () => {
      const done = buildSessionModel(indexed(fixEvents('done')), FIX_DETAIL)
      expect(done.pendingCall).toBeUndefined()
      expect(done.report?.filesChanged).toEqual(['ledger.go', 'ledger_test.go'])
      const test = done.calls.find((c) => c.summary === 'go test ./...')!
      expect(test).toMatchObject({ decision: 'approved', suggestedRule: 'go test *', failed: true, exitCode: 1 })
      expect(test.tookMs).toBe(3000)
      const after = done.calls.find((c) => c.summary.startsWith('go build'))!
      expect(after).toMatchObject({ decision: 'approved', failed: false })
      expect(done.checks.map((c) => c.outcome)).toEqual(['failed', 'ok', 'ok', 'ok'])
      expect(done.items.filter((i) => i.kind === 'wrote')[0]).toMatchObject({ what: 'Wrote the report' })
      expect(done.asked).toBe(4)
    })
  })

  it('is empty for nothing', () => {
    expect(buildSessionModel([], null).items).toEqual([])
  })
})

describe('paths', () => {
  it('shows a path under the root, or beside it, relative to it', () => {
    expect(relativeTo('/r/app/ledger.go', '/r/app')).toBe('ledger.go')
    expect(relativeTo('/r/app/.sirdar/worktrees/x/ledger.go', '/r/app')).toBe('.sirdar/worktrees/x/ledger.go')
    expect(relativeTo('/r/other/x.go', '/r/app')).toBe('other/x.go')
    expect(relativeTo('/elsewhere/x.go', '/r/app')).toBe('/elsewhere/x.go')
    expect(relativeTo('/r/app', '')).toBe('/r/app')
  })

  it('folds the workspace’s parent to an ellipsis inside a command', () => {
    expect(shortenPaths('ls -la /Users/me/Personal/sirdar-sandbox/app', '/Users/me/Personal/sirdar-sandbox/app')).toBe('ls -la …/sirdar-sandbox/app')
    expect(shortenPaths('ls -la /r/app', '/r/app')).toBe('ls -la /r/app')
    expect(shortenPaths('rg -n x', '/r/app')).toBe('rg -n x')
  })
})

describe('shapes', () => {
  it('reads an rg result as file · line · match rows', () => {
    const shaped = shapeOutput('Bash', 'rg -n "Return|restock" --type go', 'ledger.go:27:\tcase Return:\nledger.go:34:\t\tl.restock(m.ProductID, m.Quantity)\n')
    expect(shaped.kind).toBe('matches')
    if (shaped.kind === 'matches') {
      expect(shaped.rows).toHaveLength(2)
      expect(shaped.rows[1]).toEqual({ file: 'ledger.go', line: 34, text: '\t\tl.restock(m.ProductID, m.Quantity)' })
      expect(shaped.files).toBe(1)
    }
  })

  it('reads a file read as numbered lines', () => {
    const shaped = shapeOutput('Read', '', '1\tpackage ledger\n2\t\n3\ttype Ledger struct {')
    expect(shaped.kind).toBe('lines')
    if (shaped.kind === 'lines') expect(shaped.rows[2]).toEqual({ n: 3, text: 'type Ledger struct {' })
  })

  it('colours a test run’s verdict lines', () => {
    const shaped = shapeOutput('Bash', 'go test ./...', 'Exit code 1\n--- FAIL: TestX (0.00s)\n    ledger_test.go:82: CurrentStock = 12, want 11\nFAIL\nok  \tsandbox/ledger\t2.070s')
    expect(shaped.kind).toBe('test')
    if (shaped.kind === 'test') {
      expect(shaped.lines.map((l) => l.tone)).toEqual(['fail', 'fail', 'fail', 'fail', 'ok'])
    }
  })

  it('leaves anything else as text', () => {
    expect(shapeOutput('Bash', 'ls -la', 'total 48\n.').kind).toBe('text')
    expect(shapeOutput('Bash', 'rg x', 'nothing matched').kind).toBe('text')
  })

  it('sizes and formats', () => {
    expect(sizeOf('a\nb\n')).toEqual({ bytes: 4, lines: 2 })
    expect(sizeOf('')).toEqual({ bytes: 0, lines: 0 })
    expect(formatBytes(624)).toBe('624 B')
    expect(formatBytes(1100)).toBe('1.1 kB')
    expect(formatBytes(13_600)).toBe('13.6 kB')
    expect(formatMs(82)).toBe('82 ms')
    expect(formatMs(2600)).toBe('2.6 s')
    expect(formatMs(64_000)).toBe('1:04')
    expect(clock('2026-09-15T12:13:11Z', '2026-09-15T12:11:05Z')).toBe('02:06')
    expect(clock(undefined, '2026-09-15T12:11:05Z')).toBe('')
    expect(exitCodeOf('Exit code 1\nFAIL')).toBe(1)
    expect(exitCodeOf('ok')).toBeUndefined()
  })
})
