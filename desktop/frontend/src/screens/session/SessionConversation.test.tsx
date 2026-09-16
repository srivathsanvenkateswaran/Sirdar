import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { AppEvent, RunDetail, RunEvent } from '../../api/types'
import { PrimaryActionProvider, usePrimaryAction } from '../../components/shell/primaryAction'
import { resetRunJobs } from '../../lib/jobs'
import { createFakeTransport, diff, type FakeTransport } from '../../store/fakeTransport'
import {
  FIX_DETAIL,
  FIX_PATCH,
  TRIAGE_DETAIL,
  TRIAGE_NOTE,
  TRIAGE_PROMPT,
  fixEvents,
  triageEvents,
} from './fixtures'
import SessionConversation from './SessionConversation'

/*
 * The six scenes of the mock (docs/design/2026-09-16-session/A), against
 * the fake transport carrying the SBX-1 runs: S1 the finished triage with
 * its answer, S2 the fix run blocked on a command, S3 a search expanded
 * into a table, S4 the bundle, S5 the tools table paired with a card, S6
 * the fix run's change.
 */

interface Fake {
  transport: FakeTransport
  emit: (e: AppEvent) => void
}

function fake(over: { detail?: RunDetail; events?: RunEvent[]; note?: string; prompt?: string; diff?: ReturnType<typeof diff> | null } = {}): Fake {
  const detail = over.detail ?? TRIAGE_DETAIL
  const events = over.events ?? triageEvents()
  const transport = createFakeTransport({ diff: over.diff })
  transport.run = vi.fn(async () => detail)
  transport.events = vi.fn(async () => ({ events, next: events.length }))
  transport.note = vi.fn(async () => over.note ?? '')
  transport.prompt = vi.fn(async () => over.prompt ?? '')
  transport.resume = vi.fn(async () => ({ jobId: 'job-2' }))
  transport.cancel = vi.fn(async () => {})
  const inner = transport.steer
  transport.steer = vi.fn(inner)
  return {
    transport,
    emit: (e) => {
      act(() => transport.emit(e))
    },
  }
}

function Published(): JSX.Element {
  const action = usePrimaryAction()
  return (
    <span data-testid="published">
      {action ? `${action.label}${action.placement === 'screen' ? ' inline' : ''}${action.disabled ? ' disabled' : ''}` : 'none'}
    </span>
  )
}

function renderScene(f: Fake, props: Partial<React.ComponentProps<typeof SessionConversation>> = {}) {
  const onBack = vi.fn()
  const onOpenReview = vi.fn()
  const onStartFix = vi.fn()
  const runId = props.runId ?? TRIAGE_DETAIL.runId
  const view = render(
    <PrimaryActionProvider>
      <Published />
      <SessionConversation
        transport={f.transport}
        workspaceId="ws1"
        runId={runId}
        notesDir="/Users/srivathsanv/Documents/Personal/sirdar-sandbox/notes"
        onBack={onBack}
        onOpenReview={onOpenReview}
        onStartFix={onStartFix}
        {...props}
      />
    </PrimaryActionProvider>,
  )
  return { onBack, onOpenReview, onStartFix, ...view }
}

function badge(): HTMLElement {
  return document.querySelector('.sc-topbar .sd-badge') as HTMLElement
}

/** The composer's one button, by the word it carries. */
function send(name: string | RegExp): HTMLElement {
  return within(screen.getByRole('form')).getByRole('button', { name })
}

const FIX_DIFF = diff({
  branch: 'fix-sbx-1-recording-a-customer-return-adds-its-qua',
  head: 'fix-sbx-1-recording-a-customer-return-adds-its-qua',
  worktree: '/Users/srivathsanv/Documents/Personal/sirdar-sandbox/app/.sirdar/worktrees/20260915T121451Z-bf19',
  files: [
    { path: 'ledger.go', status: 'modified', additions: 1, deletions: 11 },
    { path: 'ledger_test.go', status: 'modified', additions: 14, deletions: 0 },
  ],
  patch: FIX_PATCH,
})

