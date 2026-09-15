import { afterEach, describe, expect, it, vi } from 'vitest'
import { createTransport } from './transport'
import type { RunSummary } from './types'

const sample: RunSummary[] = [
  {
    runId: '20260910-120000-OMNI-1',
    key: 'OMNI-1',
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
    await t.configSummary('ws1')

    expect(fetchMock.mock.calls.map((c) => c[0])).toEqual([
      '/api/workspaces/ws1/golden',
      '/api/workspaces/ws1/eval',
      '/api/workspaces/ws1/config/summary',
    ])
  })

  it('surfaces the API error envelope as an Error', async () => {
    mockFetch({ error: { code: 'unsupported', message: 'no tracker configured' } }, { status: 501 })

    await expect(createTransport().queue('ws1')).rejects.toThrow(
      'unsupported: no tracker configured',
    )
  })
})
