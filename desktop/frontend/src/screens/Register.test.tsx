// @vitest-environment jsdom
import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import '@testing-library/jest-dom/vitest'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { RegisterRow, RunSummary } from '../api/types'
import { createFakeTransport, run } from '../store/fakeTransport'
import Register, { csvFileName, whenLabel } from './Register'

/*
 * The clock is frozen at noon UTC on Monday 14 September 2026, so "this
 * week" and the grid's last column are the same for every run of this file.
 * Only `Date` is faked: the timers stay real, so testing-library's polling
 * is not waiting on a clock that has to be advanced.
 */
const NOW = Date.parse('2026-09-14T12:00:00Z')

beforeEach(() => {
  vi.useFakeTimers({ toFake: ['Date'] })
  vi.setSystemTime(NOW)
})

afterEach(() => {
  cleanup()
  vi.useRealTimers()
  vi.restoreAllMocks()
})

function row(over: Partial<RegisterRow>): RegisterRow {
  return {
    key: 'OMNI-1',
    kind: 'triage',
    runId: 'run-1',
    date: '2026-09-14',
    provider: 'claude',
    model: 'sonnet',
    service: 'oxo-api',
    classification: 'null-pointer',
    confidence: 'high',
    severity: 'sev2',
    turns: 4,
    costUsd: 0.5,
    triageVerdict: '',
    notePath: '',
    title: '',
    company: '',
    ...over,
  }
}

/** Two filed rows, one failed run the register has no line for, one live run. */
const ROWS: RegisterRow[] = [
  row({
    key: 'OMNI-1',
    runId: 'run-1',
    date: '2026-09-14',
    triageVerdict: 'confirmed',
    notePath: '/vault/OMNI-1-triage.md',
    turns: 6,
    costUsd: 0.08,
  }),
  row({
    key: 'OMNI-2',
    runId: 'run-2',
    kind: 'rca',
    date: '2026-09-10',
    provider: 'codex',
    confidence: '',
    turns: 14,
    costUsd: 0.22,
    notePath: '/vault/OMNI-2-rca.md',
  }),
  row({
    key: 'OMNI-2',
    runId: 'run-2t',
    kind: 'triage',
    date: '2026-09-09',
    provider: 'codex',
    confidence: 'low',
    triageVerdict: 'wrong',
    turns: 8,
    costUsd: 0.12,
    notePath: '/vault/OMNI-2-triage.md',
  }),
]

const RUNS: RunSummary[] = [
  run({
    runId: 'run-1',
    key: 'OMNI-1',
    status: 'completed',
    title: 'Statement export times out',
    startedAt: '2026-09-14T09:41:00Z',
    updatedAt: '2026-09-14T09:43:00Z',
  }),
  run({
    runId: 'run-3',
    key: 'OMNI-3',
    kind: 'triage',
    status: 'failed',
    provider: 'qwen',
    reason: 'the provider refused the prompt',
    startedAt: '2026-09-13T16:20:00Z',
    updatedAt: '2026-09-13T16:21:00Z',
    usage: { turns: 0, inputTokens: 0, outputTokens: 0, costUsd: 0 },
  }),
  run({
    runId: 'run-4',
    key: 'OMNI-4',
    kind: 'fix',
    status: 'running',
    startedAt: '2026-09-14T11:30:00Z',
    updatedAt: '2026-09-14T11:31:00Z',
    usage: { turns: 3, inputTokens: 0, outputTokens: 0, costUsd: 0.05 },
  }),
]

function mount(seed: { register?: RegisterRow[]; runs?: RunSummary[] } = {}) {
  const transport = createFakeTransport({ register: seed.register ?? ROWS, runs: seed.runs ?? RUNS })
  const onOpenRun = vi.fn()
  const view = render(<Register transport={transport} workspaceId="ws1" onOpenRun={onOpenRun} />)
  return { transport, onOpenRun, ...view }
}

/** The table, once the ledger has landed. */
async function table(): Promise<HTMLElement> {
  return await screen.findByRole('table', { name: 'Every run' })
}

/** Mounts the sample workspace and waits for its table. */
async function shownTable(): Promise<HTMLElement> {
  mount()
  return await table()
}

