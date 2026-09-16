import { useMemo } from 'react'
import type { RunDetail, RunEvent } from '../../api/types'
import {
  callId,
  conversation,
  inputSummary,
  outputFailed,
  outputText,
  parseAnswer,
  toolInput,
  toolLabel,
  type IndexedEvent,
  type ToolCall,
} from '../../lib/events'
import { parseTime } from '../../lib/format'
import { checksFromEvents, fixReport, type FixReport, type RunCheck } from '../../lib/review'
import { clock, exitCodeOf, sizeOf, type Size } from './shape'

/*
 * The session model: the run's event log read as a conversation, the way
 * the Conversation layout draws it. `src/screens/Session.tsx` loads the run
 * and this turns its events into the items the layout lays out — a start
 * line, stacks of tool cards, thinking stamps, the operator's steers, the
 * model's prose, the answer, a finish line — plus the flat list every
 * inspector tab reads (the calls for the Tools table, the checks for
 * Changes).
 *
 * This is the layout's own adapter until the shared `useSessionModel`
 * lands with the session blocks; its names follow that brief so the swap
 * is an import.
 */

function asRecord(value: unknown): Record<string, unknown> | undefined {
  if (typeof value !== 'object' || value === null || Array.isArray(value)) return undefined
  return value as Record<string, unknown>
}

function str(value: unknown): string {
  return typeof value === 'string' ? value : ''
}

/** What the policy said to a call, in the word the card stamps. */
export type Decision = 'denied' | 'approved' | 'accepted' | 'policy'

/** One tool call with everything the layout says about it. */
export interface StepCall {
  /** 1-based across the run, the Tools table's `#`. */
  n: number
  /** The index of the `tool_started` line: the card's anchor. */
  index: number
  call: ToolCall
  tool: string
  /** The model's own one-line description of the call, when the input carries one. */
  description: string
  /** The input in one line: the command, the file, the pattern. */
  summary: string
  /** The command or path verbatim, for the expanded Input section. */
  input: Record<string, unknown> | undefined
  at: string
  startedAt: string
  decision: Decision
  /** The policy's reason, when it refused or was asked. */
  reason: string
  /** The rule the provider suggested adding, when it asked: `go test *`. */
  suggestedRule: string
  output: string
  failed: boolean
  exitCode?: number
  /** Milliseconds from start to result; undefined while it runs. */
  tookMs?: number
  size: Size
  /** No result yet. */
  pending: boolean
  /** An Edit's rough size: lines added and removed by its replacement. */
  edit?: { added: number; removed: number; file: string }
}

export type ChatItem =
  /** A quiet line across the flow: the run starting, the run finishing. */
  | { kind: 'sys'; index: number; parts: (string | { b: string })[] }
  /** Consecutive tool calls, one stack; `head` when it is long enough to summarise. */
  | { kind: 'stack'; index: number; calls: StepCall[]; head?: string }
  /** The model thought and said nothing: how long, roughly how much. */
  | { kind: 'think'; index: number; seconds: number; tokens?: number; at: string }
  /** The structured answer being written: the StructuredOutput call. */
  | { kind: 'wrote'; index: number; what: string; seconds: number; from: string; to: string }
  /** The operator's own words. */
  | { kind: 'you'; index: number; text: string; at: string; continuation?: string }
  /** The model's prose. */
  | { kind: 'say'; index: number; text: string; at: string }
  /** A `final` event. Every one but the last is `superseded`. */
  | { kind: 'answer'; index: number; event: RunEvent; at: string; superseded: boolean; revised: boolean; seconds?: number }
  | { kind: 'error'; index: number; text: string; at: string }
  | { kind: 'callout'; index: number; text: string; at: string; rateLimited: boolean }

/** Run facts read off the provider's init line, when it wrote one. */
export interface RunStart {
  at: string
  model: string
  cwd: string
}

export interface SessionModel {
  items: ChatItem[]
  calls: StepCall[]
  /** The last `final` event's parsed JSON, when it is JSON. */
  answer?: Record<string, unknown>
  /** A fix run's report, read off the same event. */
  report?: FixReport
  /** The index of the item the finished run opens on. */
  answerIndex: number
  checks: RunCheck[]
  start?: RunStart
  /** The directory paths are shown relative to: the init line's cwd. */
  root: string
  /** The last tool call without a result, when the run is waiting on something. */
  pendingCall?: StepCall
  denied: number
  asked: number
  outBytes: number
}

const EMPTY: SessionModel = {
  items: [],
  calls: [],
  answerIndex: -1,
  checks: [],
  root: '',
  denied: 0,
  asked: 0,
  outBytes: 0,
}

/** Below this many calls a stack has no summary row. */
export const STACK_HEAD_MIN = 4

