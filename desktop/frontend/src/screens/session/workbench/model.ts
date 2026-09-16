import type { RunDetail, RunEvent } from '../../../api/types'
import {
  askedQuestion,
  callId,
  conversation,
  inputJSON,
  inputSummary,
  outputFailed,
  outputText,
  parseAnswer,
  toolInput,
  toolLabel,
  type IndexedEvent,
  type ToolCall,
} from '../../../lib/events'
import { duration, parseTime, usd } from '../../../lib/format'
import { isTestCommand, noteName } from '../../../lib/review'

/**
 * The Workbench's reading of a run: `events.jsonl` as a structured log (one
 * row per thing that happened, with the offset, the tool, a one-line input,
 * the model's own description, the duration, the output size and the
 * policy's decision), the turns as rail cells, and the header's gauges.
 *
 * Everything here is pure. The rows are built once per event list and the
 * filters, the search and the rail read them; nothing here fetches. When the
 * shared session blocks land (`src/components/session`, `useSessionModel`)
 * this file is what they replace.
 */

// ------------------------------------------------------------------ offsets

/** `00:41.7`: minutes, seconds and tenths since the run started. */
export function offsetTenths(t: string | undefined, startedAt: string | undefined): string {
  const at = parseTime(t)
  const start = parseTime(startedAt)
  if (Number.isNaN(at) || Number.isNaN(start)) return ''
  const ms = Math.max(0, at - start)
  const total = Math.floor(ms / 100)
  const tenths = total % 10
  const seconds = Math.floor(total / 10) % 60
  const minutes = Math.floor(total / 600)
  return `${String(minutes).padStart(2, '0')}:${String(seconds).padStart(2, '0')}.${tenths}`
}

/** `82 ms`, `1,995 ms`, `12.4 s`, `1:04`. */
export function durationLabel(ms: number | undefined): string {
  if (ms === undefined || !Number.isFinite(ms) || ms < 0) return ''
  if (ms < 10_000) return `${Math.round(ms).toLocaleString('en-US')} ms`
  if (ms < 60_000) return `${(ms / 1000).toFixed(1)} s`
  return duration(ms)
}

function callMs(call: ToolCall): number | undefined {
  if (!call.finished) return undefined
  const a = parseTime(call.started.event.t)
  const b = parseTime(call.finished.event.t)
  if (Number.isNaN(a) || Number.isNaN(b)) return undefined
  return Math.max(0, b - a)
}

// -------------------------------------------------------------------- sizes

/** Bytes as `624 B`, `1.1 KB`, `2.3 MB`. */
export function bytesLabel(n: number): string {
  if (n < 1000) return `${n} B`
  if (n < 1_000_000) return `${(n / 1000).toFixed(1)} KB`
  return `${(n / 1_000_000).toFixed(1)} MB`
}

export function byteLength(text: string): number {
  if (typeof TextEncoder === 'function') return new TextEncoder().encode(text).length
  return text.length
}

export function lineCount(text: string): number {
  if (text === '') return 0
  const trimmed = text.replace(/\n$/, '')
  return trimmed.split('\n').length
}

/** `17 ln · 1.1 KB`, the whole output as a size until the row is opened. */
export function sizeLabel(text: string): string {
  if (!text) return ''
  return `${lineCount(text)} ln · ${bytesLabel(byteLength(text))}`
}

// -------------------------------------------------------------------- rules

/** Tools that only read, so the policy never asks about them. */
const READ_ONLY_TOOLS = new Set(['Read', 'Glob', 'Grep', 'LS', 'NotebookRead', 'TodoRead', 'WebSearch'])

