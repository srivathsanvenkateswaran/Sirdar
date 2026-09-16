import type { NoteKind, RunEvent } from '../api/types'
import { duration, parseTime, tokens, usd } from './format'

/**
 * Helpers for reading a run's `events.jsonl` in the UI.
 *
 * `internal/run` writes a thin payload (tool name, decision, text, turns,
 * cost) plus `raw`, the untouched provider line. Anything richer than the thin
 * payload — a Bash command, a file path, an MCP server name — has to be dug out
 * of `raw`, whose shape differs per provider:
 *
 *   Claude   {"type":"assistant","message":{"content":[{"type":"tool_use","name":…,"input":{…}}]}}
 *            {"type":"control_request","request":{"tool_name":…,"input":{…}}}
 *   Codex    {"method":"item/started","params":{"item":{"type":"commandExecution",…}}}
 *            {"id":…,"method":"item/commandExecution/requestApproval","params":{…}}
 *
 * Everything here is pure and defensive: a payload that does not match is not
 * an error, it just yields no summary.
 */

/** One line of the event log together with its position in the file. */
export interface IndexedEvent {
  index: number
  event: RunEvent
}

/** A run of events between two usage ticks: what the agent did in one turn. */
export interface Turn {
  /** 1-based, in file order. */
  n: number
  events: IndexedEvent[]
  /** Turn counter reported by the `usage` event that closed this group. */
  turns?: number
  /** Cumulative cost reported by that same event. */
  costUsd?: number
}

/**
 * Splits the log into turns. A turn ends on a `usage` event (the provider's own
 * per-turn tick) and a new one starts at an `assistant_text` that follows a tool
 * result, which is where a provider that reports no usage resumes talking.
 */
export function groupTurns(events: IndexedEvent[]): Turn[] {
  const turns: Turn[] = []
  let current: IndexedEvent[] = []
  let previousKind = ''

  const flush = (closer?: RunEvent) => {
    if (current.length === 0) return
    turns.push({
      n: turns.length + 1,
      events: current,
      turns: closer?.payload?.turns,
      costUsd: closer?.payload?.costUsd,
    })
    current = []
  }

  for (const item of events) {
    const kind = item.event.kind
    if (kind === 'assistant_text' && previousKind === 'tool_finished') flush()
    current.push(item)
    if (kind === 'usage') flush(item.event)
    previousKind = kind
  }
  flush()
  return turns
}

/** The visual family a row belongs to; drives glyph, rail colour and filtering. */
export type EventClass =
  | 'tool'
  | 'permission'
  | 'usage'
  | 'text'
  | 'final'
  | 'error'
  | 'callout'
  | 'system'
  /** The operator's own words: a steer the run recorded, or an answer this window posted. */
  | 'you'

export function classify(event: RunEvent): EventClass {
  switch (event.kind) {
    case 'steer':
    case 'answer':
      return 'you'
    case 'tool_started':
    case 'tool_finished':
      return 'tool'
    case 'permission':
      return 'permission'
    case 'usage':
      return 'usage'
    case 'assistant_text':
      return 'text'
    case 'final':
      return 'final'
    case 'error':
      return 'error'
    case 'rate_limited':
    case 'question':
      return 'callout'
    default:
      return 'system'
  }
}

export type Filter = 'all' | 'tools' | 'denials' | 'text'

/**
 * What the stream opens on. "All" is the raw file, and a provider that streams
 * token deltas writes tens of lines there for every one thing the agent did,
 * so the useful default is the tool calls.
 */
export const DEFAULT_FILTER: Filter = 'tools'

export const FILTERS: { id: Filter; label: string }[] = [
  { id: 'all', label: 'All' },
  { id: 'tools', label: 'Tools' },
  { id: 'denials', label: 'Denials' },
  { id: 'text', label: 'Text' },
]

