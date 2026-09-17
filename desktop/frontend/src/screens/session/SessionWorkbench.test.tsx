import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { AppEvent, RunDetail, RunEvent } from '../../api/types'
import { PrimaryActionProvider, usePrimaryAction } from '../../components/shell/primaryAction'
import { resetRunJobs } from '../../lib/jobs'
import { resetSessionLayout, setSessionLayout } from '../../lib/sessionLayout'
import { configSummary, createFakeTransport, type FakeTransport } from '../../store/fakeTransport'
import Session from '../Session'
import SessionWorkbench from './SessionWorkbench'
import { CONSOLE_COLLAPSED_KEY, CONSOLE_HEIGHT_KEY } from './workbench/Console'
import {
  BLOCKED_FIX_RUN,
  FIX_RUN,
  TRIAGE_NOTE,
  TRIAGE_PROMPT,
  TRIAGE_RUN,
  fixDiff,
  fixEvents,
  triageEvents,
} from './workbench/fixtures'

interface Fake {
  transport: FakeTransport
  emit: (e: AppEvent) => void
}

/** The fake transport carrying one of the fixture runs, its log and its artefacts. */
function fake(over: { detail?: RunDetail; events?: RunEvent[]; note?: string; prompt?: string; diff?: ReturnType<typeof fixDiff> | null } = {}): Fake {
  const detail = over.detail ?? TRIAGE_RUN
  const events = over.events ?? triageEvents()
  const transport = createFakeTransport({
    diff: over.diff === undefined ? fixDiff() : over.diff,
    configSummary: configSummary({
      permissions: { bash: ['git log*', 'git show*', 'rg *', 'ls *'], fixBash: ['git status*', 'go test*', 'go build*'], fetch: [], readAlso: [], mcp: [] },
    }),
  })
  transport.run = vi.fn(async () => detail)
  transport.events = vi.fn(async () => ({ events, next: events.length }))
  transport.note = vi.fn(async () => over.note ?? (detail.kind === 'fix' ? '' : TRIAGE_NOTE))
  transport.prompt = vi.fn(async () => over.prompt ?? TRIAGE_PROMPT)
  transport.resume = vi.fn(async () => ({ jobId: 'job-2' }))
  const inner = transport.steer
  transport.steer = vi.fn(inner)
  const innerDrop = transport.dropHunk
  transport.dropHunk = vi.fn(innerDrop)
  return { transport, emit: (e) => act(() => transport.emit(e)) }
}

function Published(): JSX.Element {
  const action = usePrimaryAction()
  return <span data-testid="published">{action ? `${action.label}${action.placement === 'screen' ? ' inline' : ''}` : 'none'}</span>
}

function renderWorkbench(f: Fake, detail: RunDetail = TRIAGE_RUN) {
  const onBack = vi.fn()
  const onOpenReview = vi.fn()
  const view = render(
    <PrimaryActionProvider>
      <Published />
      <SessionWorkbench
        transport={f.transport}
        workspaceId="ws1"
        runId={detail.runId}
        title={detail.title}
        notesDir="/Users/me/Documents/Personal/sirdar-sandbox/notes"
        onBack={onBack}
        onOpenReview={onOpenReview}
      />
    </PrimaryActionProvider>,
  )
  return { onBack, onOpenReview, ...view }
}

const header = () => screen.getByRole('banner')
const console_ = () => screen.getByRole('region', { name: 'Transcript' })
const commandBar = () => screen.getByRole('form')
const rowsIn = (el: HTMLElement) => el.querySelectorAll<HTMLElement>('.wb-row')

