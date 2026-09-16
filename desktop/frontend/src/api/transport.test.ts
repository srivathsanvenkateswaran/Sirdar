import { afterEach, describe, expect, it, vi } from 'vitest'
import { FLUSH_MS } from './coalesce'
import { createTransport, createWailsTransport } from './transport'
import type { MCPCallResult, RunDiff, RunSummary } from './types'

const sample: RunSummary[] = [
  {
    runId: '20260910-120000-OMNI-1',
    key: 'OMNI-1',
    title: 'Statement export times out',
    kind: 'triage',
    status: 'completed',
    provider: 'claude',
    model: 'sonnet',
    startedAt: '2026-09-10T12:00:00Z',
    updatedAt: '2026-09-10T12:04:00Z',
    reason: '',
    usage: { turns: 7, inputTokens: 1200, outputTokens: 800, costUsd: 0.12 },
    notes: ['triage.md'],
  },
]

function mockFetch(body: unknown, init: { status?: number; contentType?: string } = {}) {
  const fetchMock = vi.fn(async (_input: RequestInfo | URL, _init?: RequestInit) =>
    new Response(JSON.stringify(body), {
      status: init.status ?? 200,
      headers: { 'Content-Type': init.contentType ?? 'application/json' },
    }),
  )
  vi.stubGlobal('fetch', fetchMock)
  return fetchMock
}

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('http transport', () => {
  it('requests runs for a workspace filtered by key and parses the JSON', async () => {
    const fetchMock = mockFetch(sample)

    const got = await createTransport().runs('ws1', 'K')

    expect(fetchMock).toHaveBeenCalledTimes(1)
    expect(fetchMock.mock.calls[0]![0]).toBe('/api/workspaces/ws1/runs?key=K')
    expect(got).toEqual(sample)
    expect(got[0]!.usage.costUsd).toBe(0.12)
  })

  it('omits the query string when no key is given', async () => {
    const fetchMock = mockFetch([])

    await createTransport().runs('ws1')

    expect(fetchMock.mock.calls[0]![0]).toBe('/api/workspaces/ws1/runs')
  })

  /*
   * The empty note kind is the run's own note.md, which is how a fix run's
   * note is reached. It has to travel as *no* query parameter: `?kind=` and an
   * absent `kind` both mean the same thing to the server, and the query
   * builder drops empty values, so this is the shape that actually goes out.
   */
  it('asks for the run\'s own note with no kind parameter', async () => {
    const spy = vi.fn(
      async (_input: RequestInfo | URL, _init?: RequestInit) =>
        new Response('# note', { headers: { 'Content-Type': 'text/markdown' } }),
    )
    vi.stubGlobal('fetch', spy)

    await createTransport().note('ws1', 'r1', '')
    expect(spy.mock.calls[0]![0]).toBe('/api/workspaces/ws1/runs/r1/note')

    await createTransport().note('ws1', 'r1', 'triage')
    expect(spy.mock.calls[1]![0]).toBe('/api/workspaces/ws1/runs/r1/note?kind=triage')
  })

  it('posts a fix to the workspace fix route, flags and all', async () => {
    const fetchMock = mockFetch({ jobId: 'job-1' })

    await createTransport().startFix('ws 1', 'OMNI-1', { base: 'main', acceptDeviation: true })

    expect(fetchMock.mock.calls[0]![0]).toBe('/api/workspaces/ws%201/fix')
    const init = fetchMock.mock.calls[0]![1] as RequestInit
    expect(init.method).toBe('POST')
    expect(JSON.parse(String(init.body))).toEqual({
      key: 'OMNI-1',
      base: 'main',
      acceptDeviation: true,
    })
  })

  // An eval of the whole golden set sends no keys at all, which is what the
  // route reads as "every key in the set".
  it('leaves keys out of an eval over the whole set', async () => {
    const fetchMock = mockFetch({ jobId: 'job-2' })

    await createTransport().startEval('ws1', [], { provider: 'qwen' })

    expect(fetchMock.mock.calls[0]![0]).toBe('/api/workspaces/ws1/eval')
    expect(JSON.parse(String((fetchMock.mock.calls[0]![1] as RequestInit).body))).toEqual({
      provider: 'qwen',
    })
  })

  it('sends the keys it was given', async () => {
    const fetchMock = mockFetch({ jobId: 'job-3' })

    await createTransport().startEval('ws1', ['OMNI-1'])

    expect(JSON.parse(String((fetchMock.mock.calls[0]![1] as RequestInit).body))).toEqual({
      keys: ['OMNI-1'],
    })
  })

  it('reads the golden set, the reports and the config summary from their own routes', async () => {
    const fetchMock = mockFetch([])
    const t = createTransport()

    await t.golden('ws1')
    await t.evalReports('ws1')
    await t.latestRetro('ws1')
    await t.configSummary('ws1')

    expect(fetchMock.mock.calls.map((c) => c[0])).toEqual([
      '/api/workspaces/ws1/golden',
      '/api/workspaces/ws1/eval',
      '/api/workspaces/ws1/eval/retro/latest',
      '/api/workspaces/ws1/config/summary',
    ])
  })

  it('sends the retro flags with an eval start', async () => {
    const fetchMock = mockFetch({ jobId: 'job-5' })

    await createTransport().startEval('ws1', ['OMNI-1'], { retro: true, withRca: true, rubric: true })

    expect(JSON.parse(String((fetchMock.mock.calls[0]![1] as RequestInit).body))).toEqual({
      keys: ['OMNI-1'],
      retro: true,
      withRca: true,
      rubric: true,
    })
  })

  it('surfaces the API error envelope as an Error', async () => {
    mockFetch({ error: { code: 'unsupported', message: 'no tracker configured' } }, { status: 501 })

    await expect(createTransport().queue('ws1')).rejects.toThrow(
      'unsupported: no tracker configured',
    )
  })

  it('deletes a run with DELETE on its own route and resolves on 204', async () => {
    const fetchMock = vi.fn(async (_input: RequestInfo | URL, _init?: RequestInit) =>
      new Response(null, { status: 204 }),
    )
    vi.stubGlobal('fetch', fetchMock)

    await createTransport().deleteRun('ws1', 'r/1')

    expect(fetchMock.mock.calls[0]![0]).toBe('/api/workspaces/ws1/runs/r%2F1')
    expect((fetchMock.mock.calls[0]![1] as RequestInit).method).toBe('DELETE')
  })

  it('reports a refused delete with the route\'s reason', async () => {
    mockFetch({ error: { code: 'conflict', message: 'run is live' } }, { status: 409 })
    await expect(createTransport().deleteRun('ws1', 'r1')).rejects.toThrow('conflict: run is live')
  })

  it('searches with the query encoded', async () => {
    const fetchMock = mockFetch([
      { runId: 'r1', key: 'OMNI-1', kind: 'triage', status: 'completed', source: 'note', path: '/n.md', excerpt: 'times out' },
    ])

    const got = await createTransport().search('ws1', 'times out & more')

    expect(fetchMock.mock.calls[0]![0]).toBe('/api/workspaces/ws1/search?q=times+out+%26+more')
    expect(got).toHaveLength(1)
    expect(got[0]!.source).toBe('note')
  })

  // --- the review, steer and MCP routes, in the shapes internal/httpapi reads ---

  it('posts a steer with its text and reads the job and run back', async () => {
    const fetchMock = mockFetch({ jobId: 'job-9', runId: 'r1' })

    const got = await createTransport().steer('ws1', 'r1', 'also check the export worker')

    expect(fetchMock.mock.calls[0]![0]).toBe('/api/workspaces/ws1/runs/r1/steer')
    const init = fetchMock.mock.calls[0]![1] as RequestInit
    expect(init.method).toBe('POST')
    expect(JSON.parse(String(init.body))).toEqual({ text: 'also check the export worker' })
    expect(got).toEqual({ jobId: 'job-9', runId: 'r1' })
  })

  it('reads a run diff from its own route', async () => {
    const d: RunDiff = {
      base: 'main',
      head: 'sirdar/OMNI-1-fix',
      branch: 'sirdar/OMNI-1-fix',
      worktree: '/wt',
      worktreePresent: true,
      pushed: false,
      files: [],
      patch: '',
      etag: 'e1',
    }
    const fetchMock = mockFetch(d)

    const got = await createTransport().runDiff('ws1', 'r/1')

    expect(fetchMock.mock.calls[0]![0]).toBe('/api/workspaces/ws1/runs/r%2F1/diff')
    expect(got).toEqual(d)
  })

  it('drops a hunk by path, index and the etag it was read under', async () => {
    const fetchMock = mockFetch({ etag: 'e2', files: [], patch: '' })

    await createTransport().dropHunk('ws1', 'r1', { path: 'a/b.go', hunk: 2, etag: 'e1' })

    expect(fetchMock.mock.calls[0]![0]).toBe('/api/workspaces/ws1/runs/r1/diff/drop')
    const init = fetchMock.mock.calls[0]![1] as RequestInit
    expect(init.method).toBe('POST')
    expect(JSON.parse(String(init.body))).toEqual({ path: 'a/b.go', hunk: 2, etag: 'e1' })
  })

  it('lists MCP servers, connecting only when asked', async () => {
    const fetchMock = mockFetch({ servers: [], warnings: [], workspaceOnly: false, permissions: [] })
    const t = createTransport()

    await t.mcpServers('ws1')
    await t.mcpServers('ws1', true)
    await t.mcpTools('ws1', 'file system')

    expect(fetchMock.mock.calls.map((c) => c[0])).toEqual([
      '/api/workspaces/ws1/mcp',
      '/api/workspaces/ws1/mcp?connect=1',
      '/api/workspaces/ws1/mcp/file%20system/tools',
    ])
  })

  it('calls a tool with its arguments as a JSON object', async () => {
    const fetchMock = mockFetch({ server: 'fs', tool: 'read_file', verdict: 'allowed', reason: '', tookMs: 1 })

    await createTransport().mcpCall('ws1', 'fs', 'read_file', { path: 'README.md' })

    expect(fetchMock.mock.calls[0]![0]).toBe('/api/workspaces/ws1/mcp/fs/call')
    const init = fetchMock.mock.calls[0]![1] as RequestInit
    expect(init.method).toBe('POST')
    expect(JSON.parse(String(init.body))).toEqual({ tool: 'read_file', args: { path: 'README.md' } })
  })

  it('sends an empty object when a tool takes no arguments', async () => {
    const fetchMock = mockFetch({ server: 'fs', tool: 'list', verdict: 'allowed', reason: '', tookMs: 1 })
    await createTransport().mcpCall('ws1', 'fs', 'list')
    expect(JSON.parse(String((fetchMock.mock.calls[0]![1] as RequestInit).body))).toEqual({
      tool: 'list',
      args: {},
    })
  })

  // The route answers 403 with the call result itself when the workspace
  // would refuse the tool. That is an answer the tool tester shows, not an
  // error, so the transport resolves with it.
  it('resolves a denied tool call with its verdict rather than rejecting', async () => {
    const denied: MCPCallResult = {
      server: 'fs',
      tool: 'write_file',
      verdict: 'denied',
      reason: 'the tool name reads as a write',
      tookMs: 0,
    }
    mockFetch(denied, { status: 403 })

    await expect(createTransport().mcpCall('ws1', 'fs', 'write_file', {})).resolves.toEqual(denied)
  })

  it('still rejects a 403 that is not a verdict', async () => {
    mockFetch({ error: { code: 'forbidden', message: 'not on loopback' } }, { status: 403 })
    await expect(createTransport().mcpCall('ws1', 'fs', 'x', {})).rejects.toThrow(
      'forbidden: not on loopback',
    )
  })
})