export function matchesFilter(event: RunEvent, filter: Filter): boolean {
  if (filter === 'all') return true
  const family = classify(event)
  if (filter === 'tools') return family === 'tool' || family === 'permission'
  if (filter === 'denials') return family === 'permission' && event.payload?.decision === 'deny'
  return (
    family === 'text' ||
    family === 'final' ||
    family === 'callout' ||
    family === 'error' ||
    family === 'you'
  )
}

/**
 * A row of a turn as the stream draws it: one event, or a run of consecutive
 * `system` lines standing in for all of them.
 */
export type TurnItem =
  | { kind: 'event'; index: number; item: IndexedEvent }
  | { kind: 'fold'; index: number; items: IndexedEvent[] }

/** Below this many in a row, a fold hides less than the row it costs. */
export const FOLD_MIN = 3

/**
 * Collapses consecutive `system` rows — the raw `stream_event` deltas, dozens
 * of them per turn — into one foldable row. Under "All" they buried the tool
 * calls and the text they were deltas of; they are still there, behind the
 * fold, because a malformed line the provider sent is sometimes the whole
 * answer to why a run went wrong.
 *
 * Only `system` folds. Every other family is a thing the agent did.
 */
export function foldSystem(events: IndexedEvent[], min = FOLD_MIN): TurnItem[] {
  const out: TurnItem[] = []
  let run: IndexedEvent[] = []

  const flush = () => {
    if (run.length === 0) return
    if (run.length >= min) out.push({ kind: 'fold', index: run[0].index, items: run })
    else for (const item of run) out.push({ kind: 'event', index: item.index, item })
    run = []
  }

  for (const item of events) {
    if (classify(item.event) === 'system') {
      run.push(item)
      continue
    }
    flush()
    out.push({ kind: 'event', index: item.index, item })
  }
  flush()
  return out
}

/** Applies a filter without losing turn boundaries; turns left empty drop out. */
export function filterTurns(turns: Turn[], filter: Filter): Turn[] {
  if (filter === 'all') return turns
  const out: Turn[] = []
  for (const turn of turns) {
    const events = turn.events.filter((e) => matchesFilter(e.event, filter))
    if (events.length > 0) out.push({ ...turn, events })
  }
  return out
}

// ------------------------------------------------------------- tool inputs

function asRecord(value: unknown): Record<string, unknown> | undefined {
  if (typeof value !== 'object' || value === null || Array.isArray(value)) return undefined
  return value as Record<string, unknown>
}

function str(value: unknown): string {
  return typeof value === 'string' ? value : ''
}

/**
 * Pulls the tool's arguments out of `payload.raw`, whichever provider wrote it.
 * Returns undefined when the raw line carries nothing useful.
 */
export function toolInput(event: RunEvent): unknown {
  const raw = asRecord(event.payload?.raw)
  if (!raw) return undefined
  const tool = str(event.payload?.tool)

  // Claude assistant line: pick the tool_use block this event was made from.
  const message = asRecord(raw.message)
  const content = message?.content
  if (Array.isArray(content)) {
    const blocks = content.map(asRecord).filter(Boolean) as Record<string, unknown>[]
    const uses = blocks.filter((b) => b.type === 'tool_use')
    const match = uses.find((b) => str(b.name) === tool) ?? uses[0]
    if (match && match.input !== undefined) return match.input
  }

  // Claude permission control_request.
  const request = asRecord(raw.request)
  if (request?.input !== undefined) return request.input

  // Codex thread item, or the params of an approval request.
  const params = asRecord(raw.params)
  if (params) {
    if (params.item !== undefined) return params.item
    return params
  }
  return undefined
}

/** `mcp__github__list_issues` and `github/list_issues` both split the same way. */
export function mcpParts(tool: string): { server: string; tool: string } | undefined {
  const claude = /^mcp__([^_].*?)__(.+)$/.exec(tool)
  if (claude) return { server: claude[1], tool: claude[2] }
  if (tool.includes('/')) {
    const at = tool.indexOf('/')
    const server = tool.slice(0, at)
    const name = tool.slice(at + 1)
    if (server && name) return { server, tool: name }
  }
  return undefined
}

