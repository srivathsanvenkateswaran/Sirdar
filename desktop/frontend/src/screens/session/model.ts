import { useMemo, useRef } from 'react'
import type { RunDetail, RunEvent } from '../../api/types'
import {
  callId,
  classify,
  inputSummary,
  isReplace,
  joinText,
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
 * Reads the run's events as the conversation the layout draws, one event at
 * a time so a streaming run can carry on from where the last render left
 * off instead of reading the whole log again for every line.
 *
 * Calls are paired with their results and permissions here rather than by
 * `conversation()` — the pairing is why a rebuild was needed at all, since
 * a result arriving now changes a card drawn a minute ago. The rows it
 * produces are the rows `conversation(events, false)` produces, in the same
 * order, and what it makes of them is what the whole-log build made.
 *
 * Consecutive calls become one stack; a thinking burst — the provider's
 * `thinking_tokens` ticks with nothing said — becomes one stamp; the
 * StructuredOutput call that carries the answer becomes a "Wrote the
 * answer" stamp rather than a card, since the card that follows is the
 * answer itself.
 *
 * Every row it has already emitted keeps its object identity until its own
 * content changes, which is what lets a memoised card sit still while the
 * line below it arrives.
 */
class SessionBuilder {
  /** How many events have been consumed: the length of the prefix built. */
  count = 0

  private readonly detail: RunDetail | null
  private readonly startedAt: string | undefined

  private items: ChatItem[] = []
  private calls: StepCall[] = []
  private runEvents: RunEvent[] = []

  // --- pairing, the part `conversation()` does in one pass
  /** Calls started and not yet finished, oldest first. */
  private open: ToolCall[] = []
  /** The step each call became, and which item holds it. */
  private held = new Map<ToolCall, { step: StepCall; item: number }>()
  /**
   * The StructuredOutput calls: which item carries the stamp, and what the
   * writing was measured from, so the stamp can be drawn again when the
   * call comes back with a finish time.
   */
  private stamps = new Map<ToolCall, { at: number; before: string }>()
  /** The message the next assistant line continues, when it may. */
  private message: { at: number; parts: IndexedEvent[]; sealed: boolean } | null = null

  // --- the loop's own state
  private root = ''
  private start: RunStart | undefined
  /** The open stack: which item holds it, and the steps in it. */
  private stack: { at: number; calls: StepCall[] } | undefined
  private think: { first: string; tokens: number; index: number } | undefined
  private finals = 0
  private steered = false
  private starts = 0
  private lastBefore = ''
  /** The call `lastBefore` came from, so its result moves it on. */
  private lastBeforeCall: ToolCall | null = null
  private lastT = ''
  /** Where each answer item sits, so the answers a steer superseded are marked. */
  private answers: number[] = []

  // --- what the snapshot hands back, rebuilt only when it has to
  private itemsOut: ChatItem[] = []
  private callsOut: StepCall[] = []
  private itemsStale = true
  private callsStale = true
  private trail = '\n'
  /** The whole-log reads, kept until an event of a kind they read lands. */
  private derived: { answer?: Record<string, unknown>; report?: FixReport; checks: RunCheck[] } | null = null

  constructor(detail: RunDetail | null) {
    this.detail = detail
    this.startedAt = detail?.startedAt
  }

  /** Consumes one event, updating whichever rows it changes. */
  push(row: IndexedEvent): void {
    this.count += 1
    const event = row.event
    this.runEvents.push(event)
    this.lastT = event.t
    if (DERIVED_FROM.has(event.kind)) this.derived = null

    const raw = asRecord(event.payload?.raw)
    // The root every path is shown against comes off the provider's init
    // line. It is all but always the first line of the log; when it is not,
    // the calls already stepped are stepped again against it, so what the
    // reader sees does not depend on where in the log it turned up.
    if (!this.start && raw?.type === 'system' && raw.subtype === 'init') {
      this.root = str(raw.cwd)
      this.start = { at: clock(event.t, this.startedAt), model: modelName(str(raw.model)), cwd: this.root }
      if (this.root) for (const call of [...this.held.keys()]) this.restep(call)
    }

    switch (classify(event)) {
      case 'tool':
        if (event.kind === 'tool_started') this.startCall(row)
        else this.finishCall(row)
        return
      case 'permission':
        this.permit(row)
        return
      case 'text':
        this.say(row)
        return
      default:
        // Every other class is a row of its own, so no assistant line
        // after it continues the message before it.
        this.message = null
        this.event(row)
    }
  }

  // ------------------------------------------------------------- pairing

  private startCall(row: IndexedEvent): void {
    this.message = null
    const call: ToolCall = { started: row }
    this.open.push(call)
    this.callRow(row.index, call)
  }

  private finishCall(row: IndexedEvent): void {
    const event = row.event
    const id = callId(event)
    const tool = str(event.payload?.tool)
    let at = id ? this.open.findIndex((c) => callId(c.started.event) === id) : -1
    if (at === -1 && tool) at = this.open.findIndex((c) => str(c.started.event.payload?.tool) === tool)
    if (at === -1 && this.open.length > 0) at = 0
    if (at === -1) {
      // Unpaired: the log lost its call. It is a row of its own, which the
      // layout draws nothing for, and it ends any open message.
      this.message = null
      return
    }
    const call = this.open[at]
    call.finished = row
    this.open.splice(at, 1)
    this.restep(call)
  }

  private permit(row: IndexedEvent): void {
    const event = row.event
    const id = callId(event)
    const tool = str(event.payload?.tool)
    const newest = [...this.open].reverse()
    let call = id ? newest.find((c) => callId(c.started.event) === id) : undefined
    if (!call) {
      call = newest.find((c) => !c.permission && (!tool || str(c.started.event.payload?.tool) === tool))
    }
    if (!call || call.permission) {
      this.message = null
      return
    }
    call.permission = row
    this.restep(call)
  }

  private say(row: IndexedEvent): void {
    const open = this.message
    if (open && !open.sealed) {
      open.parts.push(row)
      const item = this.items[open.at]
      if (item.kind === 'say') this.replace(open.at, { ...item, text: joinText(open.parts) })
      this.mark(row.event.t, null)
      // The finished block closes the message it completes: the next
      // assistant line is the next message, not more of this one.
      if (isReplace(row.event)) open.sealed = true
      return
    }
    this.flush(row.event.t)
    const parts = [row]
    const at = this.items.length
    this.add({ kind: 'say', index: row.index, text: joinText(parts), at: clock(row.event.t, this.startedAt) })
    this.message = { at, parts, sealed: isReplace(row.event) }
    this.mark(row.event.t, null)
  }

  // -------------------------------------------------------- the row loop

  private callRow(index: number, call: ToolCall): void {
    if (/^structuredoutput$/i.test(str(call.started.event.payload?.tool))) {
      this.flush(call.started.event.t)
      this.stamps.set(call, { at: this.items.length, before: this.lastBefore })
      this.add(this.stamp(index, call, this.lastBefore))
      this.mark((call.finished ?? call.started).event.t, call)
      return
    }
    this.flushThink(call.started.event.t)
    const step = stepOf(this.calls.length + 1, call, this.startedAt, this.root)
    this.calls.push(step)
    this.callsStale = true
    if (!this.stack) {
      this.stack = { at: this.items.length, calls: [step] }
      this.add({ kind: 'stack', index, calls: this.stack.calls })
    } else {
      this.stack.calls = [...this.stack.calls, step]
      const item = this.items[this.stack.at]
      if (item.kind === 'stack') this.replace(this.stack.at, { ...item, calls: this.stack.calls })
    }
    this.held.set(call, { step, item: this.stack.at })
    this.mark((call.finished ?? call.started).event.t, call)
  }

  /**
   * The "Wrote the answer" stamp. The provider writes the StructuredOutput
   * call once its input has streamed, so the writing time is from whatever
   * came before it; a call that itself took time is measured from its own
   * start.
   */
  private stamp(index: number, call: ToolCall, before: string): ChatItem {
    const startT = call.started.event.t
    const endT = (call.finished ?? call.started).event.t
    const own = parseTime(endT) - parseTime(startT)
    const fromT = own >= 1000 || !before ? startT : before
    return {
      kind: 'wrote',
      index,
      what: this.detail?.kind === 'fix' ? 'Wrote the report' : 'Wrote the answer',
      seconds: secondsBetween(fromT, endT),
      from: clock(fromT, this.startedAt),
      to: clock(endT, this.startedAt),
    }
  }

  private event(row: IndexedEvent): void {
    const event = row.event
    const raw = asRecord(event.payload?.raw)
    switch (event.kind) {
      case 'steer':
      case 'answer':
        this.flush(event.t)
        this.steered = true
        this.add({
          kind: 'you',
          index: row.index,
          text: str(event.payload?.text),
          at: clock(event.t, this.startedAt),
          continuation: event.payload?.continuation,
        })
        this.mark(event.t, null)
        return
      case 'final': {
        this.flush(event.t)
        this.finals += 1
        const wrote = this.items[this.items.length - 1]
        // Every answer but the last is one a steer superseded.
        for (const at of this.answers) {
          const item = this.items[at]
          if (item.kind === 'answer' && !item.superseded) this.replace(at, { ...item, superseded: true })
        }
        this.answers.push(this.items.length)
        this.add({
          kind: 'answer',
          index: row.index,
          event,
          at: clock(event.t, this.startedAt),
          superseded: false,
          revised: this.steered && this.finals > 1,
          seconds: wrote?.kind === 'wrote' ? wrote.seconds : undefined,
        })
        this.mark(event.t, null)
        return
      }
      case 'error':
        this.flush(event.t)
        this.add({
          kind: 'error',
          index: row.index,
          text: str(event.payload?.text) || 'run failed',
          at: clock(event.t, this.startedAt),
        })
        return
      case 'question':
      case 'rate_limited':
        this.flush(event.t)
        this.add({
          kind: 'callout',
          index: row.index,
          text: str(event.payload?.text),
          at: clock(event.t, this.startedAt),
          rateLimited: event.kind === 'rate_limited',
        })
        return
      case 'usage':
        return
      case 'tool_finished':
      case 'permission':
        // Unpaired: the log lost its call. Nothing to draw it under.
        return
      default: {
        if (raw?.type === 'system' && raw.subtype === 'thinking_tokens') {
          this.closeStack()
          const tokens = typeof raw.estimated_tokens === 'number' ? raw.estimated_tokens : 0
          if (!this.think) this.think = { first: event.t, tokens, index: row.index }
          else this.think.tokens = Math.max(this.think.tokens, tokens)
          return
        }
        if (raw?.type === 'system' && raw.subtype === 'init' && this.start) {
          // The provider writes an init line per session: the run's own,
          // and one more each time a steer resumes it.
          if (this.starts > 0) {
            const model = modelName(str(raw.model))
            this.starts += 1
            this.add({
              kind: 'sys',
              index: row.index,
              parts: ['Resumed ', { b: clock(event.t, this.startedAt) }, model ? ` · ${model}` : ''],
            })
            return
          }
          this.starts += 1
          const detail = this.detail
          const words: (string | { b: string })[] = ['Run started ', { b: this.start.at || '00:00' }]
          if (detail?.provider) words.push(` · ${detail.provider}`)
          if (this.start.model) words.push(` · ${this.start.model}`)
          if (detail?.kind) words.push(` · ${detail.kind}`, detail.kind === 'fix' ? ' · worktree' : ' · read-only')
          if (detail?.budget) {
            words.push(` · budget ${detail.budget.maxTurns} turns / ${detail.budget.maxMinutes} min / $${detail.budget.maxUsd}`)
          }
          this.add({ kind: 'sys', index: row.index, parts: words })
          return
        }
        if (isNoise(event)) return
        // Anything else the provider said in its own words: a rate-limit
        // warning, a stall notice. Small and grey.
        const text = str(event.payload?.text) || event.kind
        if (text && text !== event.kind) {
          this.add({ kind: 'sys', index: row.index, parts: [text] })
        }
      }
    }
  }

  // ------------------------------------------------------------ plumbing

  private add(item: ChatItem): void {
    this.items.push(item)
    this.itemsStale = true
  }

  private replace(at: number, item: ChatItem): void {
    this.items[at] = item
    this.itemsStale = true
  }

  /** Remembers what the next writing stamp measures from. */
  private mark(t: string, call: ToolCall | null): void {
    this.lastBefore = t
    this.lastBeforeCall = call
  }

  /** Steps a call again after its result, its permission or the root moved. */
  private restep(call: ToolCall): void {
    if (this.lastBeforeCall === call) this.lastBefore = (call.finished ?? call.started).event.t

    const stamped = this.stamps.get(call)
    if (stamped) {
      const item = this.items[stamped.at]
      if (item.kind === 'wrote') this.replace(stamped.at, this.stamp(item.index, call, stamped.before))
      return
    }

    const was = this.held.get(call)
    if (!was) return
    const step = stepOf(was.step.n, call, this.startedAt, this.root)
    this.held.set(call, { step, item: was.item })
    this.calls[step.n - 1] = step
    this.callsStale = true

    const item = this.items[was.item]
    if (item.kind !== 'stack') return
    const next = item.calls.slice()
    const seat = next.indexOf(was.step)
    if (seat === -1) return
    next[seat] = step
    if (this.stack?.at === was.item) this.stack.calls = next
    // A stack closed before one of its calls came back keeps a summary
    // row, and that row says whether every call in it stayed within
    // policy — so it is written again too.
    this.replace(was.item, { ...item, calls: next, ...(item.head === undefined ? {} : { head: headOf(next) }) })
  }

  private closeStack(): void {
    const open = this.stack
    if (!open) return
    this.stack = undefined
    if (open.calls.length < STACK_HEAD_MIN) return
    const item = this.items[open.at]
    if (item.kind === 'stack') this.replace(open.at, { ...item, head: headOf(open.calls) })
  }

  private flushThink(endT: string): void {
    const think = this.think
    if (!think) return
    this.think = undefined
    this.add({
      kind: 'think',
      index: think.index,
      seconds: secondsBetween(think.first, endT),
      tokens: think.tokens || undefined,
      at: clock(endT, this.startedAt),
    })
  }

  private flush(t: string): void {
    this.closeStack()
    this.flushThink(t)
  }

  /**
   * The model as it stands. A stack or a thinking burst still open is
   * closed on a copy, not in place: the next line may go on adding to it,
   * where the whole-log build could close it for good because it only ever
   * ran once.
   */
  snapshot(): SessionModel {
    if (this.count === 0 && !this.detail) return EMPTY

    const open = this.stack
    const head = open && open.calls.length >= STACK_HEAD_MIN ? headOf(open.calls) : ''
    const think = this.think
      ? {
          kind: 'think' as const,
          index: this.think.index,
          seconds: secondsBetween(this.think.first, this.lastT),
          tokens: this.think.tokens || undefined,
          at: clock(this.lastT, this.startedAt),
        }
      : null
    const trail = `${head}\n${think ? `${think.index}:${think.seconds}:${think.tokens ?? ''}:${think.at}` : ''}`

    if (this.itemsStale || trail !== this.trail) {
      const items = this.items.slice()
      if (open && head) {
        const item = items[open.at]
        if (item.kind === 'stack') items[open.at] = { ...item, head }
      }
      if (think) items.push(think)
      this.itemsOut = items
      this.itemsStale = false
      this.trail = trail
    }
    if (this.callsStale) {
      this.callsOut = this.calls.slice()
      this.callsStale = false
    }
    const items = this.itemsOut
    const calls = this.callsOut

    if (!this.derived) {
      const lastFinal = [...this.runEvents].reverse().find((e) => e.kind === 'final')
      this.derived = {
        answer: parseAnswer(lastFinal?.payload?.text),
        report: fixReport(this.runEvents),
        checks: checksFromEvents(this.runEvents),
      }
    }

    // Only the last answer is not superseded, so it is the one the
    // finished run opens on.
    const last = this.answers[this.answers.length - 1]
    const answerItem = last === undefined ? undefined : items[last]
    let pendingCall: StepCall | undefined
    for (let i = calls.length - 1; i >= 0; i -= 1) {
      if (calls[i].pending) {
        pendingCall = calls[i]
        break
      }
    }

    return {
      items,
      calls,
      answer: this.derived.answer,
      report: this.derived.report,
      answerIndex: answerItem?.kind === 'answer' && !answerItem.superseded ? answerItem.index : -1,
      checks: this.derived.checks,
      start: this.start,
      root: this.root,
      pendingCall,
      denied: calls.filter((c) => c.decision === 'denied').length,
      asked: calls.filter((c) => c.decision === 'approved' || c.decision === 'accepted').length,
      outBytes: calls.reduce((n, c) => n + c.size.bytes, 0),
    }
  }
}

/** The event kinds the whole-log reads in `snapshot` are derived from. */
const DERIVED_FROM: ReadonlySet<string> = new Set(['final', 'tool_started', 'tool_finished'])

/** A stack's summary row: how many calls, over what stretch, and whether it was clean. */
function headOf(calls: StepCall[]): string {
  const first = calls[0]
  const last = calls[calls.length - 1]
  const clean = calls.every((c) => c.decision !== 'denied' && !c.failed)
  return `${calls.length} calls · ${first.at} – ${last.at}${clean ? ' · all within policy' : ''}`
}

/** Whole seconds between two stamps; zero when either cannot be read. */
function secondsBetween(from: string, to: string): number {
  const a = parseTime(from)
  const b = parseTime(to)
  return Number.isNaN(a) || Number.isNaN(b) ? 0 : Math.max(0, Math.round((b - a) / 1000))
}

/** Reads a run's whole event log as the conversation the layout draws. */
export function buildSessionModel(events: IndexedEvent[], detail: RunDetail | null): SessionModel {
  const builder = new SessionBuilder(detail)
  for (const row of events) builder.push(row)
  return builder.snapshot()
}

/**
 * The model, kept across renders.
 *
 * While a run streams, the events array is all but always the array from
 * the last render with a few more lines on the end. Rebuilding the whole
 * model for each of them handed every row a new object, so every card in
 * the transcript re-rendered for a line that changed one of them — and 120
 * events that changed no character of the transcript still cost 35 React
 * commits and five component renders a line.
 *
 * So an append carries on from where the last render stopped: the builder
 * is kept, the new lines are pushed into it, and every row it has already
 * emitted keeps the object it had. Anything else about the array — a
 * backfill placing a late line in the middle, a different run, a re-read
 * after a resync — rebuilds.
 */
export function useSessionModel(events: IndexedEvent[], detail: RunDetail | null): SessionModel {
  const held = useRef<{ builder: SessionBuilder; events: IndexedEvent[]; detail: RunDetail | null } | null>(null)

  return useMemo(() => {
    const last = held.current
    const resumable =
      last !== null && last.detail === detail && events.length >= last.events.length && grewFrom(last.events, events)

    const builder = resumable ? last.builder : new SessionBuilder(detail)
    for (let i = builder.count; i < events.length; i += 1) builder.push(events[i])
    held.current = { builder, events, detail }
    return builder.snapshot()
  }, [events, detail])
}

/** True when `next` is `prev` with lines added to the end and nothing else. */
function grewFrom(prev: IndexedEvent[], next: IndexedEvent[]): boolean {
  if (next === prev) return true
  for (let i = 0; i < prev.length; i += 1) {
    if (prev[i] !== next[i]) return false
  }
  return true
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
