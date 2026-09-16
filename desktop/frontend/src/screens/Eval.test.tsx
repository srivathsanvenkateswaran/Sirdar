import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import type { AppEvent, EvalReport, GoldenEntry, Quota, RetroReport, Transport } from '../api/types'
import { PrimaryActionProvider, usePrimaryAction } from '../components/shell/primaryAction'
import Eval, { estimate, jaccard, latestOf, pct, retroState, when } from './Eval'

/**
 * What the sidebar sees. Eval draws Run suite in its own page head; what it
 * publishes to the shell is only there so New session steps down while the
 * screen is up. This slot records that the publication happened.
 */
function PublishedProbe(): JSX.Element | null {
  const action = usePrimaryAction()
  if (!action) return null
  return <span data-testid="published">{action.label}</span>
}

const GOLDEN: GoldenEntry[] = [
  {
    key: 'OMNI-2510',
    dir: '/golden/OMNI-2510',
    bundleDir: '/golden/OMNI-2510/bundle',
    assertions: 3,
    hasExpectedNote: true,
    hasRetro: true,
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
      triage: { runId: 't1', state: 'completed', costUsd: 1.25, turns: 12, minutes: 2 },
      fix: { runId: 'f1', state: 'completed', costUsd: 2.5, commit: 'deadbee', turns: 14, minutes: 3.5 },
      triageScore: {
        classification: 'code',
        confidence: 'high',
        codeRefsPathOverlap: { matched: 1, total: 2, score: 0.5 },
        prFilesHit: { matched: 2, total: 3, score: 2 / 3 },
        missedFiles: ['internal/export/pool.go'],
      },
      fixScore: {
        filesJaccard: { intersection: 1, union: 3, score: 1 / 3 },
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

const QUOTA: Quota[] = [
  {
    provider: 'claude',
    observedAt: '2026-09-15T09:00:00Z',
    fiveHour: { utilization: 0.4, resetsAt: '2026-09-15T13:00:00Z' },
  },
]

function fakeTransport(over: Partial<Transport> = {}) {
  let handler: ((e: AppEvent) => void) | undefined
  const transport = {
    golden: vi.fn(async () => GOLDEN),
    evalReports: vi.fn(async () => [REPORT]),
    latestRetro: vi.fn(async () => RETRO),
    addGolden: vi.fn(async (_ws: string, o: { key?: string }) => ({
      key: o.key ?? 'OMNI-1',
      dir: '/golden/x',
      bundleDir: '/golden/x/bundle',
      assertions: 0,
      hasExpectedNote: false,
    })),
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
  extra: {
    defaultProvider?: string
    defaultModel?: string
    quota?: Quota[]
    jobs?: { jobId: string; label: string }[]
    onCancelJob?: (id: string) => Promise<void>
    onStartEval?: (keys?: string[], opts?: unknown) => Promise<string | void>
  } = {},
) {
  const onStartEval = extra.onStartEval ?? vi.fn(async () => {})
  const { transport, emit } = fakeTransport(over)
  render(
    <PrimaryActionProvider>
      <Eval
        transport={transport}
        workspaceId="ws1"
        defaultProvider={extra.defaultProvider}
        defaultModel={extra.defaultModel}
        quota={extra.quota}
        jobs={extra.jobs}
        onStartEval={onStartEval}
        onCancelJob={extra.onCancelJob}
      />
      <PublishedProbe />
    </PrimaryActionProvider>,
  )
  return { transport, emit, onStartEval }
}

const runSuite = () => screen.getByRole('button', { name: 'Run suite' })

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

describe('estimate', () => {
  it('counts five minutes a key and names the window only when a quota exists', () => {
    expect(estimate(5, undefined, false)).toBe('5 selected · about 25 min')
    expect(estimate(5, QUOTA[0], false)).toBe('5 selected · about 25 min · within the 5h window')
    expect(
      estimate(2, { ...QUOTA[0], fiveHour: { utilization: 1, resetsAt: '' } }, false),
    ).toBe('2 selected · about 10 min · the 5h window is used up')
  })

  it('says so when nothing is picked or a suite is already running', () => {
    expect(estimate(0, QUOTA[0], false)).toBe('Nothing selected')
    expect(estimate(3, QUOTA[0], true)).toBe('A suite is running · 3 selected')
  })
})

describe('when', () => {
  it('renders the day, month and time, and the raw stamp when it does not parse', () => {
    expect(when('2026-09-15T09:33:00')).toBe('15 Sep 09:33')
    expect(when('not a date')).toBe('not a date')
  })
})

describe('latestOf', () => {
  it('picks the newer of the plain and retro reports, whichever shape it is', () => {
    expect(latestOf([REPORT], RETRO)?.kind).toBe('retro')
    expect(latestOf([REPORT], null)?.kind).toBe('plain')
    expect(latestOf([], RETRO)?.kind).toBe('retro')
    expect(latestOf([], null)).toBeNull()
    expect(latestOf([{ ...REPORT, at: '2026-09-16T00:00:00Z' }], RETRO)?.kind).toBe('plain')
  })
})

describe('retroState', () => {
  it('is failed when any stage failed, done when every stage completed', () => {
    expect(retroState(RETRO.results[0])).toBe('completed')
    expect(retroState(RETRO.results[1])).toBe('failed')
    expect(retroState({ key: 'X', costUsd: 0, reason: 'no retro.json' })).toBe('failed')
    expect(retroState({ key: 'X', costUsd: 0 })).toBe('queued')
  })
})

describe('Eval golden set', () => {
  it('lists every key with what a human wrote about it and whether it replays as a retro', async () => {
    mount()
    await screen.findByRole('checkbox', { name: 'OMNI-2510' })
    expect(screen.getByText(/3 assertions · expected\.md/)).toBeInTheDocument()
    expect(screen.getByText(/1 assertion · no expected\.md/)).toBeInTheDocument()
    expect(screen.getByText('retro')).toBeInTheDocument()
    expect(screen.getByText('plain')).toBeInTheDocument()
  })

  it('says how to add a bundle when the set is empty, and offers no run', async () => {
    mount({ golden: vi.fn(async () => [] as GoldenEntry[]) })
    await screen.findByText(/No golden bundles yet/)
    expect(screen.getByText('sirdar golden add KEY')).toBeInTheDocument()
    expect(runSuite()).toBeDisabled()
    expect(runSuite()).toHaveAttribute('title', 'Select at least one key')
  })

  it('shows the reason the golden set could not be read', async () => {
    mount({ golden: vi.fn(async () => Promise.reject(new Error('no such workspace'))) })
    await screen.findByText('no such workspace')
  })

  it('says it is loading before the set arrives', () => {
    mount({ golden: vi.fn(() => new Promise<GoldenEntry[]>(() => {})) })
    expect(screen.getByText('Loading the golden set…')).toBeInTheDocument()
  })
})

describe('Eval run suite', () => {
  it('is disabled with a reason until a key is picked, then counts the estimate', async () => {
    mount({}, { quota: QUOTA, defaultProvider: 'claude' })
    await screen.findByRole('checkbox', { name: 'OMNI-2510' })
    expect(runSuite()).toBeDisabled()
    expect(runSuite()).toHaveAttribute('title', 'Select at least one key')
    expect(screen.getByText('Nothing selected')).toBeInTheDocument()

    fireEvent.click(screen.getByRole('checkbox', { name: 'OMNI-2510' }))
    expect(runSuite()).not.toBeDisabled()
    expect(screen.getByText('1 selected · about 5 min · within the 5h window')).toBeInTheDocument()

    fireEvent.click(screen.getByRole('checkbox', { name: 'OMNI-2511' }))
    expect(screen.getByText('2 selected · about 10 min · within the 5h window')).toBeInTheDocument()
  })

  it('leaves the window clause out when no quota has been read', async () => {
    mount()
    fireEvent.click(await screen.findByRole('checkbox', { name: 'OMNI-2510' }))
    expect(screen.getByText('1 selected · about 5 min')).toBeInTheDocument()
  })

  it('publishes its action so the sidebar steps down while the screen is up', async () => {
    mount()
    await screen.findByRole('checkbox', { name: 'OMNI-2510' })
    await waitFor(() => expect(screen.getByTestId('published')).toHaveTextContent('Run suite'))
  })

  it('runs the picked retro keys with the options, as a retro', async () => {
    const { onStartEval } = mount()
    fireEvent.click(await screen.findByRole('checkbox', { name: 'OMNI-2510' }))
    // The rubric is on by default; the RCA step is off. Flip both.
    fireEvent.click(screen.getByRole('switch', { name: 'Score with a rubric' }))
    fireEvent.click(screen.getByRole('switch', { name: 'Include the RCA step' }))

    fireEvent.click(runSuite())
    await waitFor(() =>
      expect(onStartEval).toHaveBeenCalledWith(['OMNI-2510'], {
        provider: undefined,
        model: undefined,
        retro: true,
        rubric: false,
        withRca: true,
      }),
    )
  })

  it('runs a plain replay when a picked key has no retro, and the retro options stay out', async () => {
    const { onStartEval } = mount()
    await screen.findByRole('checkbox', { name: 'OMNI-2510' })
    fireEvent.click(screen.getByRole('checkbox', { name: 'OMNI-2510' }))
    fireEvent.click(screen.getByRole('checkbox', { name: 'OMNI-2511' }))
    expect(screen.getByRole('switch', { name: 'Score with a rubric' })).toBeDisabled()

    fireEvent.click(runSuite())
    await waitFor(() =>
      expect(onStartEval).toHaveBeenCalledWith(['OMNI-2510', 'OMNI-2511'], {
        provider: undefined,
        model: undefined,
        retro: false,
      }),
    )
  })

  it('passes the one-off provider and model picked through Change', async () => {
    const { onStartEval } = mount({}, { defaultProvider: 'claude', defaultModel: 'sonnet' })
    await screen.findByRole('checkbox', { name: 'OMNI-2510' })
    expect(screen.getByText('claude · sonnet')).toBeInTheDocument()

    // Change opens the same picker New session has, not a dialog of fields.
    const change = screen.getByRole('button', { name: 'Change' })
    expect(change).toHaveAttribute('aria-haspopup', 'dialog')
    fireEvent.click(change)
    const picker = screen.getByRole('dialog', { name: 'Provider and model' })
    expect(within(picker).getByRole('tab', { name: 'Claude, workspace default' })).toBeInTheDocument()
    fireEvent.click(within(picker).getByRole('tab', { name: 'Qwen' }))
    // The search field is the free-text entry: an id no list has is typed
    // there and taken with Enter.
    const search = within(picker).getByRole('searchbox', { name: 'Search models' })
    fireEvent.change(search, { target: { value: ' qwen3-coder ' } })
    fireEvent.keyDown(search, { key: 'Enter' })
    expect(screen.queryByRole('dialog')).toBeNull()
    expect(change).toHaveFocus()
    expect(screen.getByText('qwen · qwen3-coder')).toBeInTheDocument()

    fireEvent.click(screen.getByRole('checkbox', { name: 'OMNI-2510' }))
    fireEvent.click(runSuite())
    await waitFor(() =>
      expect(onStartEval).toHaveBeenCalledWith(['OMNI-2510'], {
        provider: 'qwen',
        model: 'qwen3-coder',
        retro: true,
        rubric: true,
        withRca: false,
      }),
    )
  })

  it('reads CLI default on the row when nothing names a model, and Escape keeps a choice', async () => {
    mount({}, { defaultProvider: 'claude' })
    await screen.findByRole('checkbox', { name: 'OMNI-2510' })
    expect(screen.getByText('claude · CLI default')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Change' }))
    const picker = screen.getByRole('dialog', { name: 'Provider and model' })
    fireEvent.click(within(picker).getByRole('tab', { name: 'Codex' }))
    fireEvent.keyDown(picker, { key: 'Escape' })
    expect(screen.queryByRole('dialog')).toBeNull()
    expect(screen.getByText('codex · CLI default')).toBeInTheDocument()
  })

  it('is disabled with a reason while a suite this window started is running, and offers Cancel', async () => {
    const onCancelJob = vi.fn(async () => {})
    mount({}, { jobs: [{ jobId: 'job-4', label: 'Eval of the whole golden set' }], onCancelJob })
    await screen.findByRole('checkbox', { name: 'OMNI-2510' })
    expect(runSuite()).toBeDisabled()
    expect(runSuite()).toHaveAttribute('title', 'A suite is already running')
    expect(screen.getByText('A suite is running · 0 selected')).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: 'Cancel suite' }))
    await waitFor(() => expect(onCancelJob).toHaveBeenCalledWith('job-4'))
  })

  it('stays disabled after a start until a job finishes, when the store recorded no id to compare', async () => {
    const { transport, emit } = mount()
    fireEvent.click(await screen.findByRole('checkbox', { name: 'OMNI-2510' }))
    fireEvent.click(runSuite())
    await waitFor(() => expect(runSuite()).toBeDisabled())
    expect(transport.evalReports).toHaveBeenCalledTimes(1)

    emit({ kind: 'job.finished', jobId: 'job-1', workspaceId: 'ws1', outcomes: [] })
    await waitFor(() => expect(transport.evalReports).toHaveBeenCalledTimes(2))
    await waitFor(() => expect(runSuite()).not.toBeDisabled())
  })

  it('waits for the suite’s own job to finish, not another job in the workspace', async () => {
    const { transport, emit } = mount({}, { onStartEval: vi.fn(async () => 'job-7') })
    fireEvent.click(await screen.findByRole('checkbox', { name: 'OMNI-2510' }))
    fireEvent.click(runSuite())
    await waitFor(() => expect(runSuite()).toBeDisabled())

    // A triage ending is not the suite ending.
    emit({ kind: 'job.finished', jobId: 'job-1', workspaceId: 'ws1', outcomes: [] })
    expect(runSuite()).toBeDisabled()
    expect(transport.evalReports).toHaveBeenCalledTimes(1)

    emit({ kind: 'job.finished', jobId: 'job-7', workspaceId: 'ws1', outcomes: [] })
    await waitFor(() => expect(transport.evalReports).toHaveBeenCalledTimes(2))
    await waitFor(() => expect(runSuite()).not.toBeDisabled())
  })

  it('offers Cancel for the job the start answered with, before the store lists it', async () => {
    const onCancelJob = vi.fn(async () => {})
    mount({}, { onStartEval: vi.fn(async () => 'job-7'), onCancelJob })
    fireEvent.click(await screen.findByRole('checkbox', { name: 'OMNI-2510' }))
    expect(screen.queryByRole('button', { name: 'Cancel suite' })).toBeNull()
    fireEvent.click(runSuite())

    fireEvent.click(await screen.findByRole('button', { name: 'Cancel suite' }))
    await waitFor(() => expect(onCancelJob).toHaveBeenCalledWith('job-7'))
    await waitFor(() => expect(screen.queryByRole('button', { name: 'Cancel suite' })).toBeNull())
  })

  it('lists a job once when the store and the start both name it', async () => {
    const onCancelJob = vi.fn(async () => {})
    mount(
      {},
      {
        jobs: [{ jobId: 'job-7', label: 'Eval of the whole golden set' }],
        onStartEval: vi.fn(async () => 'job-7'),
        onCancelJob,
      },
    )
    await screen.findByRole('checkbox', { name: 'OMNI-2510' })
    expect(screen.getAllByRole('button', { name: 'Cancel suite' })).toHaveLength(1)
  })

  it('ignores another workspace finishing a job', async () => {
    const { transport, emit } = mount()
    await screen.findByRole('checkbox', { name: 'OMNI-2510' })
    emit({ kind: 'job.finished', jobId: 'job-9', workspaceId: 'ws2', outcomes: [] })
    expect(transport.evalReports).toHaveBeenCalledTimes(1)
  })

  /*
   * The store toasts a failed start and rethrows it; this is the half the
   * reader sees without leaving the screen.
   */
  it('shows the reason a suite did not start', async () => {
    mount(
      {},
      {
        onStartEval: vi.fn(async () => {
          throw new Error('the golden set holds no bundles')
        }),
      },
    )
    fireEvent.click(await screen.findByRole('checkbox', { name: 'OMNI-2510' }))
    fireEvent.click(runSuite())
    expect(await screen.findByText('the golden set holds no bundles')).toBeInTheDocument()
    expect(runSuite()).not.toBeDisabled()
  })
})

describe('Eval add golden', () => {
  it('adds the key typed into the dialog and reloads the set', async () => {
    const { transport } = mount()
    await screen.findByRole('checkbox', { name: 'OMNI-2510' })
    fireEvent.click(screen.getByRole('button', { name: 'Add golden' }))
    const dialog = screen.getByRole('dialog', { name: 'Add golden' })
    fireEvent.change(within(dialog).getByLabelText('Ticket key'), { target: { value: ' OMNI-2512 ' } })
    fireEvent.click(within(dialog).getByRole('button', { name: 'Add' }))

    await waitFor(() => expect(transport.addGolden).toHaveBeenCalledWith('ws1', { key: 'OMNI-2512' }))
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    expect(transport.golden).toHaveBeenCalledTimes(2)
  })

  it('refuses an empty key and shows the reason the add was refused', async () => {
    mount({
      addGolden: vi.fn(async () => {
        throw new Error('golden set is inside a git work tree')
      }),
    })
    await screen.findByRole('checkbox', { name: 'OMNI-2510' })
    fireEvent.click(screen.getByRole('button', { name: 'Add golden' }))
    const dialog = screen.getByRole('dialog', { name: 'Add golden' })
    fireEvent.click(within(dialog).getByRole('button', { name: 'Add' }))
    expect(within(dialog).getByText('Enter a ticket key.')).toBeInTheDocument()

    fireEvent.change(within(dialog).getByLabelText('Ticket key'), { target: { value: 'OMNI-1' } })
    fireEvent.click(within(dialog).getByRole('button', { name: 'Add' }))
    expect(await within(dialog).findByText('golden set is inside a git work tree')).toBeInTheDocument()
  })
})

describe('Eval last report', () => {
  it('draws the newest report as a retro, with its meta and the retro columns', async () => {
    mount()
    const table = await screen.findByRole('table', {
      name: 'Each key against the change a human merged',
    })
    expect(screen.getByText(`${when(RETRO.at)} · retro · rubric · claude sonnet`)).toBeInTheDocument()
    const row = within(table).getByRole('row', { name: /OMNI-2510/ })
    const cells = within(row).getAllByRole('cell')
    expect(cells.map((c) => c.textContent)).toEqual([
      'OMNI-2510',
      'done',
      'matched',
      '1/3 33%',
      'partial',
      '26',
      '$3.75',
      '5.5',
    ])
    expect(screen.getByText(/File overlap counts the files/)).toBeInTheDocument()
  })

  it('names the files the note never found, the rubric, and why a key has no scores', async () => {
    mount()
    await screen.findByText('internal/export/pool.go')
    expect(screen.getByText(/rubric partial — half the change/)).toBeInTheDocument()
    expect(screen.getByText('triage: failed — provider exited 1')).toBeInTheDocument()
    const table = screen.getByRole('table', { name: 'Each key against the change a human merged' })
    const failed = within(table).getByRole('row', { name: /OMNI-2511/ })
    expect(within(failed).getByText('failed')).toBeInTheDocument()
  })

  it('draws a plain report with the assertion columns when it is the newest', async () => {
    mount({ latestRetro: vi.fn(async () => null) })
    const table = await screen.findByRole('table', { name: 'Score per key' })
    expect(screen.getByText(`${when(REPORT.at)} · plain · claude sonnet`)).toBeInTheDocument()
    const row = within(table).getByRole('row', { name: /OMNI-2510/ })
    const cells = within(row).getAllByRole('cell')
    expect(cells.map((c) => c.textContent)).toEqual([
      'OMNI-2510',
      'done',
      '3/3',
      '4/5 80%',
      '2/2 100%',
      'yes',
      '7',
      '$0.42',
      '3.5',
    ])
    expect(screen.getByText('provider exited 1')).toBeInTheDocument()
    expect(screen.getByText(/wanted P1, got P3/)).toBeInTheDocument()
    expect(screen.getByText(/Assertions are the checks/)).toBeInTheDocument()
  })

  it('says where reports come from when the workspace has none, and Open JSON waits', async () => {
    mount({
      evalReports: vi.fn(async () => [] as EvalReport[]),
      latestRetro: vi.fn(async () => null),
    })
    await screen.findByText(/No report yet/)
    expect(screen.getByText('.sirdar/eval')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Open JSON' })).toBeDisabled()
  })

  it('Open JSON shows the report as it was written, with its path', async () => {
    mount()
    await screen.findByRole('table', { name: 'Each key against the change a human merged' })
    fireEvent.click(screen.getByRole('button', { name: 'Open JSON' }))
    const dialog = screen.getByRole('dialog', { name: 'Report JSON' })
    expect(within(dialog).getByText('/work/.sirdar/eval/20260915T090000Z-retro.json')).toBeInTheDocument()
    expect(within(dialog).getByText(/"baseCommit": "abc123"/)).toBeInTheDocument()
    fireEvent.click(within(dialog).getByRole('button', { name: 'Close' }))
    expect(screen.queryByRole('dialog')).toBeNull()
  })

  it('Copy puts the JSON on the clipboard and says so, and says why when it cannot', async () => {
    const writeText = vi.fn(async () => {})
    Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true })
    try {
      mount()
      await screen.findByRole('table', { name: 'Each key against the change a human merged' })
      fireEvent.click(screen.getByRole('button', { name: 'Open JSON' }))
      const dialog = screen.getByRole('dialog', { name: 'Report JSON' })

      fireEvent.click(within(dialog).getByRole('button', { name: 'Copy' }))
      await waitFor(() => expect(within(dialog).getByRole('button', { name: 'Copied' })).toBeInTheDocument())
      expect(writeText).toHaveBeenCalledWith(expect.stringContaining('"baseCommit": "abc123"'))

      writeText.mockRejectedValueOnce(new Error('denied'))
      fireEvent.click(within(dialog).getByRole('button', { name: /Cop/ }))
      expect(await within(dialog).findByRole('alert')).toHaveTextContent(
        'Could not reach the clipboard. Select the text and copy it.',
      )
    } finally {
      Object.defineProperty(navigator, 'clipboard', { value: undefined, configurable: true })
    }
  })
})
