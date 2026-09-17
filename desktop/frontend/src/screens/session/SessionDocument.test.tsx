import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { AppEvent } from '../../api/types'
import { PrimaryActionProvider, usePrimaryAction } from '../../components/shell/primaryAction'
import { resetRunJobs, setRunJob } from '../../lib/jobs'
import { resetSessionLayout, setSessionLayout } from '../../lib/sessionLayout'
import {
  at,
  blockedFixture,
  claudePermission,
  claudeToolResult,
  claudeToolUse,
  FIX_RUN_ID,
  FIX_START,
  fixFixture,
  TRIAGE_RUN_ID,
  triageFixture,
  type SessionFixture,
} from '../../store/fakeSession'
import { createFakeTransport, type FakeTransport } from '../../store/fakeTransport'
import Session from '../Session'

/*
 * Layout B, scene by scene against the fake transport: S1 the completed
 * triage with the note as the document, S2 the fix run blocked with the
 * Reply strip, S3 a step expanded with its twin marker lit in the document,
 * S4 and S5 the Bundle and Tools drawers over the path, S6 the fix run's
 * change as the document. Then the live states the mock describes in words.
 */

function Published(): JSX.Element {
  const action = usePrimaryAction()
  return <span data-testid="published">{action ? `${action.label}${action.disabled ? ' disabled' : ''}` : 'none'}</span>
}

function mount(fixtures: SessionFixture[], runId: string, over: { transport?: (t: FakeTransport) => void } = {}) {
  const sessions = Object.fromEntries(fixtures.map((f) => [f.detail.runId, f]))
  const transport = createFakeTransport({ sessions })
  over.transport?.(transport)
  const onBack = vi.fn()
  const onOpenReview = vi.fn()
  const onStartFix = vi.fn()
  const view = render(
    <PrimaryActionProvider>
      <Published />
      <Session transport={transport} workspaceId="ws1" runId={runId} title="Product 00219 stock shows 1 more than the movement report" onBack={onBack} onOpenReview={onOpenReview} onStartFix={onStartFix} />
    </PrimaryActionProvider>,
  )
  const emit = (e: AppEvent) => act(() => transport.emit(e))
  return { transport, onBack, onOpenReview, onStartFix, emit, ...view }
}

async function opened(key = 'SBX-1'): Promise<HTMLElement> {
  await screen.findByRole('heading', { name: key })
  return screen.getByTestId('session-document')
}

beforeEach(() => {
  resetRunJobs()
  localStorage.clear()
  setSessionLayout('document')
})
afterEach(() => {
  localStorage.clear()
  resetSessionLayout()
})

describe('S1 · the completed triage', () => {
  it('opens on the note as the document, the path grouped by turn on the left, Steer under it', async () => {
    mount([triageFixture()], TRIAGE_RUN_ID)
    const doc = await opened()
    expect(within(doc).getByText('completed · note saved')).toBeInTheDocument()
    const note = await within(doc).findByTestId('note-document')
    expect(within(note).getByRole('heading', { level: 1 })).toHaveTextContent(/Recording a customer return adds its quantity to stock twice/)
    expect(within(note).getByText(/written by the agent at 03:13 after your steer/)).toBeInTheDocument()
    expect(within(note).getByText('SBX-1 recording-a-customer-return-adds-its-quantity-to-stock-twice.md')).toBeInTheDocument()
    const path = within(doc).getByRole('log', { name: 'Path' })
    expect(within(path).getAllByTestId('tool-step')).toHaveLength(15)
    expect(within(path).getByRole('region', { name: 'turns 3–8' })).toBeInTheDocument()
    expect(within(path).getByTestId('you-card')).toHaveTextContent('resumed the session')
    expect(within(path).getByText('claude 7-day window at 90%')).toBeInTheDocument()
    expect(within(doc).getByText('15 calls')).toBeInTheDocument()
    const strip = within(doc).getByTestId('composer-strip')
    expect(strip).toHaveAttribute('data-mode', 'steer')
    expect(screen.getByTestId('published')).toHaveTextContent('Steer')
    // Stopping a run belongs to the composer; the header never carries it.
    expect(within(doc).queryByRole('button', { name: 'Cancel' })).toBeNull()
  })

  it('the last step in the path is the accent-coloured note write, and the evidence traces to steps', async () => {
    mount([triageFixture()], TRIAGE_RUN_ID)
    const doc = await opened()
    await within(doc).findByTestId('note-document')
    const steps = within(doc).getAllByTestId('tool-step')
    const last = steps[steps.length - 1]
    expect(last).toHaveAttribute('data-kind', 'output')
    expect(last).toHaveTextContent('Rewrote the note · final')
    const evidence = within(doc).getByTestId('evidence-list')
    expect(within(evidence).getAllByRole('button', { name: /^step 0/ }).length).toBeGreaterThanOrEqual(7)
  })

  it('steers: posts the text, the run goes running, the steer joins the path as a you card', async () => {
    const { transport } = mount([triageFixture()], TRIAGE_RUN_ID)
    const doc = await opened()
    await within(doc).findByTestId('note-document')
    const box = within(doc).getByRole('textbox', { name: 'Steer' })
    fireEvent.change(box, { target: { value: 'Now check the adjustment path too.' } })
    fireEvent.click(within(doc).getByRole('button', { name: /Steer/ }))
    await waitFor(() => expect(transport.calls.steer).toEqual([{ ws: 'ws1', runId: TRIAGE_RUN_ID, text: 'Now check the adjustment path too.', model: '' }]))
    await waitFor(() => expect(within(doc).getByText('running')).toBeInTheDocument())
    expect(within(doc).getAllByTestId('you-card')).toHaveLength(2)
    // The run went running, so the strip's button became the Stop.
    expect(within(doc).getByRole('button', { name: 'Stop the run' })).toBeInTheDocument()
    expect(within(doc).queryByRole('button', { name: 'Cancel' })).toBeNull()
  })
})

