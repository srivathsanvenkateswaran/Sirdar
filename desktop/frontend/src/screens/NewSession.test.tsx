import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { RunSummary } from '../api/types'
import { PrimaryActionProvider, usePrimaryAction } from '../components/shell/primaryAction'
import { resetRunJobs, setRunJob } from '../lib/jobs'
import { createFakeTransport, run, ticket, workspace, type FakeTransport } from '../store/fakeTransport'
import NewSession, {
  extractKey,
  hasTriageNote,
  newestFirst,
  ticketSource,
  type SessionMode,
  type StartOverrides,
} from './NewSession'

afterEach(() => {
  resetRunJobs()
})

/**
 * The sidebar footer, as far as these cases care: what the screen published,
 * and where it asked for it to be drawn. New session draws Start itself and
 * asks the footer to stand down, so this reads the placement rather than
 * drawing a second button.
 */
function PrimarySlot(): JSX.Element | null {
  const action = usePrimaryAction()
  if (!action) return null
  return (
    <p data-testid="primary">
      {action.label} · {action.placement ?? 'footer'} · {action.disabled ? 'off' : 'on'}
    </p>
  )
}

const TRIAGED = run({ runId: 'r-t', key: 'OMNI-2', kind: 'triage', status: 'completed' })

function mount(
  over: {
    transport?: FakeTransport
    runs?: RunSummary[]
    onStart?: (mode: SessionMode, key: string, o: StartOverrides) => Promise<string>
  } = {},
) {
  const transport = over.transport ?? createFakeTransport({ tickets: [] })
  const onStart = over.onStart ?? vi.fn(async () => 'job-1')
  const onOpenRun = vi.fn()
  const runs = over.runs ?? []
  const view = render(
    <PrimaryActionProvider>
      <NewSession
        transport={transport}
        workspaceId="ws1"
        workspace={workspace()}
        runs={runs}
        onStart={onStart}
        onOpenRun={onOpenRun}
      />
      <PrimarySlot />
    </PrimaryActionProvider>,
  )
  function rerender(next: RunSummary[]): void {
    view.rerender(
      <PrimaryActionProvider>
        <NewSession
          transport={transport}
          workspaceId="ws1"
          workspace={workspace()}
          runs={next}
          onStart={onStart}
          onOpenRun={onOpenRun}
        />
        <PrimarySlot />
      </PrimaryActionProvider>,
    )
  }
  return { transport, onStart, onOpenRun, rerender }
}

const bar = () => screen.getByRole('searchbox', { name: 'Ticket key or URL' })
const startButton = () => screen.getByRole('button', { name: /^Start/ })

describe('extractKey', () => {
  it('takes a key as typed, in any case', () => {
    expect(extractKey('OMNI-2510')).toBe('OMNI-2510')
    expect(extractKey('  omni-2510 ')).toBe('OMNI-2510')
  })

  it('takes the last path segment of a tracker URL when it is a key', () => {
    expect(extractKey('https://acme.atlassian.net/browse/OMNI-2510')).toBe('OMNI-2510')
    expect(extractKey('https://linear.app/acme/issue/SBX-7/')).toBe('SBX-7')
    expect(extractKey('https://acme.atlassian.net/browse/OMNI-2510?focusedCommentId=1')).toBe(
      'OMNI-2510',
    )
  })

  it('answers null for prose, an empty box, and a URL that ends in no key', () => {
    expect(extractKey('')).toBeNull()
    expect(extractKey('the export is slow')).toBeNull()
    expect(extractKey('https://acme.atlassian.net/jira/software/projects/OMNI/boards/1')).toBeNull()
  })
})

describe('hasTriageNote', () => {
  it('is true only for a completed triage of that key', () => {
    expect(hasTriageNote([TRIAGED], 'OMNI-2')).toBe(true)
    expect(hasTriageNote([TRIAGED], 'OMNI-3')).toBe(false)
    expect(hasTriageNote([run({ key: 'OMNI-2', kind: 'triage', status: 'running' })], 'OMNI-2')).toBe(false)
    expect(hasTriageNote([run({ key: 'OMNI-2', kind: 'fix', status: 'completed' })], 'OMNI-2')).toBe(false)
  })
})

