import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { AppEvent, RunDetail as RunDetailData, RunEvent, Transport } from '../api/types'
import { resetRunJobs, setRunJob } from '../lib/jobs'
import { resetPreferRTL, setPreferRTL } from '../lib/rtl'
import RunDetail from './RunDetail'

const RUN: RunDetailData = {
  runId: '20260910-1000-omni-2510',
  key: 'OMNI-2510',
  kind: 'triage',
  status: 'running',
  provider: 'claude',
  model: 'claude-haiku-4-5',
  startedAt: '2026-09-10T10:00:00Z',
  updatedAt: '2026-09-10T10:04:12Z',
  reason: '',
  usage: { turns: 3, inputTokens: 18000, outputTokens: 520, costUsd: 0.0698 },
  notes: ['/work/notes/OMNI-2510-triage.md'],
  promptPath: '/work/.sirdar/runs/OMNI-2510/prompt.md',
  bundleDir: '/work/.sirdar/runs/OMNI-2510/bundle',
  warnings: [],
  handle: 'ab02',
  budget: { maxTurns: 40, maxMinutes: 20, maxUsd: 5 },
}

function toolEvent(command: string): RunEvent {
  return {
    t: '2026-09-10T10:00:04Z',
    kind: 'tool_started',
    payload: {
      tool: 'Bash',
      raw: {
        type: 'assistant',
        message: { content: [{ type: 'tool_use', name: 'Bash', input: { command } }] },
      },
    },
  }
}

/** A raw provider delta: what "All" used to list dozens of, per turn. */
function streamEvent(text: string): RunEvent {
  return { t: '2026-09-10T10:00:05Z', kind: 'stream_event', payload: { text } }
}

interface Fake {
  transport: Transport
  emit: (e: AppEvent) => void
  unsubscribe: ReturnType<typeof vi.fn>
}

function fakeTransport(over: Partial<Transport> & { detail?: RunDetailData } = {}): Fake {
  let handler: ((e: AppEvent) => void) | undefined
  const unsubscribe = vi.fn()
  const detail = over.detail ?? RUN
  const transport = {
    workspaces: vi.fn(),
    addWorkspace: vi.fn(),
    removeWorkspace: vi.fn(),
    queue: vi.fn(),
    runs: vi.fn(),
    run: vi.fn(async () => detail),
    events: vi.fn(async () => ({ events: [] as RunEvent[], next: 0 })),
    note: vi.fn(async () => ''),
    prompt: vi.fn(async () => ''),
    startTriage: vi.fn(),
    startRCA: vi.fn(async () => ({ jobId: 'job-1' })),
    addGolden: vi.fn(async () => ({
      key: 'OMNI-2510',
      dir: '/golden/OMNI-2510',
      bundleDir: '/golden/OMNI-2510/bundle',
      assertions: 0,
      hasExpectedNote: false,
    })),
    resume: vi.fn(async () => ({ jobId: 'job-2' })),
    cancel: vi.fn(),
    register: vi.fn(),
    doctor: vi.fn(),
    quota: vi.fn(),
    subscribe: vi.fn((h: (e: AppEvent) => void) => {
      handler = h
      return unsubscribe
    }),
    ...over,
  } as unknown as Transport

  return {
    transport,
    emit: (e) => {
      act(() => handler?.(e))
    },
    unsubscribe,
  }
}

function renderRun(
  fake: Fake,
  onBack = vi.fn(),
  onStartRCA = vi.fn(),
  onStartFix = vi.fn(),
  defaultProvider?: string,
) {
  return {
    onBack,
    onStartRCA,
    onStartFix,
    ...render(
      <RunDetail
        transport={fake.transport}
        workspaceId="ws1"
        runId={RUN.runId}
        defaultProvider={defaultProvider}
        onBack={onBack}
        onStartRCA={onStartRCA}
        onStartFix={onStartFix}
      />,
    ),
  }
}