describe('S2 · the fix run blocked on go test', () => {
  it('shows the amber badge, the waiting step expanded with the policy, and the Reply strip with the decision segment', async () => {
    mount([blockedFixture()], FIX_RUN_ID)
    const doc = await opened()
    expect(within(doc).getByText('blocked · waiting on you')).toBeInTheDocument()
    const path = within(doc).getByRole('log', { name: 'Path' })
    // The open step is re-drawn on its card, so the line is found again each time.
    await waitFor(() =>
      expect(within(path).getByText('waiting on you').closest('[data-testid="tool-step"]')).toHaveAttribute('aria-expanded', 'true'),
    )
    expect(within(path).getByText('Asks to run')).toBeInTheDocument()
    expect(within(path).getByText(/The agent suggests allowing/)).toHaveTextContent('go test *')
    expect(within(path).getByRole('region', { name: 'turns 1–2' })).toBeInTheDocument()
    expect(within(path).getByRole('button', { name: 'Marker C1' })).toBeInTheDocument()
    const strip = within(doc).getByTestId('composer-strip')
    expect(strip).toHaveAttribute('data-mode', 'reply')
    expect(within(strip).getByRole('group', { name: "The agent's question" })).toHaveTextContent('Run go test ./... in the worktree?')
    expect(within(strip).getByRole('radiogroup', { name: 'Decision' })).toBeInTheDocument()
    expect(screen.getByTestId('published')).toHaveTextContent('Answer')
    const change = await within(doc).findByTestId('changes-view')
    expect(within(change).getByText('1 · +14 −0')).toBeInTheDocument()
    expect(within(change).getByText(/which needs your answer below/)).toBeInTheDocument()
    expect(within(doc).queryByRole('button', { name: 'Cancel' })).toBeNull()
  })

  it('answers: the decision goes to resume as words and the answer joins the path', async () => {
    const { transport } = mount([blockedFixture()], FIX_RUN_ID, {
      transport: (t) => {
        t.resume = vi.fn(async () => ({ jobId: 'job-2' }))
      },
    })
    const doc = await opened()
    const strip = within(doc).getByTestId('composer-strip')
    fireEvent.click(within(strip).getByRole('radio', { name: 'Deny' }))
    fireEvent.change(within(strip).getByRole('textbox', { name: 'Answer' }), { target: { value: 'Run only the ledger package.' } })
    fireEvent.click(within(strip).getByRole('button', { name: /Answer/ }))
    await waitFor(() => expect(transport.resume).toHaveBeenCalledWith('ws1', FIX_RUN_ID, 'No, do not run it. Run only the ledger package.'))
    expect(await within(doc).findByText('No, do not run it. Run only the ledger package.')).toBeInTheDocument()
  })

  it("the composer's Stop cancels once the shell knows the job", async () => {
    setRunJob(TRIAGE_RUN_ID, 'job-9')
    const f = triageFixture({ status: 'running', notes: [] })
    f.events = f.events.slice(0, 40)
    f.note = ''
    const { transport } = mount([f], TRIAGE_RUN_ID)
    const doc = await opened()
    fireEvent.click(within(doc).getByRole('button', { name: 'Stop the run' }))
    await waitFor(() => expect(transport.calls.cancel).toEqual(['job-9']))
  })
})