/** The name shown in the stream: MCP tools collapse to `server/tool`. */
export function toolLabel(tool: string): string {
  const mcp = mcpParts(tool)
  return mcp ? `${mcp.server}/${mcp.tool}` : tool
}

export function truncate(text: string, max = 120): string {
  const flat = text.replace(/\s+/g, ' ').trim()
  return flat.length > max ? `${flat.slice(0, max - 1)}…` : flat
}

const PATH_FIELDS = ['file_path', 'filePath', 'path', 'notebook_path', 'target_file']

/**
 * One line describing what a tool was asked to do. Bash gets its command, file
 * tools their path, search tools their pattern, MCP tools their server and tool
 * name; anything unrecognised falls back to the first short scalar argument.
 */
export function inputSummary(event: RunEvent): string {
  const tool = str(event.payload?.tool)
  const input = toolInput(event)
  const args = asRecord(input)

  const mcp = mcpParts(tool)
  if (mcp) {
    const detail = args ? firstScalar(args, ['name', 'query', 'path', 'url']) : ''
    return detail ? `${mcp.server}/${mcp.tool} ${detail}` : `${mcp.server}/${mcp.tool}`
  }

  if (args) {
    // Codex reports the tool as an item type; the arguments live beside it.
    const command = str(args.command) || str(args.cmd)
    if (command) return truncate(command)
    if (Array.isArray(args.command)) return truncate((args.command as unknown[]).join(' '))

    const pattern = str(args.pattern) || str(args.query) || str(args.regex)
    const path = firstScalar(args, PATH_FIELDS)
    if (pattern) return truncate(path ? `${pattern} in ${path}` : pattern)
    if (path) return truncate(path)

    const url = str(args.url)
    if (url) return truncate(url)

    const changes = args.changes
    if (Array.isArray(changes)) {
      const paths = changes
        .map((c) => firstScalar(asRecord(c) ?? {}, PATH_FIELDS))
        .filter(Boolean)
      if (paths.length > 0) return truncate(paths.join(', '))
    }

    const fallback = firstScalar(args, ['description', 'prompt', 'text', 'subagent_type', 'title'])
    if (fallback) return truncate(fallback)

    const scalar = anyScalar(args)
    if (scalar) return truncate(scalar)
  }

  if (typeof input === 'string') return truncate(input)
  return ''
}

function firstScalar(args: Record<string, unknown>, fields: string[]): string {
  for (const field of fields) {
    const value = args[field]
    if (typeof value === 'string' && value !== '') return value
    if (typeof value === 'number') return String(value)
  }
  return ''
}

function anyScalar(args: Record<string, unknown>): string {
  for (const [key, value] of Object.entries(args)) {
    if (key === 'type' || key === 'id') continue
    if (typeof value === 'string' && value !== '') return value
    if (typeof value === 'number' || typeof value === 'boolean') return String(value)
  }
  return ''
}

/** Pretty-printed arguments for the expanded row, or '' when there are none. */
export function inputJSON(event: RunEvent): string {
  const input = toolInput(event)
  if (input === undefined) return ''
  try {
    return JSON.stringify(input, null, 2)
  } catch {
    return ''
  }
}

// -------------------------------------------------------------- formatting

/**
 * Cost, tokens and durations come from `lib/format` so a run reads the same on
 * this screen as it does on a board card.
 */
export const formatCost = usd
export const formatTokens = tokens
export const formatDuration = duration

/** Wall time from the run's start to its last update, or to now while it runs. */
export function elapsed(
  run: { startedAt?: string; updatedAt?: string; status?: string } | undefined,
  now = Date.now(),
): string {
  const start = parseTime(run?.startedAt)
  if (Number.isNaN(start)) return '—'
  const live = run?.status === 'running' || run?.status === 'preparing'
  const end = live ? now : parseTime(run?.updatedAt) || now
  return duration(end - start)
}

