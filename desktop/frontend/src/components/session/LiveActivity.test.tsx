import { act, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { IndexedEvent } from '../../lib/events'
import { STALL_MS } from '../../lib/activity'
import LiveActivity from './LiveActivity'

// Event shapes from the 2026-10-04 OMNI-3413 rerun's events.jsonl, the same
// ones `activity.test.ts` builds its fixtures from.
let n = 0
function row(t: string, kind: string, payload: Record<string, unknown>): IndexedEvent {
  n += 1
  return { index: n, event: { t: `2026-10-04T10:${t}Z`, kind, payload } }
}
const stream = (t: string, event: Record<string, unknown>) => row(t, 'system', { text: 'stream_event', raw: { type: 'stream_event', event } })
const blockStart = (t: string, i: number, block: Record<string, unknown>) => stream(t, { type: 'content_block_start', index: i, content_block: block })
const json = (t: string, i: number, partial: string) => stream(t, { type: 'content_block_delta', index: i, delta: { type: 'input_json_delta', partial_json: partial } })
const at = (t: string) => Date.parse(`2026-10-04T10:${t}Z`)

describe('LiveActivity', () => {
  beforeEach(() => {
    vi.useFakeTimers()
  })
  afterEach(() => {
    vi.useRealTimers()
  })

  it('renders nothing when the run is not working', () => {
    vi.setSystemTime(at('30:12'))
    const events = [stream('29:13', { type: 'message_start' }), blockStart('29:13', 0, { type: 'thinking' })]
    render(<LiveActivity events={events} working={false} />)
    expect(screen.queryByTestId('live-activity')).toBeNull()
  })

  it('shows the answer streaming, its elapsed time ticking, and its size', () => {
    vi.setSystemTime(at('30:12'))
    // tokens() (lib/format.ts) rounds a k-count of 10 or more to the nearest
    // thousand, so 21,900 characters reads as "22k" — the same rule that
    // already holds tokens(12_400) to "12k" in format.test.ts.
    const padding = '0'.repeat(21_900)
    const events = [
      stream('29:13', { type: 'message_start' }),
      blockStart('29:13', 0, { type: 'thinking' }),
      stream('29:27', { type: 'content_block_stop', index: 0 }),
      blockStart('29:27', 1, { type: 'tool_use', name: 'StructuredOutput' }),
      json('29:30', 1, padding),
    ]
    render(<LiveActivity events={events} working={true} />)
    expect(screen.getByTestId('live-activity')).toHaveTextContent('Writing the answer')
    expect(screen.getByTestId('live-activity')).toHaveTextContent('0:45')
    expect(screen.getByTestId('live-activity')).toHaveTextContent('22k characters')

    act(() => vi.advanceTimersByTime(1000))
    expect(screen.getByTestId('live-activity')).toHaveTextContent('0:46')
  })

  it('calls a long silence a stall after STALL_MS and sets data-stalled', () => {
    vi.setSystemTime(at('28:43'))
    const events = [row('28:42', 'tool_started', { tool: 'Bash' }), row('28:43', 'tool_finished', { tool: 'Bash', text: 'ok' })]
    render(<LiveActivity events={events} working={true} />)
    expect(screen.getByTestId('live-activity')).not.toHaveAttribute('data-stalled')

    act(() => vi.advanceTimersByTime(STALL_MS + 1000))
    expect(screen.getByTestId('live-activity')).toHaveAttribute('data-stalled')
    // STALL_MS plus the test's own 1s margin; "No output for 2:0x" either way.
    expect(screen.getByTestId('live-activity')).toHaveTextContent(/No output for 2:0\d/)
  })

  it('measures the stalled label from the silence after a long answer stream, not from the stream itself', () => {
    // The answer block opens at 29:13 and streams for 66s, its last delta
    // landing at 30:19; then nothing arrives for just over STALL_MS. Had the
    // stalled label measured from the block's own open time (29:13) rather
    // than that last delta, "now" here — 30:19 + STALL_MS + 1s — would read
    // as roughly 3:06 of silence instead of the roughly 2:00 that actually
    // elapsed since the stream went quiet.
    vi.setSystemTime(at('30:19') + STALL_MS + 1000)
    const events = [
      stream('29:13', { type: 'message_start' }),
      blockStart('29:13', 0, { type: 'tool_use', name: 'StructuredOutput' }),
      json('30:19', 0, 'x'),
    ]
    render(<LiveActivity events={events} working={true} />)
    expect(screen.getByTestId('live-activity')).toHaveTextContent(/No output for 2:0\d/)
  })

  it('says the note is being filed during the note phase', () => {
    vi.setSystemTime(at('31:00'))
    const events = [
      stream('30:40', { type: 'message_start' }),
      blockStart('30:40', 0, { type: 'tool_use', name: 'StructuredOutput' }),
      json('30:50', 0, '{"summary":"x"}'),
    ]
    render(<LiveActivity events={events} working={true} phase="note" />)
    const line = screen.getByRole('status')
    expect(line).toHaveAttribute('data-what', 'note')
    expect(line).toHaveTextContent('Filing the note…')
    expect(line).not.toHaveTextContent('Writing the answer')
    expect(line).toHaveTextContent('0:20')
  })

  it('says the note is being filed before the note turn has written a line', () => {
    vi.setSystemTime(at('31:00'))
    render(<LiveActivity events={[]} working={true} phase="note" />)
    const line = screen.getByRole('status')
    expect(line).toHaveAttribute('data-what', 'note')
    expect(line).toHaveTextContent(/^Filing the note…$/)
  })

  it('says nothing of the note once the run stops working', () => {
    render(<LiveActivity events={[]} working={false} phase="note" />)
    expect(screen.queryByTestId('live-activity')).toBeNull()
  })

  it('updates the label when a new event arrives', () => {
    vi.setSystemTime(at('28:50'))
    const base = [blockStart('28:20', 0, { type: 'tool_use', name: 'mcp__metabase__run_query' })]
    const { rerender } = render(<LiveActivity events={base} working={true} />)
    expect(screen.getByTestId('live-activity')).toHaveTextContent('Preparing metabase/run_query')

    const withCall = [...base, stream('28:41', { type: 'content_block_stop', index: 0 }), row('28:41', 'tool_started', { tool: 'mcp__metabase__run_query' })]
    rerender(<LiveActivity events={withCall} working={true} />)
    expect(screen.getByTestId('live-activity')).toHaveTextContent('Running metabase/run_query')
  })
})
