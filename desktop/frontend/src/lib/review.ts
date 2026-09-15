import type { DiffFile, RunEvent } from '../api/types'
import { inputSummary } from './events'

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
    for (let j = i + 1; j < events.length; j += 1) {
      const next = events[j]
      if (next.kind === 'tool_started') break
      if (next.kind === 'tool_finished') {
        outcome = finishedInError(next) ? 'failed' : 'ran'
        break
      }
    }
    checks.push({ command, outcome, result: '' })
  }
  return checks
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