/** Gutter label: seconds since the run started, as `+4:07`. */
export function offsetLabel(t: string | undefined, startedAt: string | undefined): string {
  const at = parseTime(t)
  const start = parseTime(startedAt)
  if (Number.isNaN(at) || Number.isNaN(start)) return ''
  const total = Math.max(0, Math.floor((at - start) / 1000))
  const m = Math.floor(total / 60)
  const s = total % 60
  return `+${m}:${String(s).padStart(2, '0')}`
}

// ------------------------------------------------------------------- notes

export interface Frontmatter {
  fields: { key: string; value: string }[]
  body: string
}

/**
 * Splits a note's YAML frontmatter from its markdown. Only top-level scalars
 * are lifted into the table; nested blocks stay part of the body so nothing is
 * silently dropped.
 */
export function splitFrontmatter(markdown: string): Frontmatter {
  const text = markdown.replace(/^﻿/, '')
  if (!text.startsWith('---')) return { fields: [], body: markdown }
  const end = text.indexOf('\n---', 3)
  if (end === -1) return { fields: [], body: markdown }

  const head = text.slice(text.indexOf('\n') + 1, end)
  const rest = text.slice(end + 4).replace(/^(\r?\n)+/, '')
  const fields: { key: string; value: string }[] = []
  for (const line of head.split('\n')) {
    if (/^\s/.test(line) || line.trim() === '' || line.trimStart().startsWith('#')) continue
    const at = line.indexOf(':')
    if (at <= 0) continue
    const key = line.slice(0, at).trim()
    let value = line.slice(at + 1).trim()
    if (
      (value.startsWith('"') && value.endsWith('"') && value.length > 1) ||
      (value.startsWith("'") && value.endsWith("'") && value.length > 1)
    ) {
      value = value.slice(1, -1)
    }
    if (key) fields.push({ key, value })
  }
  return { fields, body: rest }
}

/**
 * Attachment paths from the prompt's `Files:` block, which `internal/prompt`
 * writes as one `- <path>` line per attachment (or `(none)`).
 */
export function promptAttachments(prompt: string): string[] {
  const lines = prompt.split('\n')
  const start = lines.findIndex((l) => l.trim() === 'Files:')
  if (start === -1) return []
  const out: string[] = []
  for (const line of lines.slice(start + 1)) {
    const trimmed = line.trim()
    if (trimmed === '') continue
    if (!trimmed.startsWith('- ')) break
    out.push(trimmed.slice(2).trim())
  }
  return out
}

/** The question text behind a blocked run, when the run stopped to ask one. */
export function askedQuestion(reason: string | undefined): string {
  if (!reason) return ''
  const match = /^agent asked:\s*/i.exec(reason)
  return match ? reason.slice(match[0].length).trim() : ''
}

/** Last file name of a path, for showing a note or attachment compactly. */
export function baseName(path: string): string {
  const parts = path.split(/[\\/]/)
  return parts[parts.length - 1] || path
}

/**
 * The file a note kind lives in, among the paths the run recorded. The run
 * writes each note into its own directory and then files a copy in the vault,
 * appending both paths in that order: the resolution note's pair starts at
 * `note-resolution.md`, the other kind's pair is everything before it. The
 * filed copy, when there is one, is the path a person opens.
 */
export function notePathFor(kind: NoteKind, notes: string[] | undefined): string {
  if (!notes || notes.length === 0) return ''
  const split = notes.findIndex((p) => baseName(p) === 'note-resolution.md')
  let group: string[]
  if (kind === 'resolution') group = split === -1 ? [] : notes.slice(split)
  else group = split === -1 ? notes : notes.slice(0, split)
  return group[group.length - 1] ?? ''
}

// ------------------------------------------------------------ conversation

/**
 * The id a provider uses to tie a call's start, the policy's answer to it and
 * its result together: Claude's `tool_use` id (on the assistant block, the
 * permission request and the `tool_result`), Codex's item id (on the item
 * and on the approval that names it). '' when the line carries none.
 */
