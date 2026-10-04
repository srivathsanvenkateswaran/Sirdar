import type { RunEvent } from '../api/types'
import { toolLabel, type IndexedEvent } from './events'

/**
 * How long a working run may go without a single line before the activity
 * line calls it a stall rather than a wait. Slow is normal — a model can
 * think for a minute — but two minutes of nothing at all is worth the
 * operator's eye, and the Stop beside the line is the way out.
 */
export const STALL_MS = 120_000

/**
 * What a working run is doing right now, read off the tail of its log:
 *
 * - `answer`: the structured answer's JSON is streaming, with its size so far.
 * - `thinking`: a thinking block is open.
 * - `preparing`: the input of some other tool call is being written.
 * - `tool`: a call started and has not returned.
 * - `waiting`: none of those; the last line was the run's, and the model
 *   has not answered yet.
 *
 * `since` is when that began, for the line's timer. A provider that streams
 * no deltas only ever shows `tool` and `waiting`, which is still more than
 * the nothing the transcript shows between rows.
 */
export interface Activity {
  what: 'answer' | 'thinking' | 'preparing' | 'tool' | 'waiting'
  label: string
  since: number
  chars?: number
  stalled: boolean
}

/** The structured-answer tools: the note, written as a tool call's input. */
const ANSWER_TOOLS = new Set(['StructuredOutput'])

interface OpenBlock {
  type: string
  name: string
  since: number
  chars: number
}

function record(v: unknown): Record<string, unknown> | undefined {
  return typeof v === 'object' && v !== null && !Array.isArray(v) ? (v as Record<string, unknown>) : undefined
}

function streamEvent(event: RunEvent): Record<string, unknown> | undefined {
  const raw = record(event.payload?.raw)
  return raw?.type === 'stream_event' ? record(raw.event) : undefined
}

/** How far back to look: the open block and the open call are near the end. */
const TAIL = 4000

export function currentActivity(events: IndexedEvent[], now: number): Activity | undefined {
  if (events.length === 0) return undefined
  const tail = events.slice(-TAIL)
  const lastAt = Date.parse(tail[tail.length - 1].event.t)

  // Content blocks of the message being written, by index, and the calls
  // that started without returning.
  let blocks = new Map<number, OpenBlock>()
  const calls: { tool: string; since: number }[] = []

  for (const { event } of tail) {
    const t = Date.parse(event.t)
    const se = streamEvent(event)
    if (se) {
      const index = typeof se.index === 'number' ? se.index : -1
      switch (se.type) {
        case 'message_start':
          blocks = new Map()
          break
        case 'content_block_start': {
          const block = record(se.content_block)
          blocks.set(index, { type: String(block?.type ?? ''), name: String(block?.name ?? ''), since: t, chars: 0 })
          break
        }
        case 'content_block_delta': {
          const open = blocks.get(index)
          const delta = record(se.delta)
          if (open && delta?.type === 'input_json_delta') open.chars += String(delta.partial_json ?? '').length
          break
        }
        case 'content_block_stop':
          blocks.delete(index)
          break
        case 'message_stop':
          blocks = new Map()
          break
      }
      continue
    }
    if (event.kind === 'tool_started') {
      calls.push({ tool: String(event.payload?.tool ?? ''), since: t })
    } else if (event.kind === 'tool_finished') {
      const tool = String(event.payload?.tool ?? '')
      const at = calls.map((c) => c.tool).lastIndexOf(tool)
      calls.splice(at === -1 ? 0 : at, 1)
    } else if (event.kind === 'final' || event.kind === 'steer' || event.kind === 'answer') {
      blocks = new Map()
      calls.length = 0
    }
  }

  const stalled = now - lastAt > STALL_MS
  const open = [...blocks.values()].pop()
  if (open?.type === 'tool_use' && ANSWER_TOOLS.has(open.name)) {
    return { what: 'answer', label: 'Writing the answer', since: open.since, chars: open.chars, stalled }
  }
  if (open?.type === 'thinking' || open?.type === 'redacted_thinking') {
    return { what: 'thinking', label: 'Thinking', since: open.since, stalled }
  }
  if (open?.type === 'tool_use' && open.name) {
    return { what: 'preparing', label: `Preparing ${toolLabel(open.name)}`, since: open.since, stalled }
  }
  const call = calls[calls.length - 1]
  if (call && !ANSWER_TOOLS.has(call.tool)) {
    return { what: 'tool', label: `Running ${toolLabel(call.tool)}`, since: call.since, stalled }
  }
  return { what: 'waiting', label: 'Waiting for the model', since: lastAt, stalled }
}
