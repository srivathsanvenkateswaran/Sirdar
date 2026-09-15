// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import '@testing-library/jest-dom/vitest'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { InboundDelivery } from '../store/appStore'
import { createFakeTransport, run, ticket, type FakeTransport } from '../store/fakeTransport'
import Board, { buildColumns, updatedAgo, type BoardProps } from './Board'

afterEach(() => {
  cleanup()
  vi.useRealTimers()
})

const TICKETS = [
  ticket({ key: 'OMNI-9', title: 'Statement export times out', assignee: 'sri' }),
  ticket({ key: 'OMNI-1', title: 'Login loop after reset', assignee: 'sri' }),
  ticket({ key: 'OMNI-3', title: 'Invoice total drops the VAT line', assignee: 'someone-else' }),
]

const RUNS = [
  run({ runId: 'r1', key: 'OMNI-1', status: 'running', provider: 'claude', updatedAt: '2026-09-10T09:05:00Z' }),
  run({ runId: 'r2', key: 'OMNI-2', kind: 'triage', status: 'completed', provider: 'codex' }),
  run({ runId: 'r3', key: 'OMNI-3', kind: 'fix', status: 'blocked', provider: 'claude' }),
  run({ runId: 'r4', key: 'OMNI-4', kind: 'rca', status: 'completed', provider: 'copilot' }),
  run({ runId: 'r5', key: 'OMNI-5', status: 'failed', provider: 'qwen' }),
]

function delivery(over: Partial<InboundDelivery> = {}): InboundDelivery {
  return {
    id: 1,
    source: 'jira',
    key: 'OMNI-1',
    outcome: 'started',
    at: '2026-09-10T09:05:00Z',
    ...over,
  }
}

/** The status line's text, with its numbers read out of their own elements. */
function status(container: HTMLElement): string {
  return container.querySelector('.board-status')?.textContent ?? ''
}

function lane(container: HTMLElement, id: string): HTMLElement {
  const el = container.querySelector(`[data-lane="${id}"]`)
  if (!el) throw new Error(`lane ${id} is not rendered`)
  return el as HTMLElement
}

function mount(over: Partial<BoardProps> = {}, transport: FakeTransport = createFakeTransport()) {
  const onOpenRun = vi.fn()
  const onTriage = vi.fn()
  const view = render(
    <Board
      transport={transport}
      workspaceId="ws1"
      provider="claude"
      tickets={TICKETS}
      runs={RUNS}
      queueUnsupported={false}
      loading={false}
      inbound={[]}
      onOpenRun={onOpenRun}
      onTriage={onTriage}
      {...over}
    />,
  )
  return { ...view, onOpenRun, onTriage, transport }
}

