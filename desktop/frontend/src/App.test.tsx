import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, describe, expect, it } from 'vitest'
import App from './App'
import { createAppStore, type AppStore } from './store/appStore'
import { createFakeTransport, run, ticket, workspace, type FakeTransport } from './store/fakeTransport'
import { StoreProvider } from './store/useAppStore'

let store: AppStore | null = null

afterEach(() => {
  store?.dispose()
  store = null
  try {
    localStorage.clear()
  } catch {
    // Nothing to reset.
  }
})

function mount(transport: FakeTransport) {
  store = createAppStore(transport)
  const view = render(
    <StoreProvider value={store}>
      <App />
    </StoreProvider>,
  )
  return { store: store as AppStore, ...view }
}

function lane(container: HTMLElement, id: string): HTMLElement {
  const el = container.querySelector(`[data-lane="${id}"]`)
  if (!el) throw new Error(`lane ${id} is not rendered`)
  return el as HTMLElement
}

const seeded = () =>
  createFakeTransport({
    workspaces: [workspace({ id: 'ws1', name: 'omni' })],
    tickets: [
      ticket({ key: 'OMNI-9', title: 'Statement export times out', priority: 'P2' }),
      ticket({ key: 'OMNI-1', title: 'Login loop after reset' }),
    ],
    runs: [
      run({ runId: 'r1', key: 'OMNI-1', status: 'running', updatedAt: '2026-09-10T09:05:00Z' }),
      run({ runId: 'r2', key: 'OMNI-2', kind: 'triage', status: 'completed' }),
      run({ runId: 'r3', key: 'OMNI-3', status: 'blocked', reason: 'Which tenant is affected?' }),
      run({ runId: 'r4', key: 'OMNI-4', kind: 'rca', status: 'completed' }),
      run({ runId: 'r5', key: 'OMNI-5', status: 'failed', reason: 'provider exited 1' }),
    ],
  })

describe('Board', () => {
  it('lays every lane out and sorts each run into the one that matches its state', async () => {
    const { container } = mount(seeded())

    await screen.findByRole('heading', { name: /Queue/ })
    for (const name of ['Queue', 'Gathering', 'Needs input', 'Triaged', 'Done', 'Failed']) {
      expect(screen.getByRole('heading', { name: new RegExp(name) })).toBeInTheDocument()
    }

    await waitFor(() => expect(within(lane(container, 'gathering')).getByText('OMNI-1')).toBeInTheDocument())
    expect(within(lane(container, 'triaged')).getByText('OMNI-2')).toBeInTheDocument()
    expect(within(lane(container, 'blocked')).getByText('OMNI-3')).toBeInTheDocument()
    expect(within(lane(container, 'done')).getByText('OMNI-4')).toBeInTheDocument()
    expect(within(lane(container, 'failed')).getByText('OMNI-5')).toBeInTheDocument()

    // OMNI-1 already has a run, so only the untouched ticket waits in Queue.
    expect(within(lane(container, 'queue')).getByText('OMNI-9')).toBeInTheDocument()
    expect(within(lane(container, 'queue')).queryByText('OMNI-1')).toBeNull()

    // A blocked run says why it stopped.
    expect(screen.getByText('Which tenant is affected?')).toBeInTheDocument()
  })

  it('opens run detail when a card is clicked', async () => {
    const { container, store: s } = mount(seeded())
    await waitFor(() => expect(within(lane(container, 'gathering')).getByText('OMNI-1')).toBeInTheDocument())

    fireEvent.click(within(lane(container, 'gathering')).getByRole('button', { name: /OMNI-1/ }))
    expect(s.getState().screen).toEqual({ name: 'run', runId: 'r1' })
  })

  it('a queue card starts triage for its own key', async () => {
    const transport = seeded()
    const { container } = mount(transport)
    await waitFor(() => expect(within(lane(container, 'queue')).getByText('OMNI-9')).toBeInTheDocument())

    fireEvent.click(within(lane(container, 'queue')).getByRole('button', { name: 'Triage' }))
    await waitFor(() =>
      expect(transport.calls.startTriage).toEqual([
        { ws: 'ws1', keys: ['OMNI-9'], opts: undefined },
      ]),
    )
  })

  it('filters cards by key or title, and `/` puts the cursor in the box', async () => {
    const { container } = mount(seeded())
    await waitFor(() => expect(within(lane(container, 'gathering')).getByText('OMNI-1')).toBeInTheDocument())

    const filter = screen.getByRole('searchbox', { name: /filter/i })
    fireEvent.keyDown(window, { key: '/' })
    expect(document.activeElement).toBe(filter)

    fireEvent.change(filter, { target: { value: 'statement' } })
    expect(within(lane(container, 'queue')).getByText('OMNI-9')).toBeInTheDocument()
    expect(within(lane(container, 'gathering')).queryByText('OMNI-1')).toBeNull()
    expect(within(lane(container, 'gathering')).getByText(/Nothing here matches/)).toBeInTheDocument()
  })

  it('tells the engineer to start by key when the workspace has no tracker', async () => {
    const transport = seeded()
    transport.failQueue(new Error('501 Not Implemented'))
    const { container } = mount(transport)

    await waitFor(() =>
      expect(
        within(lane(container, 'queue')).getByText(
          'This workspace has no tracker; start triage by key.',
        ),
      ).toBeInTheDocument(),
    )
  })
})

