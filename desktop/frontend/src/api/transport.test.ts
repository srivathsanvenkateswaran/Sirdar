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

  it('surfaces the API error envelope as an Error', async () => {
    mockFetch({ error: { code: 'unsupported', message: 'no tracker configured' } }, { status: 501 })

    await expect(createTransport().queue('ws1')).rejects.toThrow(
      'unsupported: no tracker configured',
    )
  })
})
