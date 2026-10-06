import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { ShadowStatus } from '../api/types'
import { createFakeTransport, shadowReport, shadowRow, shadowStatus } from '../store/fakeTransport'
import Shadow, { statusLine } from './Shadow'

afterEach(cleanup)

/** A still clock: 2026-10-06 at 10:30 local. */
const NOW = new Date(2026, 9, 6, 10, 30).getTime()
/** The next poll, a quarter of an hour on. */
const NEXT_POLL = new Date(2026, 9, 6, 10, 45).toISOString()

function mount(status: ShadowStatus = shadowStatus({ nextPoll: NEXT_POLL }), seed = {}) {
  const transport = createFakeTransport({ shadowStatus: status, shadowReport: shadowReport(), ...seed })
  const onOpenRun = vi.fn()
  const view = render(
    <Shadow transport={transport} workspaceId="ws1" status={status} now={NOW} onOpenRun={onOpenRun} />,
  )
  return { transport, onOpenRun, ...view }
}

describe('statusLine', () => {
  const today = { runs: 3, maxPerDay: 10, usd: 2.1, maxUSDPerDay: 10 }

  it('reads each state the watcher can be in', () => {
    expect(statusLine(shadowStatus({ nextPoll: NEXT_POLL, today }), NOW)).toBe(
      'Watching · next poll 10:45 · 3 of 10 today · $2.10 of $10',
    )
    expect(
      statusLine(
        shadowStatus({ state: 'paused', pausedUntil: new Date(2026, 9, 6, 14, 0).toISOString(), today }),
        NOW,
      ),
    ).toBe('Paused until 14:00 · 3 of 10 today · $2.10 of $10')
    expect(statusLine(shadowStatus({ state: 'gated', reason: 'daily cost cap reached', today }), NOW)).toBe(
      'Gated: daily cost cap reached · 3 of 10 today · $2.10 of $10',
    )
    expect(statusLine(shadowStatus({ state: 'locked-elsewhere', reason: 'sirdar serve, pid 4121' }), NOW)).toBe(
      'Watched by another process · sirdar serve, pid 4121',
    )
    expect(statusLine(shadowStatus({ state: 'stopped', reason: 'no tracker configured' }), NOW)).toBe(
      'Not watching · no tracker configured',
    )
    expect(statusLine(shadowStatus({ state: 'disabled', enabled: false }), NOW)).toBe('Off')
  })

  it('names the day of a time that is not today', () => {
    const until = new Date(2026, 9, 7, 9, 5).toISOString()
    expect(statusLine(shadowStatus({ state: 'paused', pausedUntil: until, today }), NOW)).toBe(
      'Paused until 7 Oct 09:05 · 3 of 10 today · $2.10 of $10',
    )
  })
})