export function callId(event: RunEvent): string {
  const raw = asRecord(event.payload?.raw)
  if (!raw) return ''
  const tool = str(event.payload?.tool)

  const message = asRecord(raw.message)
  const content = message?.content
  if (Array.isArray(content)) {
    const blocks = content.map(asRecord).filter(Boolean) as Record<string, unknown>[]
    const uses = blocks.filter((b) => b.type === 'tool_use')
    const match = uses.find((b) => str(b.name) === tool) ?? uses[0]
    if (match) return str(match.id)
    const result = blocks.find((b) => b.type === 'tool_result')
    if (result) return str(result.tool_use_id)
  }

  const request = asRecord(raw.request)
  if (request) return str(request.tool_use_id)

  const params = asRecord(raw.params)
  if (params) {
    const item = asRecord(params.item)
    if (item) return str(item.id)
    return str(params.itemId) || str(params.callId) || str(params.toolCallId)
  }
  return ''
}

/** True for a provider line that is one token delta of a message, not a message. */
export function isDelta(event: RunEvent): boolean {
  const raw = asRecord(event.payload?.raw)
  if (!raw) return false
  const type = str(raw.type)
  if (type === 'stream_event' || type === 'content_block_delta') return true
  return /delta/i.test(str(raw.method))
}

/**
 * What a finished tool call returned. The thin payload carries it as `text`
 * for the providers Sirdar reads line by line; Codex keeps it on the item
 * (`aggregatedOutput`), Cursor on the call's `result`, and an ACP agent in
 * the update's content blocks. '' when the line carries nothing.
 */
export function outputText(event: RunEvent | undefined): string {
  if (!event) return ''
  const text = event.payload?.text ?? ''
  if (text) return text
  const raw = asRecord(event.payload?.raw)
  if (!raw) return ''

  const params = asRecord(raw.params)
  const item = asRecord(params?.item)
  if (item) {
    for (const key of ['aggregatedOutput', 'output', 'result', 'content', 'text']) {
      const value = item[key]
      if (typeof value === 'string' && value !== '') return value
      if (value !== null && typeof value === 'object') return stringify(value)
    }
    return ''
  }

  const update = asRecord(params?.update)
  if (update) {
    const content = update.content
    if (typeof content === 'string') return content
    if (Array.isArray(content)) {
      return content
        .map((b) => {
          const block = asRecord(b)
          const inner = asRecord(block?.content)
          return str(inner?.text) || str(block?.text)
        })
        .filter(Boolean)
        .join('\n')
    }
  }

  const call = asRecord(raw.tool_call)
  if (call) {
    for (const [key, value] of Object.entries(call)) {
      if (!key.endsWith('ToolCall')) continue
      const body = asRecord(value)
      const result = body?.result
      if (typeof result === 'string') return result
      if (result !== undefined) return stringify(result)
    }
  }
  return ''
}

/** True when the provider marked the result an error, whichever way it says so. */
export function outputFailed(event: RunEvent | undefined): boolean {
  const raw = asRecord(event?.payload?.raw)
  if (!raw) return false
  const message = asRecord(raw.message)
  const content = message?.content
  if (Array.isArray(content)) {
    const result = content.map(asRecord).find((b) => b?.type === 'tool_result')
    if (result?.is_error === true) return true
  }
  const params = asRecord(raw.params)
  const item = asRecord(params?.item)
  if (item) {
    if (item.status === 'failed') return true
    if (typeof item.exitCode === 'number' && item.exitCode !== 0) return true
  }
  const update = asRecord(params?.update)
  if (update?.status === 'failed') return true
  const call = asRecord(raw.tool_call)
  if (call) {
    for (const [key, value] of Object.entries(call)) {
      if (!key.endsWith('ToolCall')) continue
      const result = asRecord(asRecord(value)?.result)
      if (result && (result.error !== undefined || result.rejected !== undefined)) return true
    }
  }
  return false
}

function stringify(value: unknown): string {
  try {
    return JSON.stringify(value, null, 2)
  } catch {
    return ''
  }
}

/** One tool call as the transcript shows it: its start, and what followed. */
export interface ToolCall {
  started: IndexedEvent
  finished?: IndexedEvent
  permission?: IndexedEvent
}

