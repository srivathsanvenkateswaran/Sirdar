import type { RunDetail, RunDiff, RunEvent } from '../../api/types'
import {
  askedQuestion,
  conversation,
  groupTurns,
  inputSummary,
  outputFailed,
  outputText,
  parseAnswer,
  toolInput,
  toolLabel,
  type IndexedEvent,
  type ToolCall,
} from '../../lib/events'
import {
  deriveChangeMarkers,
  deriveEvidenceMarkers,
  fileName,
  type EvidenceSource,
  type Marker,
  type StepLike,
} from '../../lib/evidence'
import { parseTime } from '../../lib/format'
import { checksFromEvents, fixReport, type FixReport, type RunCheck } from '../../lib/review'
import {
  byteLength,
  bytes,
  editDelta,
  isOutputTool,
  isReadTool,
  isShellTool,
  isWriteTool,
  resultSummary,
  shortCommand,
  shortPath,
} from '../../lib/toolOutput'
import { parsePatch } from '../../ui/diff-view/patch'

/**
 * The session model: the run's log read as the path the agent took, plus
 * the answer, the checks and the markers that tie one to the other.
 *
 * Every layout — Conversation, Document, Workbench — draws from this one
 * shape, so the three cannot disagree about what a step is called, which
 * turn it sits in or which evidence marker it carries. It is pure: the
 * hook in `useSessionModel` feeds it the run and the events and adds what
 * needs a transport (the note, the prompt, the change).
 */

export type StepState = 'done' | 'failed' | 'denied' | 'waiting' | 'running' | 'lost'
export type StepKind = 'read' | 'shell' | 'edit' | 'output' | 'other'

export interface SessionStep {
  /** The `tool_started` event's index: the step's id across layouts. */
  index: number
  /** `00:06`: seconds since the run started. */
  at: string
  t: string
  tool: string
  kind: StepKind
  /** Read · Ran · Run (still waiting) · Edited · Wrote · Rewrote · Called. */
  verb: string
  /** `ledger.go`, `rg -n "Return|restock"`, `the note`. */
  object: string
  /** `65 lines`, `17 matches`, `FAIL · 1 test`, `+14`, `v1 · 12.6 kB`; '' before the result. */
  result: string
  state: StepState
  decision?: string
  /** The policy's rule when it named one: `go test *`, `allow-list`; undefined when no rule applies. */
  rule?: string
  /** The policy's words: why a call was denied, or what the pause is waiting on. */
  reason?: string
  /** The model's own one-line description of the call, when the provider carries one. */
  description?: string
  /** The input verbatim: the command, the path, or the arguments as JSON. */
  input: string
  path?: string
  command?: string
  output?: string
  outputBytes: number
  durationMs?: number
  /** For a structured answer: the first one the run wrote, or its last. */
  version?: 'v1' | 'final'
  call: ToolCall
}

export type TurnItem =
  | { kind: 'step'; step: SessionStep }
  /** The model's own words between steps: a call's description, or prose it wrote. */
  | { kind: 'ann'; index: number; text: string }
  | { kind: 'you'; index: number; at: string; text: string; label: string }
  | { kind: 'system'; index: number; at: string; text: string; tone?: 'failed' }

export interface SessionTurn {
  key: string
  /** `turn 3`, `turns 3–8`, `resumed · turn 1`. */
  label: string
  at: string
  items: TurnItem[]
  steps: SessionStep[]
}

export type PathItem =
  | { kind: 'turn'; turn: SessionTurn }
  | { kind: 'you'; index: number; at: string; text: string; label: string }
  | { kind: 'system'; index: number; at: string; text: string; tone?: 'failed' }

export type ComposerState =
  /** The run stopped to ask: the question, when it was asked, and the call it is waiting on. */
  | { kind: 'reply'; question: string; since: string; pending?: SessionStep; suggestedRule?: string; reason?: string }
  | { kind: 'steer' }
  /**
   * The run is working. The strip's button is a Stop rather than a send
   * that is off, and the box says what typing there does — once.
   */
  | { kind: 'running' }
  | { kind: 'disabled'; reason: string }

