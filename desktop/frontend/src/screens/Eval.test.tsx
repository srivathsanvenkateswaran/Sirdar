import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import type { AppEvent, EvalReport, GoldenEntry, RetroReport, Transport } from '../api/types'
import { PrimaryActionProvider, usePrimaryAction } from '../components/shell/primaryAction'
import Eval, { jaccard, pct } from './Eval'

/**
 * The sidebar footer, as far as these cases are concerned.
 *
 * Eval's one commit action is published to the shell rather than drawn on the
 * screen, because `03-desktop-app.md` section 8 puts every screen's filled
 * button in the sidebar footer. The screen is still what decides what it says
 * and what it does, so this stands in for the footer and draws it.
 */
/**
 * The published action, once the footer has caught up.
 *
 * Publishing is an effect, so the footer's button lands one commit after the
 * screen that published it — which is true in the app as well, and is why
 * every case that presses it waits for it rather than reading it the moment
 * the golden set appears.
 */
async function primary(name: string | RegExp): Promise<HTMLElement> {
  const button = await screen.findByRole('button', { name })
  await waitFor(() => expect(button).not.toBeDisabled())
  return button
}

function PrimaryActionSlot(): JSX.Element | null {
  const action = usePrimaryAction()
  if (!action) return null
  return (
    <button type="button" disabled={action.disabled} onClick={action.onRun}>
      {action.label}
    </button>
  )
}

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

const RETRO: RetroReport = {
  path: '/work/.sirdar/eval/20260915T090000Z-retro.json',
  at: '2026-09-15T09:00:00Z',
  provider: 'claude',
  model: 'sonnet',
  goldenDir: '/golden',
  withRca: false,
  rubric: true,
  results: [
    {
      key: 'OMNI-2510',
      baseCommit: 'abc123',
      costUsd: 3.75,
      triage: { runId: 't1', state: 'completed', costUsd: 1.25 },
      fix: { runId: 'f1', state: 'completed', costUsd: 2.5, commit: 'deadbee' },
      triageScore: {
        classification: 'code',
        confidence: 'high',
        codeRefsPathOverlap: { matched: 1, total: 2, score: 0.5 },
        prFilesHit: { matched: 2, total: 3, score: 2 / 3 },
        missedFiles: ['internal/export/pool.go'],
      },
      fixScore: {
        filesJaccard: { intersection: 1, union: 2, score: 0.5 },
        hunkOverlap: { matched: 1, total: 2, score: 0.5 },
        linesAdded: { agent: 2, pr: 4 },
        linesRemoved: { agent: 1, pr: 3 },
        buildPassed: true,
      },
      rubric: { sameRootCause: true, sameFix: false, verdict: 'partial', reasoning: 'half the change' },
    },
    {
      key: 'OMNI-2511',
      baseCommit: 'def456',
      costUsd: 0.4,
      reason: 'triage: failed — provider exited 1',
      triage: { runId: 't2', state: 'failed', costUsd: 0.4 },
    },
  ],
}

function fakeTransport(over: Partial<Transport> = {}) {
  let handler: ((e: AppEvent) => void) | undefined
  const transport = {
    golden: vi.fn(async () => GOLDEN),
    evalReports: vi.fn(async () => [REPORT]),
    latestRetro: vi.fn(async () => RETRO),
    subscribe: vi.fn((h: (e: AppEvent) => void) => {
      handler = h
      return () => {}
    }),
    ...over,
  } as unknown as Transport
  return { transport, emit: (e: AppEvent) => act(() => handler?.(e)) }
}

