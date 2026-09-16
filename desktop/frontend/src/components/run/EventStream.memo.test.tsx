import { render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it } from 'vitest'
import type { RunEvent } from '../../api/types'
import type { IndexedEvent } from '../../lib/events'
import { renderCount, resetRenderCounts } from '../../lib/renderProbe'
import EventStream, { stableTurns } from './EventStream'

/**
 * What one more line costs the transcript. The turns are re-grouped on every
 * event, and every turn used to be re-read and re-drawn for it; the memoised
 * TurnGroup and `stableTurns` hold the line at one turn — the one the event
 * landed in.
 */

afterEach(() => {
  resetRenderCounts()
})

const at = (s: number) => new Date(Date.UTC(2026, 8, 16, 10, 0, s)).toISOString()

function ev(index: number, kind: RunEvent['kind'], payload: Record<string, unknown> = {}): IndexedEvent {
  return { index, event: { t: at(index), kind, payload } as RunEvent }
}

/** Three turns, each a message, a tool call with its result, and the usage tick that closes it. */
function turns(count: number): IndexedEvent[] {
  const out: IndexedEvent[] = []
  let i = 1
  for (let n = 0; n < count; n++) {
    out.push(ev(i++, 'assistant_text', { text: `Turn ${n + 1} says hello.` }))
    out.push(ev(i++, 'tool_started', { tool: 'read_file', id: `c${n}`, input: { path: `f${n}.go` } }))
    out.push(ev(i++, 'tool_finished', { tool: 'read_file', id: `c${n}`, output: 'ok' }))
    out.push(ev(i++, 'usage', { turns: n + 1, costUsd: 0.01 * (n + 1) }))
  }
  return out
}

describe('the transcript on one more line', () => {
  it('re-reads only the turn the line landed in', () => {
    const events = turns(3)
    const view = render(<EventStream events={events} startedAt={at(0)} live />)
    expect(screen.getAllByRole('region', { name: /turn \d/ })).toHaveLength(3)
    expect(renderCount('TurnGroup')).toBe(3)

    resetRenderCounts()
    const more = [...events, ev(events.length + 1, 'assistant_text', { text: 'And then,' })]
    view.rerender(<EventStream events={more} startedAt={at(0)} live />)
    expect(screen.getAllByRole('region', { name: /turn \d/ })).toHaveLength(4)
    // The line opened a fourth turn: that one is drawn, the three before it are not.
    expect(renderCount('TurnGroup')).toBe(1)

    resetRenderCounts()
    const grown = [...more, ev(more.length + 1, 'assistant_text', { text: ' more.' })]
    view.rerender(<EventStream events={grown} startedAt={at(0)} live />)
    expect(renderCount('TurnGroup')).toBe(1)
    expect(screen.getByText(/And then,/)).toBeInTheDocument()
  })

  it('a render that changed nothing about the events draws no turn', () => {
    const events = turns(3)
    const view = render(<EventStream events={events} startedAt={at(0)} live />)
    resetRenderCounts()
    view.rerender(<EventStream events={events} startedAt={at(0)} live />)
    expect(renderCount('TurnGroup')).toBe(0)
  })
})

describe('stableTurns', () => {
  it('keeps the previous object for a turn whose events are the same', () => {
    const events = turns(2)
    const a = [
      { n: 1, events: events.slice(0, 4), turns: 1, costUsd: 0.01 },
      { n: 2, events: events.slice(4, 8), turns: 2, costUsd: 0.02 },
    ]
    const b = [
      { n: 1, events: events.slice(0, 4), turns: 1, costUsd: 0.01 },
      { n: 2, events: [...events.slice(4, 8), ev(9, 'assistant_text', { text: 'x' })], turns: 2, costUsd: 0.02 },
    ]
    const out = stableTurns(a, b)
    expect(out[0]).toBe(a[0])
    expect(out[1]).toBe(b[1])
    expect(stableTurns(a, a.map((t) => ({ ...t })))).toBe(a)
  })
})