describe('ticketSource', () => {
  it('names the tracker or helpdesk off the ticket URL', () => {
    expect(ticketSource(ticket({ url: 'https://acme.atlassian.net/browse/OMNI-1' }))).toBe('jira')
    expect(ticketSource(ticket({ url: 'https://issues.corp.example/browse/OMNI-1' }))).toBe('jira')
    expect(ticketSource(ticket({ url: 'https://linear.app/acme/issue/SBX-1' }))).toBe('linear')
    expect(ticketSource(ticket({ url: 'https://acme.zendesk.com/agent/tickets/9' }))).toBe('zendesk')
    expect(ticketSource(ticket({ url: 'https://desk.zoho.com/agent/acme/tickets/9' }))).toBe('zoho')
    expect(ticketSource(ticket({ url: 'https://tracker.corp.example/t/9' }))).toBe('corp')
    expect(ticketSource(ticket({ url: '' }))).toBe('tracker')
    expect(ticketSource(ticket({ url: '', helpdeskRef: 'ZD-9' }))).toBe('helpdesk')
  })
})

describe('newestFirst', () => {
  it('orders by last change and keeps five', () => {
    const tickets = [1, 2, 3, 4, 5, 6].map((n) =>
      ticket({ key: `OMNI-${n}`, updatedAt: `2026-09-10T0${n}:00:00Z` }),
    )
    expect(newestFirst(tickets).map((t) => t.key)).toEqual([
      'OMNI-6',
      'OMNI-5',
      'OMNI-4',
      'OMNI-3',
      'OMNI-2',
    ])
  })
})

