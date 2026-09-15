import type { DiffFile, RunEvent } from '../api/types'
import { inputSummary, toolInput } from './events'

/**
 * What the Change review screen reads out of a fix run's event log.
 *
 * A fix run ends with the agent's JSON report — `summary`, `filesChanged`,
 * `testsRun`, `risks`, `deviationFromNote`, the shape `internal/fix.Report`
 * validates — and the run writes that answer to `result.json`, which no
 * transport route serves. The same document rides on the run's `final`
 * event: Claude puts it in the result line's `structured_output` when the
 * schema was enforced on the wire and in `result` when it was not, and
 * `internal/run` copies the latter into the thin payload's `text`. So the
 * report is read from the last `final` event, and the screen has "what the
 * agent said" and the checks it ran without a new route.
 *
 * Everything here is defensive: a run that ended without a report, or whose
 * final text is narration rather than JSON, yields nothing rather than an
 * error, and the checks fall back to the build and test commands the agent
 * was seen to run.
 */

export interface FixReport {
  summary: string
  filesChanged: string[]
  testsRun: { command: string; result: string }[]
  risks: string
  deviationFromNote: string
}

/** One check as the rail lists it: the command, its verdict, and what it said. */
export interface Check {
  command: string
  /** `ok` and `failed` are read off the result; `ran` is a command whose outcome the log does not say. */
  outcome: 'ok' | 'failed' | 'ran'
  /** The agent's own words for what the command reported, when it gave any. */
  result: string
}

function asRecord(value: unknown): Record<string, unknown> | undefined {
  if (typeof value !== 'object' || value === null || Array.isArray(value)) return undefined
  return value as Record<string, unknown>
}

function str(value: unknown): string {
  return typeof value === 'string' ? value : ''
}

/** Parses a JSON object out of text that may wrap it in a code fence or prose. */
function parseObject(text: string): Record<string, unknown> | undefined {
  const trimmed = text.trim()
  if (!trimmed) return undefined
  const candidates = [trimmed]
  const fenced = /```(?:json)?\s*([\s\S]*?)```/.exec(trimmed)
  if (fenced) candidates.unshift(fenced[1].trim())
  const first = trimmed.indexOf('{')
  const last = trimmed.lastIndexOf('}')
  if (first !== -1 && last > first) candidates.push(trimmed.slice(first, last + 1))
  for (const candidate of candidates) {
    try {
      const parsed = asRecord(JSON.parse(candidate))
      if (parsed) return parsed
    } catch {
      // Not this one.
    }
  }
  return undefined
}

function reportOf(doc: Record<string, unknown> | undefined): FixReport | undefined {
  if (!doc) return undefined
  const summary = str(doc.summary).trim()
  const tests = Array.isArray(doc.testsRun) ? doc.testsRun : []
  const files = Array.isArray(doc.filesChanged) ? doc.filesChanged : []
  const report: FixReport = {
    summary,
    filesChanged: files.map(str).filter(Boolean),
    testsRun: tests
      .map(asRecord)
      .filter((t): t is Record<string, unknown> => Boolean(t))
      .map((t) => ({ command: str(t.command).trim(), result: str(t.result).trim() }))
      .filter((t) => t.command !== ''),
    risks: str(doc.risks).trim(),
    deviationFromNote: str(doc.deviationFromNote).trim(),
  }
  // A document with none of the report's fields is some other JSON.
  if (!summary && report.testsRun.length === 0 && report.filesChanged.length === 0) return undefined
  return report
}

/** The fix report the run filed, read off its last `final` event; undefined when there is none. */
export function fixReport(events: RunEvent[]): FixReport | undefined {
  for (let i = events.length - 1; i >= 0; i -= 1) {
    const event = events[i]
    if (event.kind !== 'final') continue
    const raw = asRecord(event.payload?.raw)
    const fromStructured = reportOf(asRecord(raw?.structured_output))
    if (fromStructured) return fromStructured
    const fromResult = reportOf(parseObject(str(raw?.result)))
    if (fromResult) return fromResult
    const fromText = reportOf(parseObject(str(event.payload?.text)))
    if (fromText) return fromText
  }
  return undefined
}