const PATH_FIELDS = ['file_path', 'filePath', 'path', 'notebook_path', 'target_file']

/** `/repo/app/ledger.go` under `/repo/app` is `ledger.go`; elsewhere, the path as written. */
export function relativeTo(path: string, root: string): string {
  if (!root) return path
  const dir = root.replace(/[\\/]+$/, '')
  if (path.startsWith(dir) && /[\\/]/.test(path.charAt(dir.length))) return path.slice(dir.length + 1)
  // A fix run's worktree sits beside the root; its files read the same way.
  const parent = dir.slice(0, dir.lastIndexOf('/'))
  if (parent && path.startsWith(parent) && /[\\/]/.test(path.charAt(parent.length))) {
    return path.slice(parent.length + 1)
  }
  return path
}

/**
 * A command with everything above the workspace's parent folded to `…`, so
 * `/Users/me/Documents/Personal/sirdar-sandbox/app` reads `…/sirdar-sandbox/app`
 * and the line fits.
 */
export function shortenPaths(command: string, root: string): string {
  if (!root) return command
  const dir = root.replace(/[\\/]+$/, '')
  const parent = dir.slice(0, dir.lastIndexOf('/'))
  const above = parent.slice(0, parent.lastIndexOf('/'))
  if (!above) return command
  return command.split(above).join('…')
}

function lineCount(text: string): number {
  if (text === '') return 0
  return text.split('\n').length
}

function decisionOf(call: ToolCall): { decision: Decision; reason: string; suggestedRule: string } {
  const permission = call.permission?.event
  if (!permission) return { decision: 'policy', reason: '', suggestedRule: '' }
  const raw = asRecord(permission.payload?.raw)
  const request = asRecord(raw?.request)
  const suggestions = Array.isArray(request?.permission_suggestions) ? request.permission_suggestions : []
  let suggestedRule = ''
  let setMode = ''
  for (const s of suggestions) {
    const sug = asRecord(s)
    if (!sug) continue
    if (sug.type === 'setMode') setMode = str(sug.mode)
    if (sug.type === 'addRules' && Array.isArray(sug.rules)) {
      const rule = asRecord(sug.rules[0])
      if (rule) suggestedRule = str(rule.ruleContent)
    }
  }
  const text = str(permission.payload?.text) || str(request?.decision_reason)
  if (permission.payload?.decision === 'deny') {
    return { decision: 'denied', reason: text.replace(/^Sirdar policy:\s*/i, ''), suggestedRule }
  }
  return { decision: setMode ? 'accepted' : 'approved', reason: setMode || text, suggestedRule }
}

function stepOf(n: number, call: ToolCall, startedAt: string | undefined, root: string): StepCall {
  const started = call.started.event
  const tool = toolLabel(str(started.payload?.tool)) || 'tool'
  const input = asRecord(toolInput(started))
  const description = str(input?.description)
  let summary = inputSummary(started)
  let edit: StepCall['edit'] | undefined
  if (input) {
    const path = PATH_FIELDS.map((f) => str(input[f])).find(Boolean) ?? ''
    const command = str(input.command) || str(input.cmd)
    if (command) summary = shortenPaths(command, root)
    else if (path) summary = relativeTo(path, root)
    if (/^(?:edit|multiedit|write|replace|edit_file)$/i.test(tool) && path) {
      const oldLines = lineCount(str(input.old_string))
      const newLines = lineCount(str(input.new_string) || str(input.content))
      edit = {
        file: relativeTo(path, root),
        added: Math.max(0, newLines - oldLines),
        removed: Math.max(0, oldLines - newLines),
      }
      summary = edit.file
    }
  }
  const { decision, reason, suggestedRule } = decisionOf(call)
  const output = outputText(call.finished?.event)
  const exitCode = exitCodeOf(output)
  const failed = decision !== 'denied' && (outputFailed(call.finished?.event) || (exitCode !== undefined && exitCode !== 0))
  const a = parseTime(started.t)
  const b = parseTime(call.finished?.event.t)
  const tookMs = call.finished && !Number.isNaN(a) && !Number.isNaN(b) ? Math.max(0, b - a) : undefined
  return {
    n,
    index: call.started.index,
    call,
    tool,
    description,
    summary,
    input,
    at: clock(started.t, startedAt),
    startedAt: started.t,
    decision,
    reason,
    suggestedRule,
    output,
    failed,
    exitCode,
    tookMs,
    size: sizeOf(output),
    pending: !call.finished,
    edit,
  }
}