export interface SessionCounts {
  calls: number
  denied: number
  outBytes: number
  byTool: { tool: string; n: number; denied: number }[]
}

export interface SessionModel {
  steps: SessionStep[]
  path: PathItem[]
  /** The run's structured answer, when the last `final` carried JSON. */
  answer?: Record<string, unknown>
  /** A fix run's report, off its last `final`. */
  report?: FixReport
  checks: RunCheck[]
  markers: Marker[]
  composer: ComposerState
  counts: SessionCounts
  /** The steer the run recorded last, for the strip's "Your last steer at 02:02". */
  lastSteer?: { at: string; text: string }
  /**
   * The model the log names, for a run whose record has not reported one:
   * a workspace configured no model, so state.json says '' while the
   * provider's lines say which one answered.
   */
  model: string
}

const LIVE = new Set(['preparing', 'running'])

function str(v: unknown): string {
  return typeof v === 'string' ? v : ''
}

function asRecord(v: unknown): Record<string, unknown> | undefined {
  return typeof v === 'object' && v !== null && !Array.isArray(v) ? (v as Record<string, unknown>) : undefined
}

/** `00:06`, `02:02`, `1:04:30`: the offset from the run's start, two-digit minutes so the column lines up. */
export function clockOffset(ms: number): string {
  const total = Math.floor(Math.max(0, ms) / 1000)
  const s = String(total % 60).padStart(2, '0')
  const m = Math.floor(total / 60) % 60
  const h = Math.floor(total / 3600)
  if (h > 0) return `${h}:${String(m).padStart(2, '0')}:${s}`
  return `${String(m).padStart(2, '0')}:${s}`
}

/** `00:06` from a stamp and the run's start; '' when either is unreadable. */
export function clock(t: string | undefined, startedAt: string | undefined): string {
  const a = parseTime(t)
  const b = parseTime(startedAt)
  if (Number.isNaN(a) || Number.isNaN(b)) return ''
  return clockOffset(a - b)
}

/** `80 ms`, `1.99 s`, `1:04`. */
export function took(ms: number | undefined): string {
  if (ms === undefined) return ''
  if (ms < 10) return '<10 ms'
  if (ms < 1000) return `${Math.round(ms)} ms`
  if (ms < 60_000) return `${(ms / 1000).toFixed(2).replace(/0+$/, '').replace(/\.$/, '')} s`
  return clockOffset(ms)
}

/** The rules a Claude permission line suggested, joined: `go test *`. */
function suggestedRules(event: RunEvent | undefined): string {
  const raw = asRecord(event?.payload?.raw)
  const request = asRecord(raw?.request)
  const suggestions = request?.permission_suggestions
  if (!Array.isArray(suggestions)) return ''
  const rules: string[] = []
  for (const s of suggestions) {
    const rec = asRecord(s)
    if (!Array.isArray(rec?.rules)) continue
    for (const r of rec.rules) {
      const content = str(asRecord(r)?.ruleContent)
      if (content) rules.push(content)
    }
  }
  return rules.join(', ')
}

const PATH_FIELDS = ['file_path', 'filePath', 'path', 'notebook_path', 'target_file']

function pathOf(args: Record<string, unknown> | undefined): string {
  if (!args) return ''
  for (const f of PATH_FIELDS) {
    const v = args[f]
    if (typeof v === 'string' && v !== '') return v
  }
  return ''
}

function pretty(v: unknown): string {
  try {
    return JSON.stringify(v, null, 2)
  } catch {
    return ''
  }
}