/** Tools that change the tree; their rows take the accent. */
export const WRITE_TOOLS = new Set([
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

const SHELL_TOOLS = /^(bash|shell|commandexecution|command_execution)$/i

/** True when a `permissions.bash` pattern (`git log*`, `rg *`) matches the command. */
export function patternMatches(pattern: string, command: string): boolean {
  const escaped = pattern
    .trim()
    .split('*')
    .map((part) => part.replace(/[.*+?^${}()|[\]\\]/g, '\\$&'))
    .join('.*')
  if (!escaped) return false
  return new RegExp(`^${escaped}$`, 's').test(command.trim())
}

export interface Permissions {
  bash: string[]
  fixBash: string[]
}

/**
 * The rule that decided a call, in the words the Tools table prints after
 * the decision. A denial names the allow-list it failed; a read-only tool
 * needs no rule; a shell command names the pattern that let it through, when
 * the workspace's allow-list is known.
 */
export function ruleFor(
  tool: string,
  command: string,
  decision: string,
  reason: string,
  kind: string | undefined,
  permissions: Permissions | undefined,
): string {
  if (decision === 'deny') {
    if (/permissions\.bash/i.test(reason)) return 'not in permissions.bash'
    if (/permissions\.mcp/i.test(reason)) return 'not in permissions.mcp'
    if (/permissions\.fetch/i.test(reason)) return 'not in permissions.fetch'
    return 'policy'
  }
  if (tool === 'StructuredOutput') return 'schema-checked'
  if (READ_ONLY_TOOLS.has(tool)) return 'read-only tool'
  if (WRITE_TOOLS.has(tool)) return kind === 'fix' ? 'fix worktree' : ''
  if (SHELL_TOOLS.test(tool) && permissions && command) {
    const list = kind === 'fix' ? permissions.fixBash : permissions.bash
    const hit = list.find((p) => patternMatches(p, command))
    if (hit) return hit
    // A pipeline is judged segment by segment; name the first pattern that
    // covers its first segment, which is the one the reader will recognise.
    const first = command.split(/\s*(?:&&|\|\||\||;)\s*/)[0] ?? ''
    const partial = list.find((p) => patternMatches(p, first))
    if (partial) return partial
  }
  return ''
}

// --------------------------------------------------------------------- rows

export type RowKind =
  | 'tool'
  | 'deny'
  | 'write'
  | 'final'
  | 'steer'
  | 'sys'
  | 'think'
  | 'done'
  | 'review'
  | 'ask'
  | 'error'
  | 'prose'

export interface ConsoleRow {
  /** Stable across renders: the index of the event the row stands for. */
  id: string
  index: number
  kind: RowKind
  at: string
  atMs: number
  /** The tool's name, or the row's word: `thinking`, `you`, `session`, `result`, `completed`. */
  tool: string
  summary: string
  /** The model's one-line description of why, when the tool input carried one. */
  description?: string
  duration?: string
  durationMs?: number
  /** `17 ln · 1.1 KB`, or `12,604 ch` for a structured answer. */
  output?: string
  outputBytes?: number
  decision?: string
  rule?: string
  /** The policy's reason, on a denied call. */
  reason?: string
  /** The paired call, for the expanded body. */
  call?: ToolCall
  /** The rail cell the row belongs to. */
  turn?: string
  /** A shell command that ran tests, so the expanded output flags its failures. */
  test?: boolean
  failed?: boolean
}

export type ConsoleFilter = 'all' | 'tools' | 'denied' | 'prose'

export const CONSOLE_FILTERS: { id: ConsoleFilter; label: string }[] = [
  { id: 'all', label: 'all' },
  { id: 'tools', label: 'tools' },
  { id: 'denied', label: 'denied' },
  { id: 'prose', label: 'prose' },
]

export function matchesConsoleFilter(row: ConsoleRow, filter: ConsoleFilter): boolean {
  switch (filter) {
    case 'all':
      return true
    case 'tools':
      return Boolean(row.call)
    case 'denied':
      return row.kind === 'deny'
    case 'prose':
      return (
        row.kind === 'prose' ||
        row.kind === 'think' ||
        row.kind === 'steer' ||
        row.kind === 'final' ||
        row.kind === 'ask' ||
        row.kind === 'error'
      )
  }
}

/** True when the query is found in what the row prints. */
export function matchesSearch(row: ConsoleRow, query: string): boolean {
  const q = query.trim().toLowerCase()
  if (!q) return true
  return [row.tool, row.summary, row.description ?? '', row.reason ?? '', row.rule ?? '']
    .join('\n')
    .toLowerCase()
    .includes(q)
}

function asRecord(value: unknown): Record<string, unknown> | undefined {
  if (typeof value !== 'object' || value === null || Array.isArray(value)) return undefined
  return value as Record<string, unknown>
}

function str(value: unknown): string {
  return typeof value === 'string' ? value : ''
}

/** The `description` a Claude tool input carries beside its command. */
export function toolDescription(event: RunEvent): string {
  const args = asRecord(toolInput(event))
  return args ? str(args.description) : ''
}

/** Claude's assistant lines carry thinking blocks; the text is usually withheld. */
function thinkingOf(event: RunEvent): { has: boolean; text: string } {
  const raw = asRecord(event.payload?.raw)
  const message = asRecord(raw?.message)
  const content = message?.content
  if (!Array.isArray(content)) return { has: false, text: '' }
  const block = content.map(asRecord).find((b) => b?.type === 'thinking')
  if (!block) return { has: false, text: '' }
  return { has: true, text: str(block.thinking) }
}

/** True for an assistant line that carries a tool_use or text block: it is a message, not just a tick. */
function hasVisibleBlock(event: RunEvent): boolean {
  const raw = asRecord(event.payload?.raw)
  const message = asRecord(raw?.message)
  const content = message?.content
  if (!Array.isArray(content)) return false
  return content.map(asRecord).some((b) => b?.type === 'tool_use' || (b?.type === 'text' && str(b.text) !== ''))
}

function shortCommit(commit: string | undefined): string {
  return commit ? commit.slice(0, 7) : ''
}

function flat(text: string, max = 200): string {
  const one = text.replace(/\s+/g, ' ').trim()
  return one.length > max ? `${one.slice(0, max - 1)}…` : one
}

function subtypeOf(e: IndexedEvent): string {
  const raw = asRecord(e.event.payload?.raw)
  return str(raw?.subtype) || str(raw?.type) || e.event.kind
}

/**
 * The housekeeping worth a row: a session starting or resuming, with its
 * hooks. Stream deltas, status ticks and token counters are the provider
 * talking to itself; the prose they spell is a row of its own, so they
 * are left out rather than folded into a "system ×20" the reader cannot
 * use.
 */
function keepSystem(items: IndexedEvent[]): IndexedEvent[] {
  return items.filter((i) => {
    const sub = subtypeOf(i)
    return sub === 'init' || sub.startsWith('hook_') || sub === 'rate_limited' || sub === 'error' || sub === 'compact_boundary'
  })
}

/** The summary a system run prints: `init · cwd … · 5 hooks SessionStart:startup`, or `resume · 4 hooks SessionStart:resume`. */
function systemSummary(items: IndexedEvent[]): string {
  const hooks = items.filter((i) => subtypeOf(i) === 'hook_started')
  const hookNames = [...new Set(hooks.map((i) => str(asRecord(i.event.payload?.raw)?.hook_name)).filter(Boolean))]
  const parts: string[] = []
  const init = items.find((i) => subtypeOf(i) === 'init')
  const resumed = hookNames.some((n) => /resume/i.test(n))
  if (resumed) parts.push('resume')
  else if (init) {
    const cwd = str(asRecord(init.event.payload?.raw)?.cwd)
    parts.push(cwd ? `init · cwd ${cwd.replace(/^\/Users\/[^/]+/, '~')}` : 'init')
  }
  if (hooks.length > 0) parts.push(`${hooks.length} ${hooks.length === 1 ? 'hook' : 'hooks'} ${hookNames.join(', ')}`)
  if (parts.length === 0) {
    const counted = new Map<string, number>()
    for (const i of items) counted.set(subtypeOf(i), (counted.get(subtypeOf(i)) ?? 0) + 1)
    return [...counted].map(([k, n]) => (n > 1 ? `${k} ×${n}` : k)).join(' · ')
  }
  return parts.join(' · ')
}

/** The model the provider named on its first assistant line, for a run whose state.json has none. */
export function modelOf(events: IndexedEvent[]): string {
  for (const { event } of events) {
    const message = asRecord(asRecord(event.payload?.raw)?.message)
    const model = str(message?.model)
    if (model) return model
  }
  return ''
}

/** The result line's figures: `turns 14 · $0.72 · api 101.7 s · 44,160 cache write · 157,945 cache read · 7,966 out`. */
function resultSummary(event: RunEvent): string {
  const raw = asRecord(event.payload?.raw)
  const usage = asRecord(raw?.usage)
  const parts: string[] = []
  if (event.payload?.turns !== undefined) parts.push(`turns ${event.payload.turns}`)
  if (event.payload?.costUsd !== undefined) parts.push(usd(event.payload.costUsd))
  const api = raw?.duration_api_ms
  if (typeof api === 'number') parts.push(`api ${(api / 1000).toFixed(1)} s`)
  const n = (v: unknown) => (typeof v === 'number' ? v.toLocaleString('en-US') : '')
  if (usage) {
    if (typeof usage.cache_creation_input_tokens === 'number') parts.push(`${n(usage.cache_creation_input_tokens)} cache write`)
    if (typeof usage.cache_read_input_tokens === 'number') parts.push(`${n(usage.cache_read_input_tokens)} cache read`)
    if (typeof usage.output_tokens === 'number') parts.push(`${n(usage.output_tokens)} out`)
  }
  return parts.join(' · ')
}

/** What the reviewer did to the commit after the run: `dropped ledger_test.go · hunk 1 in review`. */
function reviewRow(event: RunEvent): Pick<ConsoleRow, 'kind' | 'tool' | 'summary'> {
  const action = str(event.payload?.action)
  return {
    kind: 'review',
    tool: 'you',
    summary: `${action === 'drop' ? 'dropped' : action} ${str(event.payload?.path)} · hunk ${(event.payload?.hunk ?? 0) + 1} in review`,
  }
}

/** The title of a structured answer, for the final row and the rail. */
export function answerTitle(text: string | undefined): string {
  const answer = parseAnswer(text)
  if (!answer) return flat(text ?? '', 160)
  const title = str(answer.title) || str(answer.summary).split('\n')[0]
  return title || 'structured answer'
}

export interface BuildOptions {
  startedAt: string | undefined
  kind?: string
  permissions?: Permissions
}

/**
 * The log as rows. Calls are paired with their result and their permission
 * (`conversation()` does the pairing), consecutive raw system lines fold
 * into one `session` row, stream deltas are left out because the prose row
 * they spell is there, and a Claude message that carried only a thinking
 * block is a `thinking` row so the gaps in the timeline are accounted for.
 */
export function buildRows(events: IndexedEvent[], opts: BuildOptions): ConsoleRow[] {
  const rows: ConsoleRow[] = []
  const start = opts.startedAt
  const structured = events.some(
    (e) => e.event.kind === 'tool_started' && str(e.event.payload?.tool) === 'StructuredOutput',
  )

  const at = (t: string) => ({ at: offsetTenths(t, start), atMs: parseTime(t) })

  for (const item of conversation(events, true)) {
    if (item.kind === 'fold') {
      // `lib/events` files a review under `system`; it is the reader's own act.
      for (const r of item.items.filter((i) => i.event.kind === 'review')) {
        rows.push({ id: `e${r.index}`, index: r.index, ...at(r.event.t), ...reviewRow(r.event) })
      }
      const kept = keepSystem(item.items.filter((i) => i.event.kind !== 'review'))
      if (kept.length === 0) continue
      const first = kept[0]
      rows.push({
        id: `e${first.index}`,
        index: first.index,
        kind: 'sys',
        ...at(first.event.t),
        tool: 'session',
        summary: systemSummary(kept),
      })
      continue
    }

    if (item.kind === 'message') {
      const text = item.text.trim()
      if (!text) continue
      rows.push({
        id: `e${item.index}`,
        index: item.index,
        kind: 'prose',
        ...at(item.parts[0].event.t),
        tool: 'prose',
        summary: flat(text, 240),
      })
      continue
    }

    if (item.kind === 'call') {
      const { call } = item
      const started = call.started.event
      const tool = toolLabel(str(started.payload?.tool)) || 'tool'
      const decision = str(call.permission?.event.payload?.decision)
      const reason = str(call.permission?.event.payload?.text)
      const denied = decision === 'deny'
      const command = inputSummary(started)
      const description = toolDescription(started)
      const out = outputText(call.finished?.event)
      const ms = callMs(call)
      const isShell = SHELL_TOOLS.test(str(started.payload?.tool))
      const test = isShell && isTestCommand(command)
      const failed = !denied && outputFailed(call.finished?.event)

      let kind: RowKind = 'tool'
      if (denied) kind = 'deny'
      else if (tool === 'StructuredOutput') kind = 'final'
      else if (WRITE_TOOLS.has(str(started.payload?.tool))) kind = 'write'

      let summary = command
      let output = sizeLabel(out)
      if (kind === 'final') {
        const args = asRecord(toolInput(started))
        summary = str(args?.title) || str(args?.summary).split('\n')[0] || 'structured answer'
        output = `${inputJSON(started).length.toLocaleString('en-US')} ch`
      } else if (kind === 'write') {
        const args = asRecord(toolInput(started))
        const path = str(args?.file_path) || str(args?.path) || command
        summary = path.split(/[\\/]/).pop() || path
      } else if (test && out) {
        const lines = out.split('\n').map((l) => l.trim())
        const fail =
          lines.find((l) => /^(?:---\s*)?FAIL\b/.test(l)) ??
          lines.find((l) => /^Exit code [1-9]/.test(l))
        const ok = out.split('\n').find((l) => /^ok\s/.test(l.trim()))
        const verdict = /Exit code (\d+)/.exec(out)
        summary = `${command}  →  ${
          verdict && verdict[1] !== '0' ? `exit ${verdict[1]} · ` : ''
        }${flat(fail ?? ok ?? lines.find(Boolean) ?? '', 120)}`
      }

      rows.push({
        id: `e${call.started.index}`,
        index: call.started.index,
        kind,
        ...at(started.t),
        tool,
        summary,
        description: description || undefined,
        duration: durationLabel(ms),
        durationMs: ms,
        output: output || undefined,
        outputBytes: out ? byteLength(out) : undefined,
        decision: decision || (call.finished || kind === 'final' ? 'allow' : ''),
        rule: ruleFor(str(started.payload?.tool), command, decision, reason, opts.kind, opts.permissions),
        reason: denied ? reason : undefined,
        call,
        test: test || undefined,
        failed: failed || undefined,
      })
      continue
    }

    // A single event.
    const { event } = item.item
    const index = item.index
    const base = { id: `e${index}`, index, ...at(event.t) }
    if (event.kind === 'system') {
      if (keepSystem([item.item]).length === 0) continue
      rows.push({ ...base, kind: 'sys', tool: 'session', summary: systemSummary([item.item]) })
      continue
    }
    switch (event.kind) {
      case 'usage': {
        const raw = asRecord(event.payload?.raw)
        if (str(raw?.type) === 'result') break // the final row carries the figures
        if (hasVisibleBlock(event)) break // a message row already stands for it
        const thinking = thinkingOf(event)
        if (!thinking.has) break
        rows.push({
          ...base,
          kind: 'think',
          tool: 'thinking',
          summary: thinking.text ? flat(thinking.text, 240) : 'thinking · content not returned by the provider',
        })
        break
      }
      case 'final': {
        if (structured) {
          rows.push({ ...base, kind: 'sys', tool: 'result', summary: resultSummary(event) })
        } else {
          rows.push({
            ...base,
            kind: 'final',
            tool: 'answer',
            summary: answerTitle(event.payload?.text),
            output: event.payload?.text ? `${event.payload.text.length.toLocaleString('en-US')} ch` : undefined,
          })
        }
        break
      }
      case 'steer':
      case 'answer':
        rows.push({
          ...base,
          kind: 'steer',
          tool: 'you',
          summary: str(event.payload?.text),
          output: str(event.payload?.continuation) || (event.kind === 'answer' ? 'answer' : undefined),
        })
        break
      case 'review':
        rows.push({ ...base, ...reviewRow(event) })
        break
      case 'question':
        rows.push({ ...base, kind: 'ask', tool: 'AskUserQuestion', summary: flat(str(event.payload?.text), 240) })
        break
      case 'rate_limited':
        rows.push({ ...base, kind: 'sys', tool: 'rate limit', summary: flat(str(event.payload?.text) || 'rate limited') })
        break
      case 'error':
        rows.push({ ...base, kind: 'error', tool: 'error', summary: flat(str(event.payload?.text) || 'run failed') })
        break
      case 'tool_finished':
        // A result whose call the log lost: shown in full rather than dropped.
        rows.push({
          ...base,
          kind: 'tool',
          tool: toolLabel(str(event.payload?.tool)) || 'result',
          summary: flat(outputText(event), 160),
          output: sizeLabel(outputText(event)),
          call: { started: item.item, finished: item.item },
        })
        break
      case 'permission':
        rows.push({
          ...base,
          kind: str(event.payload?.decision) === 'deny' ? 'deny' : 'sys',
          tool: toolLabel(str(event.payload?.tool)) || 'permission',
          summary: str(event.payload?.decision) || 'permission',
          reason: str(event.payload?.text) || undefined,
          decision: str(event.payload?.decision),
        })
        break
      default:
        rows.push({ ...base, kind: 'sys', tool: event.kind, summary: flat(str(event.payload?.text) || event.kind) })
    }
  }
  return rows
}

/**
 * The line the run ended on, which the log does not write: the note a
 * triage filed, the branch a fix committed to, the reason a run failed.
 */
export function closingRow(detail: RunDetail, notesDir?: string): ConsoleRow | undefined {
  const base = {
    id: 'closing',
    index: Number.MAX_SAFE_INTEGER,
    at: offsetTenths(detail.updatedAt, detail.startedAt),
    atMs: parseTime(detail.updatedAt),
  }
  const turns = detail.usage?.turns ?? 0
  const figures = `${turns} ${turns === 1 ? 'turn' : 'turns'} · ${usd(detail.usage?.costUsd)}`
  switch (detail.status) {
    case 'completed': {
      if (detail.kind === 'fix') {
        const parts = [
          detail.fix?.branch ? `branch ${detail.fix.branch}` : '',
          detail.fix?.commit ? `commit ${shortCommit(detail.fix.commit)}` : '',
          detail.fix?.pushed ? 'pushed' : detail.fix?.commit ? 'local, not pushed' : '',
        ].filter(Boolean)
        return { ...base, kind: 'done', tool: 'completed', summary: parts.join(' · ') || figures }
      }
      const filed = detail.notes?.[detail.notes.length - 1]
      return {
        ...base,
        kind: 'done',
        tool: 'completed',
        summary: filed ? `note written → ${noteName(filed, notesDir)} · ${figures}` : figures,
      }
    }
    case 'failed':
      return { ...base, kind: 'error', tool: 'failed', summary: detail.reason || figures }
    case 'over_budget':
      return { ...base, kind: 'error', tool: 'over budget', summary: detail.reason || figures }
    default:
      return undefined
  }
}

export interface ConsoleCounts {
  events: number
  calls: number
  denied: number
  steers: number
  questions: number
  reviews: number
}

/** The header's figures. `events` is the rows the transcript shows: the deltas the provider streams are not things that happened. */
export function consoleCounts(rows: ConsoleRow[]): ConsoleCounts {
  return {
    events: rows.length,
    calls: rows.filter((r) => r.call).length,
    denied: rows.filter((r) => r.kind === 'deny').length,
    steers: rows.filter((r) => r.kind === 'steer').length,
    questions: rows.filter((r) => r.kind === 'ask').length,
    reviews: rows.filter((r) => r.kind === 'review').length,
  }
}

/** `22 events · 15 calls · 2 denied · 1 steer`, and the question or review when there is one. */
export function countsLabel(c: ConsoleCounts): string {
  const parts = [
    `${c.events} ${c.events === 1 ? 'event' : 'events'}`,
    `${c.calls} ${c.calls === 1 ? 'call' : 'calls'}`,
    `${c.denied} denied`,
  ]
  if (c.steers > 0) parts.push(`${c.steers} ${c.steers === 1 ? 'steer' : 'steers'}`)
  if (c.questions > 0) parts.push(`${c.questions} ${c.questions === 1 ? 'question' : 'questions'}`)
  if (c.reviews > 0) parts.push(`${c.reviews} review`)
  return parts.join(' · ')
}

// --------------------------------------------------------------------- rail

export type RailKind = 'tool' | 'deny' | 'final' | 'write' | 'ask' | 'think' | 'sys'

export interface RailCell {
  key: string
  /** The turn's number as the provider counted it, or `?` for the pending question. */
  n: string
  kind: RailKind
  firstIndex: number
  lastIndex: number
}

export type RailItem = RailCell | { key: string; sep: string }

export function isRailCell(item: RailItem): item is RailCell {
  return 'n' in item
}

const RAIL_PRIORITY: RailKind[] = ['ask', 'deny', 'final', 'write', 'tool', 'think', 'sys']

function railKindOf(events: IndexedEvent[]): RailKind {
  let best: RailKind = 'sys'
  const rank = (k: RailKind) => RAIL_PRIORITY.indexOf(k)
  const consider = (k: RailKind) => {
    if (rank(k) < rank(best)) best = k
  }
  for (const { event } of events) {
    switch (event.kind) {
      case 'question':
        consider('ask')
        break
      case 'permission':
        if (str(event.payload?.decision) === 'deny') consider('deny')
        break
      case 'final':
        consider('final')
        break
      case 'tool_started': {
        const tool = str(event.payload?.tool)
        if (tool === 'StructuredOutput') consider('final')
        else if (WRITE_TOOLS.has(tool)) consider('write')
        else consider('tool')
        break
      }
      case 'assistant_text':
        consider('think')
        break
      case 'usage':
        if (thinkingOf(event).has && !hasVisibleBlock(event)) consider('think')
        break
      default:
    }
  }
  return best
}

function hasSubstance(events: IndexedEvent[]): boolean {
  return events.some((e) => !['system', 'stream_event', 'usage'].includes(e.event.kind) || (e.event.kind === 'usage' && thinkingOf(e.event).has))
}

/**
 * The rail's cells: one per turn the provider counted, in file order, with a
 * separator where the operator steered (the provider's turn counter starts
 * again) or reviewed.
 *
 * A turn opens on the provider's assistant line — the `usage` event that
 * carries its number — and holds everything that follows it until the next
 * one: the tool calls that message made, their results, the policy's word.
 * A turn the provider numbered twice — a message that only thought, then the
 * call it led to — is one cell. Lines before the first counted turn ride
 * into it, and the result line and the final answer stay in the turn that
 * produced them.
 *
 * `turnOf` says which cell each event index landed in, so a console row can
 * find its cell and a cell its rows.
 */
export function railItems(
  events: IndexedEvent[],
  status?: string,
): { items: RailItem[]; turnOf: Map<number, string> } {
  const items: RailItem[] = []
  const turnOf = new Map<number, string>()
  let segment = 0
  let counter = 0
  let current: { cell: RailCell; events: IndexedEvent[] } | undefined
  /** Lines before the segment's first counted turn. */
  let leading: IndexedEvent[] = []

  const settle = () => {
    if (!current) return
    current.cell.kind = railKindOf(current.events)
    current.cell.lastIndex = current.events[current.events.length - 1]?.index ?? current.cell.lastIndex
    for (const e of current.events) turnOf.set(e.index, current.cell.key)
  }

  const open = (label: string, e: IndexedEvent) => {
    const key = `s${segment}-${label}`
    if (current && current.cell.key === key) {
      current.events.push(e)
      return
    }
    settle()
    const cell: RailCell = { key, n: label, kind: 'sys', firstIndex: e.index, lastIndex: e.index }
    items.push(cell)
    current = { cell, events: [...leading, e] }
    if (leading.length > 0) cell.firstIndex = leading[0].index
    leading = []
  }

  for (const e of events) {
    const kind = e.event.kind
    if (kind === 'steer' || kind === 'answer' || kind === 'review') {
      settle()
      current = undefined
      // Housekeeping with no turn to ride into is left off the rail.
      leading = []
      segment += 1
      counter = 0
      const sep = kind === 'review' ? 'review' : 'steer'
      const key = `sep${segment}-${sep}`
      items.push({ key, sep })
      if (kind === 'review') {
        const cell: RailCell = { key: `s${segment}-you`, n: 'you', kind: 'write', firstIndex: e.index, lastIndex: e.index }
        items.push(cell)
        turnOf.set(e.index, cell.key)
      } else {
        turnOf.set(e.index, key)
      }
      continue
    }
    if (kind === 'usage' && str(asRecord(e.event.payload?.raw)?.type) !== 'result') {
      const n = e.event.payload?.turns
      if (n !== undefined) counter = n
      else counter += 1
      open(String(n ?? counter), e)
      continue
    }
    if (current) current.events.push(e)
    else leading.push(e)
  }
  settle()

  // A run that has produced no counted turn yet still shows what it did.
  if (!current && leading.length > 0 && hasSubstance(leading)) {
    const cell: RailCell = {
      key: `s${segment}-1`,
      n: '1',
      kind: railKindOf(leading),
      firstIndex: leading[0].index,
      lastIndex: leading[leading.length - 1].index,
    }
    items.push(cell)
    for (const e of leading) turnOf.set(e.index, cell.key)
  }

  if (status === 'blocked' && !items.some((i) => isRailCell(i) && i.kind === 'ask')) {
    items.push({ key: 'ask', n: '?', kind: 'ask', firstIndex: Number.MAX_SAFE_INTEGER, lastIndex: Number.MAX_SAFE_INTEGER })
  }
  return { items, turnOf }
}

// ------------------------------------------------------------------- gauges

export interface Gauge {
  label: string
  value: string
  cap: string
  /** 0..100 */
  pct: number
  /** The figure is not known yet, so the bar is empty and the value grey. */
  na?: boolean
}

/** The three budget gauges: turns, minutes and cost against `state.json`'s caps. */
export function gauges(detail: RunDetail, now = Date.now()): Gauge[] {
  const live = detail.status === 'running' || detail.status === 'preparing'
  const turns = detail.usage?.turns ?? 0
  const maxTurns = detail.budget?.maxTurns ?? 0
  const start = parseTime(detail.startedAt)
  const end = live || detail.status === 'blocked' ? now : parseTime(detail.updatedAt) || now
  const elapsedMs = Number.isNaN(start) ? 0 : Math.max(0, end - start)
  const maxMinutes = detail.budget?.maxMinutes ?? 0
  const cost = detail.usage?.costUsd ?? 0
  const maxUsd = detail.budget?.maxUsd ?? 0
  const pct = (v: number, cap: number) => (cap > 0 ? Math.min(100, Math.round((v / cap) * 100)) : 0)
  const costKnown = cost > 0 || (!live && detail.status !== 'blocked')
  return [
    { label: 'turns', value: String(turns), cap: maxTurns ? `/ ${maxTurns}` : '', pct: pct(turns, maxTurns) },
    {
      label: 'minutes',
      value: duration(elapsedMs),
      cap: maxMinutes ? `/ ${duration(maxMinutes * 60_000)}` : '',
      pct: pct(elapsedMs, maxMinutes * 60_000),
    },
    {
      label: costKnown ? 'cost' : 'cost · at result',
      value: costKnown ? usd(cost) : '—',
      cap: maxUsd ? `/ ${usd(maxUsd)}` : '',
      pct: costKnown ? pct(cost, maxUsd) : 0,
      na: !costKnown || undefined,
    },
  ]
}

// ----------------------------------------------------------------- question

export interface Question {
  text: string
  /** Numbered or bulleted options found in the question, if it listed any. */
  options: string[]
  /** When the run stopped to ask. */
  since: string
}

/** The question a blocked run is waiting on, from its reason or its `question` event. */
export function pendingQuestion(detail: RunDetail, events: IndexedEvent[]): Question | undefined {
  if (detail.status !== 'blocked') return undefined
  const asked = [...events].reverse().find((e) => e.event.kind === 'question')
  const text = str(asked?.event.payload?.text) || askedQuestion(detail.reason) || detail.reason || ''
  const options: string[] = []
  const lines = text.split('\n')
  for (const line of lines) {
    const m = /^\s*(?:\d+[.)]|[-*•])\s+(.+)$/.exec(line)
    if (m) options.push(m[1].trim())
  }
  const body = options.length > 0 ? lines.filter((l) => !/^\s*(?:\d+[.)]|[-*•])\s+/.test(l)).join('\n').trim() : text
  return { text: body, options, since: asked?.event.t ?? detail.updatedAt }
}

// -------------------------------------------------------------------- misc

/** The id a call was made under, for the expanded body's header. */
export function callIdOf(call: ToolCall): string {
  return callId(call.started.event)
}

/** Which tool the answer document's file references were read by: the calls whose input names the file. */
export function callsNaming(rows: ConsoleRow[], file: string): ConsoleRow[] {
  const name = file.split(/[\\/]/).pop() ?? file
  if (!name) return []
  return rows.filter((r) => r.call && (r.summary.includes(name) || (r.description ?? '').includes(name)))
}