describe('S3 · a step expanded and its twin lit', () => {
  it('clicking E5 in the document opens the rg step on the path and lights both chips; the table row it cites is hit', async () => {
    mount([triageFixture()], TRIAGE_RUN_ID)
    const doc = await opened()
    const note = await within(doc).findByTestId('note-document')
    const callout = note.querySelector('.sn-evc[data-marker="E5"]') as HTMLElement
    fireEvent.click(within(callout).getAllByRole('button', { name: 'Marker E5' })[0])
    const path = within(doc).getByRole('log', { name: 'Path' })
    const step = within(path).getByText('rg -n -i "partial|Quantity"').closest('[data-testid="tool-step"]') as HTMLElement
    expect(step).toHaveAttribute('aria-expanded', 'true')
    expect(within(step).getByRole('button', { name: 'Marker E5' })).toHaveAttribute('data-hot', 'true')
    expect(callout).toHaveAttribute('data-hot', 'true')
    expect(within(path).getByText('rg -n -i "partial|Quantity" --type go')).toHaveClass('sn-io__cmd')
    const card = step.closest('.sn-xstep') as HTMLElement
    expect(card).toHaveAttribute('data-hot', 'true')
    const table = within(card).getByRole('table')
    expect(within(table).getByText('ledger.go:33').closest('tr')).toHaveAttribute('data-hit', 'true')
  })

  it('clicking a marker on the path lights the evidence callout in the document', async () => {
    mount([triageFixture()], TRIAGE_RUN_ID)
    const doc = await opened()
    await within(doc).findByTestId('note-document')
    const path = within(doc).getByRole('log', { name: 'Path' })
    fireEvent.click(within(path).getByRole('button', { name: 'Marker E4' }))
    expect(doc.querySelector('.sn-evc[data-marker="E4"]')).toHaveAttribute('data-hot', 'true')
  })

  it('a step toggles open and closed by its line, and Show everything adds the stream lines', async () => {
    mount([triageFixture()], TRIAGE_RUN_ID)
    const doc = await opened()
    const path = within(doc).getByRole('log', { name: 'Path' })
    const line = within(path).getByText('ledger.go').closest('[data-testid="tool-step"]') as HTMLElement
    fireEvent.click(line)
    expect(within(path).getByRole('table', { name: 'File contents' })).toBeInTheDocument()
    fireEvent.click(within(path).getByText('ledger.go').closest('[data-testid="tool-step"]') as HTMLElement)
    expect(within(path).queryByRole('table', { name: 'File contents' })).toBeNull()
    const before = path.querySelectorAll('.sn-sys').length
    fireEvent.click(within(doc).getByRole('switch', { name: 'Show everything' }))
    expect(path.querySelectorAll('.sn-sys').length).toBeGreaterThanOrEqual(before)
  })
})

describe('S4 · the Bundle drawer', () => {
  it('slides over the path, shows the ticket, the RTL thread, the empty attachments and the playbooks, and closes on Escape', async () => {
    mount([triageFixture()], TRIAGE_RUN_ID)
    const doc = await opened()
    await within(doc).findByTestId('note-document')
    fireEvent.click(within(doc).getByRole('button', { name: /^Bundle/ }))
    const drawer = await screen.findByRole('dialog', { name: 'Bundle' })
    expect(drawer).toHaveTextContent('4 messages · 0 attachments')
    const view = within(drawer).getByTestId('bundle-view')
    expect(within(view).getByText('SBX-CUST-1')).toBeInTheDocument()
    expect(view.querySelectorAll('.sn-msg')).toHaveLength(4)
    expect(within(view).getByText(/None in this bundle/)).toBeInTheDocument()
    expect(within(view).getByText('10-helpdesk')).toBeInTheDocument()
    // The document is untouched behind it.
    expect(within(doc).getByTestId('note-document')).toBeInTheDocument()
    fireEvent.keyDown(document, { key: 'Escape' })
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Bundle' })).toBeNull())
  })
})