describe('New triage', () => {
  it('opens with `n`, parses a mixed key list and starts triage', async () => {
    const transport = seeded()
    mount(transport)
    await screen.findByRole('heading', { name: /Queue/ })

    fireEvent.keyDown(window, { key: 'n' })
    const dialog = await screen.findByRole('dialog', { name: 'New triage' })

    const keys = within(dialog).getByLabelText('Ticket keys')
    fireEvent.change(keys, { target: { value: 'OMNI-11, OMNI-12 OMNI-13\nOMNI-11' } })
    expect(within(dialog).getByText(/3 keys: OMNI-11 OMNI-12 OMNI-13/)).toBeInTheDocument()

    fireEvent.change(within(dialog).getByLabelText('Provider'), { target: { value: 'codex' } })
    fireEvent.click(within(dialog).getByLabelText(/Dry run/))
    fireEvent.click(within(dialog).getByRole('button', { name: 'Start triage' }))

    await waitFor(() =>
      expect(transport.calls.startTriage).toEqual([
        {
          ws: 'ws1',
          keys: ['OMNI-11', 'OMNI-12', 'OMNI-13'],
          opts: { provider: 'codex', model: undefined, dryRun: true },
        },
      ]),
    )
    expect(screen.queryByRole('dialog')).toBeNull()
  })

  it('closes on escape without starting anything', async () => {
    const transport = seeded()
    mount(transport)
    await screen.findByRole('heading', { name: /Queue/ })

    fireEvent.keyDown(window, { key: 'n' })
    await screen.findByRole('dialog', { name: 'New triage' })
    fireEvent.keyDown(window, { key: 'Escape' })

    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    expect(transport.calls.startTriage).toEqual([])
  })
})

describe('Header', () => {
  it('switches workspace and reloads that workspace', async () => {
    const transport = createFakeTransport({
      workspaces: [workspace({ id: 'ws1', name: 'omni' }), workspace({ id: 'ws2', name: 'billing' })],
    })
    mount(transport)
    await screen.findByRole('heading', { name: /Queue/ })

    fireEvent.change(screen.getByLabelText('Workspace'), { target: { value: 'ws2' } })
    await waitFor(() => expect(transport.calls.runs).toContain('ws2'))
  })

  it('"Add workspace" goes to Settings', async () => {
    const { store: s } = mount(seeded())
    await screen.findByRole('heading', { name: /Queue/ })

    fireEvent.change(screen.getByLabelText('Workspace'), { target: { value: '__add__' } })
    expect(s.getState().screen).toEqual({ name: 'settings' })
  })
})

