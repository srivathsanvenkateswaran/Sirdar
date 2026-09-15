import { describe, expect, it } from 'vitest'
import type { RunEvent } from '../api/types'
import { checksFrom, classifyCommand, describeTests, latestStep } from './checks'
import type { IndexedEvent } from './events'

let n = 0

/** A Claude Bash call, with the tool_use id the result will name. */
function bash(command: string, id: string): RunEvent {
  return {
    t: '2026-09-10T10:00:04Z',
    kind: 'tool_started',
    payload: {
      tool: 'Bash',
      raw: {
        type: 'assistant',
        message: { content: [{ type: 'tool_use', id, name: 'Bash', input: { command } }] },
      },
    },
  }
}

/** The result of that call, as Claude's user line carries it. */
function result(id: string, text: string, isError = false): RunEvent {
  return {
    t: '2026-09-10T10:00:09Z',
    kind: 'tool_finished',
    payload: {
      text,
      raw: {
        type: 'user',
        message: {
          content: [{ type: 'tool_result', tool_use_id: id, content: text, is_error: isError }],
        },
      },
    },
  }
}

function edit(path: string): RunEvent {
  return {
    t: '2026-09-10T10:00:05Z',
    kind: 'tool_started',
    payload: {
      tool: 'Edit',
      raw: {
        type: 'assistant',
        message: {
          content: [{ type: 'tool_use', id: `e${n++}`, name: 'Edit', input: { file_path: path } }],
        },
      },
    },
  }
}

function indexed(events: RunEvent[]): IndexedEvent[] {
  return events.map((event, i) => ({ index: i + 1, event }))
}

const GO_TEST_OK = [
  '=== RUN   TestPageReleasesConnection',
  '--- PASS: TestPageReleasesConnection (0.01s)',
  '=== RUN   TestClose',
  '--- PASS: TestClose (0.00s)',
  'PASS',
  'ok  \tgithub.com/acme/app/internal/export\t1.204s',
].join('\n')

describe('classifyCommand', () => {
  it.each([
    ['go test ./...', 'test'],
    ['cd app && go test -run TestX ./internal/...', 'test'],
    ['npx vitest run src/lib', 'test'],
    ['go build ./...', 'build'],
    ['go vet ./...', 'vet'],
    ['npx tsc --noEmit', 'vet'],
    ['npx tsc', 'build'],
    ['git status', undefined],
    ['rg -n "gotest" .', undefined],
  ])('%s is %s', (command, kind) => {
    expect(classifyCommand(command)).toBe(kind)
  })
})

describe('checksFrom', () => {
  it('lists build, vet and test in order with their outcomes', () => {
    const checks = checksFrom(
      indexed([
        bash('go build ./...', 'a'),
        result('a', ''),
        bash('go vet ./...', 'b'),
        result('b', ''),
        bash('go test ./internal/export/...', 'c'),
        result('c', GO_TEST_OK),
        bash('git status', 'd'),
        result('d', 'On branch main'),
      ]),
    )
    expect(checks).toEqual([
      { kind: 'build', command: 'go build ./...', ok: true, detail: 'ok' },
      { kind: 'vet', command: 'go vet ./...', ok: true, detail: 'ok' },
      {
        kind: 'test',
        command: 'go test ./internal/export/...',
        ok: true,
        detail: '2 passed · 1.2s',
        tests: 2,
        seconds: 1.204,
      },
    ])
  })

  it('reads a failed go test and a provider-flagged failure', () => {
    const checks = checksFrom(
      indexed([
        bash('go test ./...', 'a'),
        result('a', '--- FAIL: TestPage (0.00s)\n    page_test.go:12: open = 1, want 0\nFAIL\nFAIL\tapp\t0.4s'),
        bash('go build ./...', 'b'),
        result('b', './main.go:4:2: imported and not used', true),
      ]),
    )
    expect(checks[0]).toMatchObject({ ok: false, detail: '--- FAIL: TestPage (0.00s)' })
    expect(checks[1]).toMatchObject({ ok: false, detail: './main.go:4:2: imported and not used' })
  })

  it('pairs results by tool-use id, and leaves a check with no result unjudged', () => {
    const checks = checksFrom(
      indexed([
        bash('go vet ./...', 'a'),
        bash('go test ./...', 'b'),
        result('b', GO_TEST_OK),
        // A read tool's result, which names neither call.
        result('z', 'package main'),
      ]),
    )
    expect(checks[0]).toEqual({ kind: 'vet', command: 'go vet ./...', detail: '' })
    expect(checks[1]).toMatchObject({ kind: 'test', ok: true, tests: 2 })
  })

  it('reads vitest, pytest and cargo summaries', () => {
    const checks = checksFrom(
      indexed([
        bash('npx vitest run', 'a'),
        result('a', ' Test Files  3 passed (3)\n      Tests  41 passed (41)\n   Duration  2.31s'),
        bash('pytest', 'b'),
        result('b', '===== 12 passed in 0.84s ====='),
        bash('cargo test', 'c'),
        result('c', 'test result: ok. 7 passed; 0 failed; finished in 0.12s'),
      ]),
    )
    expect(checks.map((c) => c.detail)).toEqual(['41 passed · 2.3s', '12 passed · 0.8s', '7 passed · 0.1s'])
  })
})

describe('latestStep', () => {
  it('is the test run that finished last, with the files written by then', () => {
    const step = latestStep(
      indexed([
        edit('app/ledger.go'),
        edit('app/ledger_test.go'),
        edit('app/ledger.go'),
        bash('go test ./app/...', 'a'),
        result('a', GO_TEST_OK),
      ]),
    )
    expect(step).toEqual({
      kind: 'tests',
      ok: true,
      tests: 2,
      seconds: 1.204,
      filesChanged: 2,
      detail: '',
    })
    expect(describeTests(step as Extract<typeof step, { kind: 'tests' }>)).toBe(
      '2 tests in 1.2s, 2 files changed',
    )
  })

  it('is the note once the final event lands after the tests', () => {
    const step = latestStep(
      indexed([
        bash('go test ./...', 'a'),
        result('a', GO_TEST_OK),
        { t: '2026-09-10T10:01:00Z', kind: 'final', payload: {} },
      ]),
    )
    expect(step).toEqual({ kind: 'note' })
  })

  it('is nothing for a run that has neither tested nor filed', () => {
    expect(latestStep(indexed([bash('git log', 'a'), result('a', 'x')]))).toBeUndefined()
    expect(latestStep(indexed([bash('go test ./...', 'a')]))).toBeUndefined()
  })

  it('says a failed test run failed', () => {
    const step = latestStep(indexed([bash('go test ./...', 'a'), result('a', 'FAIL\tapp\t0.1s')]))
    // The line is flattened for a banner: one space where the tab was.
    expect(step).toMatchObject({ kind: 'tests', ok: false, detail: 'FAIL app 0.1s' })
  })
})
