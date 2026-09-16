import { describe, expect, it } from 'vitest'
import type { IndexedEvent } from '../../lib/events'
import {
  blockedFixture,
  fixDiff,
  fixFixture,
  triageFixture,
  type SessionFixture,
} from '../../store/fakeSession'
import { buildSessionModel, clock, took, type PathItem, type SessionTurn } from './model'

function indexed(f: SessionFixture): IndexedEvent[] {
  return f.events.map((event, i) => ({ index: i + 1, event }))
}

function turns(path: PathItem[]): SessionTurn[] {
  return path.filter((p): p is { kind: 'turn'; turn: SessionTurn } => p.kind === 'turn').map((p) => p.turn)
}

describe('the session model, on the triage run', () => {
  const f = triageFixture()
  const model = buildSessionModel(f.detail, indexed(f))

  it('reads every call as a step that says what it did and what came back', () => {
    const lines = model.steps.map((s) => `${s.at} ${s.verb} ${s.object}${s.result ? ` · ${s.result}` : ''}`)
    expect(lines).toEqual([
      '00:03 Ran ls -la …/app · 10 entries',
      '00:04 Ran ls -la …/bundle · 5 entries',
      '00:06 Read ledger.go · 32 lines',
      '00:07 Read movement.go · 10 lines',
      '00:07 Read reconcile.go · 7 lines',
      '00:08 Read ledger_test.go · 5 lines',
      '00:09 Read product.go · 3 lines',
      '00:10 Read bundle/ticket.json · 5 lines',
      '00:19 Ran git -C …/app log --stat --format=…',
      '00:20 Ran date -j -f %Y-%m-%d 2026-09-12 +%A',
      '00:22 Ran git log --stat --format=… · 1 commit · 6 files',
      '00:23 Ran rg -n "Return|restock" · 10 matches',
      '01:41 Wrote the note · v1 · 7.5 kB',
      '02:06 Ran rg -n -i "partial|Quantity" · 11 matches',
      '03:13 Rewrote the note · final · 8.1 kB',
    ])
  })

  it('stamps a denied call with the policy\'s reason and no result', () => {
    const denied = model.steps.filter((s) => s.state === 'denied')
    expect(denied.map((s) => s.object)).toEqual(['git -C …/app log --stat --format=…', 'date -j -f %Y-%m-%d 2026-09-12 +%A'])
    expect(denied[0].reason).toMatch(/passes git "-C" before the subcommand/)
    expect(denied[0].rule).toBeUndefined()
    expect(denied[0].result).toBe('')
    expect(model.counts).toMatchObject({ calls: 15, denied: 2 })
    expect(model.counts.byTool.find((t) => t.tool === 'Bash')).toEqual({ tool: 'Bash', n: 7, denied: 2 })
  })

  it('says which policy word an allowed shell call got, and leaves a read unstamped', () => {
    expect(model.steps[0].rule).toBe('allow-list')
    expect(model.steps[2].rule).toBeUndefined()
    expect(model.steps[0].durationMs).toBe(70)
    expect(took(model.steps[0].durationMs)).toBe('70 ms')
    expect(model.steps[0].description).toBe('List repository root files')
    expect(model.steps[2].description).toBeUndefined()
  })

  it('groups the path by turn, merging quiet reads, and restarts the count after the steer', () => {
    expect(turns(model.path).map((t) => t.label)).toEqual([
      'turn 1',
      'turn 2',
      'turns 3–8',
      'turn 9',
      'turn 10',
      'turn 11',
      'turn 12',
      'turn 13',
      'resumed · turn 1',
      'resumed · turn 2',
    ])
    const merged = turns(model.path)[2]
    expect(merged.steps.map((s) => s.object)).toEqual([
      'ledger.go',
      'movement.go',
      'reconcile.go',
      'ledger_test.go',
      'product.go',
      'bundle/ticket.json',
    ])
    expect(merged.at).toBe('00:06')
  })

  it('puts the model\'s words above the step they describe, once', () => {
    const turn1 = turns(model.path)[0]
    expect(turn1.items.map((i) => i.kind)).toEqual(['ann', 'step'])
    expect(turn1.items[0]).toMatchObject({ kind: 'ann', text: 'List repository root files' })
  })

  it('hoists the steer and the rate-limit warnings between the turns', () => {
    const kinds = model.path.map((p) => (p.kind === 'turn' ? p.turn.label : `${p.kind}:${p.text}`))
    expect(kinds).toEqual([
      'turn 1',
      'system:claude 7-day window at 90%',
      'turn 2',
      'turns 3–8',
      'turn 9',
      'turn 10',
      'turn 11',
      'turn 12',
      'turn 13',
      'you:Re-check whether the partial-return path is also affected, and say so in one sentence.',
      'system:claude 7-day window at 91%',
      'resumed · turn 1',
      'resumed · turn 2',
    ])
    const you = model.path.find((p) => p.kind === 'you')
    expect(you).toMatchObject({ at: '02:02', label: 'resumed the session' })
    expect(model.lastSteer).toEqual({ at: '02:02', text: 'Re-check whether the partial-return path is also affected, and say so in one sentence.' })
  })

  it('keeps the stream noise out until asked, then lists it as system lines', () => {
    const everything = buildSessionModel(f.detail, indexed(f), { everything: true })
    expect(everything.path.filter((p) => p.kind === 'system').length).toBeGreaterThanOrEqual(
      model.path.filter((p) => p.kind === 'system').length,
    )
  })

  it('parses the final answer and derives the evidence markers onto the steps', () => {
    expect(model.answer?.classification).toBe('code')
    expect(model.markers.map((m) => m.id)).toEqual(['E1', 'E2', 'E3', 'E4', 'E5', 'E6', 'E7', 'E8'])
    const byId = Object.fromEntries(model.markers.map((m) => [m.id, m.steps]))
    const step = (object: string) => model.steps.find((s) => s.object === object)?.index
    expect(byId.E1).toEqual([step('ledger.go')])
    expect(byId.E2).toEqual([step('movement.go')])
    expect(byId.E4).toEqual([step('rg -n "Return|restock"')])
    expect(byId.E5).toEqual([step('rg -n -i "partial|Quantity"')])
    expect(byId.E7).toEqual([step('git log --stat --format=…')])
    expect(byId.E8).toEqual([step('bundle/ticket.json')])
  })

  it('offers Steer once the run has completed', () => {
    expect(model.composer).toEqual({ kind: 'steer' })
  })

  it('reads a clock off the run\'s start', () => {
    expect(clock('2026-09-15T12:11:11Z', '2026-09-15T12:11:05Z')).toBe('00:06')
    expect(clock(undefined, '2026-09-15T12:11:05Z')).toBe('')
    expect(took(undefined)).toBe('')
    expect(took(1990)).toBe('1.99 s')
    expect(took(5)).toBe('<10 ms')
  })
})