describe('SessionConversation', () => {
  beforeEach(() => {
    resetRunJobs()
    localStorage.clear()
  })
  afterEach(() => {
    localStorage.clear()
  })

  describe('S1 — the finished triage', () => {
    it('heads the window with the run and closes the transcript with the answer', async () => {
      const f = fake({ note: TRIAGE_NOTE })
      renderScene(f, { title: TRIAGE_DETAIL.title })
      await screen.findByRole('heading', { name: 'SBX-1' })

      expect(badge()).toHaveTextContent('completed')
      expect(screen.getByText('triage')).toHaveClass('sd-kind')
      expect(screen.getByText('Product 00219 stock shows 1 more than the movement report')).toHaveClass('sc-title')
      expect(screen.getByText('claude-opus-5', { selector: '.sc-provider__model' })).toBeInTheDocument()
      expect(screen.getByText('turns')).toBeInTheDocument()
      // Nothing is left to cancel.
      expect(screen.queryByRole('button', { name: 'Cancel' })).toBeNull()

      const card = await screen.findByTestId('answer-card')
      expect(within(card).getByRole('heading', { level: 2 })).toHaveTextContent(/^Recording a customer return adds its quantity to stock twice/)
      expect(within(card).getByText('revised after your steer')).toBeInTheDocument()
      // The tags a reader scans first: words, with the count.
      expect(within(card).getByText('classification')).toHaveTextContent('classification code')
      expect(within(card).getByText('confidence')).toHaveTextContent('confidence high')
      expect(within(card).getByText('evidence', { selector: '.sc-tag' })).toHaveTextContent('evidence 7')
      expect(within(card).getByText('code refs')).toHaveTextContent('code refs 7')
      expect(within(card).getByText('open questions', { selector: '.sc-tag' })).toHaveTextContent('open questions 2')
      expect(within(card).getByText('service')).toHaveTextContent('service sandbox/ledger')

      // Five evidence items, the rest behind "more".
      expect(within(card).getAllByRole('listitem').filter((li) => li.closest('.sc-ev'))).toHaveLength(5)
      const more = within(card).getByRole('button', { name: /^2 more:/ })
      expect(more).toHaveTextContent('ledger_test.go:65-86 · thread')
      fireEvent.click(more)
      expect(within(card).getAllByRole('listitem').filter((li) => li.closest('.sc-ev'))).toHaveLength(7)

      // The draft, in the customer's language, as a letter.
      expect(within(card).getByText(/وعليكم السلام أستاذ أحمد/)).toBeInTheDocument()
      // The footer names the note as the vault does.
      expect(within(card).getByText('Note saved')).toBeInTheDocument()
      expect(within(card).getByText('SBX-1 recording-a-customer-return-adds-its-quantity-to-stock-twice.md')).toBeInTheDocument()

      expect(screen.getByTestId('finish-line')).toHaveTextContent('Finished 03:15 · 17 turns · $1.02 · 838k in · 15k out · note written')
      // The composer offers a follow-up, and the sidebar's New session steps down.
      expect(screen.getByRole('textbox', { name: 'Steer' })).toHaveAttribute('placeholder', expect.stringMatching(/^Steer the run or ask a follow-up/))
      await waitFor(() => expect(screen.getByTestId('published')).toHaveTextContent('Steer inline'))
    })

    it('reads the log as a chat: a start line, stacks with a head, thinking stamps, the steer as a bubble, the earlier answer folded', async () => {
      const f = fake()
      renderScene(f)
      const stream = await screen.findByTestId('conversation')
      expect(stream).toHaveAttribute('role', 'log')
      expect(stream).toHaveAttribute('dir', 'ltr')

      expect(within(stream).getByText(/^Run started/)).toHaveTextContent('Run started 00:01 · claude · claude-opus-5 · triage · read-only · budget 60 turns / 20 min / $5')
      expect(within(stream).getByRole('group', { name: '8 calls · 00:03 – 00:10 · all within policy' })).toBeInTheDocument()
      expect(within(stream).getAllByTestId('tool-step')).toHaveLength(13)

      const thinks = within(stream).getAllByTestId('think-stamp')
      expect(thinks[0]).toHaveTextContent('Thought for 8 s~440 tokens·00:19')
      expect(thinks[1]).toHaveTextContent('~1,100 tokens')
      const wrote = within(stream).getAllByTestId('wrote-stamp')
      expect(wrote[1]).toHaveTextContent('Wrote the answer63 s · 02:10 – 03:13')

      const you = within(stream).getByTestId('you-bubble')
      expect(you).toHaveTextContent('You·02:02·resume')
      expect(you).toHaveTextContent('Re-check whether the partial-return path is also affected, and say so in one sentence.')

      const prev = within(stream).getByTestId('answer-prev')
      expect(prev).toHaveTextContent('Answer at 01:41')
      expect(prev).toHaveTextContent('superseded by the revision below')
      // One full card, after the steer.
      expect(within(stream).getAllByTestId('answer-card')).toHaveLength(1)

      // The two denials carry the policy's reason under the line, in words.
      const denied = within(stream).getAllByText('denied by policy')
      expect(denied).toHaveLength(2)
      expect(within(stream).getByText(/passes git "-C" before the subcommand/)).toHaveClass('sc-tc-why')
      // The provider's bookkeeping is not drawn.
      expect(within(stream).queryByText(/stream_event/)).toBeNull()
    })

    it('opens the inspector on the note as a document, with the frontmatter as chips', async () => {
      const f = fake({ note: TRIAGE_NOTE })
      renderScene(f)
      const note = await screen.findByRole('tab', { name: 'Note' })
      expect(note).toHaveAttribute('aria-selected', 'true')
      expect(screen.queryByRole('tab', { name: /Changes/ })).toBeNull()
      expect(screen.getByRole('tab', { name: /Tools/ })).toHaveTextContent('Tools13')

      const doc = await screen.findByTestId('note-document')
      const chips = within(doc).getByRole('list', { name: 'Note metadata' })
      const items = within(chips).getAllByRole('listitem')
      expect(items[0]).toHaveTextContent('support-duty')
      expect(items[0]).toHaveAttribute('data-tag', 'true')
      expect(items[1]).toHaveTextContent('triage')
      const tracker = items.find((i) => i.textContent?.startsWith('tracker'))!
      expect(within(tracker).getByRole('link')).toHaveAttribute('href', 'https://sandbox.local/tracker/SBX-1')
      expect(items.some((i) => i.textContent === 'idSBX-CUST-1')).toBe(true)
      expect(items.some((i) => i.textContent === 'servicesandbox/ledger')).toBe(true)
      // No chip for the url alone: it rode on its key.
      expect(items.some((i) => i.textContent?.startsWith('tracker_url'))).toBe(false)

      expect(within(doc).getByRole('heading', { level: 1 })).toHaveTextContent(/^Recording a customer return/)
      expect(within(doc).getByRole('heading', { name: 'Root Cause Hypothesis' })).toBeInTheDocument()
      // The Arabic block is there, unwrapped from its Obsidian div.
      expect(within(doc).getByText(/السلام عليكم ورحمة الله/)).toBeInTheDocument()
      expect(within(doc).queryByText(/<div dir="rtl">/)).toBeNull()
      // The vault copy can be opened, or its path copied where nothing opens files.
      expect(within(doc).getByRole('button', { name: 'Copy path' })).toBeInTheDocument()
      expect(f.transport.note).toHaveBeenCalledWith('ws1', TRIAGE_DETAIL.runId, 'triage')
    })

    it('follows a file:line reference in the answer to the call that read the file', async () => {
      const f = fake()
      renderScene(f)
      const card = await screen.findByTestId('answer-card')
      const ref = within(card).getAllByRole('button', { name: 'ledger.go:33' })[0]
      fireEvent.click(ref)
      const stream = screen.getByTestId('conversation')
      const tinted = stream.querySelector('[data-on="true"]') as HTMLElement
      expect(tinted).not.toBeNull()
      expect(within(tinted).getByText('Read')).toBeInTheDocument()
      expect(within(tinted).getByText('ledger.go')).toBeInTheDocument()
    })

    it('Open in Note brings the pane back on the note when it is folded', async () => {
      localStorage.setItem('sirdar.sessionPaneCollapsed', '1')
      const f = fake({ note: TRIAGE_NOTE })
      renderScene(f)
      const card = await screen.findByTestId('answer-card')
      expect(screen.queryByRole('tab', { name: 'Note' })).toBeNull()
      fireEvent.click(within(card).getByRole('button', { name: 'Open in Note' }))
      expect(await screen.findByRole('tab', { name: 'Note' })).toHaveAttribute('aria-selected', 'true')
    })
  })

  describe('S2 — blocked, waiting on you', () => {
    const BLOCKED: RunDetail = {
      ...FIX_DETAIL,
      status: 'blocked',
      reason: 'agent asked: Sirdar wants to run a command that requires approval.',
      updatedAt: '2026-09-15T12:15:02Z',
      usage: { turns: 3, inputTokens: 90000, outputTokens: 800, costUsd: 0 },
    }

    it('says who is being waited on, shows the question with the pending command, and focuses the composer to answer', async () => {
      const f = fake({ detail: BLOCKED, events: fixEvents('blocked'), diff: FIX_DIFF })
      renderScene(f, { runId: FIX_DETAIL.runId })
      await screen.findByRole('heading', { name: 'SBX-1' })

      expect(badge()).toHaveTextContent('blocked · waiting on you')
      expect(screen.getByRole('button', { name: 'Cancel' })).toBeInTheDocument()

      // The pending call carries the waiting stamp and no result.
      const stream = screen.getByTestId('conversation')
      const steps = within(stream).getAllByTestId('tool-step')
      const pendingStep = steps[steps.length - 1]
      expect(pendingStep).toHaveTextContent('go test ./...')
      expect(within(pendingStep).getByText('waiting for your approval')).toBeInTheDocument()
      // The edit before it was accepted under acceptEdits, and reads as +14.
      expect(within(steps[2]).getByText('accepted · acceptEdits')).toBeInTheDocument()
      expect(within(steps[2]).getByText('+14')).toBeInTheDocument()

      const ask = screen.getByTestId('ask-card')
      expect(ask).toHaveTextContent('Needs your answer')
      expect(ask).toHaveTextContent('Sirdar wants to run a command that requires approval.')
      expect(within(ask).getByText('go test ./...')).toBeInTheDocument()
      expect(within(ask).getByText('Run tests before the fix')).toBeInTheDocument()

      const box = screen.getByRole('textbox', { name: 'Answer' })
      expect(box).toHaveFocus()
      expect(box).toHaveAttribute('placeholder', 'Answer the question — the run resumes with your message')
      // The send is the wide Answer, and the screen's one filled control.
      const button = send('Answer')
      expect(button.closest('.composer-send')).toHaveAttribute('data-wide', 'true')
      expect(button).toHaveTextContent('Answer')
      await waitFor(() => expect(screen.getByTestId('published')).toHaveTextContent('Answer inline'))

      // The inspector jumps to Changes so the pending test is readable while deciding.
      expect(screen.getByRole('tab', { name: /Changes/ })).toHaveAttribute('aria-selected', 'true')
      await waitFor(() => expect(screen.getByRole('tab', { name: /Changes/ })).toHaveTextContent('Changes2'))
      expect(screen.getByRole('article', { name: 'ledger_test.go' })).toBeInTheDocument()
    })

    it('posts the answer as a resume and puts it in the flow as the operator’s words', async () => {
      const f = fake({ detail: BLOCKED, events: fixEvents('blocked'), diff: FIX_DIFF })
      renderScene(f, { runId: FIX_DETAIL.runId })
      const box = await screen.findByRole('textbox', { name: 'Answer' })
      fireEvent.change(box, { target: { value: 'Allow it once.' } })
      fireEvent.click(send('Answer'))
      await waitFor(() => expect(f.transport.resume).toHaveBeenCalledWith('ws1', FIX_DETAIL.runId, 'Allow it once.'))
      expect(await screen.findByTestId('you-bubble')).toHaveTextContent('Allow it once.')
      expect(box).toHaveValue('')
    })
  })

  describe('S3 — a call expanded in place', () => {
    it('opens the search into its input and a file · line · match table, capped, with As text and Copy', async () => {
      const f = fake()
      renderScene(f)
      const stream = await screen.findByTestId('conversation')
      const row = within(stream).getByRole('button', { name: /Search Go code for partial-return handling/ })
      expect(row).toHaveTextContent('rg -n -i "partial|Quantity" --type go')
      expect(row).toHaveTextContent('11 lines')
      expect(row).toHaveAttribute('aria-expanded', 'false')

      fireEvent.click(row)
      expect(row).toHaveAttribute('aria-expanded', 'true')
      const card = row.closest('[data-testid="tool-step"]') as HTMLElement
      // Input, key by key.
      expect(within(card).getByText('command')).toBeInTheDocument()
      expect(within(card).getByText('description').nextElementSibling).toHaveTextContent('Search Go code for partial-return handling')
      // Output, shaped.
      expect(within(card).getByText('11 rows · 1.1 kB · 3 files')).toBeInTheDocument()
      const table = within(card).getByRole('table')
      expect(within(table).getAllByRole('row')).toHaveLength(12)
      const first = within(table).getAllByRole('row')[1]
      expect(first).toHaveTextContent('ledger_test.go')
      expect(first).toHaveTextContent('9')
      expect(within(card).getByRole('region', { name: 'Bash output' }) ?? within(card).getByLabelText('Bash output')).toBeTruthy()

      // As text flips to the raw stream; the table is gone.
      fireEvent.click(within(card).getByRole('button', { name: 'As text' }))
      expect(within(card).queryByRole('table')).toBeNull()
      expect(within(card).getByText(/ledger_test\.go:9:/)).toBeInTheDocument()
      fireEvent.click(within(card).getByRole('button', { name: 'As table' }))
      expect(within(card).getByRole('table')).toBeInTheDocument()

      // Copy puts the raw output on the clipboard.
      const writeText = vi.fn(async () => {})
      Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true })
      fireEvent.click(within(card).getByRole('button', { name: 'Copy' }))
      await waitFor(() => expect(writeText).toHaveBeenCalledWith(expect.stringContaining('ledger_test.go:9:')))
      expect(await within(card).findByRole('button', { name: 'Copied' })).toBeInTheDocument()

      // A second click folds it.
      fireEvent.click(row)
      expect(row).toHaveAttribute('aria-expanded', 'false')
      expect(within(card).queryByRole('table')).toBeNull()
    })

    it('shows a file read as numbered lines and a test run with its verdict lines marked', async () => {
      const f = fake({ detail: FIX_DETAIL, events: fixEvents('done'), diff: FIX_DIFF })
      renderScene(f, { runId: FIX_DETAIL.runId })
      const stream = await screen.findByTestId('conversation')

      fireEvent.click(within(stream).getAllByRole('button', { name: /ledger\.go$/ })[0])
      const read = within(stream).getAllByTestId('tool-step')[0]
      expect(within(read).getByText('package ledger')).toBeInTheDocument()
      expect(within(read).getByText('7 lines · 245 B')).toBeInTheDocument()

      const test = within(stream).getByRole('button', { name: /Run tests before the fix/ })
      expect(within(test).getByText('exit 1')).toBeInTheDocument()
      fireEvent.click(test)
      const card = test.closest('[data-testid="tool-step"]') as HTMLElement
      expect(within(card).getByText(/--- FAIL: TestApplyMovementReturnAddsQuantityOnce/)).toHaveClass('sc-code__fail')
      expect(within(card).getByText(/ledger_test.go:82: CurrentStock = 12, want 11/)).toHaveClass('sc-code__fail')
    })
  })

  describe('S4 — the bundle', () => {
    it('draws the ticket as cards, the thread as bubbles, the empty attachments honestly, and the playbooks', async () => {
      const f = fake({ prompt: TRIAGE_PROMPT })
      renderScene(f)
      fireEvent.click(await screen.findByRole('tab', { name: 'Bundle' }))
      const bundle = await screen.findByTestId('bundle-view')

      const tracker = within(bundle).getByRole('region', { name: 'Tracker' })
      expect(within(tracker).getByText('SBX-1')).toBeInTheDocument()
      expect(within(tracker).getByText('normal')).toBeInTheDocument()
      expect(within(tracker).getByRole('heading', { level: 3 })).toHaveTextContent('Product 00219 stock shows 1 more than the movement report')
      expect(within(tracker).getByRole('link')).toHaveAttribute('href', 'https://sandbox.local/tracker/SBX-1')
      expect(within(tracker).getByText('متجر الفهد للأدوات المنزلية')).toBeInTheDocument()

      const helpdesk = within(bundle).getByRole('region', { name: 'Helpdesk' })
      expect(within(helpdesk).getByText('#88341')).toBeInTheDocument()
      expect(within(helpdesk).getByText('أحمد الفهد')).toBeInTheDocument()

      const thread = within(bundle).getByRole('region', { name: 'Conversation' })
      expect(within(thread).getByText('4 messages · original language')).toBeInTheDocument()
      const bubbles = thread.querySelectorAll('.sc-bub')
      expect(bubbles).toHaveLength(4)
      expect(bubbles[0]).toHaveAttribute('data-role', 'customer')
      expect(bubbles[1]).toHaveAttribute('data-role', 'agent')
      expect(bubbles[1]).toHaveTextContent('Layla (L1)')
      expect(bubbles[3]).toHaveTextContent('تمام، وصلتنا التفاصيل.')

      expect(within(bundle).getByRole('region', { name: 'Attachments' })).toHaveTextContent('None in this bundle.')

      const playbooks = within(bundle).getByRole('region', { name: 'Playbooks' })
      expect(within(playbooks).getByText('3 in the prompt')).toBeInTheDocument()
      const chips = within(playbooks).getAllByRole('tab')
      expect(chips.map((c) => c.textContent)).toEqual(['00-environment', '10-helpdesk', '50-code'])
      expect(within(playbooks).getByRole('tabpanel')).toHaveTextContent(/^This workspace is a single Go module/)
      fireEvent.click(chips[1])
      expect(within(playbooks).getByRole('tabpanel')).toHaveTextContent('The helpdesk is the sandbox desk.')
    })

    it('says so when the prompt has not been written', async () => {
      const f = fake({ prompt: '' })
      renderScene(f)
      fireEvent.click(await screen.findByRole('tab', { name: 'Bundle' }))
      const bundle = await screen.findByTestId('bundle-view')
      expect(within(bundle).getByText('The prompt has not been written yet.')).toBeInTheDocument()
      expect(within(bundle).getByText('The prompt carried no conversation.')).toBeInTheDocument()
    })
  })

  describe('S5 — the tools table', () => {
    it('lists every call with its decision, took and output, totals them, sorts, and pairs a row with its card', async () => {
      const f = fake()
      renderScene(f)
      fireEvent.click(await screen.findByRole('tab', { name: /Tools/ }))

      expect(screen.getByText('13', { selector: '.sc-ttsum b' })).toBeInTheDocument()
      expect(screen.getByText('denied').closest('.sc-ttsum span')).toHaveTextContent('2 denied')
      expect(screen.getByText('by policy').closest('span')).toHaveTextContent('11 by policy')

      const table = screen.getByRole('table')
      const rows = within(table).getAllByRole('row').slice(1)
      expect(rows).toHaveLength(13)
      expect(rows[0]).toHaveTextContent('1')
      expect(rows[0]).toHaveTextContent('00:03')
      expect(rows[0]).toHaveTextContent('Bash')
      expect(rows[0]).toHaveTextContent('policy')
      expect(rows[8]).toHaveAttribute('data-deny', 'true')
      expect(within(rows[8]).getByText('denied')).toBeInTheDocument()
      expect(rows[2]).toHaveTextContent('245 B · 7 ln')

      // Sort by tool: the Bash calls first, and the header says so.
      fireEvent.click(within(table).getByRole('button', { name: 'tool' }))
      const sorted = within(table).getAllByRole('row').slice(1)
      expect(sorted[0]).toHaveTextContent('Bash')
      expect(within(table).getByRole('columnheader', { name: 'tool' })).toHaveAttribute('aria-sort', 'ascending')
      fireEvent.click(within(table).getByRole('button', { name: 'tool' }))
      expect(within(table).getByRole('columnheader', { name: 'tool' })).toHaveAttribute('aria-sort', 'descending')
      expect(within(table).getAllByRole('row').slice(1)[0]).toHaveTextContent('Read')

      // Clicking a row tints it and its card.
      const target = within(table).getByRole('row', { name: 'Show call 12 in the transcript' })
      fireEvent.click(target)
      expect(target).toHaveAttribute('data-on', 'true')
      const stream = screen.getByTestId('conversation')
      const card = stream.querySelector('[data-on="true"]') as HTMLElement
      expect(card).toHaveTextContent('rg -n "Return|restock" --type go')
    })

    it('Open in Tools on a card opens the table on that row', async () => {
      const f = fake()
      renderScene(f)
      const stream = await screen.findByTestId('conversation')
      fireEvent.click(within(stream).getByRole('button', { name: /Search Go code for partial-return handling/ }))
      fireEvent.click(within(stream).getByRole('button', { name: 'Open in Tools' }))
      expect(screen.getByRole('tab', { name: /Tools/ })).toHaveAttribute('aria-selected', 'true')
      const table = screen.getByRole('table')
      const on = table.querySelector('tr[data-on="true"]') as HTMLElement
      expect(on).toHaveTextContent('rg -n -i "partial|Quantity" --type go')
    })
  })

  describe('S6 — the fix run’s change', () => {
    it('closes with the fix report and opens the inspector on the change with its checks', async () => {
      const f = fake({ detail: FIX_DETAIL, events: fixEvents('done'), diff: FIX_DIFF })
      const { onOpenReview } = renderScene(f, { runId: FIX_DETAIL.runId })
      await screen.findByRole('heading', { name: 'SBX-1' })
      expect(screen.getByText('fix')).toHaveClass('sd-kind')
      expect(badge()).toHaveTextContent('completed')

      const card = await screen.findByRole('region', { name: 'Fix report' })
      expect(within(card).getByRole('heading', { level: 2 })).toHaveTextContent("Apply a return's quantity to stock once in ApplyMovement")
      expect(within(card).getByText('tests')).toHaveTextContent('tests 4 run · 3 ok')
      expect(within(card).getByText('files')).toHaveTextContent('files ledger.go · ledger_test.go')
      expect(within(card).getByText('deviation from note')).toHaveTextContent('deviation from note none')
      expect(within(card).getByRole('heading', { name: 'What changed' })).toBeInTheDocument()
      expect(within(card).getByRole('heading', { name: 'Risks' })).toBeInTheDocument()
      expect(within(card).getByText('Committed')).toBeInTheDocument()
      expect(within(card).getByText('f144936')).toBeInTheDocument()
      expect(within(card).getByText('fix-sbx-1-recording-a-customer-return-adds-its-qua', { selector: '.sc-ans__branch' })).toBeInTheDocument()

      // The stamps on the way: the report written, the failing test, the passing run.
      const stream = screen.getByTestId('conversation')
      expect(within(stream).getByTestId('wrote-stamp')).toHaveTextContent('Wrote the report')
      expect(within(stream).getByText('exit 1')).toBeInTheDocument()
      expect(within(stream).getAllByText('approved')).toHaveLength(2)
      expect(screen.getByTestId('finish-line')).toHaveTextContent('Finished 00:33 · 8 turns · $0.56 · 340k in · 2.2k out · local branch, not pushed')
      expect(screen.getByRole('textbox', { name: 'Steer' })).toHaveAttribute('placeholder', 'Ask for a change to the fix — it resumes in the same worktree')

      // Changes: two files, the four checks with the pre-fix FAIL first, the branch.
      const changes = screen.getByRole('tab', { name: /Changes/ })
      expect(changes).toHaveAttribute('aria-selected', 'true')
      await waitFor(() => expect(changes).toHaveTextContent('Changes2'))
      expect(screen.getAllByRole('article')).toHaveLength(2)
      expect(screen.getByRole('region', { name: 'ledger.go hunk 1' })).toBeInTheDocument()
      const checks = within(screen.getByRole('list', { name: 'Checks' })).getAllByRole('listitem')
      expect(checks).toHaveLength(4)
      expect(checks[0]).toHaveTextContent('failed')
      expect(checks[0]).toHaveTextContent('CurrentStock = 12, want 11')
      expect(checks[3]).toHaveTextContent('ok')
      expect(checks[3]).toHaveTextContent('go test ./...')
      expect(screen.getByText('fix-sbx-1-recording-a-customer-return-adds-its-qua', { selector: '.branch-name' })).toBeInTheDocument()

      fireEvent.click(screen.getByRole('button', { name: 'Open review' }))
      expect(onOpenReview).toHaveBeenCalled()
      // The card's footer opens the same tab.
      fireEvent.click(screen.getByRole('tab', { name: 'Note' }))
      fireEvent.click(within(card).getByRole('button', { name: 'Review changes' }))
      expect(screen.getByRole('tab', { name: /Changes/ })).toHaveAttribute('aria-selected', 'true')
    })
  })

  describe('while the run works', () => {
    it('marks the newest call running, keeps the composer down with the reason, and follows live events', async () => {
      const running: RunDetail = { ...TRIAGE_DETAIL, status: 'running', usage: { turns: 2, inputTokens: 1000, outputTokens: 20, costUsd: 0.05 } }
      const events = triageEvents().slice(0, 12)
      const f = fake({ detail: running, events })
      renderScene(f)
      const stream = await screen.findByTestId('conversation')

      expect(badge()).toHaveTextContent('running')
      expect(screen.getByRole('textbox', { name: 'Steer' })).toBeDisabled()
      expect(send('Steer')).toHaveAttribute('title', 'The run is still working. Wait for it, or cancel it.')
      expect(screen.queryByTestId('finish-line')).toBeNull()

      f.emit({
        kind: 'run.event',
        workspaceId: 'ws1',
        runId: TRIAGE_DETAIL.runId,
        index: 40,
        event: {
          t: '2026-09-15T12:11:30Z',
          kind: 'tool_started',
          payload: { tool: 'Bash', raw: { type: 'assistant', message: { content: [{ type: 'tool_use', id: 'tu-live', name: 'Bash', input: { command: 'go vet ./...', description: 'Vet the module' } }] } } },
        },
      })
      const step = await within(stream).findByRole('button', { name: /Vet the module/ })
      expect(within(step).getByText('running')).toBeInTheDocument()
      expect(screen.getByRole('tab', { name: /Tools/ })).toHaveTextContent('Tools7')
    })

    it('says why a run could not be read', async () => {
      const f = fake()
      f.transport.run = vi.fn(async () => {
        throw new Error('not_found: no such run')
      })
      renderScene(f)
      expect(await screen.findByText('not_found: no such run')).toBeInTheDocument()
    })
  })

  describe('the pane', () => {
    it('folds to a rail and remembers it, and the tabs are one stop moved by the arrows', async () => {
      const f = fake({ note: TRIAGE_NOTE })
      const { container } = renderScene(f)
      const note = await screen.findByRole('tab', { name: 'Note' })
      const bundle = screen.getByRole('tab', { name: 'Bundle' })
      note.focus()
      fireEvent.keyDown(screen.getByRole('tablist'), { key: 'ArrowRight' })
      expect(bundle).toHaveAttribute('aria-selected', 'true')
      expect(document.activeElement).toBe(bundle)

      fireEvent.click(screen.getByRole('button', { name: 'Hide panel' }))
      expect(container.querySelector('.sc-body')).toHaveAttribute('data-pane', 'collapsed')
      expect(localStorage.getItem('sirdar.sessionPaneCollapsed')).toBe('1')
      fireEvent.click(screen.getByRole('button', { name: 'Tools' }))
      expect(container.querySelector('.sc-body')).not.toHaveAttribute('data-pane')
      expect(screen.getByRole('tab', { name: /Tools/ })).toHaveAttribute('aria-selected', 'true')
    })

    it('goes back on Escape unless the reader is typing', async () => {
      const f = fake()
      const { onBack } = renderScene(f)
      await screen.findByRole('heading', { name: 'SBX-1' })
      fireEvent.keyDown(screen.getByRole('textbox', { name: 'Steer' }), { key: 'Escape' })
      expect(onBack).not.toHaveBeenCalled()
      act(() => {
        window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }))
      })
      expect(onBack).toHaveBeenCalled()
    })
  })
})