const FAILED = /\b(fail|failed|failing|failure|failures|error|errors|panic|panicked|broken)\b/i
const NONE_FAILED = /\b(0|no|zero|without)\s+(fail|failed|failures?|errors?)\b/i
const OK = /\b(ok|pass|passed|passing|passes|success|successful|succeeded|green|clean|compiled|builds?)\b/i

/** Reads a verdict out of the agent's one-line result for a command. */
export function outcomeOf(result: string): Check['outcome'] {
  if (!result.trim()) return 'ran'
  const text = result.replace(NONE_FAILED, ' ')
  if (FAILED.test(text)) return 'failed'
  if (OK.test(result)) return 'ok'
  return 'ran'
}

/**
 * The commands a fix run verifies itself with, as the agent might type them.
 * Anything else the agent ran is a tool call the transcript shows, not a
 * check.
 */
const CHECK_COMMAND =
  /^(?:cd\s+\S+\s*&&\s*)?(?:go\s+(?:build|vet|test)|npm\s+(?:test|run\s+(?:test|lint|check|build|typecheck))|npx\s+(?:vitest|jest|tsc|eslint|playwright)|pnpm\s+(?:test|run\s+\w+)|yarn\s+(?:test|lint|build)|pytest|python3?\s+-m\s+pytest|make\s+(?:test|check|lint|build)|cargo\s+(?:test|check|build|clippy)|dotnet\s+(?:test|build)|mvn\s+(?:test|verify)|gradle\s+(?:test|build)|bundle\s+exec\s+rspec|rspec)\b/

/** Claude reports a tool's failure as `is_error` on the tool_result block. */
function finishedInError(event: RunEvent): boolean {
  const raw = asRecord(event.payload?.raw)
  const message = asRecord(raw?.message)
  const content = message?.content
  if (!Array.isArray(content)) return false
  return content.some((block) => asRecord(block)?.is_error === true)
}

/**
 * The checks the run shows in its rail.
 *
 * The report's own `testsRun` is the first choice: it is the agent saying
 * which commands it ran and what each reported. A run with no report falls
 * back to the shell commands in its log that look like a build or a test,
 * paired with the tool result that followed each; those say `failed` only
 * when the provider flagged the result as an error, and `ran` otherwise,
 * because a passing exit is not something the log records.
 */
export function checksFromEvents(events: RunEvent[]): Check[] {
  const report = fixReport(events)
  if (report && report.testsRun.length > 0) {
    return report.testsRun.map((t) => ({
      command: t.command,
      result: t.result,
      outcome: outcomeOf(t.result),
    }))
  }

  const checks: Check[] = []
  for (let i = 0; i < events.length; i += 1) {
    const event = events[i]
    if (event.kind !== 'tool_started') continue
    const tool = str(event.payload?.tool)
    if (!/^(bash|shell|commandexecution|command_execution)$/i.test(tool)) continue
    const command = inputSummary(event)
    if (!CHECK_COMMAND.test(command)) continue
    let outcome: Check['outcome'] = 'ran'
    let result = ''
    for (let j = i + 1; j < events.length; j += 1) {
      const next = events[j]
      if (next.kind === 'tool_started') break
      if (next.kind === 'tool_finished') {
        // What the command printed says more than the flag: a `go test`
        // that printed `ok` passed, one that printed `FAIL` failed, and the
        // summary line is worth keeping. Without any text the flag is all
        // there is.
        const judged = judgeOutput(command, next)
        outcome = judged.outcome
        result = judged.result
        break
      }
    }
    checks.push({ command, outcome, result })
  }
  return checks
}

// ------------------------------------------------- what a command printed

/** A test run's verdict as read off its output: the count and the time, when the runner said them. */
export interface Judged {
  outcome: Check['outcome']
  /** "12 passed · 1.2s", the failing line, or '' when the log has no text. */
  result: string
  tests?: number
  seconds?: number
}

