import { act, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { RunDetail, RunEvent } from '../../api/types'
import { PrimaryActionProvider } from '../../components/shell/primaryAction'
import { renderCount, resetRenderCounts } from '../../lib/renderProbe'
import { createFakeTransport, type FakeTransport } from '../../store/fakeTransport'
import { TRIAGE_DETAIL, stream, triageEvents } from './fixtures'
import Session from '../Session'

/*
 * What a streamed line costs the Session window.
 *
 * `14-session-perf.md` finding #5: replaying 120 `system` events into a
 * 2100-event run changed no character of the transcript and still cost 35
 * React commits and 5.4 component renders a line, because the model was
 * rebuilt from the whole log on every array identity and every row was
 * drawn again from the new objects. The model is incremental now and the
 * rows are held, so these hold the line: a line the transcript does not
 * draw re-renders nothing below the screen itself, and a line it does draw
 * costs the rows it touched and no others.
 */

const RUNNING: RunDetail = { ...TRIAGE_DETAIL, status: 'running' }

function mount(events: RunEvent[], detail: RunDetail = RUNNING): FakeTransport {
  const transport = createFakeTransport()
  transport.run = vi.fn(async () => detail)
  transport.events = vi.fn(async () => ({ events, next: events.length }))
  transport.note = vi.fn(async () => '')
  transport.prompt = vi.fn(async () => '')
  render(
    <PrimaryActionProvider>
      <Session
        transport={transport}
        workspaceId="ws1"
        runId={detail.runId}
        onBack={() => {}}
        onOpenReview={() => {}}
      />
    </PrimaryActionProvider>,
  )
  return transport
}

/** Delivers one line the way the stream does, each on its own task. */
function line(transport: FakeTransport, index: number, event: RunEvent): void {
  act(() => {
    transport.emit({ kind: 'run.event', workspaceId: 'ws1', runId: RUNNING.runId, index, event })
  })
}

afterEach(() => {
  resetRenderCounts()
})

describe('what a streamed line costs', () => {
  it('costs one render for 120 lines the transcript does not show', async () => {
    const backfill = triageEvents()
    const transport = mount(backfill)
    const stream$ = await screen.findByTestId('conversation')
    const before = stream$.innerHTML

    resetRenderCounts()
    // The report's scenario: a line every 50ms for six seconds, on the clock.
    vi.useFakeTimers()
    try {
      for (let i = 0; i < 120; i += 1) {
        line(transport, backfill.length + i + 1, stream(`2026-09-15T12:14:${String(i % 60).padStart(2, '0')}Z`))
        act(() => {
          vi.advanceTimersByTime(50)
        })
      }
      // And the stream going quiet, which is when the held lines land.
      act(() => {
        vi.advanceTimersByTime(500)
      })
    } finally {
      vi.useRealTimers()
    }

    // Not a character moved, no card below the screen was drawn again, and
    // the whole six seconds cost the screen the one render that put the
    // held lines in the list. The report measured 35 commits for this.
    expect(stream$.innerHTML).toBe(before)
    expect(renderCount('ToolStep')).toBe(0)
    expect(renderCount('AnswerCard')).toBe(0)
    expect(renderCount('SessionConversation')).toBeLessThanOrEqual(5)

    // And nothing was dropped on the way: the line after them draws, in its
    // place, off a list that still carries all 120.
    line(transport, backfill.length + 121, {
      t: '2026-09-15T12:14:30Z',
      kind: 'tool_started',
      payload: {
        tool: 'Bash',
        raw: { type: 'assistant', message: { content: [{ type: 'tool_use', id: 'tu-after', name: 'Bash', input: { command: 'go vet ./...', description: 'Vet the module' } }] } },
      },
    })
    await screen.findByRole('button', { name: /Vet the module/ })
  })

  it('draws only the rows a line that does show touches', async () => {
    const backfill = triageEvents()
    const transport = mount(backfill)
    await screen.findByTestId('conversation')
    // Everything the screen asks for on open has landed before the count
    // starts: what is measured is the line, not the opening.
    await act(async () => {})

    resetRenderCounts()
    line(transport, backfill.length + 1, {
      t: '2026-09-15T12:14:00Z',
      kind: 'tool_started',
      payload: {
        tool: 'Bash',
        raw: { type: 'assistant', message: { content: [{ type: 'tool_use', id: 'tu-live', name: 'Bash', input: { command: 'go vet ./...', description: 'Vet the module' } }] } },
      },
    })

    await screen.findByRole('button', { name: /Vet the module/ })
    // The new call's card, and nothing else: the answer at the end of the
    // transcript and the fifteen cards above it are the elements the render
    // before produced, so React leaves those subtrees alone.
    expect(renderCount('ToolStep')).toBe(1)
    expect(renderCount('AnswerCard')).toBe(0)
    expect(renderCount('SessionConversation') + renderCount('ToolStep') + renderCount('AnswerCard')).toBeLessThanOrEqual(2)
  })
})