/*
 * The Wails transport is the same surface over `window.go.main.Bridge`. The
 * bound methods take positional arguments and answer Go values, where a nil
 * slice arrives as null; these cases pin both halves for the six new methods,
 * against a stub bridge.
 */
/**
 * The event stream, against a stand-in for the browser's EventSource: the
 * frames the service names arrive under their kind, and the browser's own
 * open and error signals reach the handler as the stream's state.
 */
describe('http transport events', () => {
  class FakeEventSource {
    static last: FakeEventSource | null = null
    listeners = new Map<string, EventListener[]>()
    closed = false
    constructor(public url: string) {
      FakeEventSource.last = this
    }
    addEventListener(kind: string, listener: EventListener) {
      this.listeners.set(kind, [...(this.listeners.get(kind) ?? []), listener])
    }
    removeEventListener(kind: string, listener: EventListener) {
      this.listeners.set(kind, (this.listeners.get(kind) ?? []).filter((l) => l !== listener))
    }
    close() {
      this.closed = true
    }
    fire(kind: string, data?: string) {
      for (const listener of this.listeners.get(kind) ?? []) listener({ data } as MessageEvent)
    }
  }

  afterEach(() => {
    vi.useRealTimers()
  })

  it('reports the stream lost and open again, and stops listening once unsubscribed', () => {
    vi.useFakeTimers()
    vi.stubGlobal('EventSource', FakeEventSource)
    const handler = vi.fn()
    const off = createTransport().subscribe(handler)
    const source = FakeEventSource.last as FakeEventSource
    expect(source.url).toBe('/api/events')

    source.fire('error')
    source.fire('open')
    source.fire('run.updated', JSON.stringify({ workspaceId: 'ws1', run: sample[0] }))
    // A burst is one task: nothing until the window closes, then all of it in order.
    expect(handler).not.toHaveBeenCalled()
    vi.advanceTimersByTime(FLUSH_MS)
    expect(handler.mock.calls.map(([e]) => e.kind)).toEqual(['live', 'live', 'run.updated'])
    expect(handler.mock.calls[0][0]).toEqual({ kind: 'live', state: 'lost' })
    expect(handler.mock.calls[1][0]).toEqual({ kind: 'live', state: 'open' })

    // Unsubscribing hands over what is still queued and closes the source.
    source.fire('run.updated', JSON.stringify({ workspaceId: 'ws1', run: sample[0] }))
    off()
    expect(handler).toHaveBeenCalledTimes(4)
    source.fire('error')
    vi.advanceTimersByTime(FLUSH_MS)
    expect(handler).toHaveBeenCalledTimes(4)
    expect(source.closed).toBe(true)
  })
})

