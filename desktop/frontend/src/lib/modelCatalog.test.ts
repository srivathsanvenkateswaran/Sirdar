import { act, renderHook, waitFor } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import type { ModelList } from '../api/types'
import { createFakeTransport } from '../store/fakeTransport'
import { catalogChoices, probeStatus, sourceNote, useModelCatalog } from './modelCatalog'
import { deriveLabel, NOT_VERIFIED } from './models'

const NOW = Date.parse('2026-10-01T12:00:00Z')

const discovered: ModelList = {
  provider: 'claude',
  canProbe: true,
  probeDue: false,
  probedAt: '2026-10-01T09:00:00Z',
  models: [
    { id: 'claude-opus-4-5-20251101', label: 'Opus 4.5', source: 'probe', seenAt: '2026-10-01T09:00:00Z', alias: 'opus' },
    { id: 'claude-opus-5[1m]', label: 'Opus 5 (1M)', source: 'run', seenAt: '2026-09-29T12:00:00Z' },
    { id: 'claude-sonnet-4-5', label: 'Sonnet (team)', source: 'config' },
  ],
}

describe('deriveLabel', () => {
  // The same vectors as app.TestModelLabel on the Go side.
  it.each([
    ['claude-opus-4-5-20251101', 'Opus 4.5'],
    ['claude-sonnet-4-5-20250929', 'Sonnet 4.5'],
    ['claude-haiku-4-5-20251001', 'Haiku 4.5'],
    ['claude-sonnet-4-20250514', 'Sonnet 4'],
    ['claude-3-5-sonnet-20241022', 'Sonnet 3.5'],
    ['claude-opus-5', 'Opus 5'],
    ['claude-fable-5-1', 'Fable 5.1'],
    ['claude-opus-5[1m]', 'Opus 5 (1M)'],
    ['opus', 'Opus'],
    ['sonnet', 'Sonnet'],
    ['gpt-5.6-luna', 'gpt-5.6-luna'],
    ['qwen3-coder', 'qwen3-coder'],
    ['claude-', 'claude-'],
    ['claude-a-b', 'claude-a-b'],
    ['claude-next', 'claude-next'],
    ['claude-opus', 'Opus'],
    ['', ''],
  ])('%s reads as %s', (id, label) => {
    expect(deriveLabel(id)).toBe(label)
  })
})

describe('sourceNote', () => {
  it('says when and where a model was seen', () => {
    expect(sourceNote('probe', '2026-10-01T09:00:00Z', NOW)).toBe('probed today')
    expect(sourceNote('probe', '2026-09-28T12:00:00Z', NOW)).toBe('probed 3d ago')
    expect(sourceNote('run', '2026-09-29T12:00:00Z', NOW)).toBe('seen in a run 2d ago')
    expect(sourceNote('run', undefined, NOW)).toBe('seen in a run')
    expect(sourceNote('config', undefined, NOW)).toBe('pinned in config')
  })
})

describe('probeStatus', () => {
  it('says when the CLI was asked, and says so plainly when it answered nothing', () => {
    expect(probeStatus(discovered, NOW)).toBe('Probed 3h ago')
    expect(probeStatus({ ...discovered, probedAt: undefined }, NOW)).toBe('Not probed yet')
    expect(probeStatus({ ...discovered, canProbe: false }, NOW)).toBe('From runs and config')
    const failed: ModelList = {
      ...discovered,
      models: discovered.models.filter((m) => m.source !== 'probe'),
      probeErrors: ['opus: claude: executable file not found in $PATH'],
    }
    expect(probeStatus(failed, NOW)).toBe('The CLI answered no alias 3h ago')
    // One alias failing among several that resolved is still a probe.
    expect(probeStatus({ ...discovered, probeErrors: ['haiku: no init line'] }, NOW)).toBe('Probed 3h ago')
  })
})

