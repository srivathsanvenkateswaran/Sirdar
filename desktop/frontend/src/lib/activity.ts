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
 * - `note`: a reply-first run has replied and is filing its note. The log
 *   cannot say this — the run's summary does — so `duringNote` lays it over
 *   whatever the tail read.
 *
 * `since` is when that began, for the line's timer — it can be a minute or
 * more into a long answer stream. `silentSince` is the last event's own
 * time, which is what "stalled" and "No output for X" measure from: a run
 * can be legitimately 66 seconds into an answer and 0 seconds into the
 * silence that follows it, and the two must not be conflated.
 */
export interface Activity {
  what: 'answer' | 'thinking' | 'preparing' | 'tool' | 'waiting' | 'note'
  label: string
  since: number
  silentSince: number
  chars?: number
  stalled: boolean
}

/** `Activity` without `stalled`: the part that only changes when the log does. */
export type ParsedActivity = Omit<Activity, 'stalled'>

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

/**
 * The parsed half of `currentActivity`: everything that depends only on
 * `events`, not on the clock. A caller that ticks a clock every second —
 * `LiveActivity`, say — re-parses the log once per change instead of once
 * per tick by memoizing this and computing `stalled` separately.
 */
export function readActivity(events: IndexedEvent[]): ParsedActivity | undefined {
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

  const open = [...blocks.values()].pop()
  if (open?.type === 'tool_use' && ANSWER_TOOLS.has(open.name)) {
    return { what: 'answer', label: 'Writing the answer', since: open.since, silentSince: lastAt, chars: open.chars }
  }
  if (open?.type === 'thinking' || open?.type === 'redacted_thinking') {
    return { what: 'thinking', label: 'Thinking', since: open.since, silentSince: lastAt }
  }
  if (open?.type === 'tool_use' && open.name) {
    return { what: 'preparing', label: `Preparing ${toolLabel(open.name)}`, since: open.since, silentSince: lastAt }
  }
  const call = calls[calls.length - 1]
  if (call && !ANSWER_TOOLS.has(call.tool)) {
    return { what: 'tool', label: `Running ${toolLabel(call.tool)}`, since: call.since, silentSince: lastAt }
  }
  return { what: 'waiting', label: 'Waiting for the model', since: lastAt, silentSince: lastAt }
}

/** `readActivity` plus `stalled`, measured from `silentSince` against `now`. */
export function currentActivity(events: IndexedEvent[], now: number): Activity | undefined {
  const parsed = readActivity(events)
  if (!parsed) return undefined
  return { ...parsed, stalled: now - parsed.silentSince > STALL_MS }
}

/** What the activity line says while a reply-first run files its note. */
export const NOTE_LABEL = 'Filing the note…'

/**
 * The tail's reading, said as the note being filed. The chat already holds
 * the reply, so "Writing the answer" over the note's JSON would read as a
 * second answer on its way; the timer and the size still come from the
 * tail, since how long the note has been streaming is worth knowing.
 */
export function duringNote(parsed: ParsedActivity): ParsedActivity {
  return { ...parsed, what: 'note', label: NOTE_LABEL }
}
