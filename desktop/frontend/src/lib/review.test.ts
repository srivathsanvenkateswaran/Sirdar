import { describe, expect, it } from 'vitest'
import type { RunEvent } from '../api/types'
import {
  changeTotals,
  checksFromEvents,
  fixReport,
  noteLabel,
  outcomeOf,
  pushCommand,
} from './review'

const REPORT = {
  summary: 'Guard the partial-return branch on m.Partial.\n\nA full return counts once now.',
  filesChanged: ['app/ledger.go', 'app/ledger_test.go'],
  testsRun: [
    { command: 'go build ./...', result: 'ok' },
    { command: 'go test ./app/...', result: '12 passed, 0 failed, 1.2s' },
  ],
  risks: 'none',
  deviationFromNote: 'no migration for balances already affected, per your answer',
}

function ev(kind: string, payload: RunEvent['payload'] = {}): RunEvent {
  return { t: '2026-09-10T10:00:00Z', kind, payload }
}

function bash(command: string): RunEvent {
  return ev('tool_started', {
    tool: 'Bash',
    raw: {
      type: 'assistant',
      message: { content: [{ type: 'tool_use', name: 'Bash', input: { command } }] },
    },
  })
}

function finished(isError = false): RunEvent {
  return ev('tool_finished', {
    tool: 'Bash',
    raw: { type: 'user', message: { content: [{ type: 'tool_result', is_error: isError }] } },
  })
}

describe('fixReport', () => {
  it('reads the report off the final event\'s text', () => {
    const report = fixReport([ev('assistant_text', { text: 'done' }), ev('final', { text: JSON.stringify(REPORT) })])
    expect(report?.summary).toBe(REPORT.summary)
    expect(report?.testsRun).toEqual(REPORT.testsRun)
    expect(report?.deviationFromNote).toBe(REPORT.deviationFromNote)
    expect(report?.filesChanged).toEqual(REPORT.filesChanged)
  })

  it('prefers the structured output the provider enforced on the wire', () => {
    const report = fixReport([
      ev('final', {
        text: 'Here is the report.',
        raw: { type: 'result', result: 'Here is the report.', structured_output: REPORT },
      }),
    ])
    expect(report?.summary).toBe(REPORT.summary)
  })

  it('unwraps a fenced report', () => {
    const report = fixReport([
      ev('final', { text: 'Report:\n```json\n' + JSON.stringify(REPORT) + '\n```\n' }),
    ])
    expect(report?.summary).toBe(REPORT.summary)
  })

  it('takes the last final event when a resumed session wrote two', () => {
    const report = fixReport([
      ev('final', { text: JSON.stringify({ ...REPORT, summary: 'first' }) }),
      ev('final', { text: JSON.stringify({ ...REPORT, summary: 'second' }) }),
    ])
    expect(report?.summary).toBe('second')
  })

  it('answers nothing for narration, other JSON, or no final at all', () => {
    expect(fixReport([ev('final', { text: 'I could not finish.' })])).toBeUndefined()
    expect(fixReport([ev('final', { text: '{"title":"a triage note"}' })])).toBeUndefined()
    expect(fixReport([ev('assistant_text', { text: 'x' })])).toBeUndefined()
    expect(fixReport([])).toBeUndefined()
  })
})

describe('outcomeOf', () => {
  it('reads ok, failed and ran off the agent\'s words', () => {
    expect(outcomeOf('ok')).toBe('ok')
    expect(outcomeOf('12 passed, 0 failed, 1.2s')).toBe('ok')
    expect(outcomeOf('all tests pass')).toBe('ok')
    expect(outcomeOf('FAIL: TestX')).toBe('failed')
    expect(outcomeOf('2 errors')).toBe('failed')
    expect(outcomeOf('1 passed, 1 failed')).toBe('failed')
    expect(outcomeOf('')).toBe('ran')
    expect(outcomeOf('exit status unknown')).toBe('ran')
  })
})

describe('checksFromEvents', () => {
  it('lists the report\'s testsRun with a verdict each', () => {
    const checks = checksFromEvents([bash('go test ./...'), ev('final', { text: JSON.stringify(REPORT) })])
    expect(checks).toEqual([
      { command: 'go build ./...', result: 'ok', outcome: 'ok' },
      { command: 'go test ./app/...', result: '12 passed, 0 failed, 1.2s', outcome: 'ok' },
    ])
  })

  it('falls back to the build and test commands the agent ran when there is no report', () => {
    const checks = checksFromEvents([
      bash('ls -la'),
      finished(),
      bash('go build ./...'),
      finished(),
      bash('go vet ./...'),
      finished(true),
      bash('go test ./app/...'),
      // No result yet: the run is still going.
    ])
    expect(checks).toEqual([
      { command: 'go build ./...', result: '', outcome: 'ran' },
      { command: 'go vet ./...', result: '', outcome: 'failed' },
      { command: 'go test ./app/...', result: '', outcome: 'ran' },
    ])
  })

  it('is empty for a run that ran nothing that looks like a check', () => {
    expect(checksFromEvents([bash('git status'), finished()])).toEqual([])
  })
})

describe('the footer helpers', () => {
  it('sums the file list', () => {
    expect(
      changeTotals([
        { path: 'a', status: 'modified', additions: 4, deletions: 6 },
        { path: 'b', status: 'added', additions: 21, deletions: 0 },
      ]),
    ).toEqual({ files: 2, additions: 25, deletions: 6 })
  })

  it('writes the push line, quoting only what needs it', () => {
    expect(pushCommand('/repos/omni/.sirdar/worktrees/OMNI-1', 'sirdar/OMNI-1-fix')).toBe(
      'git -C /repos/omni/.sirdar/worktrees/OMNI-1 push -u origin sirdar/OMNI-1-fix',
    )
    expect(pushCommand('/Users/me/My Repos/x', 'sirdar/X-1')).toBe(
      "git -C '/Users/me/My Repos/x' push -u origin sirdar/X-1",
    )
  })

  it('names a note by the word in its file name', () => {
    expect(noteLabel('/w/notes/SBX-1 RES double-counted-return.md')).toBe('Resolution note')
    expect(noteLabel('/w/notes/SBX-1 RCA double-counted-return.md')).toBe('RCA note')
    expect(noteLabel('/w/notes/SBX-1 double-counted-return.md')).toBe('Triage note')
    expect(noteLabel('/w/.sirdar/runs/SBX-1/r1/note.md')).toBe('Note')
  })
})