describe('catalogChoices', () => {
  it('gives the discovered models under their sources, CLI default first', () => {
    const rows = catalogChoices('claude', { state: 'ready', list: discovered }, NOW)
    expect(rows.map((r) => [r.group, r.label, r.note ?? ''])).toEqual([
      ['default', 'CLI default', 'Whatever the provider’s CLI is set to'],
      ['probe', 'Opus 4.5', 'probed today'],
      ['run', 'Opus 5 (1M)', 'seen in a run 2d ago'],
      ['config', 'Sonnet (team)', 'pinned in config'],
    ])
  })

  it('falls back to the static names, each marked unverified, when nothing was discovered', () => {
    for (const entry of [undefined, { state: 'loading' as const }, { state: 'ready' as const, list: { ...discovered, models: [] } }]) {
      const rows = catalogChoices('claude', entry, NOW)
      expect(rows[0].id).toBe('')
      expect(rows.slice(1).map((r) => r.label)).toEqual(['Fable 5.1', 'Opus 5', 'Sonnet 5', 'Haiku 4.5'])
      expect(rows.slice(1).every((r) => r.group === 'fallback' && r.note === NOT_VERIFIED)).toBe(true)
    }
    // A provider with no static names is CLI default alone.
    expect(catalogChoices('qwen', undefined, NOW).map((r) => r.id)).toEqual([''])
  })

  it('keeps showing the last list while a reload is out', () => {
    const rows = catalogChoices('claude', { state: 'loading', list: discovered }, NOW)
    expect(rows.map((r) => r.label)).toContain('Opus 4.5')
  })
})

describe('useModelCatalog', () => {
  it('reads the list on load and does not probe a list that is not due', async () => {
    const transport = createFakeTransport({ models: { claude: discovered } })
    const { result } = renderHook(() => useModelCatalog(transport, 'ws1'))
    expect(result.current.entry('claude')).toBeUndefined()
    act(() => result.current.load('claude'))
    await waitFor(() => expect(result.current.entry('claude')?.state).toBe('ready'))
    expect(transport.calls.models).toEqual([{ ws: 'ws1', provider: 'claude' }])
    expect(transport.calls.refreshModels).toEqual([])
  })

  it('probes once when the list says one is due, and never again on its own', async () => {
    const due: ModelList = { provider: 'claude', canProbe: true, probeDue: true, models: [] }
    const transport = createFakeTransport({
      models: { claude: due },
      // A CLI that answers nothing leaves the list due; the hook must not loop.
      refreshed: { claude: due },
    })
    const { result } = renderHook(() => useModelCatalog(transport, 'ws1'))
    act(() => result.current.load('claude'))
    await waitFor(() => expect(transport.calls.refreshModels).toHaveLength(1))
    await waitFor(() => {
      const e = result.current.entry('claude')
      expect(e?.state === 'ready' && !e.probing).toBe(true)
    })
    act(() => result.current.load('claude'))
    await waitFor(() => expect(transport.calls.models).toHaveLength(2))
    expect(transport.calls.refreshModels).toHaveLength(1)

    // Refresh is the person asking, and always probes.
    act(() => result.current.refresh('claude'))
    await waitFor(() => expect(transport.calls.refreshModels).toHaveLength(2))
  })

  it('never probes a provider that cannot be', async () => {
    const transport = createFakeTransport({
      models: { codex: { provider: 'codex', canProbe: false, probeDue: true, models: [] } },
    })
    const { result } = renderHook(() => useModelCatalog(transport, 'ws1'))
    act(() => result.current.load('codex'))
    await waitFor(() => expect(result.current.entry('codex')?.state).toBe('ready'))
    expect(transport.calls.refreshModels).toEqual([])
  })

  it('reports a failed read and keeps what it had', async () => {
    const transport = createFakeTransport({ models: { claude: discovered } })
    const { result } = renderHook(() => useModelCatalog(transport, 'ws1'))
    act(() => result.current.load('claude'))
    await waitFor(() => expect(result.current.entry('claude')?.state).toBe('ready'))
    transport.failModels(new Error('service unavailable'))
    act(() => result.current.load('claude'))
    await waitFor(() => expect(result.current.entry('claude')?.state).toBe('error'))
    const e = result.current.entry('claude')
    expect(e?.state === 'error' && e.message).toMatch(/service unavailable/)
    expect(e?.list).toEqual(discovered)
  })

  it('drops the answers when the workspace changes', async () => {
    const transport = createFakeTransport({ models: { claude: discovered } })
    const { result, rerender } = renderHook(({ ws }) => useModelCatalog(transport, ws), { initialProps: { ws: 'ws1' } })
    act(() => result.current.load('claude'))
    await waitFor(() => expect(result.current.entry('claude')?.state).toBe('ready'))
    rerender({ ws: 'ws2' })
    expect(result.current.entry('claude')).toBeUndefined()
  })
})