describe('the session model, on the fix run', () => {
  const f = fixFixture()
  const model = buildSessionModel(f.detail, indexed(f), { diff: fixDiff() })

  it('reads edits as deltas, tests as verdicts and the report as the step that wrote it', () => {
    const lines = model.steps.map((s) => `${s.verb} ${s.object}${s.result ? ` · ${s.result}` : ''} [${s.state}${s.rule ? ` ${s.rule}` : ''}]`)
    expect(lines).toEqual([
      'Read worktree/ledger.go · 32 lines [done]',
      'Read worktree/ledger_test.go · 3 lines [done]',
      'Edited ledger_test.go · +14 [done allowed]',
      'Ran go test ./... · FAIL · 1 test [failed go test *]',
      'Edited ledger.go · −11 +1 [done allowed]',
      'Ran go build && go vet && go test · ok · 2.070s [done go build *, go vet *, go test *]',
      'Wrote the fix report · final · 1.5 kB [done]',
    ])
  })

  it('carries the report, the checks and the review as the reader\'s own card', () => {
    expect(model.report?.filesChanged).toEqual(['ledger.go', 'ledger_test.go'])
    expect(model.checks.map((c) => c.outcome)).toEqual(['failed', 'ok', 'ok', 'ok'])
    const review = model.path.find((p) => p.kind === 'you')
    expect(review).toMatchObject({ label: 'review', text: 'Dropped hunk ledger_test.go #1', at: '00:49' })
  })

  it('numbers the change markers by the order the files were edited and puts them on the edits', () => {
    const c = model.markers.filter((m) => m.kind === 'C')
    expect(c.map((m) => [m.id, m.path, m.hunk])).toEqual([
      ['C1', 'ledger_test.go', 0],
      ['C2', 'ledger.go', 0],
      ['C3', 'ledger.go', 1],
    ])
    const editTest = model.steps.find((s) => s.object === 'ledger_test.go')?.index
    const editLedger = model.steps.find((s) => s.object === 'ledger.go')?.index
    expect(c[0].steps).toEqual([editTest])
    expect(c[1].steps).toEqual([editLedger])
  })
})

describe('the session model, on the blocked fix run', () => {
  const f = blockedFixture()
  const model = buildSessionModel(f.detail, indexed(f), { diff: f.diff })

  it('marks the call the run is waiting on and offers Reply with the question, the rule and the reason', () => {
    const pending = model.steps[model.steps.length - 1]
    expect(pending).toMatchObject({ verb: 'Run', object: 'go test ./...', state: 'waiting', rule: 'go test *', result: '' })
    expect(pending.reason).toBe('This command requires approval')
    expect(model.composer).toMatchObject({
      kind: 'reply',
      question: 'Run `go test ./...` in the worktree?',
      since: '00:11',
      suggestedRule: 'go test *',
      reason: 'This command requires approval',
    })
    if (model.composer.kind === 'reply') expect(model.composer.pending?.index).toBe(pending.index)
  })

  it('keeps a waiting step out of the quiet merge so it stands in its own turn', () => {
    const labels = turns(model.path).map((t) => t.label)
    expect(labels).toEqual(['turns 1–2', 'turn 3', 'turn 4'])
  })
})

describe('the session model, before the run is read', () => {
  it('is disabled with the reason, and empty', () => {
    const model = buildSessionModel(null, [])
    expect(model.composer).toEqual({ kind: 'disabled', reason: 'Loading the run…' })
    expect(model.steps).toEqual([])
    expect(model.path).toEqual([])
  })

  it('is disabled while the run works and when it is over budget', () => {
    const f = triageFixture({ status: 'running' })
    expect(buildSessionModel(f.detail, []).composer).toMatchObject({ kind: 'disabled', reason: /still working/ })
    expect(buildSessionModel(triageFixture({ status: 'over_budget' }).detail, []).composer).toMatchObject({
      kind: 'disabled',
      reason: /Over budget/,
    })
  })
})