function firstNumber(text: string, re: RegExp): number | undefined {
  const m = re.exec(text)
  return m ? Number(m[1]) : undefined
}

function firstLine(text: string, re: RegExp): string {
  for (const line of text.split('\n')) if (re.test(line)) return line.trim()
  return ''
}

function shorten(text: string, max = 100): string {
  const flat = text.replace(/\s+/g, ' ').trim()
  return flat.length > max ? `${flat.slice(0, max - 1)}…` : flat
}

function trimSeconds(n: number): string {
  return n >= 10 ? n.toFixed(0) : n.toFixed(1).replace(/\.0$/, '')
}

/** True when the openai loop appended a non-zero exit to the output. */
function exitedNonZero(text: string): boolean {
  const exit = /\[exit status (\d+)\]\s*$/.exec(text)
  return exit !== null && exit[1] !== '0'
}

const TEST_COMMAND =
  /(?:^|[\s;&|(])(?:go test|npm (?:run )?test|pnpm test|yarn test|bun test|(?:npx )?vitest|(?:npx )?jest|(?:python3? -m )?pytest|cargo test|make test|mvn test|gradle test|dotnet test|rspec)(?=$|[\s;&|)])/

/** True for a command that runs tests, as opposed to a build or a lint. */
export function isTestCommand(command: string): boolean {
  return TEST_COMMAND.test(command)
}

/**
 * Reads a finished command's outcome from what it printed, the way a person
 * would: the runner's own summary line for a test run, an error line for a
 * build. A result with no text at all is `ran` unless the provider flagged
 * it, because a passing exit is not something the log records.
 */
export function judgeOutput(command: string, finished: RunEvent): Judged {
  const text = str(finished.payload?.text)
  const flagged = finishedInError(finished) || exitedNonZero(text)
  if (!text.trim()) return { outcome: flagged ? 'failed' : 'ran', result: '' }

  if (isTestCommand(command)) {
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
    } else if (/vitest|jest/.test(command)) {
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
      return { outcome: 'failed', result: shorten(firstLine(text, /FAIL|failed|error/i) || text) || 'failed' }
    }
    const parts: string[] = []
    if (tests !== undefined) parts.push(`${tests} passed`)
    if (seconds !== undefined) parts.push(`${trimSeconds(seconds)}s`)
    return { outcome: 'ok', result: parts.join(' · ') || 'passed', tests, seconds }
  }

  const failed = flagged || /(?:^|\n)\S+:\d+(?::\d+)?: |\b(?:error|FAIL)\b|✖/i.test(text)
  if (failed) {
    return { outcome: 'failed', result: shorten(firstLine(text, /:\d+|error|FAIL|✖/i) || text) || 'failed' }
  }
  return { outcome: 'ok', result: 'ok' }
}

// ------------------------------------------------- the last finished step

/** The step the session banner reports: a test run finishing, or the note landing. */
export type Step =
  | {
      kind: 'tests'
      ok: boolean
      tests?: number
      seconds?: number
      /** How many distinct files the agent had written by then. */
      filesChanged: number
      /** The line that says why it failed; empty when it passed. */
      detail: string
    }
  | { kind: 'note' }

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

function pathOf(args: Record<string, unknown> | undefined): string {
  if (!args) return ''
  for (const field of PATH_FIELDS) {
    const value = args[field]
    if (typeof value === 'string' && value !== '') return value
  }
  return ''
}

/** The files a write tool call named, whichever provider recorded it. */
function writtenPaths(event: RunEvent): string[] {
  if (event.kind !== 'tool_started' || !WRITE_TOOLS.has(str(event.payload?.tool))) return []
  const args = asRecord(toolInput(event))
  const one = pathOf(args)
  if (one) return [one]
  const changes = args?.changes
  if (Array.isArray(changes)) return changes.map((c) => pathOf(asRecord(c))).filter(Boolean)
  return []
}

