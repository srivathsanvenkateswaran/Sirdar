import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { RepoSummary, RunSummary, Workspace } from '../api/types'
import { PrimaryActionProvider, usePrimaryAction } from '../components/shell/primaryAction'
import { composerPrefs, resetComposerPrefs, setIntentAssist } from '../lib/composerPrefs'
import { resetRunJobs, setRunJob } from '../lib/jobs'
import { createFakeTransport, run, ticket, workspace, type FakeTransport } from '../store/fakeTransport'
import NewSession, {
  hasTriageNote,
  lastUsedModel,
  newestFirst,
  ticketSource,
  type SessionMode,
  type StartOverrides,
} from './NewSession'

afterEach(() => {
  resetRunJobs()
  globalThis.localStorage?.clear()
  resetComposerPrefs()
})

/**
 * The sidebar footer, as far as these cases care: what the screen published,
 * and where it asked for it to be drawn. New session draws Start itself and
 * asks the footer to stand down, so this reads the placement rather than
 * drawing a second button.
 */
function PrimarySlot(): JSX.Element | null {
  const action = usePrimaryAction()
  if (!action) return null
  return (
    <p data-testid="primary">
      {action.label} · {action.placement ?? 'footer'} · {action.disabled ? 'off' : 'on'}
    </p>
  )
}

const TRIAGED = run({ runId: 'r-t', key: 'OMNI-2', kind: 'triage', status: 'completed' })

function mount(
  over: {
    transport?: FakeTransport
    runs?: RunSummary[]
    workspace?: Workspace
    workspaces?: Workspace[]
    onSelectWorkspace?: (id: string) => void
    onStart?: (mode: SessionMode, key: string, o: StartOverrides) => Promise<string>
    repos?: RepoSummary[]
  } = {},
) {
  const transport = over.transport ?? createFakeTransport({ tickets: [] })
  const onStart = over.onStart ?? vi.fn(async () => 'job-1')
  const onOpenRun = vi.fn()
  const runs = over.runs ?? []
  const ws = over.workspace ?? workspace()
  const view = render(
    <PrimaryActionProvider>
      <NewSession
        transport={transport}
        workspaceId="ws1"
        workspace={ws}
        workspaces={over.workspaces}
        onSelectWorkspace={over.onSelectWorkspace}
        runs={runs}
        repos={over.repos}
        onStart={onStart}
        onOpenRun={onOpenRun}
      />
      <PrimarySlot />
    </PrimaryActionProvider>,
  )
  function rerender(next: RunSummary[]): void {
    view.rerender(
      <PrimaryActionProvider>
        <NewSession
          transport={transport}
          workspaceId="ws1"
          workspace={ws}
          runs={next}
          onStart={onStart}
          onOpenRun={onOpenRun}
        />
        <PrimarySlot />
      </PrimaryActionProvider>,
    )
  }
  return { transport, onStart, onOpenRun, rerender }
}

const bar = () =>
  screen.getByRole('textbox', {
    name: 'What to look at: a ticket key or URL, and anything you want to say',
  })
const modelChip = () => screen.getByRole('button', { name: /^Model/ })

describe('lastUsedModel', () => {
  it('is what the newest run on the provider reported, or nothing', () => {
    const runs = [
      run({ runId: 'a', provider: 'claude', model: 'claude-opus-5', updatedAt: '2026-09-10T09:00:00Z' }),
      run({ runId: 'b', provider: 'claude', model: 'claude-sonnet-5', updatedAt: '2026-09-11T09:00:00Z' }),
      run({ runId: 'c', provider: 'claude', model: '', updatedAt: '2026-09-12T09:00:00Z' }),
      run({ runId: 'd', provider: 'codex', model: 'gpt-5.6-luna', updatedAt: '2026-09-13T09:00:00Z' }),
    ]
    // A newer run that has not reported its model does not blank the answer.
    expect(lastUsedModel(runs, 'claude')).toBe('claude-sonnet-5')
    expect(lastUsedModel(runs, 'codex')).toBe('gpt-5.6-luna')
    expect(lastUsedModel(runs, 'qwen')).toBe('')
    expect(lastUsedModel(runs, '')).toBe('')
  })
})
/** The card's round send. Its name says what pressing it does right now. */
const sendButton = () =>
  within(screen.getByRole('form', { name: 'Start' })).getByRole('button', {
    name: /^(Start|Starting|Read this|Reading)/,
  })
const modeChip = () => screen.getByRole('button', { name: /^Mode:/ })
const accessChip = () => screen.getByRole('button', { name: /^Access:/ })

describe('hasTriageNote', () => {
  it('is true only for a completed triage of that key', () => {
    expect(hasTriageNote([TRIAGED], 'OMNI-2')).toBe(true)
    expect(hasTriageNote([TRIAGED], 'OMNI-3')).toBe(false)
    expect(hasTriageNote([run({ key: 'OMNI-2', kind: 'triage', status: 'running' })], 'OMNI-2')).toBe(false)
    expect(hasTriageNote([run({ key: 'OMNI-2', kind: 'fix', status: 'completed' })], 'OMNI-2')).toBe(false)
  })
})

describe('ticketSource', () => {
  it('names the tracker or helpdesk off the ticket URL', () => {
    expect(ticketSource(ticket({ url: 'https://acme.atlassian.net/browse/OMNI-1' }))).toBe('jira')
    expect(ticketSource(ticket({ url: 'https://issues.corp.example/browse/OMNI-1' }))).toBe('jira')
    expect(ticketSource(ticket({ url: 'https://linear.app/acme/issue/SBX-1' }))).toBe('linear')
    expect(ticketSource(ticket({ url: 'https://acme.zendesk.com/agent/tickets/9' }))).toBe('zendesk')
    expect(ticketSource(ticket({ url: 'https://desk.zoho.com/agent/acme/tickets/9' }))).toBe('zoho')
    expect(ticketSource(ticket({ url: 'https://tracker.corp.example/t/9' }))).toBe('corp')
    expect(ticketSource(ticket({ url: '' }))).toBe('tracker')
    expect(ticketSource(ticket({ url: '', helpdeskRef: 'ZD-9' }))).toBe('helpdesk')
  })
})

