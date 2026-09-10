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