describe('whenLabel', () => {
  it('shows a stamped run as day, month and clock, and a bare date as day and month', () => {
    const stamped = whenLabel('2026-09-14T09:41:00Z')
    expect(stamped).toMatch(/^14 Sep \d{2}:\d{2}$/)
    expect(whenLabel('2026-09-05')).toBe('5 Sep')
    expect(whenLabel('yesterday')).toBe('yesterday')
  })
})

describe('Register', () => {
  it('says it is loading until the register answers', () => {
    mount()
    expect(screen.getByText('Loading the register…')).toBeInTheDocument()
  })

  it('shows the error the transport gave, and no table', async () => {
    const transport = createFakeTransport({})
    transport.register = () => Promise.reject(new Error('register.jsonl is not readable'))
    render(<Register transport={transport} workspaceId="ws1" />)
    expect(await screen.findByRole('alert')).toHaveTextContent('register.jsonl is not readable')
    expect(screen.queryByRole('table')).toBeNull()
  })

  it('draws the band with zeros and a sentence instead of a table when nothing has run', async () => {
    mount({ register: [], runs: [] })
    expect(await screen.findByText(/No runs recorded yet/)).toBeInTheDocument()
    expect(screen.getByText('Runs this week').nextElementSibling).toHaveTextContent('0')
    expect(screen.getByText('Confirmed').nextElementSibling).toHaveTextContent('—')
    expect(screen.getByText('of 0 verdicts recorded')).toBeInTheDocument()
    // The grid is still drawn: a workspace with no runs is a grid of zeros.
    expect(screen.getByRole('group', { name: 'Runs per day' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Export CSV' })).toBeDisabled()
  })

  it('lists every run, newest first, joined with its state', async () => {
    const t = await shownTable()
    const keys = within(t)
      .getAllByRole('row')
      .slice(1)
      .map((r) => within(r).getAllByRole('cell')[0].textContent)
    // run-4 (live, today 11:30), run-1 (today 09:41), run-3 (failed, yesterday),
    // then the two register-only rows on the 10th and 9th; the failed run's
    // reason is a row of its own.
    expect(keys).toEqual(['OMNI-4', 'OMNI-1', 'OMNI-3', 'the provider refused the prompt', 'OMNI-2', 'OMNI-2'])
    expect(within(t).getByText('running')).toBeInTheDocument()
    expect(within(t).getByText('failed')).toBeInTheDocument()
    expect(within(t).getAllByText('completed')).toHaveLength(3)
  })

  it('puts a failed run’s reason under its row, and nothing under a clean one', async () => {
    const t = await shownTable()
    const detail = within(t).getByText('the provider refused the prompt').closest('tr')
    expect(detail).toHaveClass('sd-table__detail')
    // Only the one detail row: the completed and live runs have none.
    expect(t.querySelectorAll('tr.sd-table__detail')).toHaveLength(1)
  })

  it('computes the three figures from the rows', async () => {
    await shownTable()
    // This week (8-14 Sep): run-4, run-1, run-3, OMNI-2 rca (10th), OMNI-2 triage (9th).
    expect(screen.getByText('Runs this week').nextElementSibling).toHaveTextContent('5')
    expect(screen.getByText('3 triages · 1 fix · 1 RCA')).toBeInTheDocument()
    // Spent: 0.08 + 0.22 + 0.12 + 0 + 0.05 = 0.47; codex 0.34, claude 0.13, and
    // the failed qwen run, which cost nothing and is still a run.
    expect(screen.getByText('Spent').nextElementSibling).toHaveTextContent('$0.47')
    expect(screen.getByText('codex $0.34 · claude $0.13 · qwen $0.00')).toBeInTheDocument()
    // Verdicts: confirmed and wrong recorded, one of two confirmed.
    expect(screen.getByText('Confirmed').nextElementSibling).toHaveTextContent('50%')
    expect(screen.getByText('of 2 verdicts recorded')).toBeInTheDocument()
  })

  it('buckets the grid by runs a day and filters the table to the day that is chosen', async () => {
    const t = await shownTable()
    const today = screen.getByRole('button', { name: /Monday 14 September, 2 runs/ })
    expect(today).toHaveAttribute('data-heat', '2')
    expect(screen.getByRole('button', { name: /Sunday 13 September, 1 run$/ })).toHaveAttribute('data-heat', '1')
    expect(screen.getByRole('button', { name: /Saturday 12 September, 0 runs/ })).toHaveAttribute('data-heat', '0')

    fireEvent.click(today)
    expect(await screen.findByText(/Showing 14 September only/)).toBeInTheDocument()
    await waitFor(() => expect(within(t).queryByText('OMNI-3')).toBeNull())
    expect(within(t).getByText('OMNI-1')).toBeInTheDocument()
    expect(within(t).getByText('OMNI-4')).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: 'Show every day' }))
    await waitFor(() => expect(within(t).getByText('OMNI-3')).toBeInTheDocument())
  })

  it('keeps the kind, state and provider filters in a popover under Filters', async () => {
    const t = await shownTable()
    const filters = screen.getByRole('button', { name: 'Filters' })
    expect(filters).toHaveAttribute('aria-expanded', 'false')
    expect(screen.queryByRole('group', { name: 'Filters' })).toBeNull()

    fireEvent.click(filters)
    const popover = screen.getByRole('group', { name: 'Filters' })
    expect(filters).toHaveAttribute('aria-expanded', 'true')

    fireEvent.change(within(popover).getByLabelText('Provider'), { target: { value: 'codex' } })
    await waitFor(() => expect(within(t).queryByText('OMNI-1')).toBeNull())
    expect(within(t).getAllByText('OMNI-2')).toHaveLength(2)
    expect(screen.getByRole('button', { name: 'Filters (1)' })).toBeInTheDocument()

    fireEvent.change(within(popover).getByLabelText('Kind'), { target: { value: 'rca' } })
    await waitFor(() => expect(within(t).getAllByText('OMNI-2')).toHaveLength(1))
    expect(screen.getByRole('button', { name: 'Filters (2)' })).toBeInTheDocument()

    fireEvent.change(within(popover).getByLabelText('State'), { target: { value: 'failed' } })
    expect(await screen.findByText('Nothing in the register matches these filters.')).toBeInTheDocument()

    fireEvent.click(within(popover).getByRole('button', { name: 'Clear filters' }))
    await waitFor(() => expect(screen.getByRole('button', { name: 'Filters' })).toBeInTheDocument())

    fireEvent.keyDown(document, { key: 'Escape' })
    expect(screen.queryByRole('group', { name: 'Filters' })).toBeNull()
    expect(screen.getByRole('button', { name: 'Filters' })).toHaveFocus()
  })

  it('exports the visible rows as CSV, in the order shown', async () => {
    await shownTable()
    const captured: Blob[] = []
    const createObjectURL = vi.fn((blob: Blob) => {
      captured.push(blob)
      return 'blob:register'
    })
    const revokeObjectURL = vi.fn()
    vi.stubGlobal('URL', Object.assign(URL, { createObjectURL, revokeObjectURL }))
    const click = vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => {})

    fireEvent.click(screen.getByRole('button', { name: 'Export CSV' }))

    expect(click).toHaveBeenCalledOnce()
    expect(revokeObjectURL).toHaveBeenCalledWith('blob:register')
    expect(captured).toHaveLength(1)
    expect(captured[0].type).toBe('text/csv;charset=utf-8')
    const text = await captured[0].text()
    const lines = text.split('\r\n')
    expect(lines[0]).toBe(
      'key,kind,state,provider,model,turns,cost_usd,minutes,confidence,verdict,triage_note,rca_note,resolution_note,when,reason',
    )
    expect(lines[1]).toBe('OMNI-4,fix,running,claude,sonnet,3,0.05,30,,,no,no,no,2026-09-14T11:30:00Z,')
    expect(lines[2]).toBe('OMNI-1,triage,completed,claude,sonnet,6,0.08,2,high,confirmed,yes,no,no,2026-09-14T09:41:00Z,')
    expect(lines[3]).toBe('OMNI-3,triage,failed,qwen,sonnet,0,0.00,1,,,no,no,no,2026-09-13T16:20:00Z,the provider refused the prompt')
    expect(lines).toHaveLength(7) // header, five rows, trailing newline
  })

  it('exports only what the filters leave', async () => {
    await shownTable()
    const captured: Blob[] = []
    vi.stubGlobal(
      'URL',
      Object.assign(URL, {
        createObjectURL: (blob: Blob) => {
          captured.push(blob)
          return 'blob:x'
        },
        revokeObjectURL: () => {},
      }),
    )
    vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => {})

    fireEvent.click(screen.getByRole('button', { name: 'Filters' }))
    fireEvent.change(screen.getByLabelText('State'), { target: { value: 'failed' } })
    await waitFor(() => expect(screen.getByRole('button', { name: 'Filters (1)' })).toBeInTheDocument())
    fireEvent.click(screen.getByRole('button', { name: 'Export CSV' }))

    const text = await captured[0].text()
    expect(text.split('\r\n').filter(Boolean)).toHaveLength(2)
    expect(text).toContain('OMNI-3,triage,failed')
  })

  it('names the file after the workspace and the day', () => {
    expect(csvFileName('acme support', NOW)).toBe('sirdar-register-acme-support-2026-09-14.csv')
  })

  it('moves a row when its run changes state, without a second read', async () => {
    const { transport } = mount()
    const t = await table()
    expect(within(t).getByText('running')).toBeInTheDocument()
    const reads = transport.calls.runs.length

    act(() => {
      transport.emit({
        kind: 'run.updated',
        workspaceId: 'ws1',
        run: run({
          runId: 'run-4',
          key: 'OMNI-4',
          kind: 'fix',
          status: 'failed',
          reason: 'over budget after 3 turns',
          startedAt: '2026-09-14T11:30:00Z',
          updatedAt: '2026-09-14T11:40:00Z',
        }),
      })
    })

    await waitFor(() => expect(within(t).queryByText('running')).toBeNull())
    expect(within(t).getByText('over budget after 3 turns')).toBeInTheDocument()
    expect(transport.calls.runs).toHaveLength(reads)
  })

  it('re-reads the register when a job finishes, since it may have appended a row', async () => {
    const { transport } = mount()
    await table()
    const reads = transport.calls.runs.length
    act(() => {
      transport.emit({ kind: 'job.finished', jobId: 'j1', workspaceId: 'ws1', outcomes: [] })
    })
    await waitFor(() => expect(transport.calls.runs.length).toBe(reads + 1))
  })

  it('ignores another workspace’s events', async () => {
    const { transport } = mount()
    const t = await table()
    act(() => {
      transport.emit({
        kind: 'run.updated',
        workspaceId: 'other',
        run: run({ runId: 'run-4', key: 'OMNI-4', status: 'failed' }),
      })
    })
    expect(within(t).getByText('running')).toBeInTheDocument()
  })

  it('opens the run when its key is pressed', async () => {
    const { onOpenRun } = mount()
    const t = await table()
    fireEvent.click(within(t).getByRole('button', { name: 'OMNI-3' }))
    expect(onOpenRun).toHaveBeenCalledWith('run-3')
  })

  it('carries the ticket’s title on the key, when the run knows it', async () => {
    const t = await shownTable()
    expect(within(t).getByRole('button', { name: 'OMNI-1' })).toHaveAttribute('title', 'Statement export times out')
    expect(within(t).getByRole('button', { name: 'OMNI-3' })).toHaveAttribute('title', 'Open this run')
  })

  it('names the notes a key has, so the dots are not the only copy', async () => {
    const t = await shownTable()
    expect(within(t).getAllByRole('img', { name: 'Triage note, RCA note' })).toHaveLength(2)
    expect(within(t).getByRole('img', { name: 'Triage note' })).toBeInTheDocument()
    expect(within(t).getAllByRole('img', { name: 'No notes' })).toHaveLength(2)
  })

  it('publishes no primary action: the screen is read-only', async () => {
    await shownTable()
    expect(document.querySelector('.sd-button[data-variant="primary"]')).toBeNull()
  })
})