/** One tool call as a step, before the output steps are numbered. */
function toStep(
  call: ToolCall,
  startedAt: string | undefined,
  status: string,
  runKind: string,
): SessionStep {
  const started = call.started.event
  const tool = toolLabel(str(started.payload?.tool)) || 'tool'
  const args = asRecord(toolInput(started))
  const description = str(args?.description) || undefined
  const permission = call.permission?.event
  const decision = str(permission?.payload?.decision) || undefined
  const denied = decision === 'deny'
  const finishedEvent = call.finished?.event
  const output = finishedEvent ? outputText(finishedEvent) : undefined
  const failed = !denied && finishedEvent !== undefined && outputFailed(finishedEvent)
  const a = parseTime(started.t)
  const b = parseTime(finishedEvent?.t)
  const durationMs = !Number.isNaN(a) && !Number.isNaN(b) ? Math.max(0, b - a) : undefined

  let kind: StepKind = 'other'
  let verb = 'Called'
  let object = tool
  let input = pretty(args ?? toolInput(started))
  let path: string | undefined
  let command: string | undefined
  let result = ''

  if (isReadTool(tool)) {
    kind = 'read'
    verb = 'Read'
    path = pathOf(args)
    object = path ? shortPath(path) : tool
    input = path || input
    result = denied ? '' : resultSummary(tool, '', output)
  } else if (isShellTool(tool)) {
    kind = 'shell'
    command = str(args?.command) || str(args?.cmd) || (Array.isArray(args?.command) ? (args.command as unknown[]).join(' ') : '')
    verb = call.finished || denied ? 'Ran' : 'Run'
    object = command ? shortCommand(command) : tool
    input = command || input
    result = denied ? '' : resultSummary(tool, command, output)
  } else if (isWriteTool(tool)) {
    kind = 'edit'
    path = pathOf(args)
    const isWrite = /^write/i.test(tool)
    verb = isWrite ? 'Wrote' : 'Edited'
    // An edit is always in the worktree; the file alone names it.
    object = path ? fileName(path) : tool
    input = path || input
    if (!denied) {
      result = isWrite
        ? editDelta(undefined, str(args?.content))
        : editDelta(str(args?.old_string), str(args?.new_string))
      if (result === '±0' && !args?.old_string && !args?.new_string) result = ''
    }
  } else if (isOutputTool(tool) || tool === 'final') {
    kind = 'output'
    verb = 'Wrote'
    object = runKind === 'fix' ? 'the fix report' : 'the note'
    // The size of the answer as it went over the wire, not as it is pretty-printed here.
    result = bytes(byteLength(args ? JSON.stringify(args) : input))
  } else {
    const summary = inputSummary(started)
    object = summary ? `${tool} ${summary}` : tool
    result = denied ? '' : resultSummary(tool, '', output)
  }

  let state: StepState
  if (denied) state = 'denied'
  else if (finishedEvent) state = failed ? 'failed' : 'done'
  else if (status === 'blocked') state = 'waiting'
  else if (LIVE.has(status)) state = 'running'
  else state = 'lost'
  if (state === 'waiting' && kind === 'shell') verb = 'Run'

  const suggested = suggestedRules(permission)
  let rule: string | undefined
  if (decision === 'allow') rule = suggested || 'allowed'
  else if (decision === 'ask') rule = suggested || undefined
  else if (!decision && kind === 'shell' && call.finished && !denied) rule = 'allow-list'

  return {
    index: call.started.index,
    at: clock(started.t, startedAt),
    t: started.t,
    tool,
    kind,
    verb,
    object,
    result,
    state,
    decision,
    rule,
    reason: denied || decision === 'ask' ? str(permission?.payload?.text) || str(asRecord(asRecord(permission?.payload?.raw)?.request)?.decision_reason) || undefined : undefined,
    description,
    input,
    path,
    command,
    output,
    outputBytes: output ? byteLength(output) : 0,
    durationMs,
    call,
  }
}

