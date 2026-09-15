import type { RunEvent } from '../api/types'
import { toolInput, type IndexedEvent } from './events'

/**
 * What a run's shell commands say about the change: the build, vet and test
 * runs the agent made, read off the event log, and the last step that
 * finished — which is what the session banner reports.
 *
 * Nothing here is authoritative. The provider records a command and the text
 * it printed, not an exit status (Claude marks a failed tool result, the
 * openai loop appends `[exit status N]`, Codex says nothing), so success is
 * judged from the output the way a person reading it would: a `go test`
 * that printed `FAIL` failed, one that printed `ok` passed. A command with
 * no result yet is listed with no verdict rather than left out.
 */

export type CheckKind = 'test' | 'build' | 'vet'

export interface Check {
  kind: CheckKind
  command: string
  /** Absent while the command is still running. */
  ok?: boolean
  /** "12 passed · 1.2s", or the line that says why it failed. */
  detail: string
  tests?: number
  seconds?: number
}

/** The last step the run finished: a test run, or the note landing. */
export type Step =
  | { kind: 'tests'; ok: boolean; tests?: number; seconds?: number; filesChanged: number }
  | { kind: 'note' }

function asRecord(value: unknown): Record<string, unknown> | undefined {
  if (typeof value !== 'object' || value === null || Array.isArray(value)) return undefined
  return value as Record<string, unknown>
}

/** The shell command a tool call ran, whichever provider recorded it. */
export function commandOf(event: RunEvent): string {
  const args = asRecord(toolInput(event))
  if (!args) return ''
  const command = args.command ?? args.cmd
  if (typeof command === 'string') return command
  if (Array.isArray(command)) return command.map(String).join(' ')
  return ''
}

const TEST =
  /(?:^|[\s;&|(])(?:go test|npm (?:run )?test|pnpm test|yarn test|bun test|(?:npx )?vitest|(?:npx )?jest|(?:python3? -m )?pytest|cargo test|make test|mvn test|gradle test|dotnet test)(?=$|[\s;&|)])/
const VET =
  /(?:^|[\s;&|(])(?:go vet|golangci-lint|staticcheck|(?:npx )?eslint|npm run lint|pnpm lint|ruff|flake8|cargo clippy|mypy|(?:npx )?tsc --noEmit)(?=$|[\s;&|)])/
const BUILD =
  /(?:^|[\s;&|(])(?:go build|go install|npm run build|pnpm build|yarn build|cargo build|make(?: build)?|(?:npx )?tsc|dotnet build|mvn (?:package|compile))(?=$|[\s;&|)])/

/** Which check a command is, or undefined for any other command. */
export function classifyCommand(command: string): CheckKind | undefined {
  if (TEST.test(command)) return 'test'
  if (VET.test(command)) return 'vet'
  if (BUILD.test(command)) return 'build'
  return undefined
}

function firstNumber(text: string, re: RegExp): number | undefined {
  const m = re.exec(text)
  return m ? Number(m[1]) : undefined
}

function firstLine(text: string, re: RegExp): string {
  for (const line of text.split('\n')) {
    if (re.test(line)) return line.trim()
  }
  return ''
}

function shorten(text: string, max = 100): string {
  const flat = text.replace(/\s+/g, ' ').trim()
  return flat.length > max ? `${flat.slice(0, max - 1)}…` : flat
}

/** True when the provider itself marked the result as a failure. */
function flaggedError(event: RunEvent): boolean {
  const raw = asRecord(event.payload?.raw)
  const message = asRecord(raw?.message)
  const content = message?.content
  if (Array.isArray(content)) {
    for (const block of content) {
      const b = asRecord(block)
      if (b?.type === 'tool_result' && b.is_error === true) return true
    }
  }
  const text = event.payload?.text ?? ''
  const exit = /\[exit status (\d+)\]\s*$/.exec(text)
  return exit !== null && exit[1] !== '0'
}