describe('Board', () => {
  it('lays the six lanes out with their counts and sorts each card into its lane', () => {
    const { container } = mount()

    for (const name of ['Queue (1)', 'Gathering (1)', 'Blocked (1)', 'Triaged (1)', 'Done (1)', 'Failed (1)']) {
      expect(screen.getByRole('region', { name })).toBeInTheDocument()
    }
    // A key with a run leaves the queue: OMNI-1 and OMNI-3 are runs, OMNI-9 waits.
    expect(within(lane(container, 'queue')).getByRole('button', { name: /OMNI-9/ })).toBeInTheDocument()
    expect(within(lane(container, 'queue')).queryByText('OMNI-1')).toBeNull()
    expect(within(lane(container, 'gathering')).getByRole('button', { name: /OMNI-1/ })).toBeInTheDocument()
    expect(within(lane(container, 'blocked')).getByRole('button', { name: /OMNI-3/ })).toBeInTheDocument()
    expect(within(lane(container, 'triaged')).getByRole('button', { name: /OMNI-2/ })).toBeInTheDocument()
    expect(within(lane(container, 'done')).getByRole('button', { name: /OMNI-4/ })).toBeInTheDocument()
    expect(within(lane(container, 'failed')).getByRole('button', { name: /OMNI-5/ })).toBeInTheDocument()
    // Done is the board's word for a completed RCA; the CLI's is completed.
    expect(within(lane(container, 'done')).getByText('done')).toBeInTheDocument()
    expect(within(lane(container, 'triaged')).getByText('completed')).toBeInTheDocument()
  })

  it('draws the queued ticket as a card whose name says a click starts a triage', () => {
    const { container, onTriage } = mount()
    const card = within(lane(container, 'queue')).getByRole('button', {
      name: 'Start triage of OMNI-9: Statement export times out',
    })
    expect(within(card).getByText('queued')).toBeInTheDocument()
    expect(within(card).getByText('triage')).toBeInTheDocument()
    fireEvent.click(card)
    expect(onTriage).toHaveBeenCalledWith(['OMNI-9'])
  })

  it('opens the run when its card is clicked', () => {
    const { container, onOpenRun } = mount()
    fireEvent.click(within(lane(container, 'blocked')).getByRole('button', { name: /OMNI-3/ }))
    expect(onOpenRun).toHaveBeenCalledWith('r3')
  })

  it('gives the live card the accent rail and a clock, and nothing else', () => {
    const { container } = mount()
    const live = within(lane(container, 'gathering')).getByRole('button', { name: /OMNI-1/ })
    expect(live).toHaveAttribute('data-live', 'true')
    expect(live.querySelector('.sd-state__clock')).not.toBeNull()
    const done = within(lane(container, 'done')).getByRole('button', { name: /OMNI-4/ })
    expect(done).not.toHaveAttribute('data-live')
    expect(container.querySelectorAll('.sd-run-card[data-live="true"]')).toHaveLength(1)
    // The lane's rail is a second copy of the same hue.
    expect(lane(container, 'gathering').querySelector('.sd-lane__rail')).not.toBeNull()
  })

  it('reports how many runs there are, how many are live, and when the newest changed', () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-09-10T09:05:12Z'))
    const { container } = mount()
    expect(status(container)).toBe('5 runs · 1 live · updated 12s ago')
  })

  it('says it is loading before the store has answered', () => {
    const { container } = mount({ runs: [], tickets: [], loading: true })
    expect(status(container)).toBe('Loading runs…')
  })

  it('says in prose what each empty lane means, and what to do without a tracker', () => {
    const { container } = mount({ runs: [], tickets: [], queueUnsupported: true })
    expect(status(container)).toBe('0 runs · 0 live')
    expect(within(lane(container, 'queue')).getByText('This workspace has no tracker; start triage by key.')).toBeInTheDocument()
    expect(within(lane(container, 'gathering')).getByText('No run is gathering evidence right now.')).toBeInTheDocument()
    expect(within(lane(container, 'failed')).getByText('Nothing has failed.')).toBeInTheDocument()
  })

  it('filters by key, title or provider from the well', () => {
    const { container } = mount()
    const well = screen.getByRole('searchbox', { name: 'Filter cards' })

    fireEvent.change(well, { target: { value: 'statement' } })
    expect(within(lane(container, 'queue')).getByRole('button', { name: /OMNI-9/ })).toBeInTheDocument()
    expect(within(lane(container, 'gathering')).queryByRole('button', { name: /OMNI-1/ })).toBeNull()
    expect(within(lane(container, 'gathering')).getByText('Nothing here matches “statement”.')).toBeInTheDocument()

    fireEvent.change(well, { target: { value: 'codex' } })
    expect(within(lane(container, 'triaged')).getByRole('button', { name: /OMNI-2/ })).toBeInTheDocument()
    expect(within(lane(container, 'failed')).queryByRole('button', { name: /OMNI-5/ })).toBeNull()

    fireEvent.change(well, { target: { value: 'omni-5' } })
    expect(within(lane(container, 'failed')).getByRole('button', { name: /OMNI-5/ })).toBeInTheDocument()
    expect(screen.getByRole('region', { name: 'Failed (1)' })).toBeInTheDocument()
    expect(screen.getByRole('region', { name: 'Queue (0)' })).toBeInTheDocument()

    fireEvent.keyDown(well, { key: 'Escape' })
    expect(well).toHaveValue('')
  })

  it('opens the quick filters from either control and narrows the lanes by kind', () => {
    const { container } = mount()
    const filters = screen.getByRole('button', { name: 'Filters' })
    expect(filters).toHaveAttribute('aria-expanded', 'false')
    expect(screen.queryByRole('radiogroup', { name: 'Kind' })).toBeNull()

    fireEvent.click(screen.getByRole('button', { name: 'Quick filters' }))
    expect(filters).toHaveAttribute('aria-expanded', 'true')
    fireEvent.click(within(screen.getByRole('radiogroup', { name: 'Kind' })).getByRole('radio', { name: 'Fix' }))

    expect(within(lane(container, 'blocked')).getByRole('button', { name: /OMNI-3/ })).toBeInTheDocument()
    expect(within(lane(container, 'queue')).queryByRole('button', { name: /OMNI-9/ })).toBeNull()
    expect(within(lane(container, 'queue')).getByText('Nothing here matches the filters.')).toBeInTheDocument()

    // A queued ticket is what a triage would be, so it counts as one.
    fireEvent.click(within(screen.getByRole('radiogroup', { name: 'Kind' })).getByRole('radio', { name: 'Triage' }))
    expect(within(lane(container, 'queue')).getByRole('button', { name: /OMNI-9/ })).toBeInTheDocument()
    expect(within(lane(container, 'blocked')).queryByRole('button', { name: /OMNI-3/ })).toBeNull()

    fireEvent.click(filters)
    expect(screen.queryByRole('radiogroup', { name: 'Kind' })).toBeNull()
  })

  it('asks the tracker which keys are the reader’s own for Mine, and keeps only those', async () => {
    const transport = createFakeTransport()
    const asked: unknown[] = []
    transport.queue = async (_ws, f) => {
      asked.push(f)
      return TICKETS.filter((t) => t.assignee === 'sri')
    }
    const { container } = mount({}, transport)

    fireEvent.click(screen.getByRole('button', { name: 'Filters' }))
    fireEvent.click(within(screen.getByRole('radiogroup', { name: 'Show' })).getByRole('radio', { name: 'Mine' }))

    await waitFor(() => expect(asked).toEqual([{ assignee: 'me' }]))
    await waitFor(() =>
      expect(within(lane(container, 'blocked')).queryByRole('button', { name: /OMNI-3/ })).toBeNull(),
    )
    expect(within(lane(container, 'gathering')).getByRole('button', { name: /OMNI-1/ })).toBeInTheDocument()
    expect(within(lane(container, 'queue')).getByRole('button', { name: /OMNI-9/ })).toBeInTheDocument()
    // OMNI-2 is nobody's in the queue, so it is not the reader's.
    expect(within(lane(container, 'triaged')).queryByRole('button', { name: /OMNI-2/ })).toBeNull()
  })

  it('keeps every card and says why when the tracker cannot answer Mine', async () => {
    const transport = createFakeTransport()
    transport.queue = async () => {
      throw new Error('tracker is down')
    }
    const { container } = mount({}, transport)

    fireEvent.click(screen.getByRole('button', { name: 'Filters' }))
    fireEvent.click(within(screen.getByRole('radiogroup', { name: 'Show' })).getByRole('radio', { name: 'Mine' }))

    expect(await screen.findByText(/Could not load your tickets\. tracker is down/)).toBeInTheDocument()
    expect(within(lane(container, 'blocked')).getByRole('button', { name: /OMNI-3/ })).toBeInTheDocument()
  })

  it('lists the landed deliveries with the outcome coloured and the reason in the meta line', () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-09-10T09:07:00Z'))
    const { container, onOpenRun } = mount({
      inbound: [
        delivery({ id: 3, key: 'OMNI-1', outcome: 'started', at: '2026-09-10T09:05:00Z' }),
        delivery({ id: 2, key: 'OMNI-9', source: 'zendesk', outcome: 'skipped', at: '2026-09-10T09:00:00Z' }),
        delivery({ id: 1, key: 'OMNI-7', outcome: 'rejected', at: '2026-09-10T08:53:00Z' }),
      ],
    })

    const landed = screen.getByRole('region', { name: 'Landed today' })
    const rows = landed.querySelectorAll('.sd-item')
    expect(rows).toHaveLength(3)

    // The title is the tracker's when the key is known, and the key otherwise.
    expect([...landed.querySelectorAll('.sd-item__title')].map((t) => t.textContent)).toEqual([
      'Login loop after reset',
      'Statement export times out',
      'OMNI-7',
    ])

    expect(rows[0]).toHaveAttribute('data-tone', 'live')
    expect(rows[1]).toHaveAttribute('data-tone', 'blocked')
    expect(rows[2]).toHaveAttribute('data-tone', 'failed')

    const outcomes = [...container.querySelectorAll('.board-landed__outcome')]
    expect(outcomes.map((o) => o.textContent)).toEqual(['started', 'skipped', 'rejected'])
    expect(outcomes.map((o) => o.getAttribute('data-outcome'))).toEqual(['started', 'skipped', 'rejected'])

    expect(rows[1].textContent).toContain('zendesk · skipped · that key is busy or in its cooldown · 7m ago')
    expect(rows[2].textContent).toContain('jira · rejected · the delivery did not verify · 14m ago')
    expect(rows[0].textContent).toContain('jira · started · 2m ago')

    // The started row has a run behind it, so the row opens it.
    fireEvent.click(within(landed).getByRole('button', { name: 'Open OMNI-1: Login loop after reset' }))
    expect(onOpenRun).toHaveBeenCalledWith('r1')
    expect(within(landed).queryByRole('button', { name: /OMNI-7/ })).toBeNull()
  })

  it('says so when nothing has landed', () => {
    mount({ inbound: [] })
    expect(screen.getByText(/No webhook delivery has arrived in this session/)).toBeInTheDocument()
  })

  it('moves a card between lanes as the store reports a change', () => {
    const { container, rerender, transport, onOpenRun, onTriage } = mount()
    expect(within(lane(container, 'gathering')).getByRole('button', { name: /OMNI-1/ })).toBeInTheDocument()

    const runs = RUNS.map((r) => (r.runId === 'r1' ? { ...r, status: 'blocked' as const } : r))
    rerender(
      <Board
        transport={transport}
        workspaceId="ws1"
        provider="claude"
        tickets={TICKETS}
        runs={runs}
        queueUnsupported={false}
        loading={false}
        inbound={[]}
        onOpenRun={onOpenRun}
        onTriage={onTriage}
      />,
    )
    expect(within(lane(container, 'gathering')).queryByRole('button', { name: /OMNI-1/ })).toBeNull()
    expect(within(lane(container, 'blocked')).getByRole('button', { name: /OMNI-1/ })).toBeInTheDocument()
    expect(screen.getByRole('region', { name: 'Blocked (2)' })).toBeInTheDocument()
    expect(status(container)).toMatch(/^5 runs · 0 live/)
  })
})

