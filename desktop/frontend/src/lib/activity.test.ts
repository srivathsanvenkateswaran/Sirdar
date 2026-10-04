import { describe, expect, it } from 'vitest'
import type { IndexedEvent } from './events'
import { currentActivity, duringNote, NOTE_LABEL, readActivity, STALL_MS } from './activity'

// Event shapes from the 2026-10-04 OMNI-3413 rerun's events.jsonl.
let n = 0
function row(t: string, kind: string, payload: Record<string, unknown>): IndexedEvent {
  n += 1
  return { index: n, event: { t: `2026-10-04T10:${t}Z`, kind, payload } }
}
const stream = (t: string, event: Record<string, unknown>) => row(t, 'system', { text: 'stream_event', raw: { type: 'stream_event', event } })
const blockStart = (t: string, i: number, block: Record<string, unknown>) => stream(t, { type: 'content_block_start', index: i, content_block: block })
const json = (t: string, i: number, partial: string) => stream(t, { type: 'content_block_delta', index: i, delta: { type: 'input_json_delta', partial_json: partial } })
const blockStop = (t: string, i: number) => stream(t, { type: 'content_block_stop', index: i })
const at = (t: string) => Date.parse(`2026-10-04T10:${t}Z`)

describe('currentActivity', () => {
  it('says the answer is being written while the StructuredOutput input streams, with its size', () => {
    const events = [
      stream('29:13', { type: 'message_start' }),
      blockStart('29:13', 0, { type: 'thinking' }),
      blockStop('29:27', 0),
      blockStart('29:27', 1, { type: 'tool_use', name: 'StructuredOutput' }),
      json('29:30', 1, '{"ticket":{"key":'),
      json('30:10', 1, '"OMNI-3413"'),
    ]
    expect(currentActivity(events, at('30:12'))).toEqual({
      what: 'answer',
      label: 'Writing the answer',
      since: at('29:27'),
      silentSince: at('30:10'),
      chars: 28,
      stalled: false,
    })
  })

  it('says the model is thinking while a thinking block is open', () => {
    const events = [stream('29:13', { type: 'message_start' }), blockStart('29:13', 0, { type: 'thinking' })]
    expect(currentActivity(events, at('29:20'))).toMatchObject({ what: 'thinking', label: 'Thinking', since: at('29:13') })
  })

  it('names a tool whose call is still being written', () => {
    const events = [stream('28:20', { type: 'message_start' }), blockStart('28:20', 0, { type: 'tool_use', name: 'mcp__metabase__run_query' })]
    expect(currentActivity(events, at('28:21'))).toMatchObject({ what: 'preparing', label: 'Preparing metabase/run_query' })
  })

  it('names a call that started and has not returned', () => {
    const events = [
      blockStart('28:41', 0, { type: 'tool_use', name: 'mcp__metabase__run_query' }),
      blockStop('28:42', 0),
      row('28:42', 'tool_started', { tool: 'mcp__metabase__run_query' }),
    ]
    expect(currentActivity(events, at('28:50'))).toMatchObject({ what: 'tool', label: 'Running metabase/run_query', since: at('28:42') })
  })

  it('waits on the model once the last call returned, and calls a long silence a stall', () => {
    const events = [row('28:42', 'tool_started', { tool: 'Bash' }), row('28:43', 'tool_finished', { tool: 'Bash', text: 'ok' })]
    expect(currentActivity(events, at('28:50'))).toMatchObject({ what: 'waiting', label: 'Waiting for the model', since: at('28:43'), stalled: false })
    expect(currentActivity(events, at('28:43') + STALL_MS + 1000)).toMatchObject({ what: 'waiting', stalled: true })
  })

  it('says nothing about a log with no events', () => {
    expect(currentActivity([], at('28:00'))).toBeUndefined()
  })

  it('measures the stall from the last event, not from the open block — a 66s answer then silence reads under 2 minutes once the silence itself has lasted just past that', () => {
    const events = [
      stream('29:13', { type: 'message_start' }),
      blockStart('29:13', 0, { type: 'thinking' }),
      blockStop('29:27', 0),
      blockStart('29:27', 1, { type: 'tool_use', name: 'StructuredOutput' }),
      json('29:30', 1, 'x'),
      // The block has been open 66s by 30:33; nothing has arrived since.
      json('29:33', 1, 'y'),
    ]
    // At 30:33 + STALL_MS it is silent for exactly STALL_MS from the 29:33
    // delta, not from the 29:27 block open — `since` alone would call this
    // stalled far earlier than the real two minutes of silence.
    const justUnder = currentActivity(events, at('29:33') + STALL_MS - 1000)
    expect(justUnder).toMatchObject({ stalled: false, silentSince: at('29:33') })
    const justOver = currentActivity(events, at('29:33') + STALL_MS + 1000)
    expect(justOver).toMatchObject({ stalled: true, silentSince: at('29:33') })
  })
})

describe('readActivity', () => {
  it('parses the same shape as currentActivity, minus `stalled`', () => {
    const events = [stream('29:13', { type: 'message_start' }), blockStart('29:13', 0, { type: 'thinking' })]
    expect(readActivity(events)).toEqual({ what: 'thinking', label: 'Thinking', since: at('29:13'), silentSince: at('29:13') })
  })
})

describe('duringNote', () => {
  it('says the note is being filed and keeps the tail’s timer and size', () => {
    const events = [
      stream('30:40', { type: 'message_start' }),
      blockStart('30:40', 0, { type: 'tool_use', name: 'StructuredOutput' }),
      json('30:50', 0, '{"summary":"x"}'),
    ]
    const parsed = readActivity(events)!
    expect(duringNote(parsed)).toEqual({ ...parsed, what: 'note', label: NOTE_LABEL })
    expect(NOTE_LABEL).toBe('Filing the note…')
  })
})