describe('newestFirst', () => {
  it('orders by last change and keeps five', () => {
    const tickets = [1, 2, 3, 4, 5, 6].map((n) =>
      ticket({ key: `OMNI-${n}`, updatedAt: `2026-09-10T0${n}:00:00Z` }),
    )
    expect(newestFirst(tickets).map((t) => t.key)).toEqual([
      'OMNI-6',
      'OMNI-5',
      'OMNI-4',
      'OMNI-3',
      'OMNI-2',
    ])
  })
})

describe('NewSession', () => {
  it('asks what to look at in the workspace, and draws the card, the three chips and its own send', async () => {
    mount()
    expect(screen.getByRole('heading', { name: /What should we look at in/ })).toHaveTextContent(
      'What should we look at in omni?',
    )
    expect(bar()).toHaveFocus()
    expect(bar()).toHaveAttribute('placeholder', 'A ticket key, a #helpdesk number, or a ticket or Slack link')
    const form = screen.getByRole('form', { name: 'Start' })
    const chips = form.querySelector('.composer-bar__chips')!
    expect(within(chips as HTMLElement).getAllByRole('button').map((b) => b.getAttribute('aria-label'))).toEqual([
      'Model claude · Sonnet',
      'Mode: Triage',
      'Access: Read-only',
    ])
    expect(screen.getByRole('img', { name: 'Claude' })).toBeInTheDocument()
    // The send is on the screen, filled and round, off until there is a key;
    // the footer is told to stand down rather than draw a second one.
    expect(sendButton()).toHaveAttribute('data-variant', 'primary')
    expect(sendButton()).toHaveAttribute('data-icon-only', 'true')
    expect(sendButton()).toBeDisabled()
    expect(sendButton()).toHaveAttribute(
      'title',
      'No ticket yet — type a key like SBX-1 or a helpdesk number like #28310, or paste a ticket or Slack link',
    )
    await waitFor(() => expect(screen.getByTestId('primary')).toHaveTextContent('Start · screen · off'))
    // The Playbook chip is gone; nothing in the bar is a fact without a menu.
    expect(screen.queryByText('Playbook')).toBeNull()
  })

  it('names the workspace as the switcher: the word opens the list and picks another', () => {
    const onSelectWorkspace = vi.fn()
    mount({
      workspaces: [workspace(), workspace({ id: 'ws2', name: 'billing', root: '/repos/billing' })],
      onSelectWorkspace,
    })
    const word = screen.getByRole('button', { name: 'omni. Change workspace' })
    expect(word).toHaveClass('switcher-word')
    fireEvent.click(word)
    const list = screen.getByRole('listbox', { name: 'Workspaces' })
    expect(list.style.position).toBe('fixed')
    fireEvent.click(within(list).getByRole('option', { name: /billing/ }))
    expect(onSelectWorkspace).toHaveBeenCalledWith('ws2')
    expect(screen.queryByRole('listbox')).toBeNull()
  })

  it('starts a triage for the key in the box and opens the run once the job has one', async () => {
    const { onStart, onOpenRun, rerender } = mount()
    fireEvent.change(bar(), { target: { value: 'omni-2510' } })
    expect(sendButton()).toBeEnabled()
    expect(sendButton()).toHaveAttribute('title', 'Triage · OMNI-2510 (↵)')
    fireEvent.click(sendButton())

    await waitFor(() =>
      expect(onStart).toHaveBeenCalledWith('triage', 'OMNI-2510', {
        provider: undefined,
        model: undefined,
        dryRun: undefined,
        instruction: undefined,
        noPr: undefined,
        local: undefined,
        prUrl: undefined,
        resolution: undefined,
      }),
    )
    expect(await screen.findByRole('button', { name: 'Starting…' })).toHaveAttribute('aria-disabled', 'true')
    expect(onOpenRun).not.toHaveBeenCalled()

    // The store pairs the job with the run as its first update arrives; the
    // screen opens that run and not the older one for the same key.
    const older = run({ runId: 'r-old', key: 'OMNI-2510', status: 'completed' })
    const fresh = run({ runId: 'r-new', key: 'OMNI-2510', status: 'preparing' })
    act(() => setRunJob('r-new', 'job-1'))
    rerender([older, fresh])
    await waitFor(() => expect(onOpenRun).toHaveBeenCalledWith('r-new'))
  })

  it('opens the run through the newest open callback, not the one it was mounted with', async () => {
    const transport = createFakeTransport({ tickets: [] })
    const onStart = vi.fn(async () => 'job-1')
    const first = vi.fn()
    const second = vi.fn()
    const tree = (runs: RunSummary[], onOpenRun: (id: string) => void) => (
      <PrimaryActionProvider>
        <NewSession
          transport={transport}
          workspaceId="ws1"
          workspace={workspace()}
          runs={runs}
          onStart={onStart}
          onOpenRun={onOpenRun}
        />
      </PrimaryActionProvider>
    )
    const view = render(tree([], first))
    fireEvent.change(bar(), { target: { value: 'OMNI-2510' } })
    fireEvent.click(sendButton())
    await waitFor(() => expect(onStart).toHaveBeenCalled())

    // App hands over a fresh callback every render; the pairing lands after.
    view.rerender(tree([], second))
    act(() => setRunJob('r-new', 'job-1'))
    view.rerender(tree([run({ runId: 'r-new', key: 'OMNI-2510', status: 'preparing' })], second))
    await waitFor(() => expect(second).toHaveBeenCalledWith('r-new'))
    expect(first).not.toHaveBeenCalled()
  })

  it('reads the key out of a tracker URL, and sends on Cmd with Enter', async () => {
    const { onStart } = mount()
    fireEvent.change(bar(), { target: { value: 'https://acme.atlassian.net/browse/OMNI-77' } })
    fireEvent.keyDown(bar(), { key: 'Enter', metaKey: true })
    await waitFor(() => expect(onStart).toHaveBeenCalledWith('triage', 'OMNI-77', expect.anything()))
  })

  it('carries what was typed around the key as the run\u2019s instruction, and says so in the chips', async () => {
    const { onStart } = mount()
    fireEvent.change(bar(), {
      target: { value: 'OMNI-2510 check the tax rounding on invoice lines' },
    })
    expect(screen.getByRole('status')).toHaveTextContent('Triage · OMNI-2510 · with your note')
    expect(sendButton()).toHaveAttribute('title', 'Triage · OMNI-2510 · with your note (↵)')
    fireEvent.click(sendButton())
    await waitFor(() =>
      expect(onStart).toHaveBeenCalledWith(
        'triage',
        'OMNI-2510',
        expect.objectContaining({ instruction: 'check the tax rounding on invoice lines' }),
      ),
    )
  })

  it('a mode word sets the Mode chip live, and a chosen mode wins from then on', () => {
    mount({ runs: [TRIAGED] })
    fireEvent.change(bar(), { target: { value: 'fix OMNI-2' } })
    expect(modeChip()).toHaveAccessibleName('Mode: Fix')
    expect(screen.getByRole('status')).toHaveTextContent('Fix · OMNI-2')

    fireEvent.click(modeChip())
    fireEvent.click(screen.getByRole('menuitemradio', { name: /Triage/ }))
    expect(modeChip()).toHaveAccessibleName('Mode: Triage')
    // The word is still in the box; the pick outranks it.
    fireEvent.change(bar(), { target: { value: 'fix OMNI-2 now' } })
    expect(modeChip()).toHaveAccessibleName('Mode: Triage')
  })

  it('says no ticket is named yet, and does not start, on a line with none', () => {
    const { onStart } = mount()
    fireEvent.change(bar(), { target: { value: 'the export is slow since Tuesday' } })
    expect(screen.getByRole('status')).toHaveTextContent(
      'No ticket named yet — press Enter and I will read what you typed',
    )
    expect(onStart).not.toHaveBeenCalled()
  })

  it('starts an RCA and a fix for a key that has a triage note, from the Mode menu', async () => {
    const { onStart } = mount({ runs: [TRIAGED] })
    fireEvent.change(bar(), { target: { value: 'OMNI-2' } })
    fireEvent.click(modeChip())
    fireEvent.click(screen.getByRole('menuitemradio', { name: /RCA/ }))
    expect(modeChip()).toHaveAccessibleName('Mode: RCA')
    fireEvent.click(sendButton())
    await waitFor(() => expect(onStart).toHaveBeenLastCalledWith('rca', 'OMNI-2', expect.anything()))

    // A dry run is a triage's or a fix's option, never an RCA's.
    expect(screen.getByLabelText(/Dry run/)).toBeDisabled()
  })

  it('says what the mode does to the tree on the Access chip, which explains and does not change', () => {
    mount({ runs: [TRIAGED] })
    expect(accessChip()).toHaveAccessibleName('Access: Read-only')
    expect(accessChip()).toHaveAttribute('aria-haspopup', 'dialog')
    fireEvent.click(accessChip())
    const dialog = screen.getByRole('dialog', { name: 'Access' })
    expect(within(dialog).queryByRole('menuitemradio')).toBeNull()
    expect(dialog).toHaveTextContent('Nothing is written.')
    expect(dialog).toHaveTextContent('Sirdar commits and pushes, never the agent.')
    fireEvent.keyDown(dialog, { key: 'Escape' })

    // Fix runs in a worktree; the chip follows the mode.
    fireEvent.change(bar(), { target: { value: 'OMNI-2' } })
    fireEvent.click(modeChip())
    fireEvent.click(screen.getByRole('menuitemradio', { name: /Fix/ }))
    expect(accessChip()).toHaveAccessibleName('Access: Worktree')
  })

  it('starts a fix with the provider and model from the chip and dry run from More options', async () => {
    const { onStart } = mount({ runs: [TRIAGED] })
    fireEvent.change(bar(), { target: { value: 'OMNI-2' } })
    fireEvent.click(modeChip())
    fireEvent.click(screen.getByRole('menuitemradio', { name: /Fix/ }))
    fireEvent.click(modelChip())
    const popover = screen.getByRole('dialog', { name: 'Provider and model' })
    fireEvent.click(within(popover).getByRole('tab', { name: 'Codex' }))
    // The search field is the free-text entry: an id no list has is typed
    // there and taken with Enter.
    const search = within(popover).getByRole('searchbox', { name: 'Search models' })
    fireEvent.change(search, { target: { value: ' o3 ' } })
    fireEvent.keyDown(search, { key: 'Enter' })
    // The chip follows the override, so the reader sees what will run.
    expect(modelChip()).toHaveAccessibleName('Model codex · o3')
    // Provider and model are not under More options; dry run still is.
    fireEvent.click(screen.getByLabelText(/Dry run/))
    expect(screen.queryByLabelText('Provider')).toBeNull()

    fireEvent.click(sendButton())
    await waitFor(() =>
      expect(onStart).toHaveBeenCalledWith('fix', 'OMNI-2', {
        provider: 'codex',
        model: 'o3',
        dryRun: true,
        instruction: undefined,
        noPr: undefined,
        local: undefined,
        prUrl: undefined,
        resolution: undefined,
      }),
    )
  })

  it('carries a model chosen on the workspace own provider without a provider override', async () => {
    const { onStart } = mount()
    fireEvent.change(bar(), { target: { value: 'OMNI-9' } })
    fireEvent.click(modelChip())
    const popover = screen.getByRole('dialog', { name: 'Provider and model' })
    expect(within(popover).getByRole('tab', { name: 'Claude, workspace default' })).toBeInTheDocument()
    fireEvent.click(within(popover).getByRole('option', { name: /Sonnet 5/ }))
    expect(screen.queryByRole('dialog')).toBeNull()
    expect(modelChip()).toHaveAccessibleName('Model claude · Sonnet 5')

    fireEvent.click(sendButton())
    await waitFor(() =>
      expect(onStart).toHaveBeenCalledWith('triage', 'OMNI-9', {
        provider: undefined,
        model: 'claude-sonnet-5',
        dryRun: undefined,
        instruction: undefined,
        noPr: undefined,
        local: undefined,
        prUrl: undefined,
        resolution: undefined,
      }),
    )
  })

  describe('the Model chip', () => {
    const noModel = () => workspace({ model: '' })

    it('names the workspace model when the config has one', () => {
      mount()
      expect(modelChip()).toHaveAccessibleName('Model claude · Sonnet')
    })

    it('says CLI default when the config names no model and no run has reported one', () => {
      mount({ workspace: noModel() })
      expect(modelChip()).toHaveAccessibleName('Model claude · CLI default')
    })

    it('appends what the newest run on that provider reported', () => {
      mount({
        workspace: noModel(),
        runs: [
          run({ runId: 'a', provider: 'claude', model: 'claude-opus-5', updatedAt: '2026-09-10T09:00:00Z' }),
          run({ runId: 'b', provider: 'claude', model: 'claude-sonnet-5', updatedAt: '2026-09-11T09:00:00Z' }),
          run({ runId: 'c', provider: 'codex', model: 'gpt-5.6-luna', updatedAt: '2026-09-12T09:00:00Z' }),
        ],
      })
      expect(modelChip()).toHaveAccessibleName('Model claude · CLI default · last used claude-sonnet-5')
      // Switching provider follows that provider's newest run instead.
      fireEvent.click(modelChip())
      const popover = screen.getByRole('dialog', { name: 'Provider and model' })
      fireEvent.click(within(popover).getByRole('tab', { name: 'Codex' }))
      expect(modelChip()).toHaveAccessibleName('Model codex · CLI default · last used gpt-5.6-luna')
    })

    it('drops the last-used clause once a model is chosen', () => {
      mount({ workspace: noModel(), runs: [run({ provider: 'claude', model: 'claude-sonnet-5' })] })
      fireEvent.click(modelChip())
      const popover = screen.getByRole('dialog', { name: 'Provider and model' })
      fireEvent.click(within(popover).getByRole('option', { name: /Opus 5/ }))
      expect(modelChip()).toHaveAccessibleName('Model claude · Opus 5')
    })
  })

  it('turns RCA and Fix off in the menu, with the reason, while the key has no triage note', () => {
    mount({ runs: [TRIAGED] })
    fireEvent.change(bar(), { target: { value: 'OMNI-3' } })
    fireEvent.click(modeChip())
    const rca = screen.getByRole('menuitemradio', { name: /RCA/ })
    const fix = screen.getByRole('menuitemradio', { name: /Fix/ })
    expect(rca).toHaveAttribute('aria-disabled', 'true')
    expect(fix).toHaveAttribute('aria-disabled', 'true')
    expect(rca).toHaveAttribute('title', 'Needs a triage note for OMNI-3 first')
    fireEvent.click(rca)
    expect(modeChip()).toHaveAccessibleName('Mode: Triage')
    fireEvent.keyDown(screen.getByRole('menu'), { key: 'Escape' })
    // On a triage the missing note is a note under the chips, not in place
    // of them: it stops an RCA and a fix, and nothing else.
    expect(screen.getByRole('status')).toHaveTextContent('Triage · OMNI-3')
    expect(
      screen.getByText('RCA and Fix need a triage note for OMNI-3 first. Start a triage.'),
    ).toBeInTheDocument()
    // Triage itself is still on.
    expect(sendButton()).toBeEnabled()

    // A note arriving turns them back on.
    fireEvent.change(bar(), { target: { value: 'OMNI-2' } })
    fireEvent.click(modeChip())
    expect(screen.getByRole('menuitemradio', { name: /RCA/ })).not.toHaveAttribute('aria-disabled')
    expect(screen.getByRole('menuitemradio', { name: /Fix/ })).not.toHaveAttribute('aria-disabled')
  })

  it('keeps the send off when the chosen mode is one the key cannot run yet', () => {
    mount({ runs: [TRIAGED] })
    fireEvent.change(bar(), { target: { value: 'OMNI-2' } })
    fireEvent.click(modeChip())
    fireEvent.click(screen.getByRole('menuitemradio', { name: /Fix/ }))
    expect(sendButton()).toBeEnabled()
    // The key changes under the chosen mode; the send waits rather than
    // starting a fix the core would refuse.
    fireEvent.change(bar(), { target: { value: 'OMNI-3' } })
    expect(sendButton()).toBeDisabled()
    expect(sendButton()).toHaveAttribute('title', 'Needs a triage note for OMNI-3 first')
  })

  it('shows the reason a start was refused, under the card', async () => {
    const onStart = vi.fn(async () => {
      throw new Error('OMNI-9 is busy')
    })
    mount({ onStart })
    fireEvent.change(bar(), { target: { value: 'OMNI-9' } })
    fireEvent.click(sendButton())
    expect(await screen.findByRole('status')).toHaveTextContent('OMNI-9 is busy')
    expect(sendButton()).toBeEnabled()
  })

  it('stops waiting when the job ends, opening the run it names if it names one', async () => {
    const transport = createFakeTransport({ tickets: [] })
    const { onOpenRun } = mount({ transport })
    fireEvent.change(bar(), { target: { value: 'OMNI-9' } })
    fireEvent.click(sendButton())
    await screen.findByRole('button', { name: 'Starting…' })

    act(() =>
      transport.emit({
        kind: 'job.finished',
        jobId: 'job-1',
        workspaceId: 'ws1',
        outcomes: [{ key: 'OMNI-9', status: 'failed', runId: 'r-9' }],
      }),
    )
    await waitFor(() => expect(onOpenRun).toHaveBeenCalledWith('r-9'))
  })

  it('says so when the job ends with no run at all', async () => {
    const transport = createFakeTransport({ tickets: [] })
    const { onOpenRun } = mount({ transport })
    fireEvent.change(bar(), { target: { value: 'OMNI-9' } })
    fireEvent.click(sendButton())
    await screen.findByRole('button', { name: 'Starting…' })

    act(() => transport.emit({ kind: 'job.finished', jobId: 'job-1', workspaceId: 'ws1', outcomes: [] }))
    expect(await screen.findByRole('status')).toHaveTextContent('The job ended before a session started.')
    expect(onOpenRun).not.toHaveBeenCalled()
    expect(sendButton()).toBeEnabled()
  })

  describe('the fix Then choice', () => {
    const chooseFix = () => {
      fireEvent.click(modeChip())
      fireEvent.click(screen.getByRole('menuitemradio', { name: /Fix/ }))
    }

    it('opens a pull request by default and says so', () => {
      mount({ runs: [TRIAGED] })
      fireEvent.change(bar(), { target: { value: 'OMNI-2' } })
      chooseFix()
      const group = screen.getByRole('radiogroup', { name: 'Then' })
      expect(within(group).getByRole('radio', { checked: true })).toHaveAccessibleName(
        'Open a pull request',
      )
      expect(screen.getByText('The branch is pushed and a pull request is opened.')).toBeInTheDocument()
    })

    it('carries push-only on the start', async () => {
      const { onStart } = mount({ runs: [TRIAGED] })
      fireEvent.change(bar(), { target: { value: 'OMNI-2' } })
      chooseFix()
      fireEvent.click(screen.getByRole('radio', { name: 'Push the branch only' }))
      expect(screen.getByText('The branch is pushed; no pull request is opened.')).toBeInTheDocument()
      fireEvent.click(sendButton())
      await waitFor(() =>
        expect(onStart).toHaveBeenLastCalledWith(
          'fix',
          'OMNI-2',
          expect.objectContaining({ noPr: true, local: undefined }),
        ),
      )
    })

    it('carries keep-local on the start, and remembers the choice for the workspace', async () => {
      const { onStart } = mount({ runs: [TRIAGED] })
      fireEvent.change(bar(), { target: { value: 'OMNI-2' } })
      chooseFix()
      fireEvent.click(screen.getByRole('radio', { name: 'Keep it local' }))
      expect(
        screen.getByText(
          'The commit stays in the worktree — nothing is pushed, and the change is read in Change review.',
        ),
      ).toBeInTheDocument()
      fireEvent.click(sendButton())
      await waitFor(() =>
        expect(onStart).toHaveBeenLastCalledWith(
          'fix',
          'OMNI-2',
          expect.objectContaining({ local: true, noPr: undefined }),
        ),
      )
      expect(composerPrefs('ws1').fixThen).toBe('local')
    })

    it('is not drawn for a triage or an RCA', () => {
      mount({ runs: [TRIAGED] })
      fireEvent.change(bar(), { target: { value: 'OMNI-2' } })
      expect(screen.queryByRole('radiogroup', { name: 'Then' })).toBeNull()
      fireEvent.click(modeChip())
      fireEvent.click(screen.getByRole('menuitemradio', { name: /RCA/ }))
      expect(screen.queryByRole('radiogroup', { name: 'Then' })).toBeNull()
    })
  })

  describe('the RCA fields', () => {
    const chooseRCA = () => {
      fireEvent.click(modeChip())
      fireEvent.click(screen.getByRole('menuitemradio', { name: /RCA/ }))
    }

    it('carries the pull request URL and what was done, and says what an empty pair means', async () => {
      const { onStart } = mount({ runs: [TRIAGED] })
      fireEvent.change(bar(), { target: { value: 'OMNI-2' } })
      chooseRCA()
      expect(
        screen.getByText(/Left empty, the note carries `<fill: pull request>`/),
      ).toBeInTheDocument()

      fireEvent.change(screen.getByLabelText('Pull request URL'), {
        target: { value: 'https://github.com/acme/api/pull/42' },
      })
      fireEvent.change(screen.getByLabelText('What was done'), {
        target: { value: 'Reprocessed the stuck queue.' },
      })
      fireEvent.click(sendButton())
      await waitFor(() =>
        expect(onStart).toHaveBeenCalledWith(
          'rca',
          'OMNI-2',
          expect.objectContaining({
            prUrl: 'https://github.com/acme/api/pull/42',
            resolution: 'Reprocessed the stuck queue.',
          }),
        ),
      )
    })

    it('says a pull request URL that is not an http address will not do', () => {
      mount({ runs: [TRIAGED] })
      fireEvent.change(bar(), { target: { value: 'OMNI-2' } })
      chooseRCA()
      const field = screen.getByLabelText('Pull request URL')
      fireEvent.change(field, { target: { value: 'pull/42' } })
      expect(field).toHaveAttribute('aria-invalid', 'true')
      expect(
        screen.getByText('The pull request URL has to be an http or https address.'),
      ).toBeInTheDocument()
    })
  })

  describe('a helpdesk number', () => {
    it('is resolved through the helpdesk and starts on the tracker key it names', async () => {
      const transport = createFakeTransport({
        tickets: [],
        helpdesk: { '25312': { number: '25312', key: 'OMNI-3233', subject: 'Invoice total' } },
      })
      const { onStart } = mount({ transport })
      fireEvent.change(bar(), { target: { value: '#25312 the total is off by one fils' } })

      expect(
        await screen.findByText(/Triage · #25312 → OMNI-3233 · from the helpdesk record · with your note/),
      ).toBeInTheDocument()
      // The reference alone is asked about, not the whole line.
      expect(transport.calls.resolve).toEqual([{ ws: 'ws1', text: '#25312' }])
      fireEvent.click(sendButton())
      await waitFor(() =>
        expect(onStart).toHaveBeenCalledWith(
          'triage',
          'OMNI-3233',
          expect.objectContaining({ instruction: 'the total is off by one fils' }),
        ),
      )
    })

    it('says so when the helpdesk cannot name a tracker issue', async () => {
      const transport = createFakeTransport({ tickets: [] })
      mount({ transport })
      fireEvent.change(bar(), { target: { value: '#25312' } })
      expect(await screen.findByText('no tracker issue names #25312')).toBeInTheDocument()
      expect(sendButton()).toBeDisabled()
    })
  })

  describe('repositories', () => {
    const facts = { exists: true, git: true, behind: 0, ahead: 0 }
    const REPOS: RepoSummary[] = [
      { name: 'acme-api', path: '/src/acme-api', origin: 'git@github.com:acme/acme-api.git', workspace: true, facts, state: '' },
      { name: 'Acme.Web', path: '/src/Acme.Web', origin: 'git@github.com:acme/Acme.Web.git', facts, state: '' },
      { name: 'Acme.Flutter.POS', path: '/src/Acme.Flutter.POS', facts, state: '' },
    ]

    it('says which repository the line asks to look in, and carries the words to the run', async () => {
      const { onStart } = mount({ repos: REPOS })
      fireEvent.change(bar(), { target: { value: 'SBX-1 check the POS app first' } })
      expect(screen.getByRole('status')).toHaveTextContent('Triage · SBX-1 · look in Acme.Flutter.POS · with your note')
      fireEvent.click(sendButton())
      await waitFor(() =>
        expect(onStart).toHaveBeenCalledWith(
          'triage',
          'SBX-1',
          expect.objectContaining({ instruction: 'check the POS app first' }),
        ),
      )
    })

    it('names a repository the line mentions, configured or not', () => {
      mount({ repos: REPOS })
      fireEvent.change(bar(), { target: { value: 'SBX-1 broke after acme/Acme.Web#828 or other/Billing.Service#3' } })
      expect(screen.getByRole('status')).toHaveTextContent(
        'Triage · SBX-1 · mentions Acme.Web (companion repo) · mentions Billing.Service (not configured — add it under repos:) · with your note',
      )
    })

    it('will not start on a repository nobody configured, and says why', () => {
      const { onStart } = mount({ repos: REPOS })
      fireEvent.change(bar(), { target: { value: 'SBX-1 look in Billing.Service' } })
      expect(screen.getByRole('status')).toHaveTextContent('Billing.Service is not a configured repository')
      expect(sendButton()).toBeDisabled()
      expect(screen.getByTestId('primary')).toHaveTextContent('off')
      fireEvent.keyDown(bar(), { key: 'Enter' })
      expect(onStart).not.toHaveBeenCalled()
    })

    it('does not repeat a mention the ticket resolution already named', async () => {
      const transport = createFakeTransport({
        tickets: [],
        intake: {
          'SBX-1': {
            input: 'key',
            key: 'SBX-1',
            via: [],
            summary: 'SBX-1 · mentions Acme.Web (companion repo)',
            repos: [{ name: 'Acme.Web', slug: 'acme/Acme.Web', ref: 'acme/Acme.Web#828', status: 'companion' }],
          },
        },
      })
      mount({ transport, repos: REPOS })
      fireEvent.change(bar(), { target: { value: 'SBX-1 see acme/Acme.Web#828' } })
      expect(
        await screen.findByText('Triage · SBX-1 · mentions Acme.Web (companion repo) · with your note'),
      ).toBeInTheDocument()
    })
  })

  describe('a Slack link', () => {
    const SLACK = 'https://acme.slack.com/archives/C0123ABCD/p1712345678901234'

    it('says it is reading, then starts on the ticket the thread names and carries the link', async () => {
      const transport = createFakeTransport({
        tickets: [],
        intake: {
          [SLACK]: {
            input: 'slack',
            key: 'SBX-1',
            helpdeskNumber: '28310',
            via: [
              { from: 'Slack thread', to: '#28310', source: 'slack' },
              { from: '#28310', to: 'SBX-1', how: 'matched by title', source: 'recent tickets' },
            ],
            summary: 'Slack thread → #28310 → SBX-1 · matched by title',
            slack: { url: SLACK, messages: 3, thread: true },
          },
        },
      })
      const { onStart } = mount({ transport })
      fireEvent.change(bar(), { target: { value: `${SLACK} look at the export first` } })
      expect(screen.getByRole('status')).toHaveTextContent('Reading the Slack thread…')
      expect(sendButton()).toBeDisabled()

      expect(
        await screen.findByText('Triage · Slack thread → #28310 → SBX-1 · matched by title · with your note'),
      ).toBeInTheDocument()
      expect(transport.calls.resolve).toEqual([{ ws: 'ws1', text: SLACK }])
      fireEvent.click(sendButton())
      await waitFor(() =>
        expect(onStart).toHaveBeenCalledWith(
          'triage',
          'SBX-1',
          expect.objectContaining({ instruction: 'look at the export first', slack: SLACK }),
        ),
      )
    })

    it('triages a thread that names no ticket under its Slack key, and says when its PR is in another repository', async () => {
      const DM = 'https://acme.slack.com/archives/D0FAKEDM01/p1791100254656059'
      const summary =
        'Slack thread · no ticket yet · will triage the thread · mentions Billing.Service (not configured — add it under repos:)'
      const transport = createFakeTransport({
        tickets: [],
        intake: {
          [DM]: {
            input: 'slack',
            key: 'SLACK-D0FAKEDM01-1791100254',
            via: [{ from: 'Slack thread', to: 'SLACK-D0FAKEDM01-1791100254', how: 'no ticket yet', source: 'slack' }],
            summary,
            subject: 'Coupon totals are wrong on the receipt',
            slack: { url: DM, messages: 2, thread: true },
            slackOnly: true,
            repos: [
              {
                name: 'Billing.Service',
                slug: 'acme-co/Billing.Service',
                ref: 'https://github.com/acme-co/Billing.Service/pull/412',
                status: 'unknown',
              },
            ],
          },
        },
      })
      const { onStart } = mount({ transport })
      fireEvent.change(bar(), { target: { value: DM } })
      expect(await screen.findByText(`Triage · ${summary}`)).toBeInTheDocument()
      expect(sendButton()).toBeEnabled()
      fireEvent.click(sendButton())
      await waitFor(() =>
        expect(onStart).toHaveBeenCalledWith(
          'triage',
          'SLACK-D0FAKEDM01-1791100254',
          expect.objectContaining({ slack: DM }),
        ),
      )
    })

    it('says what to configure when the workspace has no Slack token', async () => {
      const reason =
        'Slack is not configured: set sources.slack.token (a Slack user token with channels:history, groups:history) in config.yaml'
      const transport = createFakeTransport({
        tickets: [],
        intake: { [SLACK]: { input: 'slack', key: '', via: [], reason } },
      })
      mount({ transport })
      fireEvent.change(bar(), { target: { value: SLACK } })
      expect(await screen.findByText(reason)).toBeInTheDocument()
      expect(sendButton()).toBeDisabled()
    })
  })

  describe('a helpdesk link', () => {
    it('is resolved to the tracker key and starts with no Slack thread', async () => {
      const url = 'https://desk.zoho.com/agent/acme/support/tickets/details/123400000456789'
      const transport = createFakeTransport({
        tickets: [],
        intake: {
          [url]: {
            input: 'helpdesk-url',
            key: 'SBX-1',
            helpdeskNumber: '28310',
            helpdeskId: '123400000456789',
            via: [{ from: '#28310', to: 'SBX-1', how: 'from Zoho field cf_jira_ticket_id', source: 'helpdesk record' }],
            summary: '#28310 → SBX-1 · from Zoho field cf_jira_ticket_id',
          },
        },
      })
      const { onStart } = mount({ transport })
      fireEvent.change(bar(), { target: { value: url } })
      expect(screen.getByRole('status')).toHaveTextContent('Looking up the helpdesk ticket…')
      expect(
        await screen.findByText('Triage · #28310 → SBX-1 · from Zoho field cf_jira_ticket_id'),
      ).toBeInTheDocument()
      fireEvent.click(sendButton())
      await waitFor(() =>
        expect(onStart).toHaveBeenCalledWith('triage', 'SBX-1', expect.objectContaining({ slack: undefined })),
      )
    })
  })

  describe('a key', () => {
    it('can start at once, and names its helpdesk ticket once the tracker says', async () => {
      const transport = createFakeTransport({
        tickets: [],
        intake: {
          'SBX-1': { input: 'key', key: 'SBX-1', helpdeskNumber: '28310', via: [], summary: 'SBX-1 · #28310' },
        },
      })
      mount({ transport })
      fireEvent.change(bar(), { target: { value: 'SBX-1' } })
      expect(sendButton()).toBeEnabled()
      expect(await screen.findByText('Triage · SBX-1 · #28310')).toBeInTheDocument()
    })
  })

  describe('a line the parser cannot settle', () => {
    it('is read once by the model and started only on a second send', async () => {
      const transport = createFakeTransport({
        tickets: [],
        composed: { key: 'OMNI-3233', mode: 'fix', instruction: 'the rounding', confidence: 0.8 },
      })
      const { onStart } = mount({ transport, runs: [run({ key: 'OMNI-3233', kind: 'triage', status: 'completed' })] })
      fireEvent.change(bar(), { target: { value: 'sort out the rounding on 3233' } })
      expect(screen.getByRole('status')).toHaveTextContent(
        'No ticket named yet — press Enter and I will read what you typed',
      )
      expect(transport.calls.composeIntent).toEqual([])

      fireEvent.click(sendButton())
      await waitFor(() =>
        expect(transport.calls.composeIntent).toEqual([
          { ws: 'ws1', text: 'sort out the rounding on 3233' },
        ]),
      )
      // The reading is shown, and nothing has started on it.
      expect(await screen.findByText('Confirm')).toBeInTheDocument()
      expect(screen.getByRole('status')).toHaveTextContent('Fix · OMNI-3233 · with your note')
      expect(onStart).not.toHaveBeenCalled()

      fireEvent.click(sendButton())
      await waitFor(() =>
        expect(onStart).toHaveBeenCalledWith(
          'fix',
          'OMNI-3233',
          expect.objectContaining({ instruction: 'the rounding' }),
        ),
      )
    })

    it('asks about two keys rather than starting on the first of them', async () => {
      const transport = createFakeTransport({
        tickets: [],
        composed: { key: 'OMNI-2', mode: 'triage', instruction: '', confidence: 0.7 },
      })
      const { onStart } = mount({ transport })
      fireEvent.change(bar(), { target: { value: 'is OMNI-1 the same bug as OMNI-2' } })
      expect(screen.getByRole('status')).toHaveTextContent(
        'Two ticket keys in there — press Enter and I will work out which you meant',
      )
      fireEvent.click(sendButton())
      await waitFor(() => expect(transport.calls.composeIntent).toHaveLength(1))
      expect(onStart).not.toHaveBeenCalled()
      expect(await screen.findByText('Confirm')).toBeInTheDocument()
      expect(screen.getByRole('status')).toHaveTextContent('Triage · OMNI-2')
    })

    it('starts on the first key when the setting is off', async () => {
      setIntentAssist('ws1', false)
      const transport = createFakeTransport({ tickets: [] })
      const { onStart } = mount({ transport })
      fireEvent.change(bar(), { target: { value: 'is OMNI-1 the same bug as OMNI-2' } })
      expect(screen.getByRole('status')).toHaveTextContent('Triage · OMNI-1')
      fireEvent.click(sendButton())
      await waitFor(() => expect(onStart).toHaveBeenCalledWith('triage', 'OMNI-1', expect.anything()))
      expect(transport.calls.composeIntent).toEqual([])
    })

    it('drops the reading the moment another character is typed', async () => {
      const transport = createFakeTransport({
        tickets: [],
        composed: { key: 'OMNI-3233', mode: 'triage', instruction: '', confidence: 0.6 },
      })
      mount({ transport })
      fireEvent.change(bar(), { target: { value: 'the rounding on 3233' } })
      fireEvent.click(sendButton())
      expect(await screen.findByText('Confirm')).toBeInTheDocument()

      fireEvent.change(bar(), { target: { value: 'the rounding on 3233 again' } })
      expect(screen.queryByText('Confirm')).toBeNull()
    })

    it('is never read when the setting is off, nor when the line reads cleanly', async () => {
      const transport = createFakeTransport({
        tickets: [],
        composed: { key: 'OMNI-1', mode: 'triage', instruction: '', confidence: 1 },
      })
      setIntentAssist('ws1', false)
      const { onStart } = mount({ transport })
      fireEvent.change(bar(), { target: { value: 'sort out the rounding' } })
      expect(sendButton()).toBeDisabled()
      expect(screen.getByRole('status')).toHaveTextContent(
        'No ticket yet — type a key like SBX-1 or a helpdesk number like #28310, or paste a ticket or Slack link',
      )

      // A line that reads cleanly never spends a call, setting or no setting.
      setIntentAssist('ws1', true)
      fireEvent.change(bar(), { target: { value: 'OMNI-1 check the rounding' } })
      fireEvent.click(sendButton())
      await waitFor(() => expect(onStart).toHaveBeenCalled())
      expect(transport.calls.composeIntent).toEqual([])
    })
  })

  it('starts on Enter, and Shift with Enter is a new line', async () => {
    const { onStart } = mount()
    fireEvent.change(bar(), { target: { value: 'OMNI-2510' } })
    fireEvent.keyDown(bar(), { key: 'Enter', shiftKey: true })
    expect(onStart).not.toHaveBeenCalled()
    fireEvent.keyDown(bar(), { key: 'Enter' })
    await waitFor(() => expect(onStart).toHaveBeenCalledWith('triage', 'OMNI-2510', expect.anything()))
  })

  describe('Landed today', () => {
    it("lists the reader's newest five with key, source and age, and Triage per row", async () => {
      const transport = createFakeTransport({
        tickets: [1, 2, 3, 4, 5, 6].map((n) =>
          ticket({
            key: `SBX-${n}`,
            title: `Ticket ${n}`,
            url: n === 3 ? 'https://acme.zendesk.com/agent/tickets/3' : 'https://acme.atlassian.net/browse/SBX-' + n,
            updatedAt: `2026-09-10T0${n}:00:00Z`,
          }),
        ),
      })
      const { onStart } = mount({ transport })

      await screen.findByText('Ticket 6')
      expect(transport.calls.queue).toEqual(['ws1'])
      const rows = screen.getAllByRole('button', { name: /^Triage SBX-/ })
      expect(rows.map((b) => b.getAttribute('aria-label'))).toEqual([
        'Triage SBX-6',
        'Triage SBX-5',
        'Triage SBX-4',
        'Triage SBX-3',
        'Triage SBX-2',
      ])
      expect(screen.getByText(/SBX-3 · zendesk · /)).toBeInTheDocument()
      expect(screen.getByText(/SBX-6 · jira · /)).toBeInTheDocument()

      fireEvent.click(screen.getByRole('button', { name: 'Triage SBX-5' }))
      await waitFor(() => expect(onStart).toHaveBeenCalledWith('triage', 'SBX-5', expect.anything()))
    })

    it('asks the tracker for the reader own tickets', async () => {
      const queue = vi.fn(async () => [])
      const transport = createFakeTransport({ tickets: [] })
      transport.queue = queue
      mount({ transport })
      await waitFor(() => expect(queue).toHaveBeenCalledWith('ws1', { assignee: 'me', limit: 5 }))
    })

    it('says the row carries a run, and opens it from the row', async () => {
      const transport = createFakeTransport({
        tickets: [
          ticket({
            key: 'SBX-1',
            title: 'Login loop',
            latestRun: run({ runId: 'r-1', key: 'SBX-1', status: 'blocked' }),
          }),
        ],
      })
      const { onOpenRun } = mount({ transport })
      await screen.findByText('Login loop')
      expect(screen.getByText(/SBX-1 · .* · blocked$/)).toBeInTheDocument()
      fireEvent.click(screen.getByRole('button', { name: 'Open SBX-1, Login loop' }))
      expect(onOpenRun).toHaveBeenCalledWith('r-1')
    })

    it('says Nothing landed today when the queue is empty', async () => {
      mount()
      expect(await screen.findByText('Nothing landed today')).toBeInTheDocument()
    })

    it('says so while the queue is still loading', () => {
      mount()
      expect(screen.getByText('Loading what landed…')).toBeInTheDocument()
    })

    it('tells the reader to start by key when the workspace has no tracker', async () => {
      const transport = createFakeTransport({ tickets: [] })
      transport.failQueue(new Error('501 Not Implemented'))
      mount({ transport })
      expect(await screen.findByText('This workspace has no tracker; start by key.')).toBeInTheDocument()
    })

    it('shows the reason the queue could not be read', async () => {
      const transport = createFakeTransport({ tickets: [] })
      transport.failQueue(new Error('tracker: 401 Unauthorized'))
      mount({ transport })
      expect(await screen.findByText(/Could not read the queue\. tracker: 401 Unauthorized/)).toBeInTheDocument()
    })

    it('reads the queue again when a webhook delivery lands', async () => {
      const transport = createFakeTransport({ tickets: [] })
      mount({ transport })
      await screen.findByText('Nothing landed today')
      transport.setTickets([ticket({ key: 'SBX-9', title: 'Just landed' })])

      act(() => transport.emit({ kind: 'hook.received', source: 'jira', key: 'SBX-9', outcome: 'started' }))
      expect(await screen.findByText('Just landed')).toBeInTheDocument()
      expect(transport.calls.queue).toEqual(['ws1', 'ws1'])
    })
  })
})
