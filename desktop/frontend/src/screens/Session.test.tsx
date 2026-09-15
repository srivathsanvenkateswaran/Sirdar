import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { AppEvent, RunDetail, RunEvent } from '../api/types'
import { PrimaryActionProvider, usePrimaryAction } from '../components/shell/primaryAction'
import { resetRunJobs, setRunJob } from '../lib/jobs'
import { resetPreferRTL, setPreferRTL } from '../lib/rtl'
import { createFakeTransport, diff, type FakeTransport } from '../store/fakeTransport'
import Session from './Session'

const RUN: RunDetail = {
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

/** A completed fix run whose commit sits on its branch, unpushed. */
const FIX: RunDetail = {
  ...RUN,
  kind: 'fix',
  status: 'completed',
  fix: { branch: 'sirdar/OMNI-2510', base: 'main', commit: '9f2c1ab77e4d5c6b' },
}

let ids = 0

function toolEvent(command: string, t = '2026-09-10T10:00:04Z'): RunEvent {
  return {
    t,
    kind: 'tool_started',
    payload: {
      tool: 'Bash',
      raw: {
        type: 'assistant',
        message: {
          content: [{ type: 'tool_use', id: `tu${ids++}`, name: 'Bash', input: { command } }],
        },
      },
    },
  }
}

/** A Bash result as Claude's user line carries it, paired to the call by id. */
function resultEvent(callEvent: RunEvent, text: string): RunEvent {
  const raw = callEvent.payload.raw as { message: { content: { id: string }[] } }
  return {
    t: '2026-09-10T10:00:09Z',
    kind: 'tool_finished',
    payload: {
      text,
      raw: {
        type: 'user',
        message: { content: [{ type: 'tool_result', tool_use_id: raw.message.content[0].id, content: text }] },
      },
    },
  }
}

/** A raw provider delta: what "All" used to list dozens of, per turn. */
function streamEvent(text: string): RunEvent {
  return { t: '2026-09-10T10:00:05Z', kind: 'stream_event', payload: { text } }
}

const GO_TEST_OK = [
  '--- PASS: TestApplyMovement_PartialReturn (0.01s)',
  '--- PASS: TestApplyMovement_FullReturn (0.00s)',
  'PASS',
  'ok  \tapp\t1.204s',
].join('\n')

interface Fake {
  transport: FakeTransport
  emit: (e: AppEvent) => void
}

/**
 * The store's fake transport, with the run and the event log this test
 * wants, and every method a test asserts on wrapped so its calls are seen.
 */
function fake(over: { detail?: RunDetail; events?: RunEvent[]; note?: string; diff?: ReturnType<typeof diff> | null } = {}): Fake {
  const detail = over.detail ?? RUN
  const events = over.events ?? []
  const transport = createFakeTransport({ diff: over.diff })
  transport.run = vi.fn(async () => detail)
  transport.events = vi.fn(async () => ({ events, next: events.length }))
  transport.note = vi.fn(async () => over.note ?? '')
  transport.prompt = vi.fn(async () => '')
  transport.resume = vi.fn(async () => ({ jobId: 'job-2' }))
  transport.cancel = vi.fn(async () => {})
  const inner = transport.steer
  transport.steer = vi.fn(inner)
  const innerDrop = transport.dropHunk
  transport.dropHunk = vi.fn(innerDrop)
  return {
    transport,
    emit: (e) => {
      act(() => transport.emit(e))
    },
  }
}

/** What the sidebar would draw: the published action, for the one-primary rule. */
function Published(): JSX.Element {
  const action = usePrimaryAction()
  // A span, not an <output>: that element has the status role the banner uses.
  return (
    <span data-testid="published">
      {action ? `${action.label}${action.placement === 'screen' ? ' inline' : ''}${action.disabled ? ' disabled' : ''}` : 'none'}
    </span>
  )
}

function renderSession(
  f: Fake,
  props: Partial<React.ComponentProps<typeof Session>> = {},
) {
  const onBack = vi.fn()
  const onOpenReview = vi.fn()
  const onStartFix = vi.fn()
  const view = render(
    <PrimaryActionProvider>
      <Published />
      <Session
        transport={f.transport}
        workspaceId="ws1"
        runId={RUN.runId}
        onBack={onBack}
        onOpenReview={onOpenReview}
        onStartFix={onStartFix}
        {...props}
      />
    </PrimaryActionProvider>,
  )
  return { onBack, onOpenReview, onStartFix, ...view }
}

/** The composer's one button, by the word it carries. */
function primary(name: string | RegExp): HTMLElement {
  return within(screen.getByRole('form')).getByRole('button', { name })
}

function badge(): HTMLElement {
  return screen.getByRole('heading', { name: RUN.key }).parentElement!.querySelector('.sd-badge')!
}

describe('Session', () => {
  // The run-to-job pairing is module state the shell fills in; reset it so one
  // test's answer does not enable another's Cancel button.
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

  it('backfills the event log and draws the topbar', async () => {
    const f = fake({ events: [toolEvent('git log -1 --stat')] })
    renderSession(f, { title: 'Statement export times out' })

    expect(await screen.findByRole('heading', { name: 'OMNI-2510' })).toBeInTheDocument()
    expect(await screen.findByText('git log -1 --stat')).toBeInTheDocument()
    expect(badge()).toHaveTextContent('running')
    expect(screen.getByText('triage')).toHaveClass('sd-kind')
    expect(screen.getByText('Statement export times out')).toBeInTheDocument()
    // The mark is in the topbar and again on the composer's Model chip.
    expect(screen.getAllByRole('img', { name: 'Claude' })).toHaveLength(2)
    expect(screen.getByText('claude-haiku-4-5')).toBeInTheDocument()
    expect(screen.getByText('turns')).toBeInTheDocument()
    expect(f.transport.events).toHaveBeenCalledWith('ws1', RUN.runId, 0)
  })

  it('always names the model in the topbar, and says so when the run has not reported one', async () => {
    const f = fake({ detail: { ...RUN, model: '' } })
    renderSession(f)
    await screen.findByRole('heading', { name: 'OMNI-2510' })
    expect(screen.getByText('model unknown')).toHaveClass('session-provider-model')
    expect(screen.getByText('claude · model unknown')).toBeInTheDocument()
  })

  it('keeps the composer Model chip read-only: a steer resumes the same session', async () => {
    const f = fake()
    renderSession(f)
    await screen.findByRole('heading', { name: 'OMNI-2510' })
    const chip = screen.getByRole('button', { name: /^Model claude · claude-haiku-4-5/ })
    expect(chip).toBeDisabled()
    expect(chip).toHaveAttribute('title', expect.stringContaining('A steer resumes the same session'))
    expect(screen.getByText('claude · claude-haiku-4-5')).toBeInTheDocument()
  })

  it('appends a subscribed run.event for this run and ignores another run', async () => {
    const f = fake()
    renderSession(f)
    await screen.findByRole('heading', { name: 'OMNI-2510' })

    f.emit({ kind: 'run.event', workspaceId: 'ws1', runId: RUN.runId, index: 1, event: toolEvent('rg -n "nil pointer"') })
    expect(await screen.findByText('rg -n "nil pointer"')).toBeInTheDocument()

    f.emit({ kind: 'run.event', workspaceId: 'ws1', runId: 'some-other-run', index: 2, event: toolEvent('rm -rf /') })
    expect(screen.queryByText('rm -rf /')).toBeNull()
    // The Tools tab counts what this run did.
    expect(screen.getByRole('tab', { name: /Tools/ })).toHaveTextContent('Tools1')
  })

  // The watcher and Service.Events both number events from 1. Backfilling
  // from 0 left the last line of the page indexed one below the live event
  // that repeats it, so the stream showed it twice.
  it('does not re-append an event already backfilled', async () => {
    const f = fake({ events: [toolEvent('ls -la'), toolEvent('git status')] })
    renderSession(f)
    await screen.findByText('git status')

    f.emit({ kind: 'run.event', workspaceId: 'ws1', runId: RUN.runId, index: 2, event: toolEvent('git status') })
    expect(screen.getAllByText('git status')).toHaveLength(1)
    expect(screen.getByRole('tab', { name: /Tools/ })).toHaveTextContent('Tools2')

    f.emit({ kind: 'run.event', workspaceId: 'ws1', runId: RUN.runId, index: 3, event: toolEvent('go test ./...') })
    expect(await screen.findByText('go test ./...')).toBeInTheDocument()
  })

  it('applies run.updated to the topbar', async () => {
    const f = fake()
    renderSession(f)
    await screen.findByRole('heading', { name: 'OMNI-2510' })
    expect(badge()).toHaveTextContent('running')

    f.emit({ kind: 'run.updated', workspaceId: 'ws1', run: { ...RUN, status: 'completed' } })
    await waitFor(() => expect(badge()).toHaveTextContent('completed'))
  })

  describe('the composer', () => {
    it('reads Answer while the run is blocked, and posts the answer as a resume', async () => {
      const f = fake({
        detail: { ...RUN, status: 'blocked', reason: 'agent asked: which database holds the ledger?' },
      })
      renderSession(f)
      await screen.findByRole('heading', { name: 'OMNI-2510' })

      expect(badge()).toHaveTextContent('blocked · waiting on you')
      await waitFor(() => expect(screen.getByTestId('published')).toHaveTextContent('Answer inline'))
      const box = screen.getByRole('textbox', { name: 'Answer' })
      expect(box).toHaveAttribute('placeholder', 'Answer the question')
      expect(primary('Answer')).toBeDisabled()

      fireEvent.change(box, { target: { value: 'the ledger service' } })
      fireEvent.click(primary('Answer'))

      await waitFor(() => expect(f.transport.resume).toHaveBeenCalledWith('ws1', RUN.runId, 'the ledger service'))
      // The answer joins the transcript as the operator's own bubble, and the
      // box empties for the next one.
      expect(await screen.findByTestId('you-bubble')).toHaveTextContent('the ledger service')
      expect(box).toHaveValue('')
    })

    it('sends with the keyboard: Cmd and Enter', async () => {
      const f = fake({ detail: { ...RUN, status: 'blocked', reason: 'agent asked: which tenant?' } })
      renderSession(f)
      const box = await screen.findByRole('textbox', { name: 'Answer' })
      fireEvent.change(box, { target: { value: 'acme' } })
      fireEvent.keyDown(box, { key: 'Enter', metaKey: true })
      await waitFor(() => expect(f.transport.resume).toHaveBeenCalledWith('ws1', RUN.runId, 'acme'))
    })

    it('reads Steer once the run has completed, posts the steer, and the run goes running', async () => {
      const f = fake({ detail: { ...RUN, status: 'completed' } })
      renderSession(f)
      await screen.findByRole('heading', { name: 'OMNI-2510' })

      await waitFor(() => expect(screen.getByTestId('published')).toHaveTextContent('Steer inline'))
      const box = screen.getByRole('textbox', { name: 'Steer' })
      fireEvent.change(box, { target: { value: 'Re-check the partial-return path' } })
      fireEvent.click(primary('Steer'))

      await waitFor(() =>
        expect(f.transport.steer).toHaveBeenCalledWith('ws1', RUN.runId, 'Re-check the partial-return path'),
      )
      await waitFor(() => expect(badge()).toHaveTextContent('running'))
      expect(screen.getByTestId('you-bubble')).toHaveTextContent('Re-check the partial-return path')
      // Running now: the button is down with the reason until the run moves.
      expect(primary('Steer')).toBeDisabled()
      expect(screen.getByText('The run is still working. Wait for it, or cancel it.')).toBeInTheDocument()
    })

    it('offers Steer on a failed run too', async () => {
      const f = fake({ detail: { ...RUN, status: 'failed', reason: 'provider exited 1' } })
      renderSession(f)
      await screen.findByRole('heading', { name: 'OMNI-2510' })
      expect(screen.getByRole('textbox', { name: 'Steer' })).toBeEnabled()
    })

    it('is disabled with the reason while the run is working', async () => {
      const f = fake()
      renderSession(f)
      await screen.findByRole('heading', { name: 'OMNI-2510' })

      expect(screen.getByRole('textbox', { name: 'Steer' })).toBeDisabled()
      expect(primary('Steer')).toBeDisabled()
      expect(primary('Steer')).toHaveAttribute('title', 'The run is still working. Wait for it, or cancel it.')
      await waitFor(() => expect(screen.getByTestId('published')).toHaveTextContent('Steer inline disabled'))
    })

    it('surfaces the refusal when the provider cannot be steered, and stays down', async () => {
      const f = fake({ detail: { ...RUN, status: 'completed', provider: 'cursor' } })
      f.transport.steer = vi.fn(async () => {
        throw new Error('conflict: app: steer refused: cursor has no continuation')
      })
      renderSession(f)
      await screen.findByRole('heading', { name: 'OMNI-2510' })

      fireEvent.change(screen.getByRole('textbox', { name: 'Steer' }), { target: { value: 'go on' } })
      fireEvent.click(primary('Steer'))

      expect(await screen.findByText('app: steer refused: cursor has no continuation')).toBeInTheDocument()
      expect(primary('Steer')).toBeDisabled()
      expect(primary('Steer')).toHaveAttribute('title', 'app: steer refused: cursor has no continuation')
      expect(screen.getByRole('textbox', { name: 'Steer' })).toBeDisabled()
    })

    it('shows any other send failure beside the box and keeps the words', async () => {
      const f = fake({ detail: { ...RUN, status: 'blocked', reason: 'agent asked: which tenant?' } })
      f.transport.resume = vi.fn(async () => {
        throw new Error('internal: the runner is not answering')
      })
      renderSession(f)
      const box = await screen.findByRole('textbox', { name: 'Answer' })
      fireEvent.change(box, { target: { value: 'acme' } })
      fireEvent.click(primary('Answer'))

      expect(await screen.findByRole('alert')).toHaveTextContent('the runner is not answering')
      expect(box).toHaveValue('acme')
      expect(primary('Answer')).toBeEnabled()
    })
  })

  describe('the banner', () => {
    it('reports the tests that passed, with the count and the files changed', async () => {
      const test = toolEvent('go test ./app/...')
      const f = fake({ detail: FIX, events: [test, resultEvent(test, GO_TEST_OK)] })
      renderSession(f)

      const banner = await screen.findByRole('status')
      expect(banner).toHaveAttribute('data-tone', 'done')
      expect(banner).toHaveTextContent('Tests passed')
      // Two files, from the change itself once the diff has been read.
      await waitFor(() => expect(banner).toHaveTextContent('2 tests in 1.2s, 2 files changed'))
    })

    it('reports the note once it has landed, by its name in the notes dir', async () => {
      const test = toolEvent('go test ./...')
      const f = fake({
        detail: { ...RUN, status: 'completed' },
        events: [test, resultEvent(test, GO_TEST_OK), { t: '2026-09-10T10:03:00Z', kind: 'final', payload: {} }],
      })
      renderSession(f, { notesDir: '/work/notes' })

      const banner = await screen.findByRole('status')
      expect(banner).toHaveTextContent('Note filed')
      expect(banner).toHaveTextContent('OMNI-2510-triage.md')
      expect(banner).not.toHaveTextContent('/work/notes/')
    })

    it('says a fix run committed, and on which branch', async () => {
      const f = fake({
        detail: FIX,
        events: [{ t: '2026-09-10T10:03:00Z', kind: 'final', payload: { text: 'Committed the fix.' } }],
      })
      renderSession(f)
      const banner = await screen.findByRole('status')
      expect(banner).toHaveTextContent('Fix committed')
      expect(banner).toHaveTextContent('sirdar/OMNI-2510')
      expect(banner).not.toHaveTextContent('Note filed')
    })

    it('says a fix run finished when it recorded no commit', async () => {
      const f = fake({
        detail: { ...FIX, fix: { branch: 'sirdar/OMNI-2510', base: 'main' } },
        events: [{ t: '2026-09-10T10:03:00Z', kind: 'final', payload: { text: 'Nothing to change.' } }],
      })
      renderSession(f)
      const banner = await screen.findByRole('status')
      expect(banner).toHaveTextContent('Run finished')
      expect(banner).not.toHaveTextContent('Fix committed')
    })

    it('says when the tests failed', async () => {
      const test = toolEvent('go test ./...')
      const f = fake({ events: [test, resultEvent(test, '--- FAIL: TestX (0.00s)\nFAIL\nFAIL\tapp\t0.1s')] })
      renderSession(f)
      const banner = await screen.findByRole('status')
      expect(banner).toHaveAttribute('data-tone', 'failed')
      expect(banner).toHaveTextContent('Tests failed')
      expect(banner).toHaveTextContent('--- FAIL: TestX (0.00s)')
    })

    it('draws none for a run that has neither tested nor filed', async () => {
      renderSession(fake({ events: [toolEvent('git log')] }))
      await screen.findByText('git log')
      expect(screen.queryByRole('status')).toBeNull()
    })
  })

  describe('the Changes tab', () => {
    it('shows the fake diff for a fix run, with the checks and the branch', async () => {
      const build = toolEvent('go build ./...')
      const test = toolEvent('go test ./app/...')
      const f = fake({ detail: FIX, events: [build, resultEvent(build, ''), test, resultEvent(test, GO_TEST_OK)] })
      const { onOpenReview } = renderSession(f)

      const tab = await screen.findByRole('tab', { name: /Changes/ })
      expect(tab).toHaveAttribute('aria-selected', 'true')
      await waitFor(() => expect(tab).toHaveTextContent('Changes2'))
      expect(f.transport.calls.runDiff).toEqual([{ ws: 'ws1', runId: RUN.runId }])

      // One article per file, each headed by its path.
      expect(screen.getAllByRole('article')).toHaveLength(2)
      expect(screen.getByRole('article', { name: 'internal/export/statement_test.go' })).toBeInTheDocument()

      const checks = screen.getByRole('list', { name: 'Checks' })
      expect(within(checks).getAllByRole('listitem')).toHaveLength(2)
      expect(within(checks).getAllByRole('listitem')[1]).toHaveTextContent('ok')
      expect(within(checks).getAllByRole('listitem')[1]).toHaveTextContent('go test ./app/... · 2 passed · 1.2s')

      expect(screen.getByText('sirdar/OMNI-1-fix')).toBeInTheDocument()
      fireEvent.click(screen.getByRole('button', { name: 'Open review' }))
      expect(onOpenReview).toHaveBeenCalled()
    })

    it('Drop hands the hunk and the etag to dropHunk and draws the change that comes back', async () => {
      const f = fake({ detail: FIX })
      renderSession(f)
      const first = await screen.findByRole('region', { name: 'internal/export/statement.go hunk 1' })
      fireEvent.click(within(first).getByRole('button', { name: 'Drop' }))

      await waitFor(() =>
        expect(f.transport.calls.dropHunk).toEqual([
          { ws: 'ws1', runId: RUN.runId, req: { path: 'internal/export/statement.go', hunk: 0, etag: 'etag-1' } },
        ]),
      )
      // The fake reverts the first hunk and moves the etag; the pane shows what came back.
      await waitFor(() => expect(screen.queryByText(/@@ -41,7 \+41,9 @@/)).toBeNull())
      expect(screen.getByText(/@@ -88,3 \+90,6 @@/)).toBeInTheDocument()
      expect(screen.queryByRole('region', { name: 'internal/export/statement.go hunk 2' })).toBeNull()
    })

    it('shows a refused Drop under its hunk and reads the diff again', async () => {
      const f = fake({ detail: FIX })
      f.transport.dropHunk = vi.fn(async () => {
        throw new Error('conflict: the diff has changed since it was read; read it again')
      })
      renderSession(f)
      const first = await screen.findByRole('region', { name: 'internal/export/statement.go hunk 1' })
      fireEvent.click(within(first).getByRole('button', { name: 'Drop' }))

      expect(await screen.findByRole('alert')).toHaveTextContent(
        'internal/export/statement.go hunk 1: the diff has changed since it was read; read it again',
      )
      await waitFor(() => expect(f.transport.calls.runDiff).toHaveLength(2))
    })

    it('Keep marks the hunk and says the file is reviewed once every hunk is', async () => {
      const f = fake({ detail: FIX })
      renderSession(f)
      const test = await screen.findByRole('region', { name: 'internal/export/statement_test.go hunk 1' })
      fireEvent.click(within(test).getByRole('button', { name: 'Keep' }))
      expect(within(test).getByRole('button', { name: 'Kept' })).toHaveAttribute('aria-pressed', 'true')
      // A new file keeps its own word; the kept mark shows on the button.
      expect(f.transport.calls.dropHunk).toEqual([])
    })

    it('says when a fix run has no change to show', async () => {
      const f = fake({ detail: FIX, diff: null })
      renderSession(f)
      expect(await screen.findByText('the run has no change to show')).toBeInTheDocument()
      expect(screen.getByRole('tab', { name: 'Changes' })).toBeInTheDocument()
    })

    it('reads the change again when the fix run it is watching finishes', async () => {
      let current: RunDetail = { ...FIX, status: 'running' }
      const f = fake({ detail: FIX })
      f.transport.run = vi.fn(async () => current)
      renderSession(f)
      await waitFor(() => expect(f.transport.calls.runDiff).toHaveLength(1))

      current = FIX
      f.emit({ kind: 'run.updated', workspaceId: 'ws1', run: current })
      await waitFor(() => expect(f.transport.calls.runDiff).toHaveLength(2))
    })

    it('is absent for a triage run, which opens on the note', async () => {
      const f = fake({ detail: { ...RUN, status: 'completed' } })
      renderSession(f)
      await screen.findByRole('heading', { name: 'OMNI-2510' })
      expect(screen.queryByRole('tab', { name: /Changes/ })).toBeNull()
      expect(screen.getByRole('tab', { name: 'Note' })).toHaveAttribute('aria-selected', 'true')
      expect(f.transport.calls.runDiff).toEqual([])
    })

    /*
     * The deviation gate: the commit is described, what the agent did
     * instead is quoted, and accepting reruns the fix with acceptDeviation
     * rather than starting another session.
     */
    it('shows a deviated fix and publishes the reviewed commit on accept', async () => {
      const f = fake({
        detail: { ...FIX, fix: { ...FIX.fix, deviation: 'Changed the generator template rather than the generated column.' } },
      })
      const { onStartFix } = renderSession(f)

      const panel = await screen.findByRole('region', { name: 'Fix result' })
      expect(within(panel).getByText('sirdar/OMNI-2510 (from origin/main)')).toBeInTheDocument()
      expect(within(panel).getByText(/Changed the generator template/)).toBeInTheDocument()

      fireEvent.click(within(panel).getByRole('button', { name: 'Accept and publish' }))
      await waitFor(() => expect(onStartFix).toHaveBeenCalledWith('OMNI-2510', { acceptDeviation: true }))
    })
  })

  describe('the other tabs', () => {
    it('is one tab stop, moved by the arrows, and names its panel', async () => {
      const f = fake({ detail: FIX })
      renderSession(f)
      const changes = await screen.findByRole('tab', { name: /Changes/ })
      const note = screen.getByRole('tab', { name: 'Note' })
      const panel = screen.getByRole('tabpanel')
      expect(changes).toHaveAttribute('tabindex', '0')
      expect(note).toHaveAttribute('tabindex', '-1')
      expect(changes).toHaveAttribute('aria-controls', panel.id)
      expect(panel).toHaveAttribute('aria-labelledby', changes.id)

      changes.focus()
      fireEvent.keyDown(screen.getByRole('tablist'), { key: 'ArrowRight' })
      expect(note).toHaveAttribute('aria-selected', 'true')
      expect(note).toHaveAttribute('tabindex', '0')
      expect(document.activeElement).toBe(note)
      expect(panel).toHaveAttribute('aria-labelledby', note.id)

      fireEvent.keyDown(screen.getByRole('tablist'), { key: 'End' })
      expect(screen.getByRole('tab', { name: /Tools/ })).toHaveAttribute('aria-selected', 'true')
      fireEvent.keyDown(screen.getByRole('tablist'), { key: 'ArrowRight' })
      expect(changes).toHaveAttribute('aria-selected', 'true')
    })


    it('renders the note markdown with its frontmatter as a table', async () => {
      const note = ['---', 'key: OMNI-2510', 'service: payments-api', '---', '', '## Summary', '', 'The 500 comes from an unchecked nil in the ledger handler.', ''].join('\n')
      const f = fake({ note })
      renderSession(f)

      expect(await screen.findByText('The 500 comes from an unchecked nil in the ledger handler.')).toBeInTheDocument()
      expect(screen.getByRole('heading', { name: 'Summary' })).toBeInTheDocument()
      expect(screen.getByText('payments-api')).toBeInTheDocument()
      expect(f.transport.note).toHaveBeenCalledWith('ws1', RUN.runId, 'triage')
    })

    // A fix run has no triage note — the service refuses the mismatch with a
    // 404 — so the tab asks for the empty kind, which is whatever note.md the
    // run itself wrote.
    it('asks a fix run for its own note, not for a triage note', async () => {
      const f = fake({ detail: FIX })
      renderSession(f)
      fireEvent.click(await screen.findByRole('tab', { name: 'Note' }))
      await waitFor(() => expect(f.transport.note).toHaveBeenCalledWith('ws1', RUN.runId, ''))
      expect(f.transport.note).not.toHaveBeenCalledWith('ws1', RUN.runId, 'triage')
    })

    it('asks an RCA run for both its notes', async () => {
      const f = fake({ detail: { ...RUN, kind: 'rca', status: 'completed' } })
      renderSession(f)
      await waitFor(() => expect(f.transport.note).toHaveBeenCalledWith('ws1', RUN.runId, 'rca'))
      expect(f.transport.note).toHaveBeenCalledWith('ws1', RUN.runId, 'resolution')
    })

    it('Bundle carries the directory, the attachments and the prompt', async () => {
      const f = fake()
      f.transport.prompt = vi.fn(async () => '# Ticket\n\nOMNI-2510\n\nFiles:\n- bundle/screenshot.png\n\n# Playbook\n\nstock-ledger\n')
      renderSession(f)
      fireEvent.click(await screen.findByRole('tab', { name: 'Bundle' }))
      expect(await screen.findByText('bundle/screenshot.png')).toBeInTheDocument()
      expect(screen.getByText('/work/.sirdar/runs/OMNI-2510/bundle')).toBeInTheDocument()
      expect(screen.getByText('Playbook')).toBeInTheDocument()
    })

    it('Tools lists the calls with their count, without the prose', async () => {
      const f = fake({
        events: [
          toolEvent('rg -n "nil tenant"'),
          { t: '2026-09-10T10:00:05Z', kind: 'assistant_text', payload: { text: 'Looking at the handler.' } },
        ],
      })
      renderSession(f)
      const tab = await screen.findByRole('tab', { name: /Tools/ })
      expect(tab).toHaveTextContent('Tools1')
      fireEvent.click(tab)
      const pane = screen.getByRole('tab', { name: /Tools/ }).closest('.session-right') as HTMLElement
      expect(within(pane).getByText('rg -n "nil tenant"')).toBeInTheDocument()
      expect(within(pane).queryByText('Looking at the handler.')).toBeNull()
    })
  })

  describe('Cancel', () => {
    it('is disabled until the shell knows the job, and the screen unsubscribes on unmount', async () => {
      const f = fake()
      const { unmount } = renderSession(f)
      await screen.findByRole('heading', { name: 'OMNI-2510' })

      expect(screen.getByRole('button', { name: 'Cancel' })).toBeDisabled()
      act(() => setRunJob(RUN.runId, 'job-7'))
      fireEvent.click(screen.getByRole('button', { name: 'Cancel' }))
      await waitFor(() => expect(f.transport.cancel).toHaveBeenCalledWith('job-7'))

      expect(f.transport.subscriberCount()).toBe(1)
      unmount()
      expect(f.transport.subscriberCount()).toBe(0)
    })

    // `blocked` is not the end of a run: it resumes, and its note is not
    // written yet, so nothing is re-asked for and Cancel stays on offer.
    it('stays while the run is only blocked, and nothing is re-asked for', async () => {
      const f = fake()
      renderSession(f)
      await screen.findByRole('heading', { name: 'OMNI-2510' })

      f.emit({ kind: 'run.updated', workspaceId: 'ws1', run: { ...RUN, status: 'blocked', reason: 'agent asked: which tenant?' } })
      await waitFor(() => expect(badge()).toHaveTextContent('blocked · waiting on you'))
      expect(f.transport.run).toHaveBeenCalledTimes(1)
      expect(screen.getByRole('button', { name: 'Cancel' })).toBeInTheDocument()
    })

    it.each(['completed', 'failed', 'over_budget'] as const)('is not offered on a run that ended %s', async (status) => {
      renderSession(fake({ detail: { ...RUN, status } }))
      await screen.findByRole('heading', { name: 'OMNI-2510' })
      expect(screen.queryByRole('button', { name: 'Cancel' })).toBeNull()
    })

    it('goes away as the run it is watching completes', async () => {
      let current: RunDetail = RUN
      const f = fake()
      f.transport.run = vi.fn(async () => current)
      renderSession(f)
      await screen.findByRole('heading', { name: 'OMNI-2510' })
      expect(screen.getByRole('button', { name: 'Cancel' })).toBeInTheDocument()

      current = { ...RUN, status: 'completed' }
      f.emit({ kind: 'run.updated', workspaceId: 'ws1', run: current })
      await waitFor(() => expect(screen.queryByRole('button', { name: 'Cancel' })).toBeNull())
    })
  })

  /*
   * A run opened while it was still working. The note is written as the run
   * finishes, so the pane that asked once on mount went on saying "No note
   * yet" for a run that had one, and the only way to see it was to leave the
   * screen and come back.
   */
  it('asks for the note and the run again when the run it is watching finishes', async () => {
    let current: RunDetail = RUN
    const f = fake()
    f.transport.run = vi.fn(async () => current)
    f.transport.note = vi.fn(async () =>
      current.status === 'completed' ? '# Summary\n\nThe ledger handler dereferences a nil tenant.' : '',
    )
    renderSession(f)

    expect(await screen.findByText('No note yet. It is written when the run completes.')).toBeInTheDocument()
    expect(f.transport.run).toHaveBeenCalledTimes(1)

    current = { ...RUN, status: 'completed' }
    f.emit({ kind: 'run.updated', workspaceId: 'ws1', run: current })

    expect(await screen.findByText('The ledger handler dereferences a nil tenant.')).toBeInTheDocument()
    await waitFor(() => expect(f.transport.run).toHaveBeenCalledTimes(2))
  })

  it('goes back on escape, unless the reader is typing', async () => {
    const f = fake({ detail: { ...RUN, status: 'completed' } })
    const { onBack } = renderSession(f)
    await screen.findByRole('heading', { name: 'OMNI-2510' })

    fireEvent.keyDown(screen.getByRole('textbox', { name: 'Steer' }), { key: 'Escape' })
    expect(onBack).not.toHaveBeenCalled()

    act(() => {
      window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }))
    })
    expect(onBack).toHaveBeenCalled()
  })

  it('shows the steer the run recorded as the operator\'s own words', async () => {
    const f = fake({
      detail: { ...RUN, status: 'completed' },
      events: [
        { t: '2026-09-10T10:05:00Z', kind: 'steer', payload: { text: 'Now write the RCA from this', continuation: 'primed' } },
        toolEvent('git log'),
      ],
    })
    renderSession(f)
    const bubble = await screen.findByTestId('you-bubble')
    expect(bubble).toHaveTextContent('Now write the RCA from this')
    expect(bubble).toHaveTextContent('continued in a new session')
  })

  /*
   * A provider that streams token deltas writes a `stream_event` line per
   * delta. They fold into one row per burst so the tool calls between them
   * stay findable, and open on demand.
   */
  it('keeps the raw stream events out of sight until Show everything, then folds them into one row that opens', async () => {
    const f = fake({
      events: [
        toolEvent('rg -n "nil tenant"'),
        streamEvent('delta one'),
        streamEvent('delta two'),
        streamEvent('delta three'),
        streamEvent('delta four'),
      ],
    })
    renderSession(f)
    await screen.findByText('rg -n "nil tenant"')
    expect(screen.queryByRole('button', { name: /4 stream events/ })).toBeNull()
    expect(screen.queryByText('delta one')).toBeNull()

    const everything = screen.getByRole('switch', { name: 'Show everything' })
    expect(everything).toHaveAttribute('aria-checked', 'false')
    fireEvent.click(everything)

    const fold = screen.getByRole('button', { name: /4 stream events/ })
    expect(fold).toHaveAttribute('aria-expanded', 'false')
    expect(screen.queryByText('delta one')).toBeNull()

    fireEvent.click(fold)
    expect(screen.getByText('delta one')).toBeInTheDocument()
    expect(screen.getByText('delta four')).toBeInTheDocument()
  })

  it('keeps the per-turn usage ticks out of the transcript until Show everything', async () => {
    const f = fake({
      events: [
        toolEvent('go vet ./...'),
        { t: '2026-09-10T10:00:06Z', kind: 'usage', payload: { turns: 2, costUsd: 0.05 } },
      ],
    })
    renderSession(f)
    await screen.findByText('go vet ./...')
    const stream = screen.getByTestId('event-stream')
    expect(stream).toHaveAttribute('role', 'log')
    expect(stream).toHaveAttribute('aria-live', 'polite')
    expect(within(stream).queryByText(/\$0\.05/)).toBeNull()

    fireEvent.click(screen.getByRole('switch', { name: 'Show everything' }))
    expect(within(stream).getByText(/\$0\.05/)).toBeInTheDocument()
  })

  it('tells a screen reader the state as it moves', async () => {
    const f = fake()
    renderSession(f)
    await screen.findByRole('heading', { name: RUN.key })
    expect(screen.getByText('Run running')).toHaveAttribute('aria-live', 'polite')
    f.emit({ kind: 'run.updated', workspaceId: 'ws1', run: { ...RUN, status: 'completed' } })
    await waitFor(() => expect(screen.getByText('Run completed')).toBeInTheDocument())
  })

  /*
   * The event log is tool names, file paths and JSON. Laying that out right to
   * left puts leading slashes and brackets at the wrong end, so the stream is
   * pinned LTR even for an engineer who reads notes right to left.
   */
  it('keeps the transcript left to right whatever the note preference is', async () => {
    setPreferRTL(true)
    const f = fake({ events: [toolEvent('rg -n "التصدير" internal/export')] })
    renderSession(f)
    const stream = await screen.findByTestId('event-stream')
    expect(stream.closest('.stream')).toHaveAttribute('dir', 'ltr')
  })

  it('says why a run could not be read', async () => {
    const f = fake()
    f.transport.run = vi.fn(async () => {
      throw new Error('not_found: no such run')
    })
    renderSession(f)
    expect(await screen.findByText('not_found: no such run')).toBeInTheDocument()
    expect(screen.getByTestId('published')).toHaveTextContent('none')
  })
})
