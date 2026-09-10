import { afterEach, describe, expect, it } from 'vitest'
import { createAppStore, isQueueUnsupported, type AppStore } from './appStore'
import { createFakeTransport, run, ticket, workspace } from './fakeTransport'

let store: AppStore | null = null

afterEach(() => {
  store?.dispose()
  store = null
  try {
    localStorage.clear()
  } catch {
    // No storage in this environment; nothing to reset.
  }
})

/** Lets the promises `init()` fans out settle before asserting. */
const settle = () => new Promise((resolve) => setTimeout(resolve, 0))

describe('createAppStore', () => {
  it('loads workspaces, runs and tickets, then applies run.updated', async () => {
    const transport = createFakeTransport({
      workspaces: [workspace({ id: 'ws1', name: 'omni' })],
      runs: [run({ runId: 'r1', key: 'OMNI-1', status: 'completed' })],
      tickets: [ticket({ key: 'OMNI-9' })],
    })
    store = createAppStore(transport)
    await store.init()
    await settle()

    let state = store.getState()
    expect(state.workspaces.map((w) => w.id)).toEqual(['ws1'])
    expect(state.currentWorkspaceId).toBe('ws1')
    expect(state.runsByWorkspace.ws1?.map((r) => r.runId)).toEqual(['r1'])
    expect(state.ticketsByWorkspace.ws1?.map((t) => t.key)).toEqual(['OMNI-9'])
    expect(state.loading).toBe(false)
    expect(transport.subscriberCount()).toBe(1)

    transport.emit({
      kind: 'run.updated',
      workspaceId: 'ws1',
      run: run({ runId: 'r1', key: 'OMNI-1', status: 'running' }),
    })
    transport.emit({
      kind: 'run.updated',
      workspaceId: 'ws1',
      run: run({ runId: 'r2', key: 'OMNI-2', status: 'preparing' }),
    })

    state = store.getState()
    expect(state.runsByWorkspace.ws1).toHaveLength(2)
    expect(state.runsByWorkspace.ws1?.find((r) => r.runId === 'r1')?.status).toBe('running')
    expect(state.runsByWorkspace.ws1?.find((r) => r.runId === 'r2')?.status).toBe('preparing')
  })

  it('remembers the last workspace and restores it on the next init', async () => {
    const seed = {
      workspaces: [workspace({ id: 'ws1' }), workspace({ id: 'ws2', name: 'billing' })],
    }
    store = createAppStore(createFakeTransport(seed))
    await store.init()
    await settle()
    store.setWorkspace('ws2')
    store.dispose()

    store = createAppStore(createFakeTransport(seed))
    await store.init()
    await settle()
    expect(store.getState().currentWorkspaceId).toBe('ws2')
  })

  it('refreshes runs and toasts when a job finishes', async () => {
    const transport = createFakeTransport({ runs: [] })
    store = createAppStore(transport)
    await store.init()
    await settle()
    const before = transport.calls.runs.length

    transport.setRuns([run({ runId: 'r7', key: 'OMNI-7', status: 'completed' })])
    transport.emit({
      kind: 'job.finished',
      jobId: 'job-1',
      workspaceId: 'ws1',
      outcomes: [{ key: 'OMNI-7', status: 'completed', runId: 'r7' }],
    })
    await settle()

    expect(transport.calls.runs.length).toBe(before + 1)
    expect(store.getState().runsByWorkspace.ws1?.map((r) => r.runId)).toEqual(['r7'])
    expect(store.getState().toasts.map((t) => t.text)).toEqual(['Finished 1 run.'])
  })

  it('marks the workspace queue unsupported when the tracker answers 501', async () => {
    const transport = createFakeTransport({ tickets: [ticket()] })
    transport.failQueue(new Error('501 Not Implemented'))
    store = createAppStore(transport)
    await store.init()
    await settle()

    const state = store.getState()
    expect(state.queueUnsupported.ws1).toBe(true)
    expect(state.ticketsByWorkspace.ws1).toEqual([])
    // A missing tracker is a configuration fact, not an error to shout about.
    expect(state.toasts).toEqual([])
  })

  it('reports a real queue failure as an error toast', async () => {
    const transport = createFakeTransport()
    transport.failQueue(new Error('connection refused'))
    store = createAppStore(transport)
    await store.init()
    await settle()

    expect(store.getState().queueUnsupported.ws1).toBeFalsy()
    expect(store.getState().toasts[0]?.tone).toBe('error')
  })

  it('startTriage forwards the keys and options to the transport', async () => {
    const transport = createFakeTransport()
    store = createAppStore(transport)
    await store.init()
    await settle()

    await store.startTriage(['OMNI-1', 'OMNI-2'], { provider: 'codex', dryRun: true })
    expect(transport.calls.startTriage).toEqual([
      { ws: 'ws1', keys: ['OMNI-1', 'OMNI-2'], opts: { provider: 'codex', dryRun: true } },
    ])
    expect(store.getState().toasts[0]?.text).toBe('Triage started for 2 keys.')
  })

  it('toasts when triage cannot start', async () => {
    const transport = createFakeTransport()
    transport.startTriage = async () => {
      throw new Error('provider not configured')
    }
    store = createAppStore(transport)
    await store.init()
    await settle()

    await store.startTriage(['OMNI-1'])
    const toast = store.getState().toasts[0]
    expect(toast?.tone).toBe('error')
    expect(toast?.text).toContain('provider not configured')
  })

  it('navigate replaces the screen', async () => {
    store = createAppStore(createFakeTransport())
    await store.init()
    await settle()
    store.navigate({ name: 'run', runId: 'r1' })
    expect(store.getState().screen).toEqual({ name: 'run', runId: 'r1' })
  })

  it('quota.updated keeps one reading per provider', async () => {
    const transport = createFakeTransport({
      quota: [{ provider: 'claude', observedAt: '2026-09-10T09:00:00Z', usedPercent: 10 }],
    })
    store = createAppStore(transport)
    await store.init()
    await settle()

    transport.emit({
      kind: 'quota.updated',
      quota: { provider: 'claude', observedAt: '2026-09-10T10:00:00Z', usedPercent: 40 },
    })
    transport.emit({
      kind: 'quota.updated',
      quota: { provider: 'codex', observedAt: '2026-09-10T10:00:00Z', usedPercent: 5 },
    })

    const quota = store.getState().quota
    expect(quota).toHaveLength(2)
    expect(quota.find((q) => q.provider === 'claude')?.usedPercent).toBe(40)
  })
})

describe('isQueueUnsupported', () => {
  it('recognises the shapes both transports can produce', () => {
    expect(isQueueUnsupported(new Error('501 Not Implemented'))).toBe(true)
    expect(isQueueUnsupported(new Error('ErrUnsupported: no tracker configured'))).toBe(true)
    expect(isQueueUnsupported(new Error('500 Internal Server Error'))).toBe(false)
  })
})
