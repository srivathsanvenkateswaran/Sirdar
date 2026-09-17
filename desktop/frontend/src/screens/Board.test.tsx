// @vitest-environment jsdom
import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import '@testing-library/jest-dom/vitest'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { resetSessionsShow, setSessionsShow } from '../lib/sessionsShow'
import { FILTER_DEBOUNCE_MS } from '../lib/useDebounced'
import type { InboundDelivery } from '../store/appStore'
import type { MeSummary } from '../api/types'
import { createFakeTransport, run, ticket, type FakeTransport } from '../store/fakeTransport'
import Board, {
  buildColumns,
  NO_IDENTITY,
  queueEmptyText,
  updatedAgo,
  type BoardProps,
} from './Board'

/** Who the sample workspace says the reader is. */
const ME: MeSummary = { email: 'sri@acme.com', names: ['Sri Venkateswaran', 'sri'], source: 'me' }

afterEach(() => {
  cleanup()
  vi.useRealTimers()
  localStorage.clear()
  resetSessionsShow()
})

const TICKETS = [
  ticket({
    key: 'OMNI-9',
    title: 'Statement export times out',
    assignee: 'sri',
    url: 'https://acme.atlassian.net/browse/OMNI-9',
  }),
  ticket({ key: 'OMNI-1', title: 'Login loop after reset', assignee: 'sri' }),
  ticket({ key: 'OMNI-3', title: 'Invoice total drops the VAT line', assignee: 'someone-else' }),
]

