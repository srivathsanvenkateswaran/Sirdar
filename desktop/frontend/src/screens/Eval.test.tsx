import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import type { AppEvent, EvalReport, GoldenEntry, Transport } from '../api/types'
import Eval, { pct } from './Eval'

const GOLDEN: GoldenEntry[] = [
  {
    key: 'OMNI-2510',
    dir: '/golden/OMNI-2510',
    bundleDir: '/golden/OMNI-2510/bundle',
    assertions: 3,
    hasExpectedNote: true,
  },
  {
    key: 'OMNI-2511',
    dir: '/golden/OMNI-2511',
    bundleDir: '/golden/OMNI-2511/bundle',
    assertions: 1,
    hasExpectedNote: false,
  },
]

const REPORT: EvalReport = {
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
      turns: 7,
      costUsd: 0.42,
      minutes: 3.5,
      schemaValid: true,
      checks: [{ key: 'classification', kind: 'equals', pass: true }],
      passed: 3,
      total: 3,
      overlap: {
        refs: { matched: 4, total: 5, score: 0.8 },
        headings: { matched: 2, total: 2, score: 1 },
      },
    },
    {
      key: 'OMNI-2511',
      runId: 'r2',
      state: 'failed',
      reason: 'provider exited 1',
      turns: 2,
      costUsd: 0.03,
      minutes: 0.4,
      schemaValid: false,
      checks: [{ key: 'severity', kind: 'equals', pass: false, detail: 'wanted P1, got P3' }],
      passed: 0,
      total: 1,
    },
  ],
}

function fakeTransport(over: Partial<Transport> = {}) {
  let handler: ((e: AppEvent) => void) | undefined
  const transport = {
    golden: vi.fn(async () => GOLDEN),
    evalReports: vi.fn(async () => [REPORT]),
    subscribe: vi.fn((h: (e: AppEvent) => void) => {
      handler = h
      return () => {}
    }),
    ...over,
  } as unknown as Transport
  return { transport, emit: (e: AppEvent) => act(() => handler?.(e)) }
}

function mount(over: Partial<Transport> = {}, defaultProvider?: string) {
  const onStartEval = vi.fn()
  const { transport, emit } = fakeTransport(over)
  render(
    <Eval
      transport={transport}
      workspaceId="ws1"
      defaultProvider={defaultProvider}
      onStartEval={onStartEval}
    />,
  )
  return { transport, emit, onStartEval }
}

describe('pct', () => {
  it('renders a fraction the way the CLI table does, and a dash for nothing', () => {
    expect(pct({ matched: 4, total: 5, score: 0.8 })).toBe('4/5 80%')
    expect(pct({ matched: 0, total: 0, score: 0 })).toBe('—')
    expect(pct(undefined)).toBe('—')
  })
})

describe('Eval', () => {
  it('lists the golden set with how much a human has written about each key', async () => {
    mount()
    await screen.findByRole('checkbox', { name: 'OMNI-2510' })
    expect(screen.getByText('3 assertions')).toBeInTheDocument()
    expect(screen.getByText('expected.md')).toBeInTheDocument()
    expect(screen.getByText('1 assertion')).toBeInTheDocument()
    expect(screen.getByText('no expected.md')).toBeInTheDocument()
  })

  it('draws the score table of the newest report', async () => {
    mount()
    const row = (await screen.findByRole('row', { name: /OMNI-2510/ })) as HTMLElement
    const cells = within(row).getAllByRole('cell')
    expect(cells.map((c) => c.textContent)).toEqual([
      'completed',
      '7',
      '$0.42',
      '3.5',
      'yes',
      '3/3',
      '4/5 80%',
      '2/2 100%',
    ])
    expect(screen.getByText(/\/work\/\.sirdar\/eval\/20260910-1200\.json/)).toBeInTheDocument()
  })

  it('says why a key failed and which assertion did not hold', async () => {
    mount()
    await screen.findByText('provider exited 1')
    expect(screen.getByText(/wanted P1, got P3/)).toBeInTheDocument()
  })

  it('runs the whole set when nothing is picked', async () => {
    const { onStartEval } = mount()
    fireEvent.click(await screen.findByRole('button', { name: 'Run eval on the whole set' }))
    await waitFor(() => expect(onStartEval).toHaveBeenCalledWith(undefined, {
      provider: undefined,
      model: undefined,
    }))
  })

  it('runs only the keys that were ticked, with the one-off provider and model', async () => {
    const { onStartEval } = mount({}, 'claude')
    await screen.findByRole('checkbox', { name: 'OMNI-2510' })

    fireEvent.click(screen.getByRole('checkbox', { name: 'OMNI-2511' }))
    fireEvent.change(screen.getByLabelText('Provider'), { target: { value: 'qwen' } })
    fireEvent.change(screen.getByLabelText('Model'), { target: { value: ' qwen3-coder ' } })

    const start = screen.getByRole('button', { name: 'Run eval on 1 key' })
    fireEvent.click(start)
    await waitFor(() =>
      expect(onStartEval).toHaveBeenCalledWith(['OMNI-2511'], {
        provider: 'qwen',
        model: 'qwen3-coder',
      }),
    )
    expect(screen.getByRole('option', { name: 'Workspace default (claude)' })).toBeInTheDocument()
  })

  it('reloads the table when a job finishes, because that is when the report is written', async () => {
    const { transport, emit } = mount()
    await screen.findByRole('checkbox', { name: 'OMNI-2510' })
    expect(transport.evalReports).toHaveBeenCalledTimes(1)

    emit({ kind: 'job.finished', jobId: 'job-1', workspaceId: 'ws1', outcomes: [] })
    await waitFor(() => expect(transport.evalReports).toHaveBeenCalledTimes(2))
  })

  it('points at the two ways to add a bundle when the set is empty, and offers no run', async () => {
    mount({ golden: vi.fn(async () => [] as GoldenEntry[]) })
    await screen.findByText(/No golden bundles yet/)
    expect(screen.getByText('sirdar golden add KEY')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Run eval on the whole set' })).toBeDisabled()
  })

  it('says where reports are written when the workspace has none', async () => {
    mount({ evalReports: vi.fn(async () => [] as EvalReport[]) })
    await screen.findByText(/No eval has been recorded/)
    expect(screen.getByText('.sirdar/eval')).toBeInTheDocument()
  })

  it('shows the reason the golden set could not be read', async () => {
    mount({ golden: vi.fn(async () => Promise.reject(new Error('no such workspace'))) })
    await screen.findByText('no such workspace')
  })
})