/** `claude 7-day window at 90%` from a rate-limit line; the line's own text otherwise. */
export function systemLine(event: RunEvent, provider: string): string {
  const raw = asRecord(event.payload?.raw)
  const info = asRecord(raw?.rate_limit_info)
  if (info) {
    const window = str(info.rateLimitType).replace(/^seven_day$/, '7-day').replace(/^five_hour$/, '5-hour').replace(/_/g, ' ')
    const pct = typeof info.utilization === 'number' ? `${Math.round(info.utilization * 100)}%` : ''
    return [provider, window, 'window', pct ? `at ${pct}` : ''].filter(Boolean).join(' ')
  }
  const text = str(event.payload?.text)
  if (text) return text
  const subtype = str(raw?.subtype) || str(asRecord(raw?.event)?.type) || str(raw?.type)
  return subtype || event.kind
}

/** The model the provider's lines name: Claude's `message.model` or its init line's `model`; '' when none says. */
export function modelOf(events: IndexedEvent[]): string {
  for (const { event } of events) {
    const raw = asRecord(event.payload?.raw)
    const fromMessage = str(asRecord(raw?.message)?.model)
    if (fromMessage) return fromMessage
    if (raw?.type === 'system' && raw.subtype === 'init' && str(raw.model)) return str(raw.model)
    if (str(event.payload?.model)) return str(event.payload?.model)
  }
  return ''
}

/** True for a system line worth a row without Show everything: the provider's rate-limit warnings and errors. */
function noteworthy(event: RunEvent): boolean {
  const raw = asRecord(event.payload?.raw)
  return raw?.type === 'rate_limit_event' || event.kind === 'error'
}

export interface BuildOptions {
  /** Show every system line, not only the rate-limit warnings. */
  everything?: boolean
  /** Evidence items from the note, for a run whose answer carries none. */
  noteEvidence?: EvidenceSource[]
  /** The fix run's change, for the C markers. */
  diff?: RunDiff | null
}

/** The evidence array inside a structured answer, wherever the schema put it. */
export function evidenceOf(answer: Record<string, unknown> | undefined): EvidenceSource[] {
  if (!answer) return []
  const candidates: unknown[] = [answer.evidence, asRecord(answer.rootCause)?.evidence]
  for (const c of candidates) {
    if (!Array.isArray(c)) continue
    const items = c
      .map(asRecord)
      .filter((r): r is Record<string, unknown> => Boolean(r))
      .map((r) => ({ source: str(r.source), query: str(r.query), finding: str(r.finding) || str(r.text) }))
      .filter((r) => r.finding || r.query)
    if (items.length > 0) return items
  }
  return []
}