/** Two of these five belong to somebody else, which is what Mine narrows to. */
const RUNS = [
  run({ runId: 'r1', key: 'OMNI-1', status: 'running', provider: 'claude', updatedAt: '2026-09-10T09:05:00Z' }),
  run({ runId: 'r2', key: 'OMNI-2', kind: 'triage', status: 'completed', provider: 'codex', assignee: 'rana@acme.com', mine: false }),
  run({ runId: 'r3', key: 'OMNI-3', kind: 'fix', status: 'blocked', provider: 'claude', assignee: 'someone-else', mine: false }),
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

/**
 * The Queue lane is the tracker's answer to `assignee: me`, so a test that
 * wants cards in it seeds the transport, not the `tickets` prop. The default
 * seed is the three tickets above, of which the fake calls two the reader's.
 */
function mount(
  over: Partial<BoardProps> = {},
  transport: FakeTransport = createFakeTransport({ tickets: TICKETS }),
) {
  const onOpenRun = vi.fn()
  const onTriage = vi.fn()
  const view = render(
    <Board
      transport={transport}
      workspaceId="ws1"
      provider="claude"
      tickets={TICKETS}
      runs={RUNS}
      me={ME}
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

/** Opens the filters row and the assignee menu, and hands back its listbox. */
function openAssignees(): HTMLElement {
  fireEvent.click(screen.getByRole('button', { name: 'Filters' }))
  fireEvent.click(screen.getByRole('button', { name: /^Assignee/ }))
  return screen.getByRole('listbox', { name: 'Assignee' })
}

/** The assignee trigger's words, without the label in front of them. */
function triggerWords(): string {
  return screen.getByRole('button', { name: /^Assignee/ }).textContent?.trim() ?? ''
}

describe('Board', () => {
  it('lays the six lanes out with their counts and sorts each card into its lane', async () => {
    const { container } = mount()

    await screen.findByRole('region', { name: 'Queue (1)' })
    for (const name of ['Queue (1)', 'Gathering (1)', 'Blocked (1)', 'Triaged (1)', 'Done (1)', 'Failed (1)']) {
      expect(screen.getByRole('region', { name })).toBeInTheDocument()
    }
    // A key with a run leaves the queue: OMNI-1 and OMNI-3 are runs, OMNI-9 waits.
    expect(within(lane(container, 'queue')).getByRole('link', { name: /OMNI-9/ })).toBeInTheDocument()
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

  it('shows the helpdesk number on that preference, with the key in the tooltip, and the key where a run has none', async () => {
    setSessionsShow('helpdesk')
    const sources = {
      tracker: { adapter: 'jira', name: 'Jira', host: 'acme.atlassian.net' },
      helpdesk: { adapter: 'zohodesk', name: 'Zoho Desk', host: 'desk.zoho.com' },
    }
    const runs = [
      run({ runId: 'r1', key: 'OMNI-1', helpdeskKey: '25312', status: 'running', title: 'Login loop' }),
      run({ runId: 'r2', key: 'OMNI-2', status: 'completed', title: 'Invoice total' }),
    ]
    const tickets = [
      ticket({
        key: 'OMNI-9',
        helpdeskRef: '25401',
        title: 'Statement export times out',
        assignee: 'sri',
        url: 'https://acme.atlassian.net/browse/OMNI-9',
      }),
    ]
    const { container } = mount({ runs, sources }, createFakeTransport({ tickets }))
    await screen.findByRole('region', { name: 'Queue (1)' })

    const live = within(lane(container, 'gathering')).getByRole('button', { name: /#25312/ })
    expect(live.querySelector('.sd-run-card__key')).toHaveTextContent('#25312')
    expect(live.querySelector('.sd-run-card__key')).toHaveAttribute('title', 'Jira OMNI-1')
    // No helpdesk number: the key, with nothing to point at.
    const done = within(lane(container, 'triaged')).getByRole('button', { name: /OMNI-2/ })
    expect(done.querySelector('.sd-run-card__key')).toHaveTextContent('OMNI-2')
    expect(done.querySelector('.sd-run-card__key')).not.toHaveAttribute('title')
    // A queued ticket follows the same preference through its helpdeskRef.
    const queued = within(lane(container, 'queue')).getByRole('link', { name: /#25401/ })
    expect(queued.querySelector('.sd-run-card__key')).toHaveAttribute('title', 'Jira OMNI-9')
  })

  it('draws the queued ticket as a link to the tracker, with Triage a button of its own', async () => {
    const { container, onTriage } = mount()
    await screen.findByRole('region', { name: 'Queue (1)' })
    const queue = within(lane(container, 'queue'))
    const card = queue.getByRole('link', {
      name: 'OMNI-9: Statement export times out, queued, assigned to sri',
    })
    expect(card).toHaveAttribute('href', 'https://acme.atlassian.net/browse/OMNI-9')
    expect(within(card).getByText('queued')).toBeInTheDocument()
    expect(within(card).getByText('triage')).toBeInTheDocument()

    // The card's body spends nothing; only the button starts the run.
    fireEvent.click(card)
    expect(onTriage).not.toHaveBeenCalled()
    fireEvent.click(queue.getByRole('button', { name: 'Triage OMNI-9' }))
    expect(onTriage).toHaveBeenCalledWith(['OMNI-9'])
  })

  it('draws a queued ticket with no tracker page as a plain card, still with its Triage button', async () => {
    const { container, onTriage } = mount(
      {},
      createFakeTransport({
        tickets: [ticket({ key: 'OMNI-8', title: 'No URL', url: '', assignee: 'sri' })],
      }),
    )
    await screen.findByRole('region', { name: 'Queue (1)' })
    const queue = within(lane(container, 'queue'))
    expect(queue.queryByRole('link')).toBeNull()
    expect(queue.getAllByRole('button')).toHaveLength(1)
    fireEvent.click(queue.getByRole('button', { name: 'Triage OMNI-8' }))
    expect(onTriage).toHaveBeenCalledWith(['OMNI-8'])
  })

  it('titles a run card from the run itself, and shows the key once when nothing names it', () => {
    const { container } = mount({
      tickets: [],
      runs: [
        run({ runId: 'r1', key: 'OMNI-1', status: 'running', title: 'Login loop after reset' }),
        run({ runId: 'r6', key: 'OMNI-6', status: 'running' }),
      ],
    })
    const gathering = within(lane(container, 'gathering'))
    expect(gathering.getByRole('button', { name: /^OMNI-1: Login loop after reset, running/ })).toBeInTheDocument()
    const bare = gathering.getByRole('button', { name: /^OMNI-6, running/ })
    expect(within(bare).getAllByText('OMNI-6')).toHaveLength(1)
    expect(bare.querySelector('.sd-run-card__key')).toBeNull()
  })

  it('prefers the run’s own title to the tracker’s', () => {
    const { container } = mount({
      runs: [run({ runId: 'r1', key: 'OMNI-1', status: 'running', title: 'What the bundle recorded' })],
    })
    expect(
      within(lane(container, 'gathering')).getByRole('button', {
        name: /^OMNI-1: What the bundle recorded, running/,
      }),
    ).toBeInTheDocument()
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
    const { container } = mount(
      { runs: [], tickets: [], queueUnsupported: true },
      createFakeTransport(),
    )
    expect(status(container)).toBe('0 runs · 0 live')
    // No tracker, no "assigned to you": the lane is narrowed by nothing.
    expect(lane(container, 'queue').querySelector('.sd-lane__note')).toBeNull()
    expect(within(lane(container, 'queue')).getByText('This workspace has no tracker; start triage by key.')).toBeInTheDocument()
    expect(within(lane(container, 'gathering')).getByText('No run is gathering evidence right now.')).toBeInTheDocument()
    expect(within(lane(container, 'failed')).getByText('Nothing has failed.')).toBeInTheDocument()
  })

  it('filters by key, title or provider from the well, a beat after the keystroke', async () => {
    const { container } = mount()
    await screen.findByRole('region', { name: 'Queue (1)' })
    const well = screen.getByRole('searchbox', { name: 'Filter cards' })
    vi.useFakeTimers()
    // The field takes the keystroke at once; the lanes narrow once it has held still.
    const type = (value: string) => {
      fireEvent.change(well, { target: { value } })
      expect(well).toHaveValue(value)
      act(() => {
        vi.advanceTimersByTime(FILTER_DEBOUNCE_MS)
      })
    }

    fireEvent.change(well, { target: { value: 'statement' } })
    expect(within(lane(container, 'gathering')).getByRole('button', { name: /OMNI-1/ })).toBeInTheDocument()
    act(() => {
      vi.advanceTimersByTime(FILTER_DEBOUNCE_MS)
    })
    expect(within(lane(container, 'queue')).getByRole('link', { name: /OMNI-9/ })).toBeInTheDocument()
    expect(within(lane(container, 'gathering')).queryByRole('button', { name: /OMNI-1/ })).toBeNull()
    expect(within(lane(container, 'gathering')).getByText('Nothing here matches “statement”.')).toBeInTheDocument()

    type('codex')
    expect(within(lane(container, 'triaged')).getByRole('button', { name: /OMNI-2/ })).toBeInTheDocument()
    expect(within(lane(container, 'failed')).queryByRole('button', { name: /OMNI-5/ })).toBeNull()

    type('omni-5')
    expect(within(lane(container, 'failed')).getByRole('button', { name: /OMNI-5/ })).toBeInTheDocument()
    expect(screen.getByRole('region', { name: 'Failed (1)' })).toBeInTheDocument()
    expect(screen.getByRole('region', { name: 'Queue (0)' })).toBeInTheDocument()

    // Clearing is not a beat late: the lanes fill back in with the field.
    fireEvent.keyDown(well, { key: 'Escape' })
    expect(well).toHaveValue('')
    expect(screen.getByRole('region', { name: 'Queue (1)' })).toBeInTheDocument()
  })

  it('opens the quick filters from either control and narrows the lanes by kind', async () => {
    const { container } = mount()
    await screen.findByRole('region', { name: 'Queue (1)' })
    const filters = screen.getByRole('button', { name: 'Filters' })
    expect(filters).toHaveAttribute('aria-expanded', 'false')
    expect(screen.queryByRole('radiogroup', { name: 'Kind' })).toBeNull()

    fireEvent.click(screen.getByRole('button', { name: 'Quick filters' }))
    expect(filters).toHaveAttribute('aria-expanded', 'true')
    fireEvent.click(within(screen.getByRole('radiogroup', { name: 'Kind' })).getByRole('radio', { name: 'Fix' }))

    expect(within(lane(container, 'blocked')).getByRole('button', { name: /OMNI-3/ })).toBeInTheDocument()
    expect(within(lane(container, 'queue')).queryByRole('link', { name: /OMNI-9/ })).toBeNull()
    expect(within(lane(container, 'queue')).getByText('Nothing here matches the filters.')).toBeInTheDocument()

    // A queued ticket is what a triage would be, so it counts as one.
    fireEvent.click(within(screen.getByRole('radiogroup', { name: 'Kind' })).getByRole('radio', { name: 'Triage' }))
    expect(within(lane(container, 'queue')).getByRole('link', { name: /OMNI-9/ })).toBeInTheDocument()
    expect(within(lane(container, 'blocked')).queryByRole('button', { name: /OMNI-3/ })).toBeNull()

    fireEvent.click(filters)
    expect(screen.queryByRole('radiogroup', { name: 'Kind' })).toBeNull()
  })

  it('builds the Queue lane from the reader’s own untouched keys, and says so in the head', async () => {
    const asked: unknown[] = []
    const transport = createFakeTransport({ tickets: TICKETS })
    const answer = transport.queue
    transport.queue = async (ws, f) => {
      asked.push(f)
      return answer(ws, f)
    }
    const { container } = mount({}, transport)

    await screen.findByRole('region', { name: 'Queue (1)' })
    expect(asked).toEqual([{ assignee: 'me' }])

    const queue = within(lane(container, 'queue'))
    // OMNI-9 is the reader's and untouched; OMNI-1 is theirs but has a run,
    // and OMNI-3 is somebody else's, so the tracker never offered it.
    expect(queue.getByRole('link', { name: /OMNI-9/ })).toBeInTheDocument()
    expect(queue.queryByText('OMNI-1')).toBeNull()
    expect(queue.queryByText('OMNI-3')).toBeNull()
    expect(lane(container, 'queue').querySelector('.sd-lane__note')).toHaveTextContent(
      '· assigned to you',
    )
  })

  it('draws the assignee’s initials on a queued ticket', async () => {
    const { container } = mount()
    await screen.findByRole('region', { name: 'Queue (1)' })
    const avatar = lane(container, 'queue').querySelector('.sd-avatar') as HTMLElement
    expect(avatar).toHaveTextContent('S')
    expect(avatar).toHaveAttribute('title', 'sri')
  })

  it('says the queue could not be read, in the lane it would have filled', async () => {
    const transport = createFakeTransport({ tickets: TICKETS })
    transport.failQueue(new Error('tracker is down'))
    const { container } = mount({}, transport)

    expect(await screen.findByText(/Could not load your queue\. tracker is down/)).toBeInTheDocument()
    // A tracker that cannot answer empties no lane but its own.
    expect(within(lane(container, 'blocked')).getByRole('button', { name: /OMNI-3/ })).toBeInTheDocument()
    expect(within(lane(container, 'gathering')).getByRole('button', { name: /OMNI-1/ })).toBeInTheDocument()
  })

  it('builds the assignee menu from the runs and the queue, Me first and counted', async () => {
    mount()
    await screen.findByRole('region', { name: 'Queue (1)' })

    const list = openAssignees()
    const rows = within(list).getAllByRole('option')
    expect(rows.map((r) => r.querySelector('.board-assignee__name')?.textContent)).toEqual([
      'Me',
      'rana@acme.com',
      'someone-else',
    ])
    // Three of the five runs are the reader's; the other two are one each.
    expect(rows.map((r) => r.querySelector('.board-assignee__count')?.textContent)).toEqual([
      '3 runs',
      '1 run',
      '1 run',
    ])
    // The avatar is the initials of the name the row stands for.
    expect(rows[0].querySelector('.sd-avatar')?.textContent).toBe('S')
    // Under nine people the menu has no search field to get in the way.
    expect(within(list.parentElement as HTMLElement).queryByLabelText('Find a person')).toBeNull()
  })

  it('narrows to the reader’s own runs for Me without asking the tracker again', async () => {
    const { container, transport } = mount()
    await screen.findByRole('region', { name: 'Queue (1)' })
    expect(transport.calls.queue).toHaveLength(1)

    const list = openAssignees()
    fireEvent.click(within(list).getByRole('option', { name: /^Me/ }))

    expect(triggerWords()).toBe('Me')
    expect(within(lane(container, 'gathering')).getByRole('button', { name: /OMNI-1/ })).toBeInTheDocument()
    expect(within(lane(container, 'done')).getByRole('button', { name: /OMNI-4/ })).toBeInTheDocument()
    expect(within(lane(container, 'queue')).getByRole('link', { name: /OMNI-9/ })).toBeInTheDocument()
    // OMNI-2 and OMNI-3 belong to somebody else.
    expect(within(lane(container, 'triaged')).queryByRole('button', { name: /OMNI-2/ })).toBeNull()
    expect(within(lane(container, 'blocked')).queryByRole('button', { name: /OMNI-3/ })).toBeNull()
    expect(status(container)).toMatch(/^3 of 5 runs · 1 live/)
    // A lane the filter emptied says whose work is missing from it.
    expect(within(lane(container, 'triaged')).getByText('No runs assigned to sri@acme.com.')).toBeInTheDocument()
    expect(transport.calls.queue).toHaveLength(1)

    // Unpicking puts the other two back, and the count line stops counting.
    fireEvent.click(within(list).getByRole('option', { name: /^Me/ }))
    expect(triggerWords()).toBe('Anyone')
    expect(within(lane(container, 'triaged')).getByRole('button', { name: /OMNI-2/ })).toBeInTheDocument()
    expect(within(lane(container, 'blocked')).getByRole('button', { name: /OMNI-3/ })).toBeInTheDocument()
    expect(status(container)).toMatch(/^5 runs · 1 live/)
    expect(transport.calls.queue).toHaveLength(1)
  })

  it('picks several people at once and says so on the trigger', async () => {
    const { container } = mount()
    await screen.findByRole('region', { name: 'Queue (1)' })

    const list = openAssignees()
    fireEvent.click(within(list).getByRole('option', { name: /rana@acme\.com/ }))
    expect(triggerWords()).toBe('rana@acme.com')
    expect(within(lane(container, 'triaged')).getByRole('button', { name: /OMNI-2/ })).toBeInTheDocument()
    expect(within(lane(container, 'gathering')).queryByRole('button', { name: /OMNI-1/ })).toBeNull()
    expect(status(container)).toMatch(/^1 of 5 runs/)

    fireEvent.click(within(list).getByRole('option', { name: /someone-else/ }))
    expect(triggerWords()).toBe('rana@acme.com, someone-else')
    expect(within(lane(container, 'blocked')).getByRole('button', { name: /OMNI-3/ })).toBeInTheDocument()
    expect(status(container)).toMatch(/^2 of 5 runs/)

    // Three picked is two names and a count of the rest.
    fireEvent.click(within(list).getByRole('option', { name: /^Me/ }))
    expect(triggerWords()).toBe('Me, rana@acme.com +1')
    expect(status(container)).toMatch(/^5 of 5 runs/)
  })

  it('refuses Me and says why when the workspace can name nobody', async () => {
    const nobody: MeSummary = { email: '', names: [], source: '' }
    const { container } = mount({ me: nobody })
    await screen.findByRole('region', { name: 'Queue (1)' })

    const list = openAssignees()
    const me = within(list).getByRole('option', { name: /^Me/ })
    expect(me).toHaveAttribute('aria-disabled', 'true')
    expect(me).toHaveAttribute('title', NO_IDENTITY)
    fireEvent.click(me)
    expect(me).toHaveAttribute('aria-selected', 'false')
    expect(status(container)).toMatch(/^5 runs/)

    expect(screen.getByText(NO_IDENTITY, { selector: '.board-filters__note' })).toBeInTheDocument()
    expect(
      within(lane(container, 'queue')).getByText(/assigned to you · set who you are in Settings/),
    ).toBeInTheDocument()
  })

  it('grows a search field once there are more than eight people', async () => {
    const crowd = Array.from({ length: 9 }, (_, i) =>
      run({ runId: `x${i}`, key: `OMNI-${100 + i}`, assignee: `person-${i}`, mine: false }),
    )
    mount({ runs: crowd })
    // None of these nine runs is on a queued key, so both of the reader's
    // tickets are still waiting in the lane.
    await screen.findByRole('region', { name: 'Queue (2)' })

    const list = openAssignees()
    const field = screen.getByLabelText('Find a person')
    fireEvent.change(field, { target: { value: 'person-3' } })
    expect(within(list).getAllByRole('option')).toHaveLength(1)
    fireEvent.change(field, { target: { value: 'nobody here' } })
    expect(screen.getByText(/Nobody here matches/)).toBeInTheDocument()
  })

  it('keeps every run visible under Me while the queue is still answering', async () => {
    let release = (): void => {}
    const transport = createFakeTransport({ tickets: TICKETS })
    const answer = transport.queue
    transport.queue = async (ws, f) => {
      await new Promise<void>((resolve) => {
        release = resolve
      })
      return answer(ws, f)
    }
    const { container } = mount({}, transport)

    const list = openAssignees()
    fireEvent.click(within(list).getByRole('option', { name: /^Me/ }))

    // The runs already say whose they are, so Me answers at once and the
    // slow queue only holds up the lane it fills.
    expect(within(lane(container, 'gathering')).getByRole('button', { name: /OMNI-1/ })).toBeInTheDocument()
    expect(within(lane(container, 'blocked')).queryByRole('button', { name: /OMNI-3/ })).toBeNull()
    expect(within(lane(container, 'queue')).getByText('Reading the tickets assigned to you…')).toBeInTheDocument()

    release()
    await waitFor(() =>
      expect(within(lane(container, 'queue')).getByRole('link', { name: /OMNI-9/ })).toBeInTheDocument(),
    )
  })

  it('narrows nothing under Me when the service says nothing about ownership', async () => {
    // The cards still name a person; what the service never decided is
    // whether that person is the reader.
    const older = RUNS.map(({ mine: _mine, ...rest }) => rest)
    const { container } = mount({ runs: older })
    await screen.findByRole('region', { name: 'Queue (1)' })

    const list = openAssignees()
    fireEvent.click(within(list).getByRole('option', { name: /^Me/ }))

    expect(within(lane(container, 'blocked')).getByRole('button', { name: /OMNI-3/ })).toBeInTheDocument()
    expect(within(lane(container, 'triaged')).getByRole('button', { name: /OMNI-2/ })).toBeInTheDocument()
    expect(screen.getByText(/records no owner on a run/)).toBeInTheDocument()

    // A name still narrows, because the card carries the spelling whatever
    // the service says about ownership.
    fireEvent.click(within(list).getByRole('option', { name: /^Me/ }))
    fireEvent.click(within(list).getByRole('option', { name: /rana@acme\.com/ }))
    expect(within(lane(container, 'triaged')).getByRole('button', { name: /OMNI-2/ })).toBeInTheDocument()
    expect(within(lane(container, 'blocked')).queryByRole('button', { name: /OMNI-3/ })).toBeNull()
  })

  it('lists the day’s deliveries with the outcome coloured and the reason in the meta line', () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-09-10T09:07:00Z'))
    const { container, onOpenRun } = mount({
      inbound: [
        delivery({ id: 3, key: 'OMNI-1', outcome: 'started', at: '2026-09-10T09:05:00Z' }),
        delivery({ id: 2, key: 'OMNI-9', source: 'zendesk', outcome: 'skipped', at: '2026-09-10T09:00:00Z' }),
        delivery({ id: 1, key: 'OMNI-7', outcome: 'rejected', at: '2026-09-10T08:53:00Z' }),
      ],
    })

    const landed = screen.getByRole('region', { name: 'Deliveries today' })
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

describe('the queue lane’s ticket types', () => {
  const sources = (queueTypes?: string[]) => ({
    tracker: { adapter: 'jira', name: 'Jira', host: 'acme.atlassian.net' },
    queueTypes,
  })

  it('shows each queued ticket’s type beside its Triage button', async () => {
    const tickets = [
      ticket({ key: 'OMNI-9', title: 'Statement export times out', type: 'bug', assignee: 'sri' }),
    ]
    const { container } = mount({ sources: sources(['bug']) }, createFakeTransport({ tickets }))
    await screen.findByRole('region', { name: 'Queue (1)' })

    expect(within(lane(container, 'queue')).getByText('bug')).toBeInTheDocument()
  })

  it('shows no chip for a ticket whose tracker names no type', async () => {
    const tickets = [ticket({ key: 'OMNI-9', title: 'Statement export', type: '', assignee: 'sri' })]
    const { container } = mount({ sources: sources(['*']) }, createFakeTransport({ tickets }))
    await screen.findByRole('region', { name: 'Queue (1)' })

    expect(lane(container, 'queue').querySelector('.board-ticket__type')).toBeNull()
  })

  it('says which type the empty lane was looking for', async () => {
    const { container } = mount({ sources: sources(['bug']) }, createFakeTransport({ tickets: [] }))
    await screen.findByRole('region', { name: 'Queue (0)' })

    expect(lane(container, 'queue')).toHaveTextContent('No bug tickets assigned to you.')
  })

  it('keeps the lane’s own sentence when the filter is off', async () => {
    const { container } = mount({ sources: sources(['*']) }, createFakeTransport({ tickets: [] }))
    await screen.findByRole('region', { name: 'Queue (0)' })

    expect(lane(container, 'queue')).toHaveTextContent(
      'Nothing in the tracker is assigned to you and untouched.',
    )
  })
})

describe('queueEmptyText', () => {
  const unfiltered = 'Nothing in the tracker is assigned to you and untouched.'

  it('names the filter the lane was reading under', () => {
    expect(queueEmptyText(['bug'], unfiltered)).toBe('No bug tickets assigned to you.')
    expect(queueEmptyText(['bug', 'incident'], unfiltered)).toBe(
      'No bug or incident tickets assigned to you.',
    )
    expect(queueEmptyText(['bug', 'story', 'task'], unfiltered)).toBe(
      'No bug, story or task tickets assigned to you.',
    )
  })

  it('falls back to the lane’s own sentence when nothing was filtered out', () => {
    expect(queueEmptyText([], unfiltered)).toBe(unfiltered)
    expect(queueEmptyText(['*'], unfiltered)).toBe(unfiltered)
    expect(queueEmptyText(['bug', '*'], unfiltered)).toBe(unfiltered)
  })

  it('reads an absent filter as the default, which is what an older server sent', () => {
    expect(queueEmptyText(undefined, unfiltered)).toBe('No bug tickets assigned to you.')
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