/**
 * A row of the conversation. A message is every consecutive assistant line
 * merged into one block, so a provider that streams deltas grows the block
 * rather than stacking rows; a call is a tool call with its result and the
 * policy's word on it; a fold is a run of raw stream lines; anything else is
 * one event.
 */
export type ConversationItem =
  | { kind: 'message'; index: number; parts: IndexedEvent[]; text: string }
  | { kind: 'call'; index: number; call: ToolCall }
  | { kind: 'event'; index: number; item: IndexedEvent }
  | { kind: 'fold'; index: number; items: IndexedEvent[] }

/**
 * Joins consecutive assistant lines. A delta continues the text as it is; a
 * whole message that follows another starts a new paragraph.
 */
export function joinText(parts: IndexedEvent[]): string {
  let out = ''
  for (const part of parts) {
    const text = part.event.payload?.text ?? ''
    if (text === '') continue
    if (out === '' || isDelta(part.event)) out += text
    else out += (out.endsWith('\n\n') ? '' : '\n\n') + text
  }
  return out
}

/**
 * Reads a turn's events as a conversation: pairs each tool call with its
 * result and its permission, merges streamed text into one message, and
 * with `fold` collapses runs of raw stream lines the way `foldSystem` does.
 *
 * A result is paired by id when the provider gives one, else with the oldest
 * call still waiting — the same tool if the result names one. A permission
 * attaches to the newest waiting call of its tool. Anything that pairs with
 * nothing stays a row of its own, so no line of the log is lost.
 */
export function conversation(events: IndexedEvent[], fold = false): ConversationItem[] {
  const out: ConversationItem[] = []
  const open: ToolCall[] = []
  let system: IndexedEvent[] = []

  const flushSystem = () => {
    if (system.length === 0) return
    if (fold && system.length >= FOLD_MIN) {
      out.push({ kind: 'fold', index: system[0].index, items: system })
    } else {
      for (const item of system) out.push({ kind: 'event', index: item.index, item })
    }
    system = []
  }

  for (const item of events) {
    const event = item.event
    if (classify(event) === 'system') {
      system.push(item)
      continue
    }
    flushSystem()

    if (event.kind === 'tool_started') {
      const call: ToolCall = { started: item }
      open.push(call)
      out.push({ kind: 'call', index: item.index, call })
      continue
    }

    if (event.kind === 'tool_finished') {
      const id = callId(event)
      const tool = str(event.payload?.tool)
      let at = id ? open.findIndex((c) => callId(c.started.event) === id) : -1
      if (at === -1 && tool) at = open.findIndex((c) => str(c.started.event.payload?.tool) === tool)
      if (at === -1 && open.length > 0) at = 0
      if (at === -1) {
        out.push({ kind: 'event', index: item.index, item })
        continue
      }
      open[at].finished = item
      open.splice(at, 1)
      continue
    }

    if (event.kind === 'permission') {
      const id = callId(event)
      const tool = str(event.payload?.tool)
      const newest = [...open].reverse()
      let call = id ? newest.find((c) => callId(c.started.event) === id) : undefined
      if (!call) {
        call = newest.find(
          (c) => !c.permission && (!tool || str(c.started.event.payload?.tool) === tool),
        )
      }
      if (!call || call.permission) {
        out.push({ kind: 'event', index: item.index, item })
        continue
      }
      call.permission = item
      continue
    }

    if (event.kind === 'assistant_text') {
      const last = out[out.length - 1]
      if (last && last.kind === 'message') {
        last.parts.push(item)
        last.text = joinText(last.parts)
      } else {
        out.push({ kind: 'message', index: item.index, parts: [item], text: joinText([item]) })
      }
      continue
    }

    out.push({ kind: 'event', index: item.index, item })
  }
  flushSystem()
  return out
}

/** The index of the run's last tool call: the one that opens on its own. */
export function lastCallIndex(events: IndexedEvent[]): number {
  for (let i = events.length - 1; i >= 0; i--) {
    if (events[i].event.kind === 'tool_started') return events[i].index
  }
  return -1
}

