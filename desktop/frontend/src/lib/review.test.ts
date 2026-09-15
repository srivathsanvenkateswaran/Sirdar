import { describe, expect, it } from 'vitest'
import type { RunEvent } from '../api/types'
import {
  changeTotals,
  checksFromEvents,
  describeTests,
  fixReport,
  judgeOutput,
  latestStep,
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

/** A result that printed something, which is what the verdict is read from. */
function printed(text: string, isError = false): RunEvent {
  return ev('tool_finished', {
    tool: 'Bash',
    text,
    raw: { type: 'user', message: { content: [{ type: 'tool_result', content: text, is_error: isError }] } },
  })
}

function edit(path: string): RunEvent {
  return ev('tool_started', {
    tool: 'Edit',
    raw: { type: 'assistant', message: { content: [{ type: 'tool_use', name: 'Edit', input: { file_path: path } }] } },
  })
}

const GO_TEST_OK = [
  '=== RUN   TestPageReleasesConnection',
  '--- PASS: TestPageReleasesConnection (0.01s)',
  '=== RUN   TestClose',
  '--- PASS: TestClose (0.00s)',
  'PASS',
  'ok  \tgithub.com/acme/app/internal/export\t1.204s',
].join('\n')

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

describe('judgeOutput', () => {
  it('reads a go test summary into a count and a time', () => {
    expect(judgeOutput('go test ./...', printed(GO_TEST_OK))).toEqual({
      outcome: 'ok',
      result: '2 passed · 1.2s',
      tests: 2,
      seconds: 1.204,
    })
  })

  it('reads a failed go test, a flagged build, and a clean build', () => {
    expect(judgeOutput('go test ./...', printed('--- FAIL: TestPage (0.00s)\n    page_test.go:12: open = 1, want 0\nFAIL\nFAIL\tapp\t0.4s'))).toMatchObject({
      outcome: 'failed',
      result: '--- FAIL: TestPage (0.00s)',
    })
    expect(judgeOutput('go build ./...', printed('./main.go:4:2: imported and not used', true))).toEqual({
      outcome: 'failed',
      result: './main.go:4:2: imported and not used',
    })
    expect(judgeOutput('go build ./...', printed('ok'))).toEqual({ outcome: 'ok', result: 'ok' })
  })

  it('reads vitest, pytest and cargo summaries', () => {
    expect(judgeOutput('npx vitest run', printed(' Test Files  3 passed (3)\n      Tests  41 passed (41)\n   Duration  2.31s')).result).toBe('41 passed · 2.3s')
    expect(judgeOutput('pytest', printed('===== 12 passed in 0.84s =====')).result).toBe('12 passed · 0.8s')
    expect(judgeOutput('cargo test', printed('test result: ok. 7 passed; 0 failed; finished in 0.12s')).result).toBe('7 passed · 0.1s')
  })

  it('judges a printed result inside checksFromEvents too', () => {
    expect(checksFromEvents([bash('go test ./app/...'), printed(GO_TEST_OK)])).toEqual([
      { command: 'go test ./app/...', result: '2 passed · 1.2s', outcome: 'ok' },
    ])
  })
})

describe('latestStep', () => {
  it('is the test run that finished last, with the files written by then', () => {
    const step = latestStep([
      edit('app/ledger.go'),
      edit('app/ledger_test.go'),
      edit('app/ledger.go'),
      bash('go test ./app/...'),
      printed(GO_TEST_OK),
    ])
    expect(step).toEqual({ kind: 'tests', ok: true, tests: 2, seconds: 1.204, filesChanged: 2, detail: '' })
    expect(describeTests(step as Extract<typeof step, { kind: 'tests' }>)).toBe('2 tests in 1.2s, 2 files changed')
  })

  it('is the note once the final event lands after the tests', () => {
    expect(latestStep([bash('go test ./...'), printed(GO_TEST_OK), ev('final')])).toEqual({ kind: 'note' })
  })

  it('is nothing for a run that has neither tested nor filed, or whose test has no result yet', () => {
    expect(latestStep([bash('git log'), printed('x')])).toBeUndefined()
    expect(latestStep([bash('go test ./...')])).toBeUndefined()
    expect(latestStep([bash('go test ./...'), finished()])).toBeUndefined()
  })

  it('says a failed test run failed, with the line that says so', () => {
    expect(latestStep([bash('go test ./...'), printed('FAIL\tapp\t0.1s')])).toMatchObject({
      kind: 'tests',
      ok: false,
      detail: 'FAIL app 0.1s',
    })
  })
})