describe('S5 · the Tools drawer', () => {
  it('lists every call with decision, duration, output and evidence; a row goes to the step and closes the drawer', async () => {
    mount([triageFixture()], TRIAGE_RUN_ID)
    const doc = await opened()
    await within(doc).findByTestId('note-document')
    fireEvent.click(within(doc).getByRole('button', { name: /^Tools/ }))
    const drawer = await screen.findByRole('dialog', { name: 'Tools' })
    expect(drawer).toHaveTextContent('15 calls · 2 denied')
    const table = within(drawer).getByRole('table')
    const rows = within(table).getAllByRole('row')
    expect(rows).toHaveLength(17)
    expect(within(rows[3]).getByRole('button', { name: 'Marker E1' })).toBeInTheDocument()
    fireEvent.click(rows[12])
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Tools' })).toBeNull())
    const path = within(doc).getByRole('log', { name: 'Path' })
    const step = within(path).getByText('rg -n "Return|restock"').closest('[data-testid="tool-step"]') as HTMLElement
    expect(step).toHaveAttribute('aria-expanded', 'true')
  })

  it('Open in Tools on an expanded step opens the drawer on that row', async () => {
    mount([triageFixture()], TRIAGE_RUN_ID)
    const doc = await opened()
    const path = within(doc).getByRole('log', { name: 'Path' })
    fireEvent.click(within(path).getByText('rg -n "Return|restock"').closest('[data-testid="tool-step"]') as HTMLElement)
    fireEvent.click(within(path).getByRole('button', { name: 'Open in Tools' }))
    const drawer = await screen.findByRole('dialog', { name: 'Tools' })
    expect(drawer.querySelector('tr[data-on="true"]')).toHaveTextContent('rg -n "Return|restock"')
  })
})

describe('S6 · the fix run\'s change as the document', () => {
  it('shows the green badge with the commit, the strip, the checks, both files with Keep/Drop and C markers, and Push', async () => {
    mount([fixFixture()], FIX_RUN_ID)
    const doc = await opened()
    expect(within(doc).getByText('completed · committed f144936')).toBeInTheDocument()
    const change = await within(doc).findByTestId('changes-view')
    expect(within(change).getByText('f144936 · not pushed')).toBeInTheDocument()
    expect(within(change).getByText('2 · +15 −11')).toBeInTheDocument()
    expect(within(change).getByRole('list', { name: 'Checks' })).toHaveTextContent('FAIL')
    expect(within(change).getAllByRole('button', { name: 'Keep' })).toHaveLength(3)
    expect(within(change).getAllByRole('button', { name: 'Marker C3' })).toHaveLength(1)
    expect(within(change).getByText('dropped by you · 00:49')).toBeInTheDocument()
    fireEvent.click(within(change).getByRole('button', { name: /^Push/ }))
    expect(within(change).getByTestId('push-command')).toHaveTextContent('git -C')
    const path = within(doc).getByRole('log', { name: 'Path' })
    expect(within(path).getByText('the fix report').closest('[data-testid="tool-step"]')).toHaveAttribute('data-kind', 'output')
    expect(within(path).getByTestId('you-card')).toHaveTextContent('Dropped hunk ledger_test.go #1')
    expect(within(doc).queryByRole('button', { name: /^Bundle/ })).toBeNull()
    expect(within(doc).getByTestId('composer-strip')).toHaveAttribute('data-mode', 'steer')
  })

  it('a C marker on an edit step lights its hunk; Drop hands the hunk and the etag to dropHunk', async () => {
    const { transport } = mount([fixFixture()], FIX_RUN_ID)
    const doc = await opened()
    const change = await within(doc).findByTestId('changes-view')
    const path = within(doc).getByRole('log', { name: 'Path' })
    fireEvent.click(within(path).getByRole('button', { name: 'Marker C2' }))
    expect(within(change).getAllByRole('button', { name: 'Marker C2' })[0]).toHaveAttribute('data-hot', 'true')
    fireEvent.click(within(change).getAllByRole('button', { name: 'Drop' })[0])
    await waitFor(() => expect(transport.calls.dropHunk).toEqual([{ ws: 'ws1', runId: FIX_RUN_ID, req: { path: 'ledger.go', hunk: 0, etag: 'etag-fix-1' } }]))
    await waitFor(() => expect(within(change).getAllByRole('button', { name: 'Drop' })).toHaveLength(2))
  })
})