/** Reads the outcome of a finished check from what the command printed. */
export function judge(kind: CheckKind, command: string, event: RunEvent): Omit<Check, 'kind' | 'command'> {
  const text = event.payload?.text ?? ''
  const flagged = flaggedError(event)

  if (kind === 'test') {
    let tests: number | undefined
    let seconds: number | undefined
    let failed = flagged

    if (/\bgo test\b/.test(command)) {
      failed ||= /^(?:FAIL\b|--- FAIL)/m.test(text)
      const passes = text.match(/^\s*--- PASS:/gm)
      if (passes) tests = passes.length
      let total = 0
      let any = false
      for (const m of text.matchAll(/^ok\s+\S+\s+(?:\(cached\)|([\d.]+)s)/gm)) {
        if (m[1] !== undefined) {
          total += Number(m[1])
          any = true
        }
      }
      if (any) seconds = total
    } else if (/vitest/.test(command) || /jest/.test(command)) {
      failed ||= /^\s*(?:Tests?:?\s+\d+ failed|FAIL\s)/m.test(text)
      tests = firstNumber(text, /Tests:?\s+(\d+) passed/)
      seconds = firstNumber(text, /(?:Duration|Time):?\s+([\d.]+)\s*s/)
    } else if (/pytest/.test(command)) {
      failed ||= /\d+ failed|FAILED/.test(text)
      tests = firstNumber(text, /(\d+) passed/)
      seconds = firstNumber(text, /in ([\d.]+)s/)
    } else if (/cargo test/.test(command)) {
      failed ||= /test result: FAILED/.test(text)
      tests = firstNumber(text, /test result: ok\. (\d+) passed/)
      seconds = firstNumber(text, /finished in ([\d.]+)s/)
    } else {
      failed ||= /\b(?:FAIL(?:ED)?|\d+ failed)\b/.test(text)
    }

    if (failed) {
      return { ok: false, detail: shorten(firstLine(text, /FAIL|failed|error/i) || text) || 'failed' }
    }
    const parts: string[] = []
    if (tests !== undefined) parts.push(`${tests} passed`)
    if (seconds !== undefined) parts.push(`${trimSeconds(seconds)}s`)
    return { ok: true, detail: parts.join(' · ') || 'passed', tests, seconds }
  }

  const failed = flagged || /(?:^|\n)\S+:\d+(?::\d+)?: |\b(?:error|FAIL)\b|✖/i.test(text)
  if (failed) {
    return { ok: false, detail: shorten(firstLine(text, /:\d+|error|FAIL|✖/i) || text) || 'failed' }
  }
  return { ok: true, detail: 'ok' }
}

function trimSeconds(n: number): string {
  return n >= 10 ? n.toFixed(0) : n.toFixed(1).replace(/\.0$/, '')
}

interface Pending {
  id?: string
  tool: string
  kind: CheckKind
  command: string
  check: Check
}

/** The Claude tool_use id on a started call, or the tool_use_id on its result. */
function useId(event: RunEvent): string | undefined {
  const raw = asRecord(event.payload?.raw)
  const message = asRecord(raw?.message)
  const content = message?.content
  if (!Array.isArray(content)) return undefined
  for (const block of content) {
    const b = asRecord(block)
    if (!b) continue
    if (b.type === 'tool_use' && typeof b.id === 'string') return b.id
    if (b.type === 'tool_result' && typeof b.tool_use_id === 'string') return b.tool_use_id
  }
  return undefined
}

/**
 * Every build, vet and test command the run made, in the order it made
 * them, each with its outcome once the result line has arrived. A result is
 * paired with its call by the tool-use id where the provider gives one, and
 * by order otherwise.
 */