describe('Shadow', () => {
  it('draws the headline as four stat cards and a row per key', async () => {
    mount()
    expect(await screen.findByText('of 5 started')).toBeInTheDocument()
    const graded = screen.getByText('Graded').closest('.sd-stat') as HTMLElement
    expect(within(graded).getByText('4')).toBeInTheDocument()
    const confirmed = screen.getByText('Confirmed').closest('.sd-stat') as HTMLElement
    expect(within(confirmed).getByText('25%')).toBeInTheDocument()
    expect(within(confirmed).getByText('50% confirmed or partial')).toBeInTheDocument()
    const median = screen.getByText('Median time').closest('.sd-stat') as HTMLElement
    expect(within(median).getByText('4 min')).toBeInTheDocument()
    expect(within(median).getByText('Ready before you started: 1 of 2')).toBeInTheDocument()
    const cost = screen.getByText('Cost', { selector: '.sd-stat__label' }).closest('.sd-stat') as HTMLElement
    expect(within(cost).getByText('$3.20')).toBeInTheDocument()
    expect(within(cost).getByText('$0.80 median per ticket')).toBeInTheDocument()

    const table = screen.getByRole('table')
    const rows = within(table).getAllByRole('row').slice(1)
    expect(rows).toHaveLength(2)
    expect(within(rows[0]).getByText('data-fix · The tenant id is null on imported statements.')).toBeInTheDocument()
    expect(within(rows[0]).getByText('confirmed')).toHaveAttribute('title', 'The merged PR sets the tenant on import.')
    expect(within(rows[0]).getByText('$0.84')).toBeInTheDocument()
    expect(within(rows[1]).getByText('Pending')).toBeInTheDocument()
    expect(within(rows[1]).getByText('provisional')).toBeInTheDocument()
  })

  it('shows a dash for Confirmed while nothing is graded', async () => {
    const empty = shadowReport()
    empty.headline = { ...empty.headline, graded: 0, confirmed: 0, confirmedOrPartial: 0 }
    mount(undefined, { shadowReport: empty })
    await screen.findByText('of 5 started')
    const confirmed = screen.getByText('Confirmed').closest('.sd-stat') as HTMLElement
    expect(within(confirmed).getByText('—')).toBeInTheDocument()
  })

  it('says what the table is for when there are no shadow runs yet', async () => {
    const empty = shadowReport({ rows: [] })
    mount(undefined, { shadowReport: empty })
    expect(
      await screen.findByText(
        'No shadow runs yet. New tickets assigned to you are triaged here once shadow mode is on.',
      ),
    ).toBeInTheDocument()
  })

  it('reads the status line in the head', async () => {
    mount()
    expect(
      await screen.findByText('Watching · next poll 10:45 · 3 of 10 today · $2.10 of $10'),
    ).toBeInTheDocument()
  })

  it('pauses a watching watcher and offers Resume for a paused one', async () => {
    const { transport } = mount()
    fireEvent.click(await screen.findByRole('button', { name: 'Pause' }))
    await waitFor(() => expect(transport.calls.shadowPause).toEqual([{ ws: 'ws1', forDuration: undefined }]))
    expect(await screen.findByRole('button', { name: 'Resume' })).toBeInTheDocument()
    cleanup()

    const paused = mount(shadowStatus({ state: 'paused', pausedUntil: NEXT_POLL }))
    fireEvent.click(await screen.findByRole('button', { name: 'Resume' }))
    await waitFor(() => expect(paused.transport.calls.shadowResume).toEqual(['ws1']))
  })

  it('offers neither while another process is watching', async () => {
    mount(shadowStatus({ state: 'locked-elsewhere', reason: 'sirdar serve' }))
    await screen.findByText('of 5 started')
    expect(screen.queryByRole('button', { name: 'Pause' })).toBeNull()
    expect(screen.queryByRole('button', { name: 'Resume' })).toBeNull()
  })

  it('opens the run when its key is clicked', async () => {
    const { onOpenRun } = mount()
    const key = await screen.findByRole('button', { name: 'OMNI-1' })
    expect(key.querySelector('bdi')).toHaveAttribute('dir', 'ltr')
    fireEvent.click(key)
    expect(onOpenRun).toHaveBeenCalledWith('r1')
  })

  it('records an outcome by hand from the drawer and shows the grade it came back with', async () => {
    const report = shadowReport()
    report.rows = [shadowRow({ grade: '', gradeReason: '', final: false }), ...report.rows.slice(1)]
    const { transport } = mount(undefined, { shadowReport: report })
    fireEvent.click(await screen.findByRole('button', { name: 'Statement export times out' }))
    const drawer = screen.getByRole('dialog', { name: 'OMNI-1' })
    fireEvent.change(within(drawer).getByLabelText('Pull requests'), {
      target: { value: 'https://github.com/o/r/pull/9\n' },
    })
    fireEvent.change(within(drawer).getByLabelText('Resolution'), { target: { value: 'Fixed null tenant' } })
    fireEvent.click(within(drawer).getByRole('button', { name: 'Save' }))

    await waitFor(() =>
      expect(transport.calls.shadowOutcome).toEqual([
        {
          ws: 'ws1',
          input: { key: 'OMNI-1', prUrls: ['https://github.com/o/r/pull/9'], resolution: 'Fixed null tenant' },
        },
      ]),
    )
    expect(await within(drawer).findByText('confirmed')).toBeInTheDocument()
    // The scorecard is read again so the table agrees with the drawer.
    await waitFor(() => expect(transport.calls.shadowReport.length).toBeGreaterThanOrEqual(2))
  })

  it('says why a manual outcome was refused, inside the drawer', async () => {
    const { transport } = mount()
    transport.failShadowOutcome(new Error('bad_request: not a pull request URL'))
    fireEvent.click(await screen.findByRole('button', { name: 'Statement export times out' }))
    const drawer = screen.getByRole('dialog', { name: 'OMNI-1' })
    fireEvent.change(within(drawer).getByLabelText('Pull requests'), { target: { value: 'nope' } })
    await act(async () => {
      fireEvent.click(within(drawer).getByRole('button', { name: 'Save' }))
    })
    expect(within(drawer).getByRole('alert')).toHaveTextContent('bad_request: not a pull request URL')
  })

  it('lays out the outcome, overlap, denied calls and questions in the drawer', async () => {
    const report = shadowReport()
    report.rows = [
      shadowRow({
        outcome: {
          sources: ['tracker', 'manual'],
          prUrls: ['https://github.com/o/r/pull/9'],
          resolution: 'Set the tenant on import',
          status: 'Done',
        },
        prFilesHit: { matched: 2, total: 3, score: 0.67 },
        codeRefsPathOverlap: { matched: 1, total: 2, score: 0.5 },
        denied: [{ tool: 'Bash', summary: 'psql -c …' }],
        questions: ['Which tenant imported the file?'],
      }),
    ]
    mount(undefined, { shadowReport: report })
    fireEvent.click(await screen.findByRole('button', { name: 'Statement export times out' }))
    const drawer = screen.getByRole('dialog', { name: 'OMNI-1' })
    expect(within(drawer).getByText('2 of 3 PR files named · 1 of 2 code refs in the PR')).toBeInTheDocument()
    expect(within(drawer).getByText('Bash psql -c …')).toBeInTheDocument()
    expect(within(drawer).getByText('Which tenant imported the file?')).toBeInTheDocument()
    expect(within(drawer).getByRole('link', { name: 'https://github.com/o/r/pull/9' })).toBeInTheDocument()
    expect(within(drawer).getByText('Set the tenant on import')).toBeInTheDocument()
    expect(within(drawer).getByText('reproduced')).toBeInTheDocument()
  })

  it('keeps an Arabic title in its own direction', async () => {
    const report = shadowReport()
    report.rows = [shadowRow({ title: 'لا يمكن تصدير كشف الحساب' })]
    mount(undefined, { shadowReport: report })
    const title = await screen.findByText('لا يمكن تصدير كشف الحساب')
    expect(title.closest('[dir="auto"]')).not.toBeNull()
  })
})