/** A completed triage run whose fix committed but stopped for review. */
const BLOCKED_FIX: RunDetailData = {
  ...RUN,
  status: 'completed',
  fix: {
    branch: 'sirdar/OMNI-2510',
    base: 'main',
    commit: '9f2c1ab77e4d5c6b',
    deviation: 'Changed the generator template rather than the generated column.',
  },
}

describe('RunDetail', () => {
  // The run-to-job pairing is module state the shell fills in; reset it so one
  // test's resume does not enable another's Cancel button.
  beforeEach(() => {
    resetRunJobs()
    localStorage.clear()
    resetPreferRTL()
  })
  afterEach(() => {
    vi.useRealTimers()
    localStorage.clear()
    resetPreferRTL()
  })

  it('backfills the event log and shows the run header', async () => {
    const fake = fakeTransport({
      events: vi.fn(async () => ({ events: [toolEvent('git log -1 --stat')], next: 1 })),
    } as Partial<Transport>)
    renderRun(fake)

    expect(await screen.findByText('OMNI-2510')).toBeInTheDocument()
    expect(await screen.findByText('git log -1 --stat')).toBeInTheDocument()
    expect(screen.getByText('running')).toBeInTheDocument()
    expect(fake.transport.events).toHaveBeenCalledWith('ws1', RUN.runId, 0)
  })

  it('appends a subscribed run.event for this run', async () => {
    const fake = fakeTransport()
    renderRun(fake)
    await screen.findByText('OMNI-2510')

    fake.emit({
      kind: 'run.event',
      workspaceId: 'ws1',
      runId: RUN.runId,
      index: 1,
      event: toolEvent('rg -n "nil pointer"'),
    })

    expect(await screen.findByText('rg -n "nil pointer"')).toBeInTheDocument()
  })

  it('ignores events belonging to another run', async () => {
    const fake = fakeTransport()
    renderRun(fake)
    await screen.findByText('OMNI-2510')

    fake.emit({
      kind: 'run.event',
      workspaceId: 'ws1',
      runId: 'some-other-run',
      index: 1,
      event: toolEvent('rm -rf /'),
    })

    // The stream opens on Tools, whose counter reads "shown of total".
    await waitFor(() => expect(screen.getByText('0 of 0')).toBeInTheDocument())
    expect(screen.queryByText('rm -rf /')).toBeNull()
  })

  // The watcher and Service.Events both number events from 1. Backfilling
  // from 0 left the last line of the page indexed one below the live event
  // that repeats it, so the stream showed it twice.
  it('does not re-append an event already backfilled', async () => {
    const fake = fakeTransport({
      events: vi.fn(async () => ({
        events: [toolEvent('ls -la'), toolEvent('git status')],
        next: 2,
      })),
    } as Partial<Transport>)
    renderRun(fake)
    await screen.findByText('git status')

    // The live stream delivers the last backfilled line again, under the
    // index the file actually gives it.
    fake.emit({
      kind: 'run.event',
      workspaceId: 'ws1',
      runId: RUN.runId,
      index: 2,
      event: toolEvent('git status'),
    })

    await waitFor(() => expect(screen.getByText('2 of 2')).toBeInTheDocument())
    expect(screen.getAllByText('git status')).toHaveLength(1)

    // The line after the page is new, and is appended.
    fake.emit({
      kind: 'run.event',
      workspaceId: 'ws1',
      runId: RUN.runId,
      index: 3,
      event: toolEvent('go test ./...'),
    })
    expect(await screen.findByText('go test ./...')).toBeInTheDocument()
  })

  it('applies run.updated to the header', async () => {
    const fake = fakeTransport()
    renderRun(fake)
    await screen.findByText('running')

    fake.emit({
      kind: 'run.updated',
      workspaceId: 'ws1',
      run: { ...RUN, status: 'completed' },
    })

    expect(await screen.findByText('completed')).toBeInTheDocument()
  })

  it('offers the resume box with the question when the run is blocked', async () => {
    const blocked: RunDetailData = {
      ...RUN,
      status: 'blocked',
      reason: 'agent asked: which database holds the ledger?',
    }
    const fake = fakeTransport({ detail: blocked })
    renderRun(fake)

    expect(await screen.findByText('which database holds the ledger?')).toBeInTheDocument()
    fireEvent.change(screen.getByLabelText('Answer'), {
      target: { value: 'the ledger service' },
    })
    fireEvent.click(screen.getByRole('button', { name: 'Resume run' }))

    await waitFor(() =>
      expect(fake.transport.resume).toHaveBeenCalledWith('ws1', RUN.runId, 'the ledger service'),
    )
  })

  it('renders the note markdown with its frontmatter as a table', async () => {
    const note = [
      '---',
      'key: OMNI-2510',
      'service: payments-api',
      '---',
      '',
      '## Summary',
      '',
      'The 500 comes from an unchecked nil in the ledger handler.',
      '',
    ].join('\n')
    const fake = fakeTransport({ note: vi.fn(async () => note) } as Partial<Transport>)
    renderRun(fake)

    expect(
      await screen.findByText('The 500 comes from an unchecked nil in the ledger handler.'),
    ).toBeInTheDocument()
    expect(screen.getByRole('heading', { name: 'Summary' })).toBeInTheDocument()
    expect(screen.getByText('payments-api')).toBeInTheDocument()
    expect(fake.transport.note).toHaveBeenCalledWith('ws1', RUN.runId, 'triage')
  })

  /*
   * Which note each kind of run is asked for. A fix run has no triage note —
   * the service refuses the mismatch with a 404 — so the tab asks for the
   * empty kind, which is whatever note.md the run itself wrote. It used to
   * ask for 'triage' and show every fix run an empty tab.
   */
  it('asks a fix run for its own note, not for a triage note', async () => {
    const fake = fakeTransport({ detail: { ...RUN, kind: 'fix', status: 'completed' } })
    renderRun(fake)
    await screen.findByText('completed')

    await waitFor(() => expect(fake.transport.note).toHaveBeenCalledWith('ws1', RUN.runId, ''))
    expect(fake.transport.note).not.toHaveBeenCalledWith('ws1', RUN.runId, 'triage')
  })

  it('asks an RCA run for both its notes', async () => {
    const fake = fakeTransport({ detail: { ...RUN, kind: 'rca', status: 'completed' } })
    renderRun(fake)
    await screen.findByText('completed')

    await waitFor(() => expect(fake.transport.note).toHaveBeenCalledWith('ws1', RUN.runId, 'rca'))
    expect(fake.transport.note).toHaveBeenCalledWith('ws1', RUN.runId, 'resolution')
  })

  it('disables Cancel until the shell knows the job, and unsubscribes on unmount', async () => {
    const fake = fakeTransport()
    const { unmount } = renderRun(fake)
    await screen.findByText('OMNI-2510')

    expect(screen.getByRole('button', { name: 'Cancel' })).toBeDisabled()

    act(() => setRunJob(RUN.runId, 'job-7'))
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }))
    await waitFor(() => expect(fake.transport.cancel).toHaveBeenCalledWith('job-7'))

    unmount()
    expect(fake.unsubscribe).toHaveBeenCalled()
  })

  it('goes back on escape', async () => {
    const fake = fakeTransport()
    const onBack = vi.fn()
    renderRun(fake, onBack)
    await screen.findByText('OMNI-2510')

    act(() => {
      window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }))
    })
    expect(onBack).toHaveBeenCalled()
  })

  // The shell starts the RCA, so its job id is recorded where Cancel can
  // find it. This screen hands over the key and the form's two inputs and
  // starts nothing itself: doing both would run the same ticket twice.
  it('hands an RCA to the shell rather than starting it twice', async () => {
    const fake = fakeTransport({ detail: { ...RUN, status: 'completed' } })
    const { onStartRCA } = renderRun(fake)
    await screen.findByText('completed')

    fireEvent.click(screen.getByRole('button', { name: 'Start RCA' }))
    const form = await screen.findByRole('form', { name: 'Start RCA' })
    fireEvent.change(within(form).getByLabelText('Pull request URL'), {
      target: { value: 'https://github.com/acme/api/pull/12' },
    })
    fireEvent.click(within(form).getByRole('button', { name: 'Start RCA' }))

    await waitFor(() =>
      expect(onStartRCA).toHaveBeenCalledWith('OMNI-2510', {
        prUrl: 'https://github.com/acme/api/pull/12',
        resolution: undefined,
      }),
    )
    expect(fake.transport.startRCA).not.toHaveBeenCalled()
  })

  it('offers a fix only on a completed triage run, and hands it to the shell', async () => {
    const running = fakeTransport()
    const { unmount } = renderRun(running)
    await screen.findByText('running')
    expect(screen.queryByRole('button', { name: 'Start fix' })).toBeNull()
    unmount()

    const fake = fakeTransport({ detail: { ...RUN, status: 'completed' } })
    const { onStartFix } = renderRun(fake, vi.fn(), vi.fn(), vi.fn(), 'claude')
    await screen.findByText('completed')

    fireEvent.click(screen.getByRole('button', { name: 'Start fix' }))
    const form = await screen.findByRole('form', { name: 'Start fix' })
    expect(
      within(form).getByRole('option', { name: 'Workspace default (claude)' }),
    ).toBeInTheDocument()
    fireEvent.change(within(form).getByLabelText('Base branch'), { target: { value: 'main' } })
    fireEvent.click(within(form).getByRole('button', { name: 'Start fix' }))

    await waitFor(() =>
      expect(onStartFix).toHaveBeenCalledWith('OMNI-2510', {
        dryRun: undefined,
        noPr: undefined,
        base: 'main',
        provider: undefined,
        model: undefined,
      }),
    )
  })

  it('escape closes the fix form before it leaves the screen', async () => {
    const fake = fakeTransport({ detail: { ...RUN, status: 'completed' } })
    const { onBack } = renderRun(fake)
    await screen.findByText('completed')

    fireEvent.click(screen.getByRole('button', { name: 'Start fix' }))
    await screen.findByRole('form', { name: 'Start fix' })

    fireEvent.keyDown(window, { key: 'Escape' })
    await waitFor(() => expect(screen.queryByRole('form', { name: 'Start fix' })).toBeNull())
    expect(onBack).not.toHaveBeenCalled()

    fireEvent.keyDown(window, { key: 'Escape' })
    expect(onBack).toHaveBeenCalledTimes(1)
  })

  /*
   * The deviation gate, end to end on this screen: the commit is described,
   * what the agent did instead is quoted, and accepting reruns the fix with
   * acceptDeviation rather than starting another session.
   */
  it('shows a blocked fix and publishes the reviewed commit on accept', async () => {
    const fake = fakeTransport({ detail: BLOCKED_FIX })
    const { onStartFix } = renderRun(fake)
    await screen.findByText('completed')

    const panel = await screen.findByRole('region', { name: 'Fix result' })
    expect(within(panel).getByText('sirdar/OMNI-2510 (from origin/main)')).toBeInTheDocument()
    expect(within(panel).getByText(/Changed the generator template/)).toBeInTheDocument()

    fireEvent.click(within(panel).getByRole('button', { name: 'Accept and publish' }))
    await waitFor(() =>
      expect(onStartFix).toHaveBeenCalledWith('OMNI-2510', { acceptDeviation: true }),
    )
  })

  it('a pushed fix shows its pull request and asks for no review', async () => {
    const fake = fakeTransport({
      detail: {
        ...BLOCKED_FIX,
        fix: { ...BLOCKED_FIX.fix, prUrl: 'https://github.com/acme/api/pull/42' },
      },
    })
    renderRun(fake)
    await screen.findByText('completed')

    const panel = await screen.findByRole('region', { name: 'Fix result' })
    expect(
      within(panel).getByRole('link', { name: 'https://github.com/acme/api/pull/42' }),
    ).toBeInTheDocument()
    expect(within(panel).queryByRole('button', { name: 'Accept and publish' })).toBeNull()
  })

  it('copies a completed run into the golden set and says what it was added as', async () => {
    const fake = fakeTransport({ detail: { ...RUN, status: 'completed' } })
    renderRun(fake)
    await screen.findByText('completed')

    fireEvent.click(screen.getByRole('button', { name: 'Add to golden set' }))
    await waitFor(() =>
      expect(fake.transport.addGolden).toHaveBeenCalledWith('ws1', { runId: RUN.runId }),
    )
    expect(await screen.findByText('Added to the golden set as OMNI-2510.')).toBeInTheDocument()
  })

  it('a golden set that refuses the bundle says why', async () => {
    const fake = fakeTransport({
      detail: { ...RUN, status: 'completed' },
      addGolden: vi.fn(async () => Promise.reject(new Error('the golden set is inside a git work tree'))),
    } as Partial<Transport>)
    renderRun(fake)
    await screen.findByText('completed')

    fireEvent.click(screen.getByRole('button', { name: 'Add to golden set' }))
    expect(
      await screen.findByText('the golden set is inside a git work tree'),
    ).toBeInTheDocument()
  })

  it('a running run offers no golden copy: only a finished bundle is worth replaying', async () => {
    renderRun(fakeTransport())
    await screen.findByText('running')
    expect(screen.queryByRole('button', { name: 'Add to golden set' })).toBeNull()
  })

  it('a run with no fix state shows no fix panel at all', async () => {
    renderRun(fakeTransport({ detail: { ...RUN, status: 'completed' } }))
    await screen.findByText('completed')
    expect(screen.queryByRole('region', { name: 'Fix result' })).toBeNull()
  })


  /*
   * A run opened while it was still working. The note is written as the run
   * finishes, so the pane that asked once on mount went on saying "No note
   * yet" for a run that had one, and the only way to see it was to leave the
   * screen and come back.
   */
  it('asks for the note and the run again when the run it is watching finishes', async () => {
    let current: RunDetailData = RUN
    const fake = fakeTransport({
      run: vi.fn(async () => current),
      note: vi.fn(async () =>
        current.status === 'completed'
          ? '# Summary\n\nThe ledger handler dereferences a nil tenant.'
          : '',
      ),
    } as Partial<Transport>)
    renderRun(fake)

    expect(
      await screen.findByText('No note yet. It is written when the run completes.'),
    ).toBeInTheDocument()
    expect(fake.transport.run).toHaveBeenCalledTimes(1)

    current = { ...RUN, status: 'completed' }
    fake.emit({ kind: 'run.updated', workspaceId: 'ws1', run: current })

    expect(
      await screen.findByText('The ledger handler dereferences a nil tenant.'),
    ).toBeInTheDocument()
    // State and the fix panel are read off the same detail, so it is re-read
    // rather than left at whatever the run had while it was working.
    await waitFor(() => expect(fake.transport.run).toHaveBeenCalledTimes(2))
  })

  // `blocked` is not the end of a run: it resumes, and its note is not written
  // yet, so nothing is re-asked for and Cancel stays on offer.
  it('does not re-ask while the run is only blocked', async () => {
    const fake = fakeTransport()
    renderRun(fake)
    await screen.findByText('running')

    fake.emit({
      kind: 'run.updated',
      workspaceId: 'ws1',
      run: { ...RUN, status: 'blocked', reason: 'agent asked: which tenant?' },
    })
    await screen.findByText('blocked')

    expect(fake.transport.run).toHaveBeenCalledTimes(1)
    expect(screen.getByRole('button', { name: 'Cancel' })).toBeInTheDocument()
  })

  it.each(['completed', 'failed', 'over_budget'] as const)(
    'offers no Cancel on a run that ended %s',
    async (status) => {
      const fake = fakeTransport({ detail: { ...RUN, status } })
      renderRun(fake)
      await screen.findByText(status.replace('_', ' '))
      expect(screen.queryByRole('button', { name: 'Cancel' })).toBeNull()
    },
  )

  it('takes Cancel away as the run it is watching completes', async () => {
    let current: RunDetailData = RUN
    const fake = fakeTransport({ run: vi.fn(async () => current) } as Partial<Transport>)
    renderRun(fake)
    await screen.findByText('running')
    expect(screen.getByRole('button', { name: 'Cancel' })).toBeInTheDocument()

    current = { ...RUN, status: 'completed' }
    fake.emit({ kind: 'run.updated', workspaceId: 'ws1', run: current })

    await waitFor(() => expect(screen.queryByRole('button', { name: 'Cancel' })).toBeNull())
  })

  /*
   * A provider that streams token deltas writes a `stream_event` line per
   * delta. Under "All" they were one row each — dozens per turn — and the tool
   * calls between them were unfindable. They fold now, and Tools is what the
   * stream opens on.
   */
  describe('the event stream', () => {
    const noisy = () =>
      fakeTransport({
        events: vi.fn(async () => ({
          events: [
            toolEvent('rg -n "nil tenant"'),
            streamEvent('delta one'),
            streamEvent('delta two'),
            streamEvent('delta three'),
            streamEvent('delta four'),
          ],
          next: 5,
        })),
      } as Partial<Transport>)

    it('opens on Tools, with the raw deltas out of the way', async () => {
      renderRun(noisy())
      expect(await screen.findByText('rg -n "nil tenant"')).toBeInTheDocument()

      expect(screen.getByRole('button', { name: 'Tools' })).toHaveAttribute(
        'aria-pressed',
        'true',
      )
      expect(screen.getByRole('button', { name: 'All' })).toHaveAttribute('aria-pressed', 'false')
      expect(screen.queryByText('delta one')).toBeNull()
      expect(screen.getByText('1 of 5')).toBeInTheDocument()
    })

    it('folds consecutive stream events into one row under All, and opens it', async () => {
      renderRun(noisy())
      await screen.findByText('rg -n "nil tenant"')

      fireEvent.click(screen.getByRole('button', { name: 'All' }))

      const fold = await screen.findByRole('button', { name: /4 stream events/ })
      expect(fold).toHaveAttribute('aria-expanded', 'false')
      expect(screen.queryByText('delta one')).toBeNull()
      // The tool call it used to be buried under is still a row of its own.
      expect(screen.getByText('rg -n "nil tenant"')).toBeInTheDocument()

      fireEvent.click(fold)
      expect(fold).toHaveAttribute('aria-expanded', 'true')
      expect(screen.getByText('delta one')).toBeInTheDocument()
      expect(screen.getByText('delta four')).toBeInTheDocument()

      fireEvent.click(fold)
      expect(screen.queryByText('delta four')).toBeNull()
    })
  })

  // A screen that closes while "Copied" is still showing must not leave the
  /*
   * The event log is tool names, file paths and JSON. Laying that out right to
   * left puts leading slashes and brackets at the wrong end, so the stream is
   * pinned LTR even for an engineer who reads notes right to left.
   */
  it('keeps the event stream left to right whatever the note preference is', async () => {
    setPreferRTL(true)
    const fake = fakeTransport({
      events: vi.fn(async () => ({ events: [toolEvent('rg -n "\u0627\u0644\u062a\u0635\u062f\u064a\u0631" internal/export')], next: 1 })),
    } as Partial<Transport>)
    renderRun(fake)
    await screen.findByText('OMNI-2510')

    const stream = await screen.findByTestId('event-stream')
    expect(stream.closest('.stream')).toHaveAttribute('dir', 'ltr')
  })

  // timer that resets the label running behind it.
  it('clears the copy timeout when it unmounts', async () => {
    const writeText = vi.fn(async () => {})
    Object.defineProperty(navigator, 'clipboard', {
      configurable: true,
      value: { writeText },
    })
    const fake = fakeTransport({ detail: { ...RUN, status: 'completed' } })
    const { unmount } = renderRun(fake)
    await screen.findByText('completed')

    vi.useFakeTimers()
    fireEvent.click(screen.getByRole('button', { name: 'Copy note path' }))
    await vi.waitFor(() => expect(writeText).toHaveBeenCalledWith(RUN.notes[0]))
    expect(vi.getTimerCount()).toBeGreaterThan(0)

    unmount()
    expect(vi.getTimerCount()).toBe(0)
  })
})