export function checksFrom(events: IndexedEvent[]): Check[] {
  const out: Check[] = []
  const pending: Pending[] = []

  for (const { event } of events) {
    if (event.kind === 'tool_started') {
      const command = commandOf(event)
      if (!command) continue
      const kind = classifyCommand(command)
      if (!kind) continue
      const check: Check = { kind, command, detail: '' }
      out.push(check)
      pending.push({ id: useId(event), tool: event.payload?.tool ?? '', kind, command, check })
      continue
    }
    if (event.kind !== 'tool_finished' || pending.length === 0) continue

    const id = useId(event)
    let at = id ? pending.findIndex((p) => p.id === id) : -1
    if (at === -1 && id && pending.some((p) => p.id)) {
      // The result names a call this list does not have: some other tool's.
      continue
    }
    if (at === -1) {
      const tool = event.payload?.tool ?? ''
      at = tool ? pending.findIndex((p) => p.tool === tool) : 0
      if (at === -1) at = 0
    }
    const [p] = pending.splice(at, 1)
    Object.assign(p.check, judge(p.kind, p.command, event))
  }

  return out
}

const WRITE_TOOLS = new Set([
  'Edit',
  'Write',
  'MultiEdit',
  'NotebookEdit',
  'write_file',
  'edit',
  'replace',
  'edit_file',
  'apply_patch',
  'fileChange',
])

const PATH_FIELDS = ['file_path', 'filePath', 'path', 'notebook_path', 'target_file']

function writtenPaths(event: RunEvent): string[] {
  if (event.kind !== 'tool_started' || !WRITE_TOOLS.has(event.payload?.tool ?? '')) return []
  const args = asRecord(toolInput(event))
  if (!args) return []
  for (const field of PATH_FIELDS) {
    const value = args[field]
    if (typeof value === 'string' && value !== '') return [value]
  }
  const changes = args.changes
  if (Array.isArray(changes)) {
    return changes
      .map((c) => {
        const r = asRecord(c)
        if (!r) return ''
        for (const field of PATH_FIELDS) {
          const value = r[field]
          if (typeof value === 'string' && value !== '') return value
        }
        return ''
      })
      .filter(Boolean)
  }
  return []
}

/**
 * The step the banner reports: the most recent test run to finish, with
 * how many files the agent had written by then, or the note landing —
 * whichever came last. Nothing, for a run that has done neither yet.
 */
export function latestStep(events: IndexedEvent[]): Step | undefined {
  const written = new Set<string>()
  const pending: Pending[] = []
  let step: Step | undefined

  for (const { event } of events) {
    for (const path of writtenPaths(event)) written.add(path)

    if (event.kind === 'final') {
      step = { kind: 'note' }
      continue
    }
    if (event.kind === 'tool_started') {
      const command = commandOf(event)
      if (command && classifyCommand(command) === 'test') {
        pending.push({
          id: useId(event),
          tool: event.payload?.tool ?? '',
          kind: 'test',
          command,
          check: { kind: 'test', command, detail: '' },
        })
      }
      continue
    }
    if (event.kind !== 'tool_finished' || pending.length === 0) continue
    const id = useId(event)
    let at = id ? pending.findIndex((p) => p.id === id) : -1
    if (at === -1 && id && pending.some((p) => p.id)) continue
    if (at === -1) at = 0
    const [p] = pending.splice(at, 1)
    const verdict = judge('test', p.command, event)
    step = {
      kind: 'tests',
      ok: verdict.ok === true,
      tests: verdict.tests,
      seconds: verdict.seconds,
      filesChanged: written.size,
    }
  }
  return step
}

/** "12 tests in 1.2s, 2 files changed" — the banner's body for a passed test run. */
export function describeTests(step: Extract<Step, { kind: 'tests' }>, filesChanged = step.filesChanged): string {
  const parts: string[] = []
  if (step.tests !== undefined) {
    parts.push(
      step.seconds !== undefined
        ? `${step.tests} ${step.tests === 1 ? 'test' : 'tests'} in ${trimSeconds(step.seconds)}s`
        : `${step.tests} ${step.tests === 1 ? 'test' : 'tests'}`,
    )
  } else if (step.seconds !== undefined) {
    parts.push(`in ${trimSeconds(step.seconds)}s`)
  }
  if (filesChanged > 0) parts.push(`${filesChanged} ${filesChanged === 1 ? 'file' : 'files'} changed`)
  return parts.join(', ')
}
