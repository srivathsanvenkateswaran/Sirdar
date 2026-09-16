import { act, fireEvent, render, screen, within } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { Workspace } from '../../api/types'
import { resetShowLibrary, setShowLibrary } from '../../lib/library'
import { stubMatchMedia } from '../../lib/mediaStub'
import { run } from '../../store/fakeTransport'
import { STATE_WORDS } from '../../ui/status-badge'
import { PrimaryActionProvider, useProvidePrimaryAction } from './primaryAction'
import Sidebar, { RAIL_AT } from './Sidebar'

afterEach(() => {
  localStorage.removeItem('sirdar.showLibrary')
  resetShowLibrary()
})

const WS: Workspace[] = [
  {
    id: 'ws1',
    name: 'omni',
    root: '/repos/omni',
    provider: 'claude',
    model: 'sonnet',
    notesDir: '.sirdar/notes',
    billing: 'subscription',
  },
  {
    id: 'ws2',
    name: 'billing',
    root: '/repos/billing',
    provider: 'codex',
    model: 'gpt',
    notesDir: '.sirdar/notes',
    billing: 'api',
  },
]

function mount(props: Partial<React.ComponentProps<typeof Sidebar>> = {}, action?: JSX.Element) {
  const onNavigate = vi.fn()
  const onSelectWorkspace = vi.fn()
  const onAddWorkspace = vi.fn()
  const view = render(
    <PrimaryActionProvider>
      {action}
      <Sidebar
        workspaces={WS}
        currentWorkspaceId="ws1"
        quota={[]}
        screen={{ name: 'board' }}
        onSelectWorkspace={onSelectWorkspace}
        onAddWorkspace={onAddWorkspace}
        onNavigate={onNavigate}
        {...props}
      />
    </PrimaryActionProvider>,
  )
  return { onNavigate, onSelectWorkspace, onAddWorkspace, ...view }
}

describe('the rail under 1024', () => {
  it('collapses to icons with each row named by its tooltip, and comes back when the window widens', () => {
    const media = stubMatchMedia([RAIL_AT])
    try {
      setShowLibrary(false)
      const { container } = mount()
      const sidebar = container.querySelector('.sd-sidebar')!
      expect(sidebar).toHaveAttribute('data-collapsed', 'true')
      const nav = screen.getByRole('navigation', { name: 'Screens' })
      for (const row of within(nav).getAllByRole('button')) {
        // The label is still in the DOM for a screen reader; the tooltip is
        // what a sighted reader gets from a 56px rail.
        expect(row).toHaveAttribute('title', row.textContent)
      }
      expect(within(nav).getByRole('button', { name: 'Board' })).toHaveAttribute('title', 'Board')

      act(() => media.set(RAIL_AT, false))
      expect(sidebar).not.toHaveAttribute('data-collapsed')
      expect(within(nav).getByRole('button', { name: 'Board' })).not.toHaveAttribute('title')
    } finally {
      media.restore()
    }
  })

  it('asks for the narrow band, the one the tokens file names', () => {
    expect(RAIL_AT).toBe('(max-width: 1023px)')
  })
})

