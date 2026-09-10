import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { AppEvent, RunDetail as RunDetailData, RunEvent, Transport } from '../api/types'
import RunDetail, { clearRunJob, setRunJob } from './RunDetail'

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

function renderRun(fake: Fake, onBack = vi.fn(), onStartRCA = vi.fn()) {
  return {
    onBack,
    onStartRCA,
    ...render(
      <RunDetail
        transport={fake.transport}
        workspaceId="ws1"
        runId={RUN.runId}
        onBack={onBack}
        onStartRCA={onStartRCA}
      />,
    ),
  }
}

describe('RunDetail', () => {
  // The run-to-job pairing is module state the shell fills in; reset it so one
  // test's resume does not enable another's Cancel button.
  beforeEach(() => clearRunJob(RUN.runId))

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
      index: 0,
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
      index: 0,
      event: toolEvent('rm -rf /'),
    })

    await waitFor(() => expect(screen.getByText('0 events')).toBeInTheDocument())
    expect(screen.queryByText('rm -rf /')).toBeNull()
  })

  it('does not re-append an event already backfilled', async () => {
    const fake = fakeTransport({
      events: vi.fn(async () => ({ events: [toolEvent('ls -la')], next: 1 })),
    } as Partial<Transport>)
    renderRun(fake)
    await screen.findByText('ls -la')

    fake.emit({
      kind: 'run.event',
      workspaceId: 'ws1',
      runId: RUN.runId,
      index: 0,
      event: toolEvent('ls -la'),
    })

    await waitFor(() => expect(screen.getByText('1 event')).toBeInTheDocument())
    expect(screen.getAllByText('ls -la')).toHaveLength(1)
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

  it('starts an RCA from a completed triage', async () => {
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
      expect(fake.transport.startRCA).toHaveBeenCalledWith('ws1', 'OMNI-2510', {
        prUrl: 'https://github.com/acme/api/pull/12',
        resolution: undefined,
      }),
    )
    await waitFor(() => expect(onStartRCA).toHaveBeenCalledWith('OMNI-2510'))
  })
})
