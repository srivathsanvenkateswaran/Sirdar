import { act, fireEvent, render, screen, within } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { Workspace } from '../../api/types'
import { resetShowLibrary, setShowLibrary } from '../../lib/library'
import { run } from '../../store/fakeTransport'
import { PrimaryActionProvider, useProvidePrimaryAction } from './primaryAction'
import Sidebar, { recentRuns } from './Sidebar'

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

describe('recent sessions', () => {
  const runs = [
    run({ runId: 'r1', key: 'OMNI-1', kind: 'triage', status: 'running', updatedAt: '2026-09-10T09:05:00Z' }),
    run({ runId: 'r2', key: 'OMNI-2', kind: 'fix', status: 'blocked', updatedAt: '2026-09-10T09:04:00Z' }),
    run({ runId: 'r3', key: 'OMNI-3', kind: 'rca', status: 'completed', updatedAt: '2026-09-10T09:03:00Z' }),
    run({ runId: 'r4', key: 'OMNI-4', kind: 'triage', status: 'completed', updatedAt: '2026-09-10T09:02:00Z' }),
    run({ runId: 'r5', key: 'OMNI-5', kind: 'triage', status: 'failed', updatedAt: '2026-09-10T09:01:00Z' }),
  ]

  it('keeps the newest four, by last change', () => {
    expect(recentRuns(runs.slice().reverse()).map((r) => r.runId)).toEqual(['r1', 'r2', 'r3', 'r4'])
  })

  it('lists them with their key and kind, and says which is live or waiting', () => {
    setShowLibrary(false)
    mount({ runs })
    const recent = screen.getByRole('navigation', { name: 'Recent sessions' })
    const rows = within(recent).getAllByRole('button')
    expect(rows.map((r) => r.getAttribute('aria-label'))).toEqual([
      'OMNI-1 triage, running',
      'OMNI-2 fix, needs input',
      'OMNI-3 rca',
      'OMNI-4 triage',
    ])
    expect(within(recent).queryByText('OMNI-5')).toBeNull()
  })

  it('marks the open run and opens another on click', () => {
    setShowLibrary(false)
    const { onNavigate } = mount({ runs, screen: { name: 'run', runId: 'r2' } })
    const recent = screen.getByRole('navigation', { name: 'Recent sessions' })
    expect(within(recent).getByRole('button', { name: /OMNI-2/ })).toHaveAttribute(
      'aria-current',
      'page',
    )
    fireEvent.click(within(recent).getByRole('button', { name: /OMNI-3/ }))
    expect(onNavigate).toHaveBeenCalledWith({ name: 'run', runId: 'r3' })
  })

  it('draws nothing when the workspace has no runs', () => {
    setShowLibrary(false)
    mount({ runs: [] })
    expect(screen.queryByRole('navigation', { name: 'Recent sessions' })).toBeNull()
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