describe('the sidebar nav', () => {
  it('lists the five screens an engineer works in, in order', () => {
    setShowLibrary(false)
    mount()
    const nav = screen.getByRole('navigation', { name: 'Screens' })
    expect(within(nav).getAllByRole('button').map((b) => b.textContent)).toEqual([
      'Sessions',
      'Board',
      'Register',
      'Eval',
      'Settings',
    ])
  })

  it('adds the Library row while the switch is on, above Settings', () => {
    setShowLibrary(true)
    mount({ screen: { name: 'library' } })
    const nav = screen.getByRole('navigation', { name: 'Screens' })
    expect(within(nav).getAllByRole('button').map((b) => b.textContent)).toEqual([
      'Sessions',
      'Board',
      'Register',
      'Eval',
      'Library',
      'Settings',
    ])
    expect(screen.getByRole('button', { name: 'Library' })).toHaveAttribute('aria-current', 'page')
  })

  it('drops the row the moment the switch is turned off', () => {
    setShowLibrary(true)
    mount()
    expect(screen.getByRole('button', { name: 'Library' })).toBeInTheDocument()
    act(() => setShowLibrary(false))
    expect(screen.queryByRole('button', { name: 'Library' })).toBeNull()
  })

  it.each([
    { name: 'run', runId: 'r1' } as const,
    { name: 'review', runId: 'r1' } as const,
    { name: 'new' } as const,
  ])('marks the Sessions row while %o is open', (open) => {
    setShowLibrary(false)
    mount({ screen: open })
    expect(screen.getByRole('button', { name: 'Sessions' })).toHaveAttribute('aria-current', 'page')
    expect(screen.getByRole('button', { name: 'Board' })).not.toHaveAttribute('aria-current')
  })

  it('opens the session that changed last from the Sessions row, or a new one', () => {
    setShowLibrary(false)
    const older = run({ runId: 'r-old', key: 'OMNI-1', updatedAt: '2026-09-10T09:00:00Z' })
    const newer = run({ runId: 'r-new', key: 'OMNI-2', updatedAt: '2026-09-12T09:00:00Z' })
    const { onNavigate, unmount } = mount({ runs: [older, newer] })
    fireEvent.click(screen.getByRole('button', { name: 'Sessions' }))
    expect(onNavigate).toHaveBeenCalledWith({ name: 'run', runId: 'r-new' })
    unmount()

    const empty = mount({ runs: [] })
    fireEvent.click(screen.getByRole('button', { name: 'Sessions' }))
    expect(empty.onNavigate).toHaveBeenCalledWith({ name: 'new' })
  })

  it('reports the screen that was asked for', () => {
    setShowLibrary(true)
    const { onNavigate } = mount()
    fireEvent.click(screen.getByRole('button', { name: 'Library' }))
    expect(onNavigate).toHaveBeenCalledWith({ name: 'library' })
  })

  it('badges the Board row with the inbound deliveries waiting, and names the count', () => {
    setShowLibrary(false)
    mount({ inboundCount: 3 })
    expect(
      screen.getByRole('button', { name: /3 inbound deliveries/ }),
    ).toBeInTheDocument()
  })

  it('draws no badge when nothing has arrived', () => {
    setShowLibrary(false)
    const { container } = mount({ inboundCount: 0 })
    expect(container.querySelector('.sd-nav-row__count')).toBeNull()
  })
})

describe('the sessions list', () => {
  /** A still clock: 2026-09-16 at 10:00 local. The list itself is tested in SessionsList.test.tsx. */
  const NOW = new Date(2026, 8, 16, 10, 0, 0).getTime()
  const runs = [
    run({
      runId: 'r1',
      key: 'OMNI-1',
      helpdeskKey: '25312',
      kind: 'triage',
      status: 'running',
      title: 'Login loop after reset',
      updatedAt: new Date(NOW - 55 * 60_000).toISOString(),
    }),
    run({ runId: 'r2', key: 'OMNI-2', kind: 'fix', status: 'completed', updatedAt: new Date(NOW - 86_400_000).toISOString() }),
  ]
  const sources = {
    tracker: { adapter: 'jira', name: 'Jira', host: 'acme.atlassian.net' },
    helpdesk: { adapter: 'zohodesk', name: 'Zoho Desk', host: 'desk.zoho.com' },
  }

  it('hands the workspace\'s runs and sources to the list, one number per row under its mark', () => {
    setShowLibrary(false)
    mount({ runs, sources, now: NOW, screen: { name: 'run', runId: 'r1' } })
    const list = screen.getByRole('navigation', { name: 'Sessions' })
    const row = within(list).getByRole('button', { name: `OMNI-1, triage, ${STATE_WORDS.running}` })
    expect(row).toHaveAttribute('aria-current', 'page')
    expect(within(row).getByRole('img', { name: 'Jira' })).toBeInTheDocument()
    expect(row).not.toHaveTextContent('Login loop')
    expect(within(list).getByRole('button', { name: 'Settled' })).toBeInTheDocument()
  })

  it('opens a run from its row', () => {
    setShowLibrary(false)
    const { onNavigate } = mount({ runs, now: NOW })
    fireEvent.click(screen.getByRole('button', { name: /OMNI-2/ }))
    expect(onNavigate).toHaveBeenCalledWith({ name: 'run', runId: 'r2' })
  })

  it('draws nothing when the workspace has no runs', () => {
    setShowLibrary(false)
    mount({ runs: [] })
    expect(screen.queryByRole('navigation', { name: 'Sessions' })).toBeNull()
  })
})