/**
 * The most recent test run to finish, with how many files the agent had
 * written by then, or the note landing — whichever came last. Nothing, for
 * a run that has done neither yet. Test results are paired with their
 * command the way `checksFromEvents` pairs them: the result that follows.
 */
export function latestStep(events: RunEvent[]): Step | undefined {
  const written = new Set<string>()
  let pending: string | undefined
  let step: Step | undefined

  for (const event of events) {
    for (const path of writtenPaths(event)) written.add(path)

    if (event.kind === 'final') {
      step = { kind: 'note' }
      pending = undefined
      continue
    }
    if (event.kind === 'tool_started') {
      const tool = str(event.payload?.tool)
      const command = /^(bash|shell|commandexecution|command_execution)$/i.test(tool)
        ? inputSummary(event)
        : ''
      pending = command && isTestCommand(command) ? command : undefined
      continue
    }
    if (event.kind !== 'tool_finished' || !pending) continue
    const judged = judgeOutput(pending, event)
    pending = undefined
    if (judged.outcome === 'ran') continue
    step = {
      kind: 'tests',
      ok: judged.outcome === 'ok',
      tests: judged.tests,
      seconds: judged.seconds,
      filesChanged: written.size,
      detail: judged.outcome === 'ok' ? '' : judged.result,
    }
  }
  return step
}

/** "12 tests in 1.2s, 2 files changed" — the banner's body for a passed test run. */
export function describeTests(
  step: Extract<Step, { kind: 'tests' }>,
  filesChanged = step.filesChanged,
): string {
  const parts: string[] = []
  const noun = step.tests === 1 ? 'test' : 'tests'
  if (step.tests !== undefined) {
    parts.push(
      step.seconds !== undefined
        ? `${step.tests} ${noun} in ${trimSeconds(step.seconds)}s`
        : `${step.tests} ${noun}`,
    )
  } else if (step.seconds !== undefined) {
    parts.push(`in ${trimSeconds(step.seconds)}s`)
  }
  if (filesChanged > 0) parts.push(`${filesChanged} ${filesChanged === 1 ? 'file' : 'files'} changed`)
  return parts.join(', ')
}

/** The word the rail prints before a check, so the verdict is never the colour alone. */
export const OUTCOME_WORDS: Record<Check['outcome'], string> = {
  ok: 'ok',
  failed: 'failed',
  ran: 'ran',
}

/** `2 files · +25 −6`, the rail's head, from the service's file list. */
export function changeTotals(files: DiffFile[]): { files: number; additions: number; deletions: number } {
  return files.reduce(
    (acc, f) => ({
      files: acc.files + 1,
      additions: acc.additions + f.additions,
      deletions: acc.deletions + f.deletions,
    }),
    { files: 0, additions: 0, deletions: 0 },
  )
}

/** The line that pushes a fix branch by hand, until an API does it. */
export function pushCommand(worktree: string, branch: string): string {
  return `git -C ${shellQuote(worktree)} push -u origin ${shellQuote(branch)}`
}

/** Quotes an argument for a shell only when it needs it. */
function shellQuote(arg: string): string {
  if (/^[A-Za-z0-9_./:@%+=,-]+$/.test(arg)) return arg
  return `'${arg.replace(/'/g, `'\\''`)}'`
}

/**
 * The kind of note a path names, from the file name the run's filename
 * pattern gave it. The defaults in `internal/config` are `{key} {slug}.md`
 * for a triage note, `{key} RCA {slug}.md` and `{key} RES {slug}.md` for the
 * RCA pair, and a run's own copy is always `note.md` in its directory. A
 * person who changed the pattern still tends to keep the word.
 */
export function noteLabel(path: string): string {
  const name = path.split(/[\\/]/).pop() ?? path
  if (/\b(RES|resolution)\b/i.test(name)) return 'Resolution note'
  if (/\bRCA\b/i.test(name)) return 'RCA note'
  if (name === 'note.md') return 'Note'
  return 'Triage note'
}