describe('NewSession', () => {
  it('draws the title, the bar, the three modes, the chips and its own Start', async () => {
    mount()
    expect(screen.getByRole('heading', { name: 'Start with a ticket' })).toBeInTheDocument()
    expect(bar()).toHaveFocus()
    const modes = screen.getByRole('radiogroup', { name: 'Mode' })
    expect(within(modes).getAllByRole('radio').map((r) => r.textContent)).toEqual([
      'Triage',
      'RCA',
      'Fix',
    ])
    expect(within(modes).getByRole('radio', { checked: true })).toHaveTextContent('Triage')
    expect(screen.getByText('auto')).toBeInTheDocument()
    expect(screen.getByRole('img', { name: 'Claude' })).toBeInTheDocument()
    expect(screen.getByText('claude · sonnet')).toBeInTheDocument()
    // Start is on the screen, filled, and off until there is a key; the
    // footer is told to stand down rather than draw a second one.
    expect(startButton()).toHaveAttribute('data-variant', 'primary')
    expect(startButton()).toBeDisabled()
    await waitFor(() => expect(screen.getByTestId('primary')).toHaveTextContent('Start · screen · off'))
  })

  it('starts a triage for the key in the bar and opens the run once the job has one', async () => {
    const { onStart, onOpenRun, rerender } = mount()
    fireEvent.change(bar(), { target: { value: 'omni-2510' } })
    expect(startButton()).toBeEnabled()
    fireEvent.click(startButton())

    await waitFor(() =>
      expect(onStart).toHaveBeenCalledWith('triage', 'OMNI-2510', {
        provider: undefined,
        model: undefined,
        dryRun: undefined,
      }),
    )
    expect(await screen.findByRole('button', { name: /Starting/ })).toHaveAttribute('aria-disabled', 'true')
    expect(onOpenRun).not.toHaveBeenCalled()

    // The store pairs the job with the run as its first update arrives; the
    // screen opens that run and not the older one for the same key.
    const older = run({ runId: 'r-old', key: 'OMNI-2510', status: 'completed' })
    const fresh = run({ runId: 'r-new', key: 'OMNI-2510', status: 'preparing' })
    act(() => setRunJob('r-new', 'job-1'))
    rerender([older, fresh])
    await waitFor(() => expect(onOpenRun).toHaveBeenCalledWith('r-new'))
  })

  it('reads the key out of a tracker URL', async () => {
    const { onStart } = mount()
    fireEvent.change(bar(), { target: { value: 'https://acme.atlassian.net/browse/OMNI-77' } })
    fireEvent.submit(screen.getByRole('search'))
    await waitFor(() => expect(onStart).toHaveBeenCalledWith('triage', 'OMNI-77', expect.anything()))
  })

  it('says what a key looks like when the box holds something else', () => {
    mount()
    fireEvent.change(bar(), { target: { value: 'the export is slow' } })
    expect(startButton()).toBeDisabled()
    expect(screen.getByRole('status')).toHaveTextContent(
      'Enter a ticket key like OMNI-2510, or a tracker URL that ends in one.',
    )
  })

  it('starts an RCA and a fix for a key that has a triage note', async () => {
    const { onStart } = mount({ runs: [TRIAGED] })
    fireEvent.change(bar(), { target: { value: 'OMNI-2' } })
    fireEvent.click(screen.getByRole('radio', { name: 'RCA' }))
    fireEvent.click(startButton())
    await waitFor(() => expect(onStart).toHaveBeenLastCalledWith('rca', 'OMNI-2', expect.anything()))

    // A dry run is a triage's or a fix's option, never an RCA's.
    expect(screen.getByLabelText(/Dry run/)).toBeDisabled()
  })

  it('starts a fix with the one-off provider, model and dry run from More options', async () => {
    const { onStart } = mount({ runs: [TRIAGED] })
    fireEvent.change(bar(), { target: { value: 'OMNI-2' } })
    fireEvent.click(screen.getByRole('radio', { name: 'Fix' }))
    fireEvent.change(screen.getByLabelText('Provider'), { target: { value: 'codex' } })
    fireEvent.change(screen.getByLabelText('Model'), { target: { value: ' o3 ' } })
    fireEvent.click(screen.getByLabelText(/Dry run/))
    // The chip follows the override, so the reader sees what will run.
    expect(screen.getByText('codex · o3')).toBeInTheDocument()

    fireEvent.click(startButton())
    await waitFor(() =>
      expect(onStart).toHaveBeenCalledWith('fix', 'OMNI-2', {
        provider: 'codex',
        model: 'o3',
        dryRun: true,
      }),
    )
  })

  it('turns RCA and Fix off, with the reason, while the key has no triage note', () => {
    mount({ runs: [TRIAGED] })
    fireEvent.change(bar(), { target: { value: 'OMNI-3' } })
    const rca = screen.getByRole('radio', { name: 'RCA' })
    const fix = screen.getByRole('radio', { name: 'Fix' })
    expect(rca).toBeDisabled()
    expect(fix).toBeDisabled()
    expect(rca).toHaveAttribute('title', 'Needs a triage note for OMNI-3 first')
    expect(screen.getByRole('status')).toHaveTextContent(
      'RCA and Fix need a triage note for OMNI-3 first. Start a triage.',
    )
    // Triage itself is still on.
    expect(startButton()).toBeEnabled()

    // A note arriving turns them back on.
    fireEvent.change(bar(), { target: { value: 'OMNI-2' } })
    expect(rca).toBeEnabled()
    expect(fix).toBeEnabled()
  })

  it('keeps Start off when the chosen mode is one the key cannot run yet', () => {
    mount({ runs: [TRIAGED] })
    fireEvent.change(bar(), { target: { value: 'OMNI-2' } })
    fireEvent.click(screen.getByRole('radio', { name: 'Fix' }))
    expect(startButton()).toBeEnabled()
    // The key changes under the chosen mode; Start waits rather than
    // starting a fix the core would refuse.
    fireEvent.change(bar(), { target: { value: 'OMNI-3' } })
    expect(startButton()).toBeDisabled()
  })

  it('shows the reason a start was refused, beside the button', async () => {
    const onStart = vi.fn(async () => {
      throw new Error('OMNI-9 is busy')
    })
    mount({ onStart })
    fireEvent.change(bar(), { target: { value: 'OMNI-9' } })
    fireEvent.click(startButton())
    expect(await screen.findByRole('status')).toHaveTextContent('OMNI-9 is busy')
    expect(startButton()).toBeEnabled()
  })

  it('stops waiting when the job ends, opening the run it names if it names one', async () => {
    const transport = createFakeTransport({ tickets: [] })
    const { onOpenRun } = mount({ transport })
    fireEvent.change(bar(), { target: { value: 'OMNI-9' } })
    fireEvent.click(startButton())
    await screen.findByRole('button', { name: /Starting/ })

    act(() =>
      transport.emit({
        kind: 'job.finished',
        jobId: 'job-1',
        workspaceId: 'ws1',
        outcomes: [{ key: 'OMNI-9', status: 'failed', runId: 'r-9' }],
      }),
    )
    await waitFor(() => expect(onOpenRun).toHaveBeenCalledWith('r-9'))
  })

  it('says so when the job ends with no run at all', async () => {
    const transport = createFakeTransport({ tickets: [] })
    const { onOpenRun } = mount({ transport })
    fireEvent.change(bar(), { target: { value: 'OMNI-9' } })
    fireEvent.click(startButton())
    await screen.findByRole('button', { name: /Starting/ })

    act(() => transport.emit({ kind: 'job.finished', jobId: 'job-1', workspaceId: 'ws1', outcomes: [] }))
    expect(await screen.findByRole('status')).toHaveTextContent('The job ended before a session started.')
    expect(onOpenRun).not.toHaveBeenCalled()
    expect(startButton()).toBeEnabled()
  })

  it('offers Recent sessions only when there is one, and it opens the newest run', () => {
    const { onOpenRun, rerender } = mount()
    expect(screen.queryByRole('button', { name: 'Recent sessions' })).toBeNull()

    rerender([
      run({ runId: 'r-1', key: 'OMNI-1', updatedAt: '2026-09-10T09:00:00Z' }),
      run({ runId: 'r-2', key: 'OMNI-2', updatedAt: '2026-09-11T09:00:00Z' }),
    ])
    fireEvent.click(screen.getByRole('button', { name: 'Recent sessions' }))
    expect(onOpenRun).toHaveBeenCalledWith('r-2')
  })

  describe('Landed today', () => {
    it("lists the reader's newest five with key, source and age, and Triage per row", async () => {
      const transport = createFakeTransport({
        tickets: [1, 2, 3, 4, 5, 6].map((n) =>
          ticket({
            key: `SBX-${n}`,
            title: `Ticket ${n}`,
            url: n === 3 ? 'https://acme.zendesk.com/agent/tickets/3' : 'https://acme.atlassian.net/browse/SBX-' + n,
            updatedAt: `2026-09-10T0${n}:00:00Z`,
          }),
        ),
      })
      const { onStart } = mount({ transport })

      await screen.findByText('Ticket 6')
      expect(transport.calls.queue).toEqual(['ws1'])
      const rows = screen.getAllByRole('button', { name: /^Triage SBX-/ })
      expect(rows.map((b) => b.getAttribute('aria-label'))).toEqual([
        'Triage SBX-6',
        'Triage SBX-5',
        'Triage SBX-4',
        'Triage SBX-3',
        'Triage SBX-2',
      ])
      expect(screen.getByText(/SBX-3 · zendesk · /)).toBeInTheDocument()
      expect(screen.getByText(/SBX-6 · jira · /)).toBeInTheDocument()

      fireEvent.click(screen.getByRole('button', { name: 'Triage SBX-5' }))
      await waitFor(() => expect(onStart).toHaveBeenCalledWith('triage', 'SBX-5', expect.anything()))
    })

    it('asks the tracker for the reader own tickets', async () => {
      const queue = vi.fn(async () => [])
      const transport = createFakeTransport({ tickets: [] })
      transport.queue = queue
      mount({ transport })
      await waitFor(() => expect(queue).toHaveBeenCalledWith('ws1', { assignee: 'me', limit: 5 }))
    })

    it('says the row carries a run, and opens it from the row', async () => {
      const transport = createFakeTransport({
        tickets: [
          ticket({
            key: 'SBX-1',
            title: 'Login loop',
            latestRun: run({ runId: 'r-1', key: 'SBX-1', status: 'blocked' }),
          }),
        ],
      })
      const { onOpenRun } = mount({ transport })
      await screen.findByText('Login loop')
      expect(screen.getByText(/SBX-1 · .* · blocked$/)).toBeInTheDocument()
      fireEvent.click(screen.getByRole('button', { name: 'Open SBX-1, Login loop' }))
      expect(onOpenRun).toHaveBeenCalledWith('r-1')
    })

    it('says Nothing landed today when the queue is empty', async () => {
      mount()
      expect(await screen.findByText('Nothing landed today')).toBeInTheDocument()
    })

    it('says so while the queue is still loading', () => {
      mount()
      expect(screen.getByText('Loading what landed…')).toBeInTheDocument()
    })

    it('tells the reader to start by key when the workspace has no tracker', async () => {
      const transport = createFakeTransport({ tickets: [] })
      transport.failQueue(new Error('501 Not Implemented'))
      mount({ transport })
      expect(await screen.findByText('This workspace has no tracker; start by key.')).toBeInTheDocument()
    })

    it('shows the reason the queue could not be read', async () => {
      const transport = createFakeTransport({ tickets: [] })
      transport.failQueue(new Error('tracker: 401 Unauthorized'))
      mount({ transport })
      expect(await screen.findByText(/Could not read the queue\. tracker: 401 Unauthorized/)).toBeInTheDocument()
    })

    it('reads the queue again when a webhook delivery lands', async () => {
      const transport = createFakeTransport({ tickets: [] })
      mount({ transport })
      await screen.findByText('Nothing landed today')
      transport.setTickets([ticket({ key: 'SBX-9', title: 'Just landed' })])

      act(() => transport.emit({ kind: 'hook.received', source: 'jira', key: 'SBX-9', outcome: 'started' }))
      expect(await screen.findByText('Just landed')).toBeInTheDocument()
      expect(transport.calls.queue).toEqual(['ws1', 'ws1'])
    })
  })
})