function mount(
  over: Partial<Transport> = {},
  defaultProvider?: string,
  extra: { jobs?: { jobId: string; label: string }[]; onCancelJob?: (id: string) => Promise<void> } = {},
) {
  const onStartEval = vi.fn()
  const { transport, emit } = fakeTransport(over)
  render(
    <PrimaryActionProvider>
      <Eval
        transport={transport}
        workspaceId="ws1"
        defaultProvider={defaultProvider}
        jobs={extra.jobs}
        onStartEval={onStartEval}
        onCancelJob={extra.onCancelJob}
      />
      <PrimaryActionSlot />
    </PrimaryActionProvider>,
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

describe('jaccard', () => {
  it('renders the file overlap the way the CLI table does', () => {
    expect(jaccard({ intersection: 1, union: 2, score: 0.5 })).toBe('1/2 50%')
  })

  // Two diffs that changed nothing between them are two missing diffs, not
  // a perfect match.
  it('renders an empty union as a dash, not as 100%', () => {
    expect(jaccard({ intersection: 0, union: 0, score: 0 })).toBe('—')
    expect(jaccard(undefined)).toBe('—')
  })
})

describe('Eval retro section', () => {
  it('draws the last retro report against the change a human merged', async () => {
    mount()
    const table = await screen.findByRole('table', {
      name: 'Each key against the change a human merged',
    })
    const row = within(table).getByRole('row', { name: /OMNI-2510/ })
    const cells = within(row).getAllByRole('cell')
    expect(cells.map((c) => c.textContent)).toEqual([
      // As above: the Data table draws the key as a cell, not a row header.
      'OMNI-2510',
      'code',
      'high',
      '1/2 50%',
      '2/3 67%',
      '1/2 50%',
      '1/2 50%',
      'yes',
      'partial',
      '$3.75',
    ])
    expect(screen.getByText(/20260915T090000Z-retro\.json/)).toBeInTheDocument()
  })

  it('names the files the note never found and the rubric it was given', async () => {
    mount()
    await screen.findByText('internal/export/pool.go')
    expect(screen.getByText(/rubric partial — half the change/)).toBeInTheDocument()
  })

  it('says why a key has no scores', async () => {
    mount()
    await screen.findByText('triage: failed — provider exited 1')
  })

  it('points at the command when the workspace has run no retro', async () => {
    mount({ latestRetro: vi.fn(async () => null) })
    await screen.findByText(/No retro has been recorded/)
    expect(screen.getByText('sirdar eval --retro')).toBeInTheDocument()
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
    // Scoped to this table: the Retro section below has a row for the
    // same key.
    const table = await screen.findByRole('table', { name: 'Score per key' })
    const row = within(table).getByRole('row', { name: /OMNI-2510/ })
    const cells = within(row).getAllByRole('cell')
    expect(cells.map((c) => c.textContent)).toEqual([
      // The key is a cell of its own now: the Data table draws every column
      // the same way, rather than making the first one a row header.
      'OMNI-2510',
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
    fireEvent.click(await primary('Run eval on the whole set'))
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

    fireEvent.click(await primary('Run eval on 1 key'))
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

  /*
   * An eval over the whole set names no key, so no run ever claims its job and
   * Run detail's Cancel can never reach it. Without a button here, the only
   * way to stop one was to quit the app and let the jobs be cancelled on
   * shutdown — after it had spent a session per bundle.
   */
  it('offers Cancel for a whole-set eval this window started', async () => {
    const onCancelJob = vi.fn(async () => {})
    mount({}, undefined, {
      jobs: [{ jobId: 'job-4', label: 'Eval of the whole golden set' }],
      onCancelJob,
    })
    await screen.findByRole('checkbox', { name: 'OMNI-2510' })

    fireEvent.click(screen.getByRole('button', { name: /Cancel eval of the whole golden set/i }))
    await waitFor(() => expect(onCancelJob).toHaveBeenCalledWith('job-4'))
  })

  it('offers no Cancel when this window has no eval running', async () => {
    mount()
    await screen.findByRole('checkbox', { name: 'OMNI-2510' })
    expect(screen.queryByRole('button', { name: /^Cancel/i })).toBeNull()
  })

  it('shows the reason a cancel was refused', async () => {
    const onCancelJob = vi.fn(async () => {
      throw new Error('no such job')
    })
    mount({}, undefined, { jobs: [{ jobId: 'job-4', label: 'Eval of the whole golden set' }], onCancelJob })
    await screen.findByRole('checkbox', { name: 'OMNI-2510' })

    fireEvent.click(screen.getByRole('button', { name: /Cancel eval/i }))
    expect(await screen.findByText('no such job')).toBeInTheDocument()
  })

  /*
   * The store toasts a failed start and rethrows it; this is the half the
   * reader sees without leaving the screen.
   */
  it('shows the reason an eval did not start beside the button', async () => {
    const { transport } = fakeTransport()
    const onStartEval = vi.fn(async () => {
      throw new Error('the golden set holds no bundles')
    })
    render(
      <PrimaryActionProvider>
        <Eval transport={transport} workspaceId="ws1" onStartEval={onStartEval} />
        <PrimaryActionSlot />
      </PrimaryActionProvider>,
    )
    await screen.findByRole('checkbox', { name: 'OMNI-2510' })

    fireEvent.click(await primary('Run eval on the whole set'))
    expect(await screen.findByText('the golden set holds no bundles')).toBeInTheDocument()
  })
})
