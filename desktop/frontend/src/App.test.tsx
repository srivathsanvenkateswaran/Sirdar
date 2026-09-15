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
  // The window's address is now part of the shell's state; a test that ends on
  // Settings would otherwise open the next one there.
  window.history.replaceState(null, '', window.location.pathname)
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
    // A run with no tracker title shows its key as the title and again at the
    // foot, so the card is found by its accessible name rather than by text.
    expect(within(lane(container, 'triaged')).getByRole('button', { name: /OMNI-2/ })).toBeInTheDocument()
    expect(within(lane(container, 'blocked')).getByRole('button', { name: /OMNI-3/ })).toBeInTheDocument()
    expect(within(lane(container, 'done')).getByRole('button', { name: /OMNI-4/ })).toBeInTheDocument()
    expect(within(lane(container, 'failed')).getByRole('button', { name: /OMNI-5/ })).toBeInTheDocument()

    // OMNI-1 already has a run, so only the untouched ticket waits in Queue.
    expect(within(lane(container, 'queue')).getByText('OMNI-9')).toBeInTheDocument()
    expect(within(lane(container, 'queue')).queryByText('OMNI-1')).toBeNull()

    // A blocked run says it is blocked and for how long; the question itself
    // is the session's, not the card's.
    const blockedCard = within(lane(container, 'blocked')).getByRole('button', { name: /OMNI-3/ })
    expect(within(blockedCard).getByText('blocked')).toBeInTheDocument()
    expect(blockedCard.querySelector('.sd-state__clock')).not.toBeNull()
    expect(screen.queryByText('Which tenant is affected?')).toBeNull()
    // Done is the board's word for a completed RCA.
    expect(within(lane(container, 'done')).getByText('done')).toBeInTheDocument()
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

describe('New session', () => {
  it('opens with `n`, starts a triage for the key, and opens the run the job produces', async () => {
    const transport = seeded()
    const { store: s } = mount(transport)
    await screen.findByRole('heading', { name: /Queue/ })

    fireEvent.keyDown(window, { key: 'n' })
    await screen.findByRole('heading', { name: 'Start with a ticket' })
    await waitFor(() => expect(window.location.hash).toBe('#/new'))
    // The screen draws its own filled Start; the footer's New session steps
    // down to the bordered style so the window has one filled button.
    expect(screen.getByRole('button', { name: 'New session' })).toHaveAttribute(
      'data-variant',
      'secondary',
    )

    fireEvent.change(screen.getByRole('searchbox', { name: 'Ticket key or URL' }), {
      target: { value: 'https://acme.atlassian.net/browse/OMNI-11' },
    })
    fireEvent.click(screen.getByRole('button', { name: /^Start/ }))
    await waitFor(() =>
      expect(transport.calls.startTriage).toEqual([
        {
          ws: 'ws1',
          keys: ['OMNI-11'],
          opts: { provider: undefined, model: undefined, dryRun: undefined },
        },
      ]),
    )

    // The runner writes the run; the store pairs it with the job and the
    // screen opens it.
    transport.emit({
      kind: 'run.updated',
      workspaceId: 'ws1',
      run: run({ runId: 'r-11', key: 'OMNI-11', status: 'preparing', startedAt: new Date().toISOString() }),
    })
    await waitFor(() => expect(s.getState().screen).toEqual({ name: 'run', runId: 'r-11' }))
  })

  it('starts an RCA and a fix through the store, on a key with a triage note', async () => {
    const transport = seeded()
    mount(transport)
    await screen.findByRole('heading', { name: /Queue/ })
    fireEvent.click(screen.getByRole('button', { name: 'New session' }))
    await screen.findByRole('heading', { name: 'Start with a ticket' })

    const bar = screen.getByRole('searchbox', { name: 'Ticket key or URL' })
    fireEvent.change(bar, { target: { value: 'OMNI-2' } })
    fireEvent.click(screen.getByRole('radio', { name: 'RCA' }))
    fireEvent.click(screen.getByRole('button', { name: /^Start/ }))
    await waitFor(() => expect(transport.calls.startRCA).toEqual([{ ws: 'ws1', key: 'OMNI-2', opts: { provider: undefined, model: undefined } }]))

    // The RCA's job is still waiting on its run; end it so Fix can go.
    transport.emit({ kind: 'job.finished', jobId: 'job-rca', workspaceId: 'ws1', outcomes: [] })
    await screen.findByText('The job ended before a session started.')

    fireEvent.click(screen.getByRole('radio', { name: 'Fix' }))
    fireEvent.click(screen.getByRole('button', { name: /^Start/ }))
    await waitFor(() =>
      expect(transport.calls.startFix).toEqual([
        { ws: 'ws1', key: 'OMNI-2', opts: { provider: undefined, model: undefined, dryRun: undefined } },
      ]),
    )
  })

  it('refuses RCA and Fix for a key with no triage note, and says why', async () => {
    mount(seeded())
    await screen.findByRole('heading', { name: /Queue/ })
    fireEvent.click(screen.getByRole('button', { name: 'New session' }))
    await screen.findByRole('heading', { name: 'Start with a ticket' })

    fireEvent.change(screen.getByRole('searchbox', { name: 'Ticket key or URL' }), {
      target: { value: 'OMNI-9' },
    })
    expect(screen.getByRole('radio', { name: 'RCA' })).toBeDisabled()
    expect(screen.getByRole('radio', { name: 'Fix' })).toBeDisabled()
    expect(screen.getByRole('status')).toHaveTextContent('RCA and Fix need a triage note for OMNI-9 first.')
  })

  it('lists what landed from the queue, and a row starts its own triage', async () => {
    const transport = seeded()
    mount(transport)
    await screen.findByRole('heading', { name: /Queue/ })
    fireEvent.click(screen.getByRole('button', { name: 'New session' }))

    expect(await screen.findByRole('heading', { name: 'Landed today' })).toBeInTheDocument()
    fireEvent.click(await screen.findByRole('button', { name: 'Triage OMNI-9' }))
    await waitFor(() =>
      expect(transport.calls.startTriage).toEqual([
        { ws: 'ws1', keys: ['OMNI-9'], opts: { provider: undefined, model: undefined, dryRun: undefined } },
      ]),
    )
  })

  it('opens on New session when a fresh window finds a workspace with no runs', async () => {
    const { store: s } = mount(
      createFakeTransport({ workspaces: [workspace({ id: 'ws1', name: 'omni' })] }),
    )
    await screen.findByRole('heading', { name: 'Start with a ticket' })
    expect(s.getState().screen).toEqual({ name: 'new' })
    await waitFor(() => expect(window.location.hash).toBe('#/new'))

    // Decided once: the board stays the board afterwards.
    fireEvent.click(screen.getByRole('button', { name: 'Board' }))
    await screen.findByRole('heading', { name: /Queue/ })
    expect(s.getState().screen).toEqual({ name: 'board' })
  })
})