export function buildSessionModel(
  detail: RunDetail | null,
  events: IndexedEvent[],
  options: BuildOptions = {},
): SessionModel {
  const startedAt = detail?.startedAt
  const status = detail?.status ?? ''
  const runKind = detail?.kind ?? 'triage'
  const provider = detail?.provider ?? ''

  const steps: SessionStep[] = []
  const path: PathItem[] = []
  const rawTurns = groupTurns(events)
  let visible = 0
  let resumed = false
  let lastSteer: SessionModel['lastSteer']
  /** Consecutive one-step turns with nothing to say, waiting to be merged. */
  let quiet: { turn: SessionTurn; n: number }[] = []
  /** A structured answer was written since the last `final`, so that final is not a step of its own. */
  let outputSinceFinal = false

  const flushQuiet = () => {
    if (quiet.length === 0) return
    if (quiet.length === 1) path.push({ kind: 'turn', turn: quiet[0].turn })
    else {
      const first = quiet[0]
      const last = quiet[quiet.length - 1]
      const prefix = resumed ? 'resumed · ' : ''
      path.push({
        kind: 'turn',
        turn: {
          key: `turns-${first.n}-${last.n}`,
          label: `${prefix}turns ${first.n}–${last.n}`,
          at: first.turn.at,
          items: quiet.flatMap((q) => q.turn.items),
          steps: quiet.flatMap((q) => q.turn.steps),
        },
      })
    }
    quiet = []
  }

  for (const raw of rawTurns) {
    const items: TurnItem[] = []
    const turnSteps: SessionStep[] = []
    const rows = conversation(raw.events, false)
    let lastAnn = ''

    for (const row of rows) {
      if (row.kind === 'call') {
        const step = toStep(row.call, startedAt, status, runKind)
        if (step.kind === 'output') outputSinceFinal = true
        if (step.description && step.description !== lastAnn) {
          items.push({ kind: 'ann', index: step.index, text: step.description })
          lastAnn = step.description
        }
        items.push({ kind: 'step', step })
        turnSteps.push(step)
        steps.push(step)
        continue
      }
      if (row.kind === 'message') {
        const text = row.text.trim()
        if (text && text !== lastAnn) {
          items.push({ kind: 'ann', index: row.index, text })
          lastAnn = text
        }
        continue
      }
      if (row.kind === 'fold') continue
      const event = row.item.event
      const at = clock(event.t, startedAt)
      switch (event.kind) {
        case 'steer':
        case 'answer': {
          const text = str(event.payload?.text)
          const label = event.kind === 'steer' ? 'resumed the session' : 'answered'
          items.push({ kind: 'you', index: row.index, at, text, label })
          if (event.kind === 'steer') lastSteer = { at, text }
          break
        }
        case 'review': {
          const p = event.payload
          items.push({
            kind: 'you',
            index: row.index,
            at,
            text: `${p?.action === 'drop' ? 'Dropped' : p?.action ?? 'Reviewed'} hunk ${str(p?.path)}${typeof p?.hunk === 'number' ? ` #${p.hunk + 1}` : ''}`,
            label: 'review',
          })
          break
        }
        case 'final': {
          if (outputSinceFinal) {
            outputSinceFinal = false
            break
          }
          {
            // A provider that files its answer without a StructuredOutput
            // call: the final line itself is the step that wrote it.
            const text = str(event.payload?.text)
            const step: SessionStep = {
              index: row.index,
              at,
              t: event.t,
              tool: 'final',
              kind: 'output',
              verb: 'Wrote',
              object: runKind === 'fix' ? 'the fix report' : parseAnswer(text) ? 'the note' : 'the answer',
              result: bytes(byteLength(text)),
              state: 'done',
              input: text,
              output: undefined,
              outputBytes: 0,
              call: { started: row.item },
            }
            items.push({ kind: 'step', step })
            turnSteps.push(step)
            steps.push(step)
          }
          break
        }
        case 'question':
          items.push({ kind: 'ann', index: row.index, text: str(event.payload?.text) })
          break
        case 'error':
          items.push({ kind: 'system', index: row.index, at, text: systemLine(event, provider), tone: 'failed' })
          break
        case 'usage':
        case 'permission':
        case 'tool_finished':
          break
        default:
          if (options.everything || noteworthy(event)) {
            items.push({ kind: 'system', index: row.index, at, text: systemLine(event, provider) })
          }
      }
    }

    // What comes before the turn's first step, or after its last, belongs
    // between turns: the steer that resumed the run, the warning the
    // provider printed, the review the reader filed.
    const hoist = (item: TurnItem) => {
      if (item.kind !== 'you' && item.kind !== 'system') return
      flushQuiet()
      path.push(item)
      if (item.kind === 'you' && item.label === 'resumed the session') {
        resumed = true
        visible = 0
      }
    }
    const isBody = (item: TurnItem) => item.kind === 'step' || item.kind === 'ann'
    const trailing: TurnItem[] = []
    while (items.length > 0 && !isBody(items[0])) hoist(items.shift() as TurnItem)
    while (items.length > 0 && !isBody(items[items.length - 1])) trailing.unshift(items.pop() as TurnItem)

    if (turnSteps.length === 0) {
      for (const item of items) hoist(item)
      for (const item of trailing) hoist(item)
      continue
    }

    visible += 1
    const prefix = resumed ? 'resumed · ' : ''
    const turn: SessionTurn = {
      key: `turn-${resumed ? 'r' : ''}${visible}`,
      label: `${prefix}turn ${visible}`,
      at: clock(raw.events[0]?.event.t, startedAt),
      items,
      steps: turnSteps,
    }
    // A turn that only read one file, with nothing said about it, merges
    // with its neighbours: "turns 3–8". Anything the agent ran, wrote or
    // was refused stands in a turn of its own.
    const isQuiet =
      turnSteps.length === 1 &&
      items.length === 1 &&
      turnSteps[0].state === 'done' &&
      turnSteps[0].kind === 'read' &&
      trailing.length === 0
    if (isQuiet) {
      quiet.push({ turn, n: visible })
      continue
    }
    flushQuiet()
    path.push({ kind: 'turn', turn })
    for (const item of trailing) hoist(item)
  }
  flushQuiet()

  // The structured answers, first to last: v1, then the final one.
  const outputs = steps.filter((s) => s.kind === 'output')
  outputs.forEach((s, i) => {
    const last = i === outputs.length - 1
    s.version = last ? 'final' : 'v1'
    if (i > 0) s.verb = 'Rewrote'
    s.result = `${last ? 'final' : `v${i + 1}`} · ${s.result}`
  })

  const runEvents = events.map((e) => e.event)
  const lastFinal = [...runEvents].reverse().find((e) => e.kind === 'final')
  const answer = runKind === 'fix' ? undefined : parseAnswer(lastFinal?.payload?.text)
  const report = runKind === 'fix' ? fixReport(runEvents) : undefined
  // A check the run is still waiting to run is not a check yet: the strip
  // asks about it, the Checks section says none have run.
  const waitingCommands = new Set(steps.filter((s) => s.state === 'waiting' || s.state === 'running').map((s) => s.command))
  const checks =
    runKind === 'fix'
      ? checksFromEvents(runEvents).filter((c) => !(c.outcome === 'ran' && waitingCommands.has(c.command)))
      : []

  const stepLikes: StepLike[] = steps.map((s) => ({ index: s.index, tool: s.tool, path: s.path, command: s.command }))
  const evidence = evidenceOf(answer)
  const markers: Marker[] = deriveEvidenceMarkers(
    evidence.length > 0 ? evidence : options.noteEvidence ?? [],
    stepLikes,
  )
  if (options.diff) {
    const files = parsePatch(options.diff.patch).map((f) => ({ path: f.path, hunks: f.hunks.length }))
    const edits = steps.filter((s) => s.kind === 'edit' && s.path).map((s) => ({ index: s.index, path: s.path as string }))
    markers.push(...deriveChangeMarkers(files, edits))
  }

  const pending = [...steps].reverse().find((s) => s.state === 'waiting')
  let composer: ComposerState
  if (!detail) composer = { kind: 'disabled', reason: 'Loading the run…' }
  else if (status === 'blocked') {
    composer = {
      kind: 'reply',
      question: askedQuestion(detail.reason) || pending?.description || 'The agent is waiting on you.',
      since: pending?.at || clock(detail.updatedAt, startedAt),
      pending,
      suggestedRule: pending?.rule,
      reason: pending?.reason,
    }
  } else if (status === 'completed' || status === 'failed') composer = { kind: 'steer' }
  else if (status === 'over_budget')
    composer = { kind: 'disabled', reason: 'Over budget: a run at its cap cannot be steered.' }
  else composer = { kind: 'running' }

  const byTool = new Map<string, { tool: string; n: number; denied: number }>()
  let denied = 0
  let outBytes = 0
  for (const s of steps) {
    const row = byTool.get(s.tool) ?? { tool: s.tool, n: 0, denied: 0 }
    row.n += 1
    if (s.state === 'denied') {
      row.denied += 1
      denied += 1
    }
    outBytes += s.outputBytes
    byTool.set(s.tool, row)
  }

  return {
    steps,
    path,
    answer,
    report,
    checks,
    markers,
    composer,
    counts: { calls: steps.length, denied, outBytes, byTool: [...byTool.values()] },
    lastSteer,
    model: modelOf(events),
  }
}