describe('Eval tab', () => {
  it('opens the Eval screen from the nav and draws the golden set and the last report', async () => {
    const transport = createFakeTransport({
      workspaces: [workspace({ id: 'ws1', name: 'omni' })],
      golden: [
        {
          key: 'OMNI-2510',
          dir: '/golden/OMNI-2510',
          bundleDir: '/golden/OMNI-2510/bundle',
          assertions: 2,
          hasExpectedNote: true,
        },
      ],
      reports: [
        {
          path: '/work/.sirdar/eval/20260910-1200.json',
          at: '2026-09-10T12:00:00Z',
          provider: 'claude',
          model: 'sonnet',
          goldenDir: '/golden',
          results: [
            {
              key: 'OMNI-2510',
              runId: 'r1',
              state: 'completed',
              turns: 5,
              costUsd: 0.3,
              minutes: 2,
              schemaValid: true,
              checks: [],
              passed: 2,
              total: 2,
            },
          ],
        },
      ],
    })
    const { store: s } = mount(transport)
    await screen.findByRole('heading', { name: /Queue/ })

    fireEvent.click(screen.getByRole('button', { name: 'Eval' }))
    expect(s.getState().screen).toEqual({ name: 'eval' })

    expect(await screen.findByRole('checkbox', { name: 'OMNI-2510' })).toBeInTheDocument()
    expect(screen.getByText('2 assertions')).toBeInTheDocument()
    expect(screen.getByRole('table')).toBeInTheDocument()
  })

  it('starts an eval through the store, so the job id is tracked like every other start', async () => {
    const transport = createFakeTransport({
      workspaces: [workspace({ id: 'ws1', name: 'omni' })],
      golden: [
        {
          key: 'OMNI-2510',
          dir: '/golden/OMNI-2510',
          bundleDir: '/golden/OMNI-2510/bundle',
          assertions: 2,
          hasExpectedNote: true,
        },
      ],
    })
    mount(transport)
    await screen.findByRole('heading', { name: /Queue/ })
    fireEvent.click(screen.getByRole('button', { name: 'Eval' }))

    fireEvent.click(await screen.findByRole('button', { name: 'Run eval on the whole set' }))
    await waitFor(() =>
      expect(transport.calls.startEval).toEqual([
        { ws: 'ws1', keys: undefined, opts: { provider: undefined, model: undefined } },
      ]),
    )
  })
})

describe('Inbound deliveries', () => {
  it('a hook.received lands on the board and raises a toast', async () => {
    const transport = seeded()
    mount(transport)
    await screen.findByRole('heading', { name: /Queue/ })

    const panel = screen.getByRole('region', { name: 'Inbound' })
    expect(within(panel).getByText(/No webhook delivery has arrived/)).toBeInTheDocument()

    transport.emit({ kind: 'hook.received', source: 'jira', key: 'OMNI-9', outcome: 'started' })

    await waitFor(() =>
      expect(within(panel).getByText('started a triage')).toBeInTheDocument(),
    )
    expect(within(panel).getByText('OMNI-9')).toBeInTheDocument()
    expect(screen.getByText(/Webhook from jira .* started a triage\./)).toBeInTheDocument()
  })
})

describe('Settings', () => {
  it("summarises the current workspace's notify and webhooks blocks", async () => {
    const transport = createFakeTransport({
      workspaces: [workspace({ id: 'ws1', name: 'omni' })],
      configSummary: {
        notify: {
          enabled: true,
          on: ['completed'],
          includeTitle: false,
          destinations: [{ type: 'slack', credential: 'env' }],
        },
        webhooks: { enabled: false, cooldown: '10m0s', match: {}, sources: [] },
      },
    })
    mount(transport)
    await screen.findByRole('heading', { name: /Queue/ })

    fireEvent.click(screen.getByRole('button', { name: 'Settings' }))
    const panel = await screen.findByRole('region', { name: 'Notifications and webhooks' })
    expect(within(panel).getByText('env: reference')).toBeInTheDocument()
    expect(within(panel).getByText(/Inbound webhooks are off/)).toBeInTheDocument()
  })
})