describe('Sidebar', () => {
  it('switches workspace and reloads that workspace', async () => {
    const transport = createFakeTransport({
      workspaces: [workspace({ id: 'ws1', name: 'omni' }), workspace({ id: 'ws2', name: 'billing' })],
    })
    mount(transport)
    await screen.findByRole('heading', { name: 'Start with a ticket' })

    // The switcher is a popover in the sidebar footer now, not a select.
    fireEvent.click(screen.getByRole('button', { name: 'Workspace: omni' }))
    fireEvent.click(screen.getByRole('option', { name: /billing/ }))
    await waitFor(() => expect(transport.calls.runs).toContain('ws2'))
  })

  it('"Add workspace" goes to Settings', async () => {
    const { store: s } = mount(seeded())
    await screen.findByRole('heading', { name: /Queue/ })

    fireEvent.click(screen.getByRole('button', { name: 'Workspace: omni' }))
    fireEvent.click(screen.getByRole('button', { name: 'Add workspace…' }))
    expect(s.getState().screen).toEqual({ name: 'settings', page: 'workspaces' })
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
    await screen.findByRole('heading', { name: 'Start with a ticket' })

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
    await screen.findByRole('heading', { name: 'Start with a ticket' })
    fireEvent.click(screen.getByRole('button', { name: 'Eval' }))

    // Eval's commit action is the sidebar footer's button, and publishing it
    // is an effect, so it lands a commit after the screen that published it.
    const start = await screen.findByRole('button', { name: /Run eval on the whole set/ })
    await waitFor(() => expect(start).not.toBeDisabled())
    fireEvent.click(start)
    await waitFor(() =>
      expect(transport.calls.startEval).toEqual([
        { ws: 'ws1', keys: undefined, opts: { provider: undefined, model: undefined } },
      ]),
    )
  })
})

/*
 * The window's address. Navigation is state the store holds, and the hash is a
 * second spelling of it, so a screen can be linked to and the Back button does
 * what it looks as though it does.
 */