describe('the sidebar footer', () => {
  it('opens a popover of workspaces and switches to the one chosen', () => {
    setShowLibrary(false)
    const { onSelectWorkspace } = mount()

    fireEvent.click(screen.getByRole('button', { name: 'Workspace: omni' }))
    const list = screen.getByRole('listbox', { name: 'Workspaces' })
    expect(within(list).getByRole('option', { name: /omni/ })).toHaveAttribute(
      'aria-selected',
      'true',
    )

    fireEvent.click(within(list).getByRole('option', { name: /billing/ }))
    expect(onSelectWorkspace).toHaveBeenCalledWith('ws2')
    expect(screen.queryByRole('listbox')).toBeNull()
  })

  it('offers registering another repository without a trip through Settings first', () => {
    setShowLibrary(false)
    const { onAddWorkspace } = mount()
    fireEvent.click(screen.getByRole('button', { name: 'Workspace: omni' }))
    fireEvent.click(screen.getByRole('button', { name: 'Add workspace…' }))
    expect(onAddWorkspace).toHaveBeenCalled()
  })

  it('closes the popover on Escape and gives focus back to the trigger', () => {
    setShowLibrary(false)
    mount()
    const trigger = screen.getByRole('button', { name: 'Workspace: omni' })
    fireEvent.click(trigger)
    expect(screen.getByRole('listbox')).toBeInTheDocument()

    fireEvent.keyDown(document, { key: 'Escape' })
    expect(screen.queryByRole('listbox')).toBeNull()
    expect(trigger).toHaveFocus()
  })

  it('draws the screen that published one, and New session when no screen did', () => {
    setShowLibrary(false)

    function Publisher(): null {
      useProvidePrimaryAction({ label: 'Run eval', onRun: () => {} })
      return null
    }

    const { unmount } = mount({}, <Publisher />)
    expect(screen.getByRole('button', { name: /Run eval/ })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'New session' })).toBeNull()
    unmount()

    const { onNavigate } = mount()
    expect(screen.queryByRole('button', { name: /Run eval/ })).toBeNull()
    const filled = screen.getByRole('button', { name: 'New session' })
    expect(filled).toHaveAttribute('data-variant', 'primary')
    fireEvent.click(filled)
    expect(onNavigate).toHaveBeenCalledWith({ name: 'new' })
  })

  it('demotes New session to the bordered style while a screen draws its own filled button', () => {
    setShowLibrary(false)

    function OnScreen(): null {
      useProvidePrimaryAction({ label: 'Start', onRun: () => {}, placement: 'screen' })
      return null
    }

    const { onNavigate } = mount({}, <OnScreen />)
    expect(screen.queryByRole('button', { name: 'Start' })).toBeNull()
    const button = screen.getByRole('button', { name: 'New session' })
    expect(button).toHaveAttribute('data-variant', 'secondary')
    fireEvent.click(button)
    expect(onNavigate).toHaveBeenCalledWith({ name: 'new' })
  })

  it('is titled Plan usage', () => {
    setShowLibrary(false)
    mount()
    expect(screen.getByRole('group', { name: 'Plan usage' })).toHaveTextContent('Plan usage')
  })

  it('shows one quota chip per window a provider reports', () => {
    setShowLibrary(false)
    mount({
      quota: [
        {
          provider: 'claude',
          observedAt: new Date().toISOString(),
          fiveHour: { utilization: 0.62, resetsAt: new Date().toISOString() },
        },
      ],
    })
    expect(screen.getByText('claude')).toBeInTheDocument()
    expect(screen.getByText('62%')).toBeInTheDocument()
  })
})
