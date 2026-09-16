import { act, render, screen, within } from '@testing-library/react'
import { afterEach, describe, expect, it } from 'vitest'
import App from './App'
import { renderCount, resetRenderCounts } from './lib/renderProbe'
import { createAppStore, type AppStore } from './store/appStore'
import { createFakeTransport, run, workspace, type FakeTransport } from './store/fakeTransport'
import { StoreProvider } from './store/useAppStore'

/**
 * What a store emit costs the shell. The store emits once per change of any
 * kind, and every subscriber used to render on each: a toast landing re-drew
 * the sidebar's rows and the board's cards. These hold the line: an emit that
 * does not touch the runs draws no sessions row.
 */

let store: AppStore | null = null

afterEach(() => {
  store?.dispose()
  store = null
  resetRenderCounts()
  try {
    localStorage.clear()
  } catch {
    // Nothing to reset.
  }
  window.history.replaceState(null, '', window.location.pathname)
})

function mount(transport: FakeTransport) {
  store = createAppStore(transport)
  return render(
    <StoreProvider value={store}>
      <App />
    </StoreProvider>,
  )
}

const seeded = () =>
  createFakeTransport({
    workspaces: [workspace({ id: 'ws1', name: 'omni' })],
    runs: [
      run({ runId: 'r1', key: 'OMNI-1', status: 'running', updatedAt: '2026-09-10T09:05:00Z' }),
      run({ runId: 'r2', key: 'OMNI-2', kind: 'triage', status: 'completed' }),
      run({ runId: 'r3', key: 'OMNI-3', status: 'blocked', reason: 'Which tenant is affected?' }),
    ],
  })

describe('what a store emit re-renders', () => {
  it('a quota reading, a toast and an inbound delivery draw no sessions row', async () => {
    const transport = seeded()
    mount(transport)
    const rows = await screen.findByRole('navigation', { name: 'Sessions' })
    within(rows).getByRole('button', { name: /OMNI-1/ })
    expect(renderCount('SessionRow')).toBeGreaterThan(0)

    resetRenderCounts()
    act(() => {
      transport.emit({
        kind: 'quota.updated',
        quota: { provider: 'claude', usedPercent: 40, observedAt: '2026-09-16T10:00:00Z' },
      })
    })
    act(() => {
      store?.toast('Something happened.')
    })
    act(() => {
      transport.emit({ kind: 'hook.received', source: 'janus', key: 'OMNI-9', outcome: 'filtered' })
    })
    expect(screen.getByText('Something happened.')).toBeInTheDocument()
    expect(renderCount('SessionRow')).toBe(0)
  })

  it('a run changing draws its own row and no other', async () => {
    const transport = seeded()
    mount(transport)
    const rows = await screen.findByRole('navigation', { name: 'Sessions' })
    within(rows).getByRole('button', { name: /OMNI-1, triage, running/ })

    resetRenderCounts()
    act(() => {
      transport.emit({
        kind: 'run.updated',
        workspaceId: 'ws1',
        run: run({ runId: 'r1', key: 'OMNI-1', status: 'completed', updatedAt: '2026-09-10T09:06:00Z' }),
      })
    })
    expect(within(rows).getByRole('button', { name: 'OMNI-1, triage' })).toBeInTheDocument()
    expect(renderCount('SessionRow')).toBe(1)
  })
})