describe('Deep links', () => {
  const at = (hash: string) => window.history.replaceState(null, '', hash)

  it.each([
    ['#/register', 'Register'],
    ['#/eval', 'Eval'],
    ['#/settings', 'Settings'],
  ])('%s opens that screen straight away', async (hash, tab) => {
    at(hash)
    mount(seeded())

    await waitFor(() =>
      expect(screen.getByRole('button', { name: tab })).toHaveAttribute('aria-current', 'page'),
    )
  })

  it('#/runs/<ws>/<id> opens the run, on the workspace the link names', async () => {
    at('#/runs/ws2/r9')
    const transport = createFakeTransport({
      workspaces: [workspace({ id: 'ws1', name: 'omni' }), workspace({ id: 'ws2', name: 'ledger' })],
    })
    const { store: s } = mount(transport)

    await waitFor(() => expect(s.getState().screen).toEqual({ name: 'run', runId: 'r9' }))
    expect(s.getState().currentWorkspaceId).toBe('ws2')
    expect(await screen.findByRole('button', { name: 'Back' })).toBeInTheDocument()
    await waitFor(() => expect(transport.calls.runs).toContain('ws2'))
  })

  // A workspace this install does not have cannot be switched to; the screen
  // still opens, on whichever one is current, rather than showing nothing.
  it('follows a run link naming an unknown workspace as far as it can', async () => {
    at('#/runs/ws-gone/r9')
    const { store: s } = mount(seeded())

    await waitFor(() => expect(s.getState().screen).toEqual({ name: 'run', runId: 'r9' }))
    expect(s.getState().currentWorkspaceId).toBe('ws1')
    await waitFor(() => expect(window.location.hash).toBe('#/runs/ws1/r9'))
  })

  it('a hash naming nothing opens the board, and says so', async () => {
    at('#/nowhere')
    const { store: s } = mount(seeded())
    await screen.findByRole('heading', { name: /Queue/ })

    expect(s.getState().screen).toEqual({ name: 'board' })
    // The address catches up with the screen, rather than describing one that
    // is not up.
    await waitFor(() => expect(window.location.hash).toBe('#/'))
  })

  it('the header buttons write the address they navigate to', async () => {
    mount(seeded())
    await screen.findByRole('heading', { name: /Queue/ })
    await waitFor(() => expect(window.location.hash).toBe('#/'))

    fireEvent.click(screen.getByRole('button', { name: 'Register' }))
    await waitFor(() => expect(window.location.hash).toBe('#/register'))

    fireEvent.click(screen.getByRole('button', { name: 'Settings' }))
    await waitFor(() => expect(window.location.hash).toBe('#/settings'))

    fireEvent.click(screen.getByRole('button', { name: 'Board' }))
    await waitFor(() => expect(window.location.hash).toBe('#/'))
  })

  it('a settings link opens the modal on the page it names, and the page writes the address', async () => {
    window.location.hash = '#/settings/notifications'
    mount(seeded())
    const dialog = await screen.findByRole('dialog', { name: 'Notifications' })
    expect(dialog).toBeInTheDocument()

    fireEvent.click(within(dialog).getByRole('button', { name: 'About' }))
    await waitFor(() => expect(window.location.hash).toBe('#/settings/about'))
    expect(screen.getByRole('dialog', { name: 'About' })).toBeInTheDocument()
  })

  it('New session in the footer opens #/new, and Sessions leads back to the newest run', async () => {
    mount(seeded())
    await screen.findByRole('heading', { name: /Queue/ })

    fireEvent.click(screen.getByRole('button', { name: 'New session' }))
    await waitFor(() => expect(window.location.hash).toBe('#/new'))
    expect(screen.getByRole('heading', { name: 'Start with a ticket' })).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: 'Sessions' }))
    await waitFor(() => expect(window.location.hash).toBe('#/runs/ws1/r1'))
  })

  it('opening a run card writes a link to that run', async () => {
    const { container } = mount(seeded())
    await waitFor(() =>
      expect(within(lane(container, 'gathering')).getByText('OMNI-1')).toBeInTheDocument(),
    )

    fireEvent.click(within(lane(container, 'gathering')).getByRole('button', { name: /OMNI-1/ }))
    await waitFor(() => expect(window.location.hash).toBe('#/runs/ws1/r1'))
  })

  // Back, in a browser or in the desktop webview, is a hashchange.
  it('follows the address back to where it was', async () => {
    const { store: s } = mount(seeded())
    await screen.findByRole('heading', { name: /Queue/ })

    fireEvent.click(screen.getByRole('button', { name: 'Eval' }))
    await waitFor(() => expect(window.location.hash).toBe('#/eval'))

    at('#/')
    fireEvent(window, new HashChangeEvent('hashchange'))
    await waitFor(() => expect(s.getState().screen).toEqual({ name: 'board' }))
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
    await screen.findByRole('heading', { name: 'Start with a ticket' })

    fireEvent.click(screen.getByRole('button', { name: 'Settings' }))
    // Settings is a modal with its own secondary nav; the summary is one page
    // of it and the board stays painted behind the scrim.
    expect(await screen.findByRole('dialog', { name: 'Workspaces' })).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Notifications' }))
    const panel = await screen.findByRole('region', { name: 'Notifications and webhooks' })
    expect(within(panel).getByText('env: reference')).toBeInTheDocument()
    expect(within(panel).getByText(/Inbound webhooks are off/)).toBeInTheDocument()
  })
})