describe('the live states', () => {
  it('while running, the badge is running, the document says the note arrives at the end, and the strip offers Stop', async () => {
    const f = triageFixture({ status: 'running', notes: [] })
    f.events = f.events.slice(0, 40)
    f.note = ''
    mount([f], TRIAGE_RUN_ID)
    const doc = await opened()
    expect(within(doc).getByText('running')).toBeInTheDocument()
    expect(await within(doc).findByTestId('document-pending')).toHaveTextContent('The note arrives when the agent finishes')
    const box = within(doc).getByRole('textbox')
    expect(box).toBeDisabled()
    // Said once: the box, and nothing beside the button.
    expect(box).toHaveAttribute('placeholder', 'Steer the run — it picks this up at its next turn')
    expect(within(doc).queryByText(/The run is still working/)).toBeNull()
    expect(within(doc).getByRole('button', { name: 'Stop the run' })).toBeInTheDocument()
    // No job here, so there is nothing this window can stop.
    expect(screen.getByTestId('published')).toHaveTextContent('Stop disabled')
  })

  it('steps land as they finish over the stream, and the note is read again when the run completes', async () => {
    const f = triageFixture({ status: 'running', notes: [] })
    f.events = []
    f.note = ''
    const { transport, emit } = mount([f], TRIAGE_RUN_ID)
    const doc = await opened()
    expect(within(doc).getByText(/No calls yet/)).toBeInTheDocument()
    const { event, id } = claudeToolUse('Bash', { command: 'go test ./...', description: 'Run tests' }, at(FIX_START, 3))
    emit({ kind: 'run.event', workspaceId: 'ws1', runId: TRIAGE_RUN_ID, index: 1, event })
    expect(await within(doc).findByText('go test ./...')).toBeInTheDocument()
    expect(within(doc).getByTestId('tool-step')).toHaveTextContent('running')
    emit({ kind: 'run.event', workspaceId: 'ws1', runId: TRIAGE_RUN_ID, index: 2, event: claudePermission('Bash', 'allow', { command: 'go test ./...' }, at(FIX_START, 3), id) })
    emit({ kind: 'run.event', workspaceId: 'ws1', runId: TRIAGE_RUN_ID, index: 3, event: claudeToolResult(id, 'ok  \tsandbox/ledger\t2.070s', at(FIX_START, 5)) })
    expect(await within(doc).findByText(/ok · 2\.070s/)).toBeInTheDocument()
    const note = vi.fn(async () => triageFixture().note)
    transport.note = note
    // The service moved state.json as the run finished: the re-read sees it too.
    const done = { ...f.detail, status: 'completed' as const, updatedAt: at(FIX_START, 9), notes: triageFixture().detail.notes }
    transport.run = vi.fn(async () => done)
    emit({ kind: 'run.updated', workspaceId: 'ws1', run: done })
    await waitFor(() => expect(note).toHaveBeenCalled())
    expect(await within(doc).findByTestId('note-document')).toBeInTheDocument()
  })
})

describe('the frame', () => {
  it('says when the run cannot be read', async () => {
    const transport = createFakeTransport()
    transport.run = vi.fn(async () => {
      throw new Error('not_found: no such run')
    })
    render(
      <PrimaryActionProvider>
        <Session transport={transport} workspaceId="ws1" runId="gone" onBack={() => {}} onOpenReview={() => {}} />
      </PrimaryActionProvider>,
    )
    expect(await screen.findByText('not_found: no such run')).toBeInTheDocument()
  })

  it('goes back on Escape unless a drawer is open or the reader is typing, and the switcher writes the preference', async () => {
    const { onBack } = mount([triageFixture()], TRIAGE_RUN_ID)
    const doc = await opened()
    await within(doc).findByTestId('note-document')
    fireEvent.keyDown(within(doc).getByRole('textbox'), { key: 'Escape' })
    expect(onBack).not.toHaveBeenCalled()
    fireEvent.click(within(doc).getByRole('button', { name: /^Tools/ }))
    await screen.findByRole('dialog', { name: 'Tools' })
    fireEvent.keyDown(document, { key: 'Escape' })
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    expect(onBack).not.toHaveBeenCalled()
    fireEvent.keyDown(window, { key: 'Escape' })
    expect(onBack).toHaveBeenCalledTimes(1)
    fireEvent.click(within(doc).getByRole('radio', { name: 'Workbench' }))
    expect(localStorage.getItem('sirdar.sessionLayout')).toBe('workbench')
  })
})