describe('SessionWorkbench', () => {
  beforeEach(() => {
    resetRunJobs()
    localStorage.clear()
    resetSessionLayout()
  })
  afterEach(() => {
    localStorage.clear()
    resetSessionLayout()
  })

  it('says so while the run loads and when it cannot be read', async () => {
    const f = fake()
    f.transport.run = vi.fn(() => new Promise<RunDetail>(() => {}))
    const { unmount } = renderWorkbench(f)
    expect(screen.getByText('Loading run…')).toBeInTheDocument()
    unmount()

    const g = fake()
    g.transport.run = vi.fn(async () => {
      throw new Error('not_found: no such run')
    })
    renderWorkbench(g)
    expect(await screen.findByText('not_found: no such run')).toBeInTheDocument()
  })

  describe('S1 · a completed triage opened fresh', () => {
    it('draws the header with the state word, the gauges, the model and the assignee', async () => {
      renderWorkbench(fake())
      expect(await screen.findByRole('heading', { name: 'SBX-1' })).toBeInTheDocument()
      const head = header()
      expect(within(head).getByText('triage')).toBeInTheDocument()
      expect(head.querySelector('.sd-badge')).toHaveTextContent('completed')
      expect(within(head).getByText(TRIAGE_RUN.title!)).toBeInTheDocument()
      const turns = within(head).getByRole('meter', { name: 'turns' })
      expect(turns).toHaveAttribute('aria-valuetext', '17 / 60')
      expect(turns).toHaveAttribute('aria-valuenow', '28')
      expect(within(head).getByRole('meter', { name: 'minutes' })).toHaveAttribute('aria-valuetext', '3:15 / 20:00')
      expect(within(head).getByRole('meter', { name: 'cost' })).toHaveAttribute('aria-valuetext', '$1.02 / $5.00')
      // Finished: the gauges are grey, not the accent.
      expect(turns).toHaveAttribute('data-live', 'false')
      expect(within(head).getByRole('img', { name: 'Claude' })).toBeInTheDocument()
      expect(within(head).getByText('claude-opus-5')).toBeInTheDocument()
      expect(within(head).getByText('ops@sandbox.local')).toBeInTheDocument()
      // Nothing is left to cancel.
      expect(within(head).queryByRole('button', { name: 'Cancel' })).not.toBeInTheDocument()
    })

    it('opens on the Answer document with its outline, facts and evidence', async () => {
      renderWorkbench(fake())
      // The answer rides in with the log, after the run itself.
      expect(
        await screen.findByRole('heading', { name: /Recording a customer return adds its quantity to stock twice/ }),
      ).toBeInTheDocument()
      const tab = screen.getByRole('tab', { name: 'Answer' })
      expect(tab).toHaveAttribute('aria-selected', 'true')
      // A triage has no change: the Diff tab is off with a reason.
      expect(screen.getByRole('tab', { name: 'Diff' })).toHaveAttribute('aria-disabled', 'true')
      const outline = screen.getByRole('navigation', { name: 'Sections' })
      expect(within(outline).getAllByRole('button').map((b) => b.textContent)).toEqual([
        'Root cause',
        'Proposed fix2 files',
        'Evidence3',
        'Blast radius',
        'Complaintar · en',
        'Timeline3',
        'Repro steps3',
        'Open questions2',
        'Reply draftar',
        'Raw JSON',
      ])
      expect(within(outline).getByRole('button', { name: 'Root cause' })).toHaveAttribute('aria-current', 'true')
      const page = screen.getByRole('region', { name: 'Answer' })
      expect(within(page).getByText('code', { selector: 'b' })).toBeInTheDocument()
      expect(within(page).getByText('high', { selector: 'b' })).toBeInTheDocument()
      expect(within(page).getByText('sandbox/ledger')).toBeInTheDocument()
      // The evidence as a source/query/finding table.
      const evidence = within(page).getByRole('table', { name: 'Evidence' })
      expect(within(evidence).getAllByRole('row')).toHaveLength(3)
      expect(within(evidence).getByText('rg -n "Return|restock" --type go')).toBeInTheDocument()
      // The reply draft as a letter with Copy.
      expect(within(page).getByRole('button', { name: 'Copy the reply draft' })).toBeInTheDocument()
      // The code references as chips, beside the same references inside the prose.
      expect(page.querySelectorAll('.wb-ref')).toHaveLength(4)
      expect(page.querySelector('.wb-ref')).toHaveTextContent('ledger.go:27-34')
    })

    it('lists the log in the console with its counts, ends on the note it wrote, and offers a follow-up', async () => {
      renderWorkbench(fake())
      await screen.findByRole('heading', { name: 'SBX-1' })
      const c = console_()
      expect(within(c).getByText(/^\d+ events · 9 calls · 1 denied · 1 steer$/)).toBeInTheDocument()
      expect(c).toHaveTextContent('838k in · 15k out')
      // Finished: follow-live is off and cannot be turned on.
      const follow = within(c).getByRole('switch', { name: 'Follow live' })
      expect(follow).toHaveAttribute('aria-checked', 'false')
      expect(follow).toBeDisabled()
      const rows = rowsIn(c)
      const texts = [...rows].map((r) => r.textContent ?? '')
      expect(texts.some((t) => t.includes('you') && t.includes('Re-check whether the partial-return path'))).toBe(true)
      expect(texts.some((t) => t.includes('answer ›'))).toBe(true)
      expect(texts[texts.length - 1]).toContain(
        'completednote written → SBX-1 recording-a-customer-return-adds-its-quantity-to-stock-twice.md · 17 turns · $1.02',
      )
      // The denied call carries its reason under the row.
      const denied = [...rows].find((r) => r.dataset.k === 'deny')!
      expect(denied).toHaveTextContent('deny')
      expect(denied.querySelector('.wb-row__reason')).toHaveTextContent(/passes git "-C"/)

      const bar = commandBar()
      expect(within(bar).getByPlaceholderText('Follow up — the run resumes from turn 17 with the note in context')).toBeInTheDocument()
      expect(within(bar).getByText('resume')).toBeInTheDocument()
      expect(within(bar).getByText('read-only')).toBeInTheDocument()
      const send = within(bar).getByRole('button', { name: /Send/ })
      expect(send).toBeDisabled()
      // The one filled control is the bar's, and the sidebar knows it.
      await waitFor(() => expect(screen.getByTestId('published')).toHaveTextContent('Send inline'))
    })

    it('steers the run from the command bar, notes the words in the log and follows live', async () => {
      const f = fake()
      renderWorkbench(f)
      await screen.findByRole('heading', { name: 'SBX-1' })
      const field = within(commandBar()).getByRole('textbox')
      fireEvent.change(field, { target: { value: 'Look at movement.go too' } })
      fireEvent.keyDown(field, { key: 'Enter', metaKey: true })
      await waitFor(() => expect(f.transport.steer).toHaveBeenCalledWith('ws1', TRIAGE_RUN.runId, 'Look at movement.go too'))
      await waitFor(() => expect(header().querySelector('.sd-badge')).toHaveTextContent('running'))
      expect(field).toHaveValue('')
      const rows = rowsIn(console_())
      expect(rows[rows.length - 1]).toHaveTextContent('“Look at movement.go too”')
      expect(within(console_()).getByRole('switch', { name: 'Follow live' })).toHaveAttribute('aria-checked', 'true')
    })

    it('maps the turns on the rail, with the steer as a break, and a click brings the turn into view', async () => {
      renderWorkbench(fake())
      await screen.findByRole('heading', { name: 'SBX-1' })
      const rail = screen.getByRole('navigation', { name: 'Turns' })
      const cells = within(rail).getAllByRole('button')
      expect(cells.map((c) => c.textContent)).toEqual(['1', '2', '3', '4', '5', '6', '7', '1', '2'])
      expect(within(rail).getByRole('separator', { name: 'steer' })).toBeInTheDocument()
      expect(cells[3]).toHaveAttribute('data-k', 'deny')
      expect(cells[6]).toHaveAttribute('data-k', 'final')
      const scrolled = vi.fn()
      Element.prototype.scrollIntoView = scrolled
      fireEvent.click(cells[3])
      expect(scrolled).toHaveBeenCalled()
      expect(console_().querySelector('[data-flash="true"]')).not.toBeNull()
    })
  })

  describe('S2 · a fix run blocked on a question', () => {
    const blocked = () => fake({ detail: BLOCKED_FIX_RUN, events: fixEvents({ untilBlocked: true }) })

    it('pins the question with its numbered options, greys the cost gauge and switches the bar to Answer', async () => {
      renderWorkbench(blocked(), BLOCKED_FIX_RUN)
      await screen.findByRole('heading', { name: 'SBX-1' })
      expect(header().querySelector('.sd-badge')).toHaveTextContent('blocked · waiting on you')
      const cost = within(header()).getByRole('meter', { name: 'cost · at result' })
      expect(cost).toHaveAttribute('aria-valuetext', '— / $5.00')
      expect(cost).toHaveAttribute('data-na', 'true')
      // The rail ends on the question.
      const cells = within(screen.getByRole('navigation', { name: 'Turns' })).getAllByRole('button')
      expect(cells[cells.length - 1]).toHaveTextContent('?')
      expect(cells[cells.length - 1]).toHaveAttribute('data-k', 'ask')
      // The Diff document is what a fix opens on; not yet summarised.
      expect(screen.getByRole('tab', { name: /Diff/ })).toHaveAttribute('aria-selected', 'true')
      expect(await screen.findByText('not yet summarised — the run is waiting on you')).toBeInTheDocument()
      const band = within(console_()).getByRole('region', { name: 'The agent is asking' })
      expect(band).toHaveTextContent('AskUserQuestion')
      expect(band).toHaveTextContent('waiting on you · the run is paused, the budget clock is not')
      expect(band).toHaveTextContent('Which should I keep?')
      const options = within(band).getAllByRole('button')
      expect(options.map((o) => o.textContent)).toEqual([
        '1remove line 33 and keep restock',
        '2remove the restock call at line 34 and the restock helper',
        '3fold every movement type with l.Stock[m.ProductID] += m.Delta()',
      ])
      const bar = commandBar()
      expect(within(bar).getByText('answer', { selector: '.wb-mono' })).toBeInTheDocument()
      expect(within(bar).getByText('worktree')).toBeInTheDocument()
      expect(within(bar).getByRole('button', { name: /Answer/ })).toBeDisabled()
      expect(screen.getByTestId('published')).toHaveTextContent('Answer inline')
    })

    it('puts a picked option into the bar and Answer resumes the run with it', async () => {
      const f = blocked()
      renderWorkbench(f, BLOCKED_FIX_RUN)
      await screen.findByRole('heading', { name: 'SBX-1' })
      const band = within(console_()).getByRole('region', { name: 'The agent is asking' })
      fireEvent.click(within(band).getAllByRole('button')[1])
      const field = within(commandBar()).getByRole('textbox')
      expect(field).toHaveValue('remove the restock call at line 34 and the restock helper')
      fireEvent.click(within(commandBar()).getByRole('button', { name: /Answer/ }))
      await waitFor(() =>
        expect(f.transport.resume).toHaveBeenCalledWith('ws1', BLOCKED_FIX_RUN.runId, 'remove the restock call at line 34 and the restock helper'),
      )
      expect(field).toHaveValue('')
    })

    it('grows as the run goes on, and the header follows the run.updated', async () => {
      const f = blocked()
      renderWorkbench(f, BLOCKED_FIX_RUN)
      await screen.findByRole('heading', { name: 'SBX-1' })
      const before = rowsIn(console_()).length
      const [edit] = fixEvents().filter((e) => e.kind === 'tool_started' && e.payload.tool === 'Edit').slice(1)
      f.emit({ kind: 'run.event', workspaceId: 'ws1', runId: BLOCKED_FIX_RUN.runId, index: 900, event: edit })
      expect(rowsIn(console_()).length).toBe(before + 1)
      f.emit({ kind: 'run.updated', workspaceId: 'ws1', run: { ...BLOCKED_FIX_RUN, status: 'running', updatedAt: '2026-09-15T12:12:00Z' } })
      expect(header().querySelector('.sd-badge')).toHaveTextContent('running')
      // Running: the gauges take the accent and follow-live is on.
      expect(within(header()).getByRole('meter', { name: 'turns' })).toHaveAttribute('data-live', 'true')
      expect(within(console_()).getByRole('switch', { name: 'Follow live' })).toHaveAttribute('aria-checked', 'true')
      expect(within(commandBar()).getByRole('button', { name: /Send/ })).toBeDisabled()
    })
  })

  describe('S3 · one call expanded in place', () => {
    it('opens the call to its coloured input, metadata and parsed output', async () => {
      renderWorkbench(fake())
      await screen.findByRole('heading', { name: 'SBX-1' })
      const row = [...rowsIn(console_())].find((r) => r.textContent?.includes('rg -n -i "partial|Quantity"'))!
      expect(row).toHaveTextContent('82 ms')
      expect(row).toHaveTextContent('7 ln · 410 B')
      expect(row).toHaveTextContent('— Search Go code for partial-return handling')
      fireEvent.click(row)
      const step = screen.getByTestId('tool-step')
      expect(row).toHaveAttribute('aria-expanded', 'true')
      // Input JSON with its keys apart from its strings.
      expect(step.querySelector('.wb-json__key')).toHaveTextContent('"command"')
      expect(step.querySelectorAll('.wb-json__string')[0]).toHaveTextContent('rg -n -i \\"partial|Quantity\\" --type go')
      // Metadata: started, decision and rule, duration, output.
      expect(step).toHaveTextContent('started02:07.3 · turn 1 after steer')
      expect(step).toHaveTextContent('decisionallow · rg *')
      expect(step).toHaveTextContent('duration82 ms')
      expect(step).toHaveTextContent('output410 B · 7 lines · text/plain')
      // Output parsed as file:line:match.
      expect(step).toHaveTextContent('7 rows · parsed as file:line:match')
      const table = within(step).getByRole('table', { name: 'Matches' })
      expect(within(table).getAllByRole('row')).toHaveLength(7)
      expect(within(table).getAllByRole('row')[0]).toHaveTextContent('ledger_test.go9')
      // Wrap, copy, raw in the corner.
      fireEvent.click(within(step).getByRole('button', { name: 'open raw' }))
      expect(within(step).queryByRole('table')).not.toBeInTheDocument()
      expect(step.querySelector('.wb-mono-block')).toHaveTextContent('ledger_test.go:9:')
      // Closes again.
      fireEvent.click(row)
      expect(screen.queryByTestId('tool-step')).not.toBeInTheDocument()
    })

    it('shows a Read as numbered lines and a failed test with its failing line flagged', async () => {
      renderWorkbench(fake({ detail: FIX_RUN, events: fixEvents() }), FIX_RUN)
      await screen.findByRole('heading', { name: 'SBX-1' })
      const rows = [...rowsIn(console_())]
      fireEvent.click(rows.find((r) => r.dataset.k === 'tool' && r.textContent?.includes('Read'))!)
      let step = screen.getByTestId('tool-step')
      expect(within(step).getByRole('table', { name: 'Lines' })).toBeInTheDocument()
      expect(step).toHaveTextContent('20 numbered lines')
      fireEvent.click(rows.find((r) => r.textContent?.includes('go test ./...  →  exit 1'))!)
      step = screen.getAllByTestId('tool-step')[1]
      const failed = step.querySelectorAll('[data-failed="true"]')
      expect(failed.length).toBeGreaterThanOrEqual(2)
      expect(failed[1]).toHaveTextContent('--- FAIL: TestApplyMovementReturnAddsQuantityOnce')
    })

    it('a file reference in the answer searches the console for the calls that read it', async () => {
      renderWorkbench(fake())
      await screen.findByRole('heading', { name: /Recording a customer return/ })
      // The chip under the hypothesis; the same reference inside the prose is a button too.
      fireEvent.click(screen.getAllByRole('button', { name: 'ledger.go:27-34' })[0])
      const search = within(console_()).getByRole('searchbox', { name: 'Search the transcript' })
      expect(search).toHaveValue('ledger.go')
      const rows = [...rowsIn(console_())]
      expect(rows.length).toBeGreaterThan(0)
      expect(rows.every((r) => r.textContent?.toLowerCase().includes('ledger.go'))).toBe(true)
      // Escape clears it.
      fireEvent.keyDown(search, { key: 'Escape' })
      expect(search).toHaveValue('')
    })
  })

  describe('S4 · the Bundle document with the console collapsed', () => {
    it('draws the same three blocks the other layouts draw, with the outline over them', async () => {
      const f = fake()
      renderWorkbench(f)
      await screen.findByRole('heading', { name: 'SBX-1' })
      fireEvent.click(screen.getByRole('tab', { name: /Bundle/ }))
      const page = await screen.findByRole('region', { name: 'Bundle' })
      expect(f.transport.prompt).toHaveBeenCalledWith('ws1', TRIAGE_RUN.runId)
      await waitFor(() => expect(screen.getByRole('tab', { name: /Bundle/ })).toHaveTextContent('ticket · 4'))
      const outline = screen.getByRole('navigation', { name: 'Sections' })
      expect(within(outline).getAllByRole('button').map((b) => b.textContent)).toEqual([
        'Ticket',
        'Conversation4',
        'Attachments0',
        'Playbooks3',
      ])

      const ticket = within(page).getByRole('region', { name: 'Ticket' })
      expect(within(ticket).getByRole('link', { name: /SBX-1/ })).toHaveAttribute('href', 'https://sandbox.local/tracker/SBX-1')
      expect(within(ticket).getByRole('link', { name: /88341/ })).toHaveAttribute('href', 'https://sandbox.local/desk/88341')
      expect(within(ticket).getByText('متجر الفهد للأدوات المنزلية')).toHaveAttribute('dir', 'auto')
      // The URL is the link's target, never text beside the identifier.
      expect(within(page).queryByText(/https:\/\//)).toBeNull()

      const thread = within(page).getByRole('region', { name: 'Conversation' })
      const messages = within(thread).getAllByRole('listitem')
      expect(messages).toHaveLength(4)
      expect(messages[0]).toHaveTextContent('أحمد الفهد')
      expect(within(page).getByRole('region', { name: 'Attachments' })).toHaveTextContent('None in this bundle.')
    })

    it('collapses the console to its header and remembers it, and the grip brings it back', async () => {
      renderWorkbench(fake())
      await screen.findByRole('heading', { name: 'SBX-1' })
      const c = console_()
      fireEvent.click(within(c).getByRole('button', { name: 'Collapse the transcript to its header' }))
      expect(c).toHaveAttribute('data-collapsed', 'true')
      expect(within(c).queryByTestId('console-log')).not.toBeInTheDocument()
      expect(within(c).getByText(/events · 9 calls/)).toBeInTheDocument()
      expect(localStorage.getItem(CONSOLE_COLLAPSED_KEY)).toBe('1')
      // The grip is a keyboard-resizable separator; up opens it taller.
      const grip = within(c).getByRole('separator', { name: 'Resize the transcript' })
      fireEvent.keyDown(grip, { key: 'ArrowUp' })
      expect(c).not.toHaveAttribute('data-collapsed')
      expect(Number(localStorage.getItem(CONSOLE_HEIGHT_KEY))).toBe(336)
      expect(c.style.blockSize).toBe('336px')
    })

    it('reads the console height back from the browser', async () => {
      localStorage.setItem(CONSOLE_HEIGHT_KEY, '480')
      renderWorkbench(fake())
      await screen.findByRole('heading', { name: 'SBX-1' })
      expect(console_().style.blockSize).toBe('480px')
    })

    it('shows the filed note as a page from its title, with the vault scaffolding gone', async () => {
      renderWorkbench(fake())
      await screen.findByRole('heading', { name: 'SBX-1' })
      fireEvent.click(screen.getByRole('tab', { name: /Note/ }))
      const page = await screen.findByRole('region', { name: 'Note' })
      expect(await within(page).findByRole('heading', { name: /Recording a customer return/ })).toBeInTheDocument()
      // Neither the frontmatter strip nor the wikilink line under the title.
      expect(within(page).queryByText('triaged', { selector: 'b' })).toBeNull()
      expect(within(page).queryByText(/Register:/)).toBeNull()
      expect(within(page).queryByText(/_Issue Register/)).toBeNull()
      const outline = screen.getByRole('navigation', { name: 'Sections' })
      expect(within(outline).getAllByRole('button').map((b) => b.textContent)).toEqual([
        'Customer Complaint (translated)',
        'Root Cause Hypothesis',
        'Proposed Fix',
        'Customer Reply Draft',
      ])
      expect(within(page).getByRole('button', { name: 'Copy this section' })).toBeInTheDocument()
      const footer = within(page).getByTestId('note-footer')
      expect(footer).toHaveTextContent('In Obsidian')
      expect(footer).toHaveTextContent('SBX-1 recording-a-customer-return-adds-its-quantity-to-stock-twice.md')
      expect(within(footer).getByRole('button', { name: 'Copy path' })).toBeInTheDocument()
    })
  })

  describe('S5 · the Tools view', () => {
    it('is the console under the tools filter: every call with its rule, totals above, sortable', async () => {
      renderWorkbench(fake())
      await screen.findByRole('heading', { name: 'SBX-1' })
      fireEvent.click(within(console_()).getByRole('radio', { name: 'tools' }))
      const table = screen.getByTestId('tools-table')
      const strip = within(table).getByLabelText('Totals')
      expect(strip).toHaveTextContent('9 calls')
      expect(strip).toHaveTextContent('8 allow')
      expect(strip).toHaveTextContent('1 deny')
      expect(strip).toHaveTextContent('2 files read')
      expect(strip).toHaveTextContent('on the model · 1:42 to first answer · 1:11 after steer')
      const rows = within(table).getAllByRole('row').slice(1)
      expect(rows).toHaveLength(9)
      expect(rows[0]).toHaveTextContent('1')
      expect(rows[0]).toHaveTextContent('allow · ls *')
      expect(rows[1]).toHaveTextContent('allow · read-only tool')
      const denied = rows.find((r) => r.dataset.k === 'deny')!
      expect(denied).toHaveTextContent('deny · not in permissions.bash')
      expect(rows.find((r) => r.dataset.k === 'final')).toHaveTextContent('allow · schema-checked')
      // Sort by duration, longest first.
      fireEvent.click(within(table).getByRole('columnheader', { name: /duration/ }))
      const sorted = within(table).getAllByRole('row').slice(1)
      expect(sorted[0]).toHaveTextContent('82 ms')
      // A row opens in place here too.
      fireEvent.click(sorted[0])
      expect(screen.getByTestId('tool-step')).toBeInTheDocument()
    })

    it('maximises the console over the documents and back', async () => {
      renderWorkbench(fake())
      await screen.findByRole('heading', { name: 'SBX-1' })
      fireEvent.click(within(console_()).getByRole('button', { name: 'Maximise the transcript' }))
      expect(console_()).toHaveAttribute('data-maximised', 'true')
      expect(screen.queryByRole('region', { name: 'Answer' })).not.toBeInTheDocument()
      fireEvent.click(screen.getByRole('button', { name: /documents collapsed/ }))
      expect(screen.getByRole('region', { name: 'Answer' })).toBeInTheDocument()
    })
  })

  describe('S6 · the change as the document', () => {
    it('draws the diff hunk by hunk with Keep and Drop, the checks with the pre-fix failure, and the branch', async () => {
      const f = fake({ detail: FIX_RUN, events: fixEvents() })
      renderWorkbench(f, FIX_RUN)
      await screen.findByRole('heading', { name: 'SBX-1' })
      expect(screen.getByRole('tab', { name: /Diff/ })).toHaveAttribute('aria-selected', 'true')
      await waitFor(() => expect(screen.getByRole('tab', { name: /Diff/ })).toHaveTextContent('2'))
      const page = screen.getByRole('region', { name: 'Diff' })
      expect(within(page).getByText("Apply a return's quantity to stock once in ApplyMovement")).toBeInTheDocument()
      expect(within(page).getByText('2 files · +15 −11')).toBeInTheDocument()
      expect(within(page).getByText('none', { selector: 'b' })).toBeInTheDocument()
      expect(within(page).getAllByRole('button', { name: 'Keep' })).toHaveLength(3)
      expect(within(page).getAllByRole('button', { name: 'Drop' })).toHaveLength(3)
      expect(within(page).getByRole('table', { name: 'ledger.go hunk 1' })).toBeInTheDocument()
      const outline = screen.getByRole('navigation', { name: 'Sections' })
      const items = within(outline).getAllByRole('button')
      expect(items.map((b) => b.textContent)).toEqual([
        'Summary',
        'ledger.go+1 −11',
        '@@ -25,13 +25,8 @@',
        '@@ -40,11 +35,6 @@',
        'ledger_test.go+14',
        '@@ -74,6 +74,20 @@',
        'hunk 1 · dropped',
        'Risks',
      ])
      expect(items[6]).toHaveAttribute('data-tone', 'dropped')

      const decide = screen.getByLabelText('Decision')
      const checks = decide.querySelectorAll('.wb-chk')
      expect(checks).toHaveLength(4)
      expect(checks[0]).toHaveAttribute('data-ok', 'false')
      expect(checks[0]).toHaveTextContent('go test ./... (after adding the regression test, before the fix) · FAIL: TestApplyMovementReturnAddsQuantityOnce')
      expect(checks[3]).toHaveAttribute('data-ok', 'true')
      expect(decide).toHaveTextContent('1 hunk dropped in review')
      expect(decide).toHaveTextContent('fix-sbx-1-recording-a-customer-return-adds-its-qua')
      expect(decide).toHaveTextContent('from main')
      expect(decide).toHaveTextContent('f144936')
      expect(decide).toHaveTextContent('local, not pushed')
      // Push shows the CLI line, since no route pushes.
      fireEvent.click(within(decide).getByRole('button', { name: 'Push branch' }))
      expect(decide).toHaveTextContent('git -C /repo/.sirdar/worktrees/20260915T121451Z-bf19 push -u origin fix-sbx-1-recording-a-customer-return-adds-its-qua')
      // The console ends on the commit and the review.
      const rows = [...rowsIn(console_())]
      expect(rows[rows.length - 2]).toHaveTextContent('branch fix-sbx-1-recording-a-customer-return-adds-its-qua · commit f144936 · local, not pushed')
      expect(rows[rows.length - 1]).toHaveTextContent('dropped ledger_test.go · hunk 1 in review')
      // The rail ends on the review.
      const rail = screen.getByRole('navigation', { name: 'Turns' })
      expect(within(rail).getByRole('separator', { name: 'review' })).toBeInTheDocument()
      const cells = within(rail).getAllByRole('button')
      expect(cells[cells.length - 1]).toHaveTextContent('you')
      expect(within(commandBar()).getByPlaceholderText('Follow up — the run resumes from turn 8 in the same worktree')).toBeInTheDocument()
    })

    it('drops a hunk through the transport and draws what came back', async () => {
      const f = fake({ detail: FIX_RUN, events: fixEvents() })
      renderWorkbench(f, FIX_RUN)
      await screen.findByRole('heading', { name: 'SBX-1' })
      const page = screen.getByRole('region', { name: 'Diff' })
      const drops = await within(page).findAllByRole('button', { name: 'Drop' })
      fireEvent.click(drops[0])
      await waitFor(() =>
        expect(f.transport.dropHunk).toHaveBeenCalledWith('ws1', FIX_RUN.runId, { path: 'ledger.go', hunk: 0, etag: 'etag-fix-1' }),
      )
      await waitFor(() => expect(within(page).getAllByRole('button', { name: 'Drop' })).toHaveLength(2))
      fireEvent.click(within(page).getAllByRole('button', { name: 'Keep' })[0])
      expect(within(page).getByRole('button', { name: 'Kept' })).toHaveAttribute('aria-pressed', 'true')
    })

    it('shows the fix report as the Answer document', async () => {
      renderWorkbench(fake({ detail: FIX_RUN, events: fixEvents() }), FIX_RUN)
      await screen.findByRole('heading', { name: 'SBX-1' })
      fireEvent.click(screen.getByRole('tab', { name: 'Answer' }))
      const page = screen.getByRole('region', { name: 'Answer' })
      expect(within(page).getByRole('heading', { name: "Apply a return's quantity to stock once in ApplyMovement" })).toBeInTheDocument()
      expect(within(page).getByRole('table', { name: 'Tests run' })).toBeInTheDocument()
      expect(within(page).getByText("None: the change follows the note’s proposed fix.")).toBeInTheDocument()
    })
  })

  describe('the dispatcher', () => {
    it('draws the Workbench when the layout preference says so, and the Conversation otherwise', async () => {
      const f = fake()
      const props = {
        transport: f.transport,
        workspaceId: 'ws1',
        runId: TRIAGE_RUN.runId,
        onBack: vi.fn(),
        onOpenReview: vi.fn(),
      }
      const { unmount } = render(
        <PrimaryActionProvider>
          <Session {...props} />
        </PrimaryActionProvider>,
      )
      await screen.findByRole('heading', { name: 'SBX-1' })
      expect(screen.queryByTestId('session-workbench')).not.toBeInTheDocument()
      unmount()
      act(() => setSessionLayout('workbench'))
      render(
        <PrimaryActionProvider>
          <Session {...props} />
        </PrimaryActionProvider>,
      )
      expect(await screen.findByTestId('session-workbench')).toBeInTheDocument()
    })
  })
})