/** How long a call took, from its start to its result: `0.4s`, `12s`, `1:04`. */
export function callDuration(call: ToolCall): string {
  if (!call.finished) return ''
  const a = parseTime(call.started.event.t)
  const b = parseTime(call.finished.event.t)
  if (Number.isNaN(a) || Number.isNaN(b)) return ''
  const ms = Math.max(0, b - a)
  if (ms < 10_000) return `${(ms / 1000).toFixed(1)}s`
  if (ms < 60_000) return `${Math.round(ms / 1000)}s`
  return duration(ms)
}

// ------------------------------------------------------------------ tables

export interface TextTable {
  head: string[]
  rows: string[][]
}

/**
 * Reads a table out of a tool's output when it is one: rows split by pipes
 * or tabs, the same number of cells in nearly every row, and a markdown rule
 * row (`|---|---|`) dropped. A tab table wants three columns, because a
 * two-cell tab line is what every numbered file listing looks like.
 */
export function detectTable(text: string): TextTable | undefined {
  const lines = text.split('\n').map((l) => l.replace(/\r$/, ''))
  while (lines.length > 0 && lines[lines.length - 1].trim() === '') lines.pop()
  while (lines.length > 0 && lines[0].trim() === '') lines.shift()
  if (lines.length < 2) return undefined

  const tabs = lines.filter((l) => l.includes('\t')).length
  const pipes = lines.filter((l) => l.includes('|')).length
  const sep = tabs >= pipes ? '\t' : '|'
  const minCols = sep === '\t' ? 3 : 2
  const withSep = sep === '\t' ? tabs : pipes
  if (withSep < lines.length * 0.9) return undefined

  const rows: string[][] = []
  for (const line of lines) {
    if (line.trim() === '') continue
    if (sep === '|' && /^[\s|:-]+$/.test(line)) continue
    let cells = line.split(sep)
    if (sep === '|') {
      if (cells[0].trim() === '') cells = cells.slice(1)
      if (cells.length > 0 && cells[cells.length - 1].trim() === '') cells = cells.slice(0, -1)
    }
    rows.push(cells.map((c) => c.trim()))
  }
  if (rows.length < 2) return undefined
  const cols = rows[0].length
  if (cols < minCols) return undefined
  if (rows[0].some((c) => c === '')) return undefined
  const regular = rows.filter((r) => r.length === cols).length
  if (regular < rows.length * 0.8) return undefined

  const fit = (r: string[]) =>
    r.length === cols ? r : [...r, ...(Array(cols).fill('') as string[])].slice(0, cols)
  return { head: rows[0], rows: rows.slice(1).map(fit) }
}

// ------------------------------------------------------------------ answer

/** The final event's text as the structured answer it is, or undefined when it is prose. */
export function parseAnswer(text: string | undefined): Record<string, unknown> | undefined {
  if (!text) return undefined
  const trimmed = text.trim()
  if (!trimmed.startsWith('{')) return undefined
  try {
    return asRecord(JSON.parse(trimmed))
  } catch {
    return undefined
  }
}

/** `rootCause` → "Root cause", `customer_reply_draft` → "Customer reply draft". */
export function fieldLabel(key: string): string {
  const words = key
    .replace(/([a-z0-9])([A-Z])/g, '$1 $2')
    .replace(/[_-]+/g, ' ')
    .trim()
    .toLowerCase()
  if (words === '') return key
  return words.charAt(0).toUpperCase() + words.slice(1)
}

export function isURL(value: string): boolean {
  return /^https?:\/\/\S+$/i.test(value.trim())
}

/** True for a value the answer card would draw nothing for. */
export function isBlank(value: unknown): boolean {
  if (value === null || value === undefined) return true
  if (typeof value === 'string') return value.trim() === ''
  if (Array.isArray(value)) return value.length === 0
  if (typeof value === 'object') return Object.keys(value as object).length === 0
  return false
}