describe('buildColumns', () => {
  it('sends a key whose RCA is written to Done and keeps its triage out of Triaged', () => {
    const columns = buildColumns(
      [],
      [
        run({ runId: 'a', key: 'OMNI-1', kind: 'triage', status: 'completed' }),
        run({ runId: 'b', key: 'OMNI-1', kind: 'rca', status: 'completed' }),
      ],
    )
    const byId = Object.fromEntries(columns.map((c) => [c.id, c.cards.map((card) => card.key)]))
    expect(byId.done).toEqual(['OMNI-1'])
    expect(byId.triaged).toEqual([])
  })

  it('orders a lane by the run that changed last', () => {
    const columns = buildColumns(
      [],
      [
        run({ runId: 'old', key: 'OMNI-1', status: 'running', updatedAt: '2026-09-10T09:00:00Z' }),
        run({ runId: 'new', key: 'OMNI-2', status: 'running', updatedAt: '2026-09-10T09:30:00Z' }),
      ],
    )
    const gathering = columns.find((c) => c.id === 'gathering')
    expect(gathering?.cards.map((c) => c.key)).toEqual(['OMNI-2', 'OMNI-1'])
  })
})

describe('updatedAgo', () => {
  const now = Date.parse('2026-09-10T09:05:12Z')

  it('counts seconds under a minute and hands over to the shared clock past it', () => {
    expect(updatedAgo(Date.parse('2026-09-10T09:05:00Z'), now)).toBe('12s ago')
    expect(updatedAgo(Date.parse('2026-09-10T09:00:00Z'), now)).toBe('5m ago')
    expect(updatedAgo(Date.parse('2026-09-10T06:05:00Z'), now)).toBe('3h ago')
  })

  it('is empty when nothing has a stamp', () => {
    expect(updatedAgo(0, now)).toBe('')
    expect(updatedAgo(Number.NaN, now)).toBe('')
  })
})
