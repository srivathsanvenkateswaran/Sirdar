import type { RunEvent } from '../api/types'
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

export function classify(event: RunEvent): EventClass {
  switch (event.kind) {
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
  return family === 'text' || family === 'final' || family === 'callout' || family === 'error'
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