/** True for the provider's own bookkeeping lines: token deltas, hooks, status. */
function isNoise(event: RunEvent): boolean {
  const raw = asRecord(event.payload?.raw)
  const type = str(raw?.type)
  if (type === 'stream_event') return true
  if (type === 'system') {
    const subtype = str(raw?.subtype)
    return subtype !== 'init' && subtype !== 'thinking_tokens'
  }
  return false
}

/** `claude-opus-5[1m]` → `claude-opus-5`. */
function modelName(model: string): string {
  return model.replace(/\[.*?\]$/, '')
}

/**
 * Reads the run's events as the conversation the layout draws.
 *
 * Calls are paired with their results and permissions by `conversation()`;
 * consecutive calls become one stack; a thinking burst — the provider's
 * `thinking_tokens` ticks with nothing said — becomes one stamp; the
 * StructuredOutput call that carries the answer becomes a "Wrote the
 * answer" stamp rather than a card, since the card that follows is the
 * answer itself.
 */
export function buildSessionModel(events: IndexedEvent[], detail: RunDetail | null): SessionModel {
  if (events.length === 0 && !detail) return EMPTY
  const startedAt = detail?.startedAt
  const items: ChatItem[] = []
  const calls: StepCall[] = []
  let root = ''
  let start: RunStart | undefined
  let stack: StepCall[] | undefined
  let think: { first: string; tokens: number; index: number } | undefined
  let finals = 0
  let steered = false
  let lastBefore = ''

  const closeStack = () => {
    if (!stack) return
    const done = stack
    stack = undefined
    const item = items.find((i) => i.kind === 'stack' && i.calls === done)
    if (item && item.kind === 'stack' && done.length >= STACK_HEAD_MIN) {
      const first = done[0]
      const last = done[done.length - 1]
      const clean = done.every((c) => c.decision !== 'denied' && !c.failed)
      item.head = `${done.length} calls · ${first.at} – ${last.at}${clean ? ' · all within policy' : ''}`
    }
  }
  const flushThink = (endT: string) => {
    if (!think) return
    const a = parseTime(think.first)
    const b = parseTime(endT)
    const seconds = Number.isNaN(a) || Number.isNaN(b) ? 0 : Math.max(0, Math.round((b - a) / 1000))
    items.push({ kind: 'think', index: think.index, seconds, tokens: think.tokens || undefined, at: clock(endT, startedAt) })
    think = undefined
  }
  const flush = (t: string) => {
    closeStack()
    flushThink(t)
  }

  // The init line comes first, and the root every path is shown against
  // comes from it, so it is read before the calls are.
  for (const { event } of events) {
    const raw = asRecord(event.payload?.raw)
    if (raw?.type === 'system' && raw.subtype === 'init') {
      root = str(raw.cwd)
      start = { at: clock(event.t, startedAt), model: modelName(str(raw.model)), cwd: root }
      break
    }
  }

  for (const row of conversation(events, false)) {
    if (row.kind === 'fold') continue

    if (row.kind === 'call') {
      const tool = str(row.call.started.event.payload?.tool)
      if (/^structuredoutput$/i.test(tool)) {
        flush(row.call.started.event.t)
        // The provider writes the call once its input has streamed, so the
        // writing time is from whatever came before it; a call that itself
        // took time is measured from its own start.
        const startT = row.call.started.event.t
        const endT = (row.call.finished ?? row.call.started).event.t
        const own = parseTime(endT) - parseTime(startT)
        const fromT = own >= 1000 || !lastBefore ? startT : lastBefore
        const a = parseTime(fromT)
        const b = parseTime(endT)
        items.push({
          kind: 'wrote',
          index: row.index,
          what: detail?.kind === 'fix' ? 'Wrote the report' : 'Wrote the answer',
          seconds: Number.isNaN(a) || Number.isNaN(b) ? 0 : Math.max(0, Math.round((b - a) / 1000)),
          from: clock(fromT, startedAt),
          to: clock(endT, startedAt),
        })
        lastBefore = (row.call.finished ?? row.call.started).event.t
        continue
      }
      flushThink(row.call.started.event.t)
      const step = stepOf(calls.length + 1, row.call, startedAt, root)
      calls.push(step)
      if (!stack) {
        stack = [step]
        items.push({ kind: 'stack', index: row.index, calls: stack })
      } else {
        stack.push(step)
      }
      lastBefore = (row.call.finished ?? row.call.started).event.t
      continue
    }

    if (row.kind === 'message') {
      flush(row.parts[0].event.t)
      items.push({ kind: 'say', index: row.index, text: row.text, at: clock(row.parts[0].event.t, startedAt) })
      lastBefore = row.parts[row.parts.length - 1].event.t
      continue
    }

    const event = row.item.event
    const raw = asRecord(event.payload?.raw)
    switch (event.kind) {
      case 'steer':
      case 'answer':
        flush(event.t)
        steered = true
        items.push({
          kind: 'you',
          index: row.index,
          text: str(event.payload?.text),
          at: clock(event.t, startedAt),
          continuation: event.payload?.continuation,
        })
        lastBefore = event.t
        break
      case 'final': {
        flush(event.t)
        finals += 1
        const wrote = items[items.length - 1]
        items.push({
          kind: 'answer',
          index: row.index,
          event,
          at: clock(event.t, startedAt),
          superseded: false,
          revised: steered && finals > 1,
          seconds: wrote?.kind === 'wrote' ? wrote.seconds : undefined,
        })
        lastBefore = event.t
        break
      }
      case 'error':
        flush(event.t)
        items.push({ kind: 'error', index: row.index, text: str(event.payload?.text) || 'run failed', at: clock(event.t, startedAt) })
        break
      case 'question':
      case 'rate_limited':
        flush(event.t)
        items.push({
          kind: 'callout',
          index: row.index,
          text: str(event.payload?.text),
          at: clock(event.t, startedAt),
          rateLimited: event.kind === 'rate_limited',
        })
        break
      case 'usage':
        break
      case 'tool_finished':
      case 'permission':
        // Unpaired: the log lost its call. Nothing to draw it under.
        break
      default: {
        if (raw?.type === 'system' && raw.subtype === 'thinking_tokens') {
          closeStack()
          const tokens = typeof raw.estimated_tokens === 'number' ? raw.estimated_tokens : 0
          if (!think) think = { first: event.t, tokens, index: row.index }
          else think.tokens = Math.max(think.tokens, tokens)
          break
        }
        if (raw?.type === 'system' && raw.subtype === 'init' && start) {
          const words: (string | { b: string })[] = ['Run started ', { b: start.at || '00:00' }]
          if (detail?.provider) words.push(` · ${detail.provider}`)
          if (start.model) words.push(` · ${start.model}`)
          if (detail?.kind) words.push(` · ${detail.kind}`, detail.kind === 'fix' ? ' · worktree' : ' · read-only')
          if (detail?.budget) {
            words.push(` · budget ${detail.budget.maxTurns} turns / ${detail.budget.maxMinutes} min / $${detail.budget.maxUsd}`)
          }
          items.push({ kind: 'sys', index: row.index, parts: words })
          break
        }
        if (isNoise(event)) break
        // Anything else the provider said in its own words: a rate-limit
        // warning, a stall notice. Small and grey.
        const text = str(event.payload?.text) || event.kind
        if (text && text !== event.kind) {
          items.push({ kind: 'sys', index: row.index, parts: [text] })
        }
      }
    }
  }
  flush(events[events.length - 1]?.event.t ?? '')

  // Every answer but the last is the one a steer superseded.
  let seen = 0
  for (const item of items) {
    if (item.kind === 'answer') {
      seen += 1
      item.superseded = seen < finals
    }
  }

  const runEvents = events.map((e) => e.event)
  const lastFinal = [...runEvents].reverse().find((e) => e.kind === 'final')
  const answer = parseAnswer(lastFinal?.payload?.text)
  const report = fixReport(runEvents)
  const answerItem = [...items].reverse().find((i) => i.kind === 'answer' && !i.superseded)

  const pendingCall = [...calls].reverse().find((c) => c.pending)

  return {
    items,
    calls,
    answer,
    report,
    answerIndex: answerItem?.index ?? -1,
    checks: checksFromEvents(runEvents),
    start,
    root,
    pendingCall,
    denied: calls.filter((c) => c.decision === 'denied').length,
    asked: calls.filter((c) => c.decision === 'approved' || c.decision === 'accepted').length,
    outBytes: calls.reduce((n, c) => n + c.size.bytes, 0),
  }
}

/** The model, memoised on the events and the run. */
export function useSessionModel(events: IndexedEvent[], detail: RunDetail | null): SessionModel {
  return useMemo(() => buildSessionModel(events, detail), [events, detail])
}

/** The id that ties a call to the events around it; exported for the table's pairing. */
export { callId }

/**
 * The call a file:line reference points at: the first call that read or
 * searched the file it names. `ref` is `ledger.go:33` or `ledger.go:27-34`;
 * the match is on the file name, since the answer writes paths relative to
 * the module and the call wrote them relative to the root.
 */
export function callForRef(calls: StepCall[], ref: string): StepCall | undefined {
  const file = ref.split(':')[0].split('/').pop() ?? ''
  if (!file) return undefined
  return calls.find((c) => /^(?:read|read_file)$/i.test(c.tool) && c.summary.split('/').pop() === file)
    ?? calls.find((c) => c.output.includes(`${file}:`) || c.summary.includes(file))
}