describe('wails transport', () => {
  function stubBridge(methods: Record<string, (...args: unknown[]) => unknown>) {
    const bound: Record<string, ReturnType<typeof vi.fn>> = {}
    for (const [name, impl] of Object.entries(methods)) bound[name] = vi.fn(impl)
    ;(window as unknown as { go: unknown }).go = { main: { Bridge: bound } }
    return bound
  }

  afterEach(() => {
    delete (window as unknown as { go?: unknown }).go
  })

  it('steers through Steer and says the run back', async () => {
    const bridge = stubBridge({ Steer: async () => 'job-7' })
    await expect(createWailsTransport().steer('ws1', 'r1', 'go on')).resolves.toEqual({
      jobId: 'job-7',
      runId: 'r1',
    })
    expect(bridge.Steer).toHaveBeenCalledWith('ws1', 'r1', 'go on')
  })

  it('deletes, searches and reveals a run directory through their bound methods', async () => {
    const bridge = stubBridge({
      DeleteRun: async () => undefined,
      Search: async () => null,
      OpenRunDir: async () => undefined,
    })
    const t = createWailsTransport()

    await t.deleteRun('ws1', 'r1')
    expect(bridge.DeleteRun).toHaveBeenCalledWith('ws1', 'r1')
    await expect(t.search('ws1', 'export')).resolves.toEqual([])
    expect(bridge.Search).toHaveBeenCalledWith('ws1', 'export')
    await t.openRunDir!('ws1', 'r1')
    expect(bridge.OpenRunDir).toHaveBeenCalledWith('ws1', 'r1')
  })

  it('reads the diff and drops a hunk with positional arguments, listing null files as none', async () => {
    const bridge = stubBridge({
      RunDiff: async () => ({ etag: 'e1', files: null, patch: '' }),
      DropHunk: async () => ({ etag: 'e2', files: null, patch: '' }),
    })
    const t = createWailsTransport()

    const before = await t.runDiff('ws1', 'r1')
    expect(before.files).toEqual([])
    expect(bridge.RunDiff).toHaveBeenCalledWith('ws1', 'r1')

    const after = await t.dropHunk('ws1', 'r1', { path: 'a.go', hunk: 0, etag: 'e1' })
    expect(after.etag).toBe('e2')
    expect(bridge.DropHunk).toHaveBeenCalledWith('ws1', 'r1', 'a.go', 0, 'e1')
  })

  it('lists servers and tools, filling nil slices in', async () => {
    const bridge = stubBridge({
      MCPServers: async () => ({ servers: null, warnings: null, workspaceOnly: true, permissions: null }),
      MCPTools: async () => ({ server: 'fs', tools: null, tookMs: 3, permissions: null }),
    })
    const t = createWailsTransport()

    await expect(t.mcpServers('ws1')).resolves.toEqual({
      servers: [],
      warnings: [],
      workspaceOnly: true,
      permissions: [],
    })
    expect(bridge.MCPServers).toHaveBeenCalledWith('ws1', false)
    await t.mcpServers('ws1', true)
    expect(bridge.MCPServers).toHaveBeenLastCalledWith('ws1', true)

    await expect(t.mcpTools('ws1', 'fs')).resolves.toEqual({
      server: 'fs',
      tools: [],
      tookMs: 3,
      permissions: [],
    })
    expect(bridge.MCPTools).toHaveBeenCalledWith('ws1', 'fs')
  })

  it('calls a tool with an object of arguments, and an empty one when there are none', async () => {
    const bridge = stubBridge({
      MCPCall: async () => ({ server: 'fs', tool: 'read_file', verdict: 'allowed', reason: '', tookMs: 1 }),
    })
    const t = createWailsTransport()

    await t.mcpCall('ws1', 'fs', 'read_file', { path: 'x' })
    expect(bridge.MCPCall).toHaveBeenCalledWith('ws1', 'fs', 'read_file', { path: 'x' })

    await t.mcpCall('ws1', 'fs', 'list')
    expect(bridge.MCPCall).toHaveBeenLastCalledWith('ws1', 'fs', 'list', {})
  })

  // Opening the config file is a desktop-only ability: the browser build
  // has no `openConfig` at all, which is what Settings keys its fallback on.
  it('opens the config file through OpenConfig, and only on the desktop', async () => {
    const bridge = stubBridge({ OpenConfig: async () => undefined })
    await expect(createWailsTransport().openConfig?.('ws1')).resolves.toBeUndefined()
    expect(bridge.OpenConfig).toHaveBeenCalledWith('ws1')

    delete (window as unknown as { go?: unknown }).go
    expect(createTransport().openConfig).toBeUndefined()
  })
})
