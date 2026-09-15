import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { RunDetail as RunDetailData, RunEvent } from '../api/types'
import { createFakeTransport, diff, ticket, type FakeTransport } from '../store/fakeTransport'
import Review from './Review'

const REPORT = {
  summary:
    'ApplyMovement applied a Return twice. The second branch is now guarded on m.Partial.\n\nThe new table test covers both paths.',
  filesChanged: ['internal/export/statement.go', 'internal/export/statement_test.go'],
  testsRun: [
    { command: 'go build ./...', result: 'ok' },
    { command: 'go test ./internal/export/...', result: '12 passed · 1.2s' },
  ],
  risks: 'none',
  deviationFromNote: '',
}

const RUN: RunDetailData = {
  runId: 'r-fix',
  key: 'OMNI-1',
  kind: 'fix',
  status: 'completed',
  provider: 'claude',
  model: 'sonnet',
  startedAt: '2026-09-10T10:00:00Z',
  updatedAt: '2026-09-10T10:05:40Z',
  reason: '',
  usage: { turns: 31, inputTokens: 18000, outputTokens: 520, costUsd: 0.58 },
  notes: ['/w/notes/OMNI-1 statement-export.md', '/w/notes/OMNI-1 RES statement-export.md'],
  promptPath: '',
  bundleDir: '',
  warnings: [],
  handle: '',
  budget: { maxTurns: 40, maxMinutes: 20, maxUsd: 5 },
  fix: {
    branch: 'sirdar/OMNI-1-fix',
    base: 'main',
    commit: '9c1e4b2abcdef',
    deviation: 'no migration for balances already affected, per your answer',
  },
}

function ev(kind: string, payload: RunEvent['payload'] = {}): RunEvent {
  return { t: '2026-09-10T10:00:04Z', kind, payload }
}

const EVENTS: RunEvent[] = [
  ev('tool_started', { tool: 'Bash' }),
  ev('final', { text: JSON.stringify(REPORT) }),
]

function fake(over: {
  detail?: Partial<RunDetailData>
  events?: RunEvent[]
  diff?: ReturnType<typeof diff> | null
  failRun?: Error
} = {}): FakeTransport {
  const t = createFakeTransport({ diff: over.diff === undefined ? diff() : over.diff })
  const detail = { ...RUN, ...(over.detail ?? {}) }
  t.run = async () => {
    if (over.failRun) throw over.failRun
    return detail
  }
  const events = over.events ?? EVENTS
  t.events = async () => ({ events, next: events.length })
  return t
}

function renderReview(t: FakeTransport, onBack = vi.fn(), onOpenNote = vi.fn()) {
  const view = render(
    <Review
      transport={t}
      workspaceId="ws1"
      runId="r-fix"
      tickets={[ticket({ key: 'OMNI-1', title: 'Statement export times out' })]}
      onBack={onBack}
      onOpenNote={onOpenNote}
    />,
  )
  return { ...view, onBack, onOpenNote }
}

afterEach(() => {
  vi.restoreAllMocks()
})

