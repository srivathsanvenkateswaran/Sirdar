import { afterEach, describe, expect, it } from 'vitest'
import { getRunJob, resetRunJobs } from '../lib/jobs'
import { createAppStore, INBOUND_LIMIT, isQueueUnsupported, type AppStore } from './appStore'
import { createFakeTransport, run, searchHit, ticket, workspace } from './fakeTransport'

let store: AppStore | null = null

afterEach(() => {
  store?.dispose()
  store = null
  resetRunJobs()
  try {
    localStorage.clear()
  } catch {
    // No storage in this environment; nothing to reset.
  }
})

/** Lets the promises `init()` fans out settle before asserting. */
const settle = () => new Promise((resolve) => setTimeout(resolve, 0))

/** A run started now, as the service would stamp it. */
const nowISO = () => new Date().toISOString()

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

  // A failed start toasts *and* rejects: the toast is for the window, the
  // rejection for the form that asked, whose error line was dead while the
  // store swallowed it.
  it('toasts and rethrows when triage cannot start', async () => {
    const transport = createFakeTransport()
    transport.startTriage = async () => {
      throw new Error('provider not configured')
    }
    store = createAppStore(transport)
    await store.init()
    await settle()

    await expect(store.startTriage(['OMNI-1'])).rejects.toThrow('provider not configured')
    const toast = store.getState().toasts[0]
    expect(toast?.tone).toBe('error')
    expect(toast?.text).toContain('provider not configured')
  })

  // Cancel only works for a job this window started, and the job id comes
  // back before the run it produces exists. The store holds it until a
  // run.updated for one of the job's keys says which run to pair it with.
  it('pairs a started job with the run that comes back for its key', async () => {
    const transport = createFakeTransport()
    store = createAppStore(transport)
    await store.init()
    await settle()

    await store.startTriage(['OMNI-1', 'OMNI-2'])
    expect(getRunJob('r-omni-1')).toBeUndefined()

    transport.emit({
      kind: 'run.updated',
      workspaceId: 'ws1',
      run: run({ runId: 'r-omni-1', key: 'OMNI-1', status: 'running', startedAt: nowISO() }),
    })
    transport.emit({
      kind: 'run.updated',
      workspaceId: 'ws1',
      run: run({ runId: 'r-omni-2', key: 'OMNI-2', status: 'running', startedAt: nowISO() }),
    })

    expect(getRunJob('r-omni-1')).toBe('job-1')
    expect(getRunJob('r-omni-2')).toBe('job-1')

    // A second report for the same run does not re-pair it, and a key the
    // job never asked for is not this job's.
    transport.emit({
      kind: 'run.updated',
      workspaceId: 'ws1',
      run: run({ runId: 'r-omni-9', key: 'OMNI-9', status: 'running', startedAt: nowISO() }),
    })
    expect(getRunJob('r-omni-9')).toBeUndefined()

    transport.emit({
      kind: 'job.finished',
      jobId: 'job-1',
      workspaceId: 'ws1',
      outcomes: [{ key: 'OMNI-1', status: 'completed', runId: 'r-omni-1' }],
    })
    await settle()

    // A finished job is no longer one the service will cancel.
    expect(getRunJob('r-omni-1')).toBeUndefined()
    expect(getRunJob('r-omni-2')).toBeUndefined()
  })

  it('does not pair a job with an older run for the same key', async () => {
    const transport = createFakeTransport()
    store = createAppStore(transport)
    await store.init()
    await settle()

    await store.startTriage(['OMNI-1'])
    transport.emit({
      kind: 'run.updated',
      workspaceId: 'ws1',
      run: run({ runId: 'r-last-week', key: 'OMNI-1', startedAt: '2026-09-03T09:00:00Z' }),
    })
    expect(getRunJob('r-last-week')).toBeUndefined()

    // The run this job actually started still claims it.
    transport.emit({
      kind: 'run.updated',
      workspaceId: 'ws1',
      run: run({ runId: 'r-now', key: 'OMNI-1', status: 'running', startedAt: nowISO() }),
    })
    expect(getRunJob('r-now')).toBe('job-1')
  })

  it('startRCA forwards the key and pairs its job with the run', async () => {
    const transport = createFakeTransport()
    store = createAppStore(transport)
    await store.init()
    await settle()

    await store.startRCA('OMNI-1', { prUrl: 'https://github.com/acme/api/pull/12' })
    expect(transport.calls.startRCA).toEqual([
      { ws: 'ws1', key: 'OMNI-1', opts: { prUrl: 'https://github.com/acme/api/pull/12' } },
    ])
    expect(store.getState().toasts[0]?.text).toBe('Root cause analysis started for OMNI-1.')

    transport.emit({
      kind: 'run.updated',
      workspaceId: 'ws1',
      run: run({ runId: 'r-rca', key: 'OMNI-1', kind: 'rca', startedAt: nowISO() }),
    })
    expect(getRunJob('r-rca')).toBe('job-rca')
  })

  it('toasts and rethrows when an RCA cannot start', async () => {
    const transport = createFakeTransport()
    transport.startRCA = async () => {
      throw new Error('no rca playbook')
    }
    store = createAppStore(transport)
    await store.init()
    await settle()

    await expect(store.startRCA('OMNI-1')).rejects.toThrow('no rca playbook')
    const toast = store.getState().toasts[0]
    expect(toast?.tone).toBe('error')
    expect(toast?.text).toContain('no rca playbook')
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

  it('startFix forwards the flags and pairs its job with the run', async () => {
    const transport = createFakeTransport()
    store = createAppStore(transport)
    await store.init()
    await settle()

    await store.startFix('OMNI-1', { base: 'main', provider: 'codex' })
    expect(transport.calls.startFix).toEqual([
      { ws: 'ws1', key: 'OMNI-1', opts: { base: 'main', provider: 'codex' } },
    ])
    expect(store.getState().toasts.at(-1)?.text).toBe('Fix started for OMNI-1.')

    transport.emit({
      kind: 'run.updated',
      workspaceId: 'ws1',
      run: run({ runId: 'r-fix', key: 'OMNI-1', kind: 'fix', startedAt: nowISO() }),
    })
    expect(getRunJob('r-fix')).toBe('job-fix-1')
  })

  it('an accepted deviation says it is publishing, not starting over', async () => {
    const transport = createFakeTransport()
    store = createAppStore(transport)
    await store.init()
    await settle()

    await store.startFix('OMNI-1', { acceptDeviation: true })
    expect(store.getState().toasts.at(-1)?.text).toBe('Publishing the reviewed commit for OMNI-1.')
  })

  it('toasts and rethrows when a fix cannot start, and refuses an empty key', async () => {
    const transport = createFakeTransport()
    transport.startFix = async () => {
      throw new Error('the triage note for OMNI-1 is not approved')
    }
    store = createAppStore(transport)
    await store.init()
    await settle()

    // An empty key never reaches the transport, so it is refused rather
    // than rejected: there is nothing for the form to report that the toast
    // does not already say.
    await store.startFix('')
    expect(store.getState().toasts.at(-1)?.text).toBe('Enter a ticket key.')

    await expect(store.startFix('OMNI-1')).rejects.toThrow('is not approved')
    expect(store.getState().toasts.at(-1)?.text).toBe(
      'Fix did not start. the triage note for OMNI-1 is not approved',
    )
  })

  it('startEval says whether it is running the whole set or a selection', async () => {
    const transport = createFakeTransport()
    store = createAppStore(transport)
    await store.init()
    await settle()

    await store.startEval()
    expect(transport.calls.startEval).toEqual([{ ws: 'ws1', keys: undefined, opts: undefined }])
    expect(store.getState().toasts.at(-1)?.text).toBe('Eval started for the whole golden set.')

    await store.startEval(['OMNI-1'], { provider: 'openai' })
    expect(store.getState().toasts.at(-1)?.text).toBe('Eval started for OMNI-1.')

    await store.startEval(['OMNI-1', 'OMNI-2'])
    expect(store.getState().toasts.at(-1)?.text).toBe('Eval started for 2 keys.')
  })

  /*
   * An eval over the whole set names no key, so `claim` can never pair it
   * with a run and Run detail's Cancel can never reach it. It is kept on the
   * state instead, and the Eval screen offers the button.
   */
  it('keeps a whole-set eval job cancellable, and forgets it when it ends', async () => {
    const transport = createFakeTransport()
    store = createAppStore(transport)
    await store.init()
    await settle()

    await store.startEval()
    const [job] = store.getState().keylessJobs
    expect(job?.workspaceId).toBe('ws1')
    expect(job?.label).toContain('golden set')

    await store.cancelJob(job.jobId)
    expect(transport.calls.cancel).toEqual([job.jobId])
    expect(store.getState().keylessJobs).toEqual([])
  })

  /*
   * An eval over named keys is many runs on a screen that shows none of
   * them, so the Eval screen keeps Cancel for the job as a whole, and each
   * run is still paired with it for Run detail.
   */
  it('an eval over named keys is paired with its runs and still cancellable as a job', async () => {
    const transport = createFakeTransport()
    store = createAppStore(transport)
    await store.init()
    await settle()

    await store.startEval(['OMNI-1'])
    expect(store.getState().keylessJobs.map((j) => j.label)).toEqual(['Eval of OMNI-1'])

    transport.emit({ kind: 'run.updated', workspaceId: 'ws1', run: run({ runId: 'r9', key: 'OMNI-1', startedAt: nowISO() }) })
    expect(getRunJob('r9')).toBeTruthy()

    await store.startEval(['OMNI-1', 'OMNI-2'])
    expect(store.getState().keylessJobs.map((j) => j.label)).toEqual(['Eval of OMNI-1', 'Eval of 2 keys'])

    // A triage is only ever the runs it produces.
    await store.startTriage(['OMNI-3'])
    expect(store.getState().keylessJobs).toHaveLength(2)
  })

  it('subscribes before the first read and lets init be tried again after a failure', async () => {
    const transport = createFakeTransport()
    let attempts = 0
    const workspaces = transport.workspaces
    transport.workspaces = async () => {
      attempts += 1
      if (attempts === 1) throw new Error('the service is not up yet')
      return workspaces()
    }
    store = createAppStore(transport)
    await store.init()
    expect(transport.subscriberCount()).toBe(1)
    expect(store.getState().loading).toBe(false)
    expect(store.getState().toasts.at(-1)?.text).toContain('the service is not up yet')

    await store.init()
    await settle()
    expect(attempts).toBe(2)
    expect(store.getState().currentWorkspaceId).toBe('ws1')
    // One stream across both attempts.
    expect(transport.subscriberCount()).toBe(1)
  })

  it('says when the stream is lost and resyncs when it is back', async () => {
    const transport = createFakeTransport()
    store = createAppStore(transport)
    await store.init()
    await settle()
    const runsBefore = transport.calls.runs.length
    const queueBefore = transport.calls.queue.length

    transport.emit({ kind: 'live', state: 'lost' })
    expect(store.getState().liveUpdates).toBe('lost')
    expect(store.getState().toasts.at(-1)?.text).toBe('Live updates lost; reconnecting.')
    // The browser retries and reports every failed attempt; one word is enough.
    transport.emit({ kind: 'live', state: 'lost' })
    expect(store.getState().toasts.filter((t) => t.text.startsWith('Live updates lost'))).toHaveLength(1)

    transport.emit({ kind: 'live', state: 'open' })
    await settle()
    expect(store.getState().liveUpdates).toBe('live')
    expect(transport.calls.runs.length).toBe(runsBefore + 1)
    expect(transport.calls.queue.length).toBe(queueBefore + 1)

    // An open on a stream that was never lost — the first one — reads nothing again.
    transport.emit({ kind: 'live', state: 'open' })
    await settle()
    expect(transport.calls.runs.length).toBe(runsBefore + 1)
  })

  it('job.finished releases a keyless job', async () => {
    const transport = createFakeTransport()
    store = createAppStore(transport)
    await store.init()
    await settle()

    await store.startEval()
    const { jobId } = store.getState().keylessJobs[0]

    transport.emit({ kind: 'job.finished', jobId, workspaceId: 'ws1', outcomes: [] })
    expect(store.getState().keylessJobs).toEqual([])
  })

  it('toasts and rethrows when a cancel is refused', async () => {
    const transport = createFakeTransport()
    transport.cancel = async () => {
      throw new Error('no such job')
    }
    store = createAppStore(transport)
    await store.init()
    await settle()

    await expect(store.cancelJob('job-9')).rejects.toThrow('no such job')
    expect(store.getState().toasts.at(-1)?.text).toContain('no such job')
  })

  it('toasts and rethrows when an eval cannot start', async () => {
    const transport = createFakeTransport()
    transport.startEval = async () => {
      throw new Error('no golden bundles')
    }
    store = createAppStore(transport)
    await store.init()
    await settle()

    await expect(store.startEval()).rejects.toThrow('no golden bundles')
    expect(store.getState().toasts.at(-1)?.text).toBe('Eval did not start. no golden bundles')
  })

  it('hook.received lands in the inbound list, newest first, and toasts', async () => {
    const transport = createFakeTransport()
    store = createAppStore(transport)
    await store.init()
    await settle()

    transport.emit({ kind: 'hook.received', source: 'jira', key: 'OMNI-1', outcome: 'started' })
    transport.emit({ kind: 'hook.received', source: 'jira', key: 'OMNI-2', outcome: 'filtered' })

    const { inbound, toasts } = store.getState()
    expect(inbound.map((d) => d.key)).toEqual(['OMNI-2', 'OMNI-1'])
    expect(inbound[0]?.outcome).toBe('filtered')
    expect(toasts.at(-1)?.text).toBe(
      "Webhook from jira \u00b7 OMNI-2 did not match this workspace's filter.",
    )
    expect(toasts.at(-1)?.tone).toBe('info')
  })

  it('a rejected delivery is an error toast, and a delivery with no key names only its source', async () => {
    const transport = createFakeTransport()
    store = createAppStore(transport)
    await store.init()
    await settle()

    transport.emit({ kind: 'hook.received', source: 'zoho', outcome: 'ignored' })
    expect(store.getState().toasts.at(-1)?.text).toBe('Webhook from zoho named no ticket.')
    expect(store.getState().inbound[0]?.key).toBe('')

    transport.emit({ kind: 'hook.received', source: 'zoho', key: 'OMNI-3', outcome: 'rejected' })
    expect(store.getState().toasts.at(-1)?.tone).toBe('error')
  })

  it('the inbound list is capped, so a chatty tracker cannot grow it without end', async () => {
    const transport = createFakeTransport()
    store = createAppStore(transport)
    await store.init()
    await settle()

    for (let i = 0; i < INBOUND_LIMIT + 5; i += 1) {
      transport.emit({ kind: 'hook.received', source: 'jira', key: `OMNI-${i}`, outcome: 'skipped' })
    }
    const { inbound } = store.getState()
    expect(inbound).toHaveLength(INBOUND_LIMIT)
    expect(inbound[0]?.key).toBe(`OMNI-${INBOUND_LIMIT + 4}`)
  })

  it('deleteRun asks the transport, drops the row, toasts, and leaves a run screen for the board', async () => {
    const transport = createFakeTransport({
      runs: [run({ runId: 'r1', key: 'OMNI-1' }), run({ runId: 'r2', key: 'OMNI-2', kind: 'fix' })],
    })
    store = createAppStore(transport)
    await store.init()
    await settle()
    store.navigate({ name: 'run', runId: 'r2' })

    await store.deleteRun('r2')

    expect(transport.calls.deleteRun).toEqual([{ ws: 'ws1', runId: 'r2' }])
    const state = store.getState()
    expect(state.runsByWorkspace.ws1?.map((r) => r.runId)).toEqual(['r1'])
    expect(state.screen).toEqual({ name: 'board' })
    expect(state.toasts.at(-1)?.text).toBe('Deleted the fix run for OMNI-2.')
    expect(state.toasts.at(-1)?.tone).toBe('info')
  })

  it('a refused delete toasts the reason, rethrows, and keeps the row', async () => {
    const transport = createFakeTransport({
      runs: [run({ runId: 'r1', key: 'OMNI-1', status: 'running' })],
    })
    store = createAppStore(transport)
    await store.init()
    await settle()

    await expect(store.deleteRun('r1')).rejects.toThrow(/live/)
    expect(store.getState().runsByWorkspace.ws1).toHaveLength(1)
    expect(store.getState().toasts.at(-1)?.text).toMatch(/^Could not delete the run\. .*live/)
    expect(store.getState().toasts.at(-1)?.tone).toBe('error')
  })

  it('run.removed from another window drops the row too, and is harmless for a row already gone', async () => {
    const transport = createFakeTransport({
      runs: [run({ runId: 'r1', key: 'OMNI-1' }), run({ runId: 'r2', key: 'OMNI-2' })],
    })
    store = createAppStore(transport)
    await store.init()
    await settle()
    store.navigate({ name: 'review', runId: 'r1' })

    transport.emit({ kind: 'run.removed', workspaceId: 'ws1', runId: 'r1' })
    expect(store.getState().runsByWorkspace.ws1?.map((r) => r.runId)).toEqual(['r2'])
    expect(store.getState().screen).toEqual({ name: 'board' })

    const before = store.getState()
    transport.emit({ kind: 'run.removed', workspaceId: 'ws1', runId: 'r1' })
    expect(store.getState()).toBe(before)
    // Another workspace's removal moves nothing here.
    transport.emit({ kind: 'run.removed', workspaceId: 'ws9', runId: 'r2' })
    expect(store.getState().runsByWorkspace.ws1).toHaveLength(1)
  })

  it('search goes to the current workspace and answers nothing for a blank query without asking', async () => {
    const transport = createFakeTransport({ hits: [searchHit()] })
    store = createAppStore(transport)
    await store.init()
    await settle()

    await expect(store.search('EXPORT')).resolves.toHaveLength(1)
    await expect(store.search('   ')).resolves.toEqual([])
    expect(transport.calls.search).toEqual([{ ws: 'ws1', q: 'EXPORT' }])

    transport.failSearch(new Error('500 Internal Server Error'))
    await expect(store.search('x')).rejects.toThrow('500')
  })
})

describe('isQueueUnsupported', () => {
  it('recognises the shapes both transports can produce', () => {
    expect(isQueueUnsupported(new Error('501 Not Implemented'))).toBe(true)
    expect(isQueueUnsupported(new Error('ErrUnsupported: no tracker configured'))).toBe(true)
    expect(isQueueUnsupported(new Error('500 Internal Server Error'))).toBe(false)
  })
})