describe('the Change review screen', () => {
  it('renders the run, the fake diff, the checks and the footer', async () => {
    renderReview(fake())
    expect(await screen.findByRole('heading', { name: 'OMNI-1' })).toBeInTheDocument()
    expect(screen.getByText('fix')).toBeInTheDocument()
    expect(screen.getByText('Completed')).toBeInTheDocument()
    expect(screen.getByText('Statement export times out')).toBeInTheDocument()
    expect(screen.getByRole('img', { name: 'Claude' })).toBeInTheDocument()
    expect(screen.getByText('31')).toBeInTheDocument()
    expect(screen.getByText('$0.58')).toBeInTheDocument()

    const rail = screen.getByRole('navigation', { name: 'Files' })
    const head = await within(rail).findByText(/2 files/)
    expect(head).toHaveTextContent('2 files · +18 −2')
    const rows = within(rail).getAllByRole('button')
    expect(rows.map((r) => r.textContent)).toEqual([
      'internal/export/statement.go+9−2modified',
      'internal/export/statement_test.go+9−0new',
    ])
    expect(rows[0]).toHaveAttribute('aria-current', 'true')

    const checks = within(rail).getByRole('list', { name: 'Checks' })
    expect(within(checks).getAllByRole('listitem').map((li) => li.textContent)).toEqual([
      'ok go build ./...',
      'ok go test ./internal/export/...',
    ])

    expect(screen.getAllByRole('region', { name: /hunk \d$/ })).toHaveLength(3)
    const change = screen.getByRole('region', { name: 'Change' })
    expect(within(change).getByText('internal/export/statement.go', { selector: '.review-dtool__path' })).toBeInTheDocument()
    expect(screen.getAllByRole('button', { name: 'Keep' })).toHaveLength(3)

    const pane = screen.getByRole('complementary', { name: 'What the agent said' })
    expect(within(pane).getByText(/applied a Return twice/)).toBeInTheDocument()
    expect(within(pane).getByText(/The new table test/)).toBeInTheDocument()
    expect(pane.querySelector('.review-notdone')).toHaveTextContent(
      'Not done: no migration for balances already affected, per your answer',
    )
    expect(within(pane).getByText('Resolution note')).toBeInTheDocument()
    expect(within(pane).getByText('/w/notes/OMNI-1 RES statement-export.md')).toBeInTheDocument()

    const foot = screen.getByRole('contentinfo')
    expect(within(foot).getByText('/repos/omni/.sirdar/worktrees/OMNI-1')).toBeInTheDocument()
    expect(within(foot).getByText('main')).toBeInTheDocument()
    expect(within(foot).getByText('sirdar/OMNI-1-fix')).toBeInTheDocument()
    expect(within(foot).getByText('git -C /repos/omni/.sirdar/worktrees/OMNI-1 push -u origin sirdar/OMNI-1-fix')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /Create branch/ })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /Discard/ })).not.toBeInTheDocument()
  })

  it('drops a hunk through the transport and shows the change as answered', async () => {
    const t = fake()
    renderReview(t)
    const first = await screen.findByRole('region', { name: 'internal/export/statement.go hunk 1' })
    fireEvent.click(within(first).getByRole('button', { name: 'Drop' }))
    await waitFor(() => expect(t.calls.dropHunk).toHaveLength(1))
    expect(t.calls.dropHunk[0]).toEqual({
      ws: 'ws1',
      runId: 'r-fix',
      req: { path: 'internal/export/statement.go', hunk: 0, etag: 'etag-1' },
    })
    // The fake removes the first hunk of the patch: two remain.
    await waitFor(() => expect(screen.getAllByRole('region', { name: /hunk \d$/ })).toHaveLength(2))
    const rail = screen.getByRole('navigation', { name: 'Files' })
    expect(within(rail).getAllByRole('button')[0]).toHaveTextContent('+6')
  })

  it('files Keep locally and calls the file reviewed once every hunk is kept', async () => {
    const t = fake()
    renderReview(t)
    const test = await screen.findByRole('region', { name: 'internal/export/statement_test.go hunk 1' })
    fireEvent.click(within(test).getByRole('button', { name: 'Keep' }))
    expect(within(test).getByRole('button', { name: 'Kept' })).toHaveAttribute('aria-pressed', 'true')
    expect(t.calls.dropHunk).toHaveLength(0)
    const rail = screen.getByRole('navigation', { name: 'Files' })
    expect(within(rail).getAllByRole('button')[1]).toHaveTextContent('reviewed')
    expect(within(rail).getAllByRole('button')[0]).toHaveTextContent('modified')
  })

  it('reports a refused drop and reads the change again', async () => {
    const t = fake()
    renderReview(t)
    const first = await screen.findByRole('region', { name: 'internal/export/statement.go hunk 1' })
    // The change moved under the screen: the fake now holds a newer etag.
    t.setDiff(diff({ etag: 'etag-9' }))
    fireEvent.click(within(first).getByRole('button', { name: 'Drop' }))
    expect(await screen.findByRole('alert')).toHaveTextContent(/conflict/)
    await waitFor(() => expect(t.calls.runDiff.length).toBeGreaterThanOrEqual(2))
    expect(screen.getAllByRole('region', { name: /hunk \d$/ })).toHaveLength(3)
  })

  it('shows why when the run has no change to show', async () => {
    const t = fake({ diff: null })
    renderReview(t)
    expect(await screen.findByText('No change to review')).toBeInTheDocument()
    expect(screen.getByText('not_found: the run has no change to show')).toBeInTheDocument()
    expect(screen.getByRole('navigation', { name: 'Files' })).toHaveTextContent('No files')
    expect(screen.queryByRole('button', { name: 'Keep' })).not.toBeInTheDocument()
    // The footer still names what the run recorded.
    const foot = screen.getByRole('contentinfo')
    expect(within(foot).getByText('main')).toBeInTheDocument()
    expect(within(foot).getByText('sirdar/OMNI-1-fix')).toBeInTheDocument()
  })

  it('goes back to the session from the button and from Escape', async () => {
    const { onBack } = renderReview(fake())
    await screen.findByRole('heading', { name: 'OMNI-1' })
    fireEvent.click(screen.getByRole('button', { name: 'Back to session' }))
    expect(onBack).toHaveBeenCalledTimes(1)
    fireEvent.keyDown(window, { key: 'Escape' })
    expect(onBack).toHaveBeenCalledTimes(2)
  })

  it('opens the note from the pane', async () => {
    const { onOpenNote } = renderReview(fake())
    fireEvent.click(await screen.findByRole('button', { name: 'Open' }))
    expect(onOpenNote).toHaveBeenCalledTimes(1)
  })

  it('says Split is next rather than drawing one', async () => {
    renderReview(fake())
    await screen.findByRole('region', { name: 'internal/export/statement.go hunk 1' })
    fireEvent.click(screen.getByRole('radio', { name: 'Split' }))
    expect(screen.getByText(/Split view is next/)).toBeInTheDocument()
    fireEvent.click(screen.getByRole('radio', { name: 'Unified' }))
    expect(screen.getAllByRole('region', { name: /hunk \d$/ })).toHaveLength(3)
  })

  it('copies the push line', async () => {
    const writeText = vi.fn(async () => {})
    Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true })
    renderReview(fake())
    fireEvent.click(await screen.findByRole('button', { name: 'Copy' }))
    await waitFor(() => expect(screen.getByRole('button', { name: 'Copied' })).toBeInTheDocument())
    expect(writeText).toHaveBeenCalledWith(
      'git -C /repos/omni/.sirdar/worktrees/OMNI-1 push -u origin sirdar/OMNI-1-fix',
    )
  })

  it('reads a pushed change without Keep or Drop and says the branch is on origin', async () => {
    const t = fake({
      diff: diff({ pushed: true }),
      detail: { fix: { ...RUN.fix, pushed: true, prUrl: 'https://github.com/x/y/pull/1' } },
    })
    renderReview(t)
    await screen.findByRole('region', { name: 'internal/export/statement.go hunk 1' })
    expect(screen.queryByRole('button', { name: 'Keep' })).not.toBeInTheDocument()
    expect(screen.getByRole('status', { name: '' })).toHaveTextContent('The branch is pushed')
    const foot = screen.getByRole('contentinfo')
    expect(within(foot).getByText(/is on origin/)).toBeInTheDocument()
    expect(within(foot).getByRole('link', { name: 'pull request' })).toHaveAttribute(
      'href',
      'https://github.com/x/y/pull/1',
    )
    expect(within(foot).queryByRole('button', { name: 'Copy' })).not.toBeInTheDocument()
  })

  it('follows the run live: the status moves and the checks arrive', async () => {
    const t = fake({ detail: { status: 'running', fix: undefined }, events: [] })
    renderReview(t)
    expect(await screen.findByText('Running')).toBeInTheDocument()
    expect(screen.getByRole('navigation', { name: 'Files' })).toHaveTextContent('None yet.')
    expect(screen.getByText(/still working/)).toBeInTheDocument()

    act(() => {
      t.emit({
        kind: 'run.event',
        workspaceId: 'ws1',
        runId: 'r-fix',
        index: 1,
        event: ev('final', { text: JSON.stringify(REPORT) }),
      })
    })
    const checks = await screen.findByRole('list', { name: 'Checks' })
    expect(within(checks).getAllByRole('listitem')).toHaveLength(2)
    expect(screen.getByText(/applied a Return twice/)).toBeInTheDocument()

    const before = t.calls.runDiff.length
    act(() => {
      t.emit({ kind: 'run.updated', workspaceId: 'ws1', run: { ...RUN, status: 'completed' } })
    })
    expect(await screen.findByText('Completed')).toBeInTheDocument()
    // The run ended: the change is asked for again.
    await waitFor(() => expect(t.calls.runDiff.length).toBeGreaterThan(before))
  })

  it('shows the reason when the run cannot be read', async () => {
    renderReview(fake({ failRun: new Error('not_found: no such run') }))
    expect(await screen.findByRole('alert')).toHaveTextContent('not_found: no such run')
    expect(screen.getByRole('button', { name: 'Back to session' })).toBeInTheDocument()
  })

  it('lets the subscription go when it unmounts', async () => {
    const t = fake()
    const { unmount } = renderReview(t)
    await screen.findByRole('heading', { name: 'OMNI-1' })
    expect(t.subscriberCount()).toBe(1)
    unmount()
    expect(t.subscriberCount()).toBe(0)
  })
})
