import { act, cleanup, fireEvent, render, screen, within } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { SourcesSummary } from '../../api/types'
import { resetSessionPrefs, sessionPrefs } from '../../lib/sessionPrefs'
import { resetSessionsShow, setSessionsShow } from '../../lib/sessionsShow'
import { run, searchHit } from '../../store/fakeTransport'
import { STATE_WORDS } from '../../ui/status-badge'
import SessionsList, {
  CARD_CLOSE_MS,
  CARD_OPEN_MS,
  runDate,
  SHOWN_LIMIT,
  shortAge,
  splitRuns,
  type SessionActions,
} from './SessionsList'

afterEach(() => {
  // Unmount first: a list still mounted when the record is reset would
  // re-read it at once, stamped with the real clock, and hand that to the
  // next test.
  cleanup()
  localStorage.clear()
  resetSessionsShow()
  resetSessionPrefs()
  vi.restoreAllMocks()
  vi.useRealTimers()
})

/** A still clock: 2026-09-16 at 10:00 local. */
const NOW = new Date(2026, 8, 16, 10, 0, 0).getTime()
const at = (daysAgo: number, hour = 9, minute = 0) =>
  new Date(2026, 8, 16 - daysAgo, hour, minute, 0).toISOString()

const SOURCES: SourcesSummary = {
  tracker: { adapter: 'exec', name: 'Janus', host: 'janus.example.com' },
  helpdesk: { adapter: 'zohodesk', name: 'Zoho Desk', host: 'desk.zoho.com' },
}

const RUNS = [
  run({
    runId: 'r1',
    key: 'OMNI-2815',
    helpdeskKey: '25312',
    kind: 'triage',
    status: 'running',
    title: 'Login loop after reset',
    provider: 'claude',
    model: 'claude-sonnet-5',
    updatedAt: at(0, 9, 5),
  }),
  run({ runId: 'r2', key: 'OMNI-2', kind: 'fix', status: 'blocked', provider: 'codex', updatedAt: at(0, 8) }),
  run({ runId: 'r3', key: 'OMNI-3', helpdeskKey: '25201', kind: 'rca', status: 'completed', updatedAt: at(1, 16) }),
  run({ runId: 'r4', key: 'OMNI-4', kind: 'triage', status: 'completed', updatedAt: at(5) }),
  run({ runId: 'r5', key: 'OMNI-5', kind: 'triage', status: 'failed', updatedAt: at(5, 8) }),
]

function mount(props: Partial<React.ComponentProps<typeof SessionsList>> = {}) {
  const onOpen = vi.fn()
  const view = render(
    <SessionsList
      runs={RUNS}
      now={NOW}
      sources={SOURCES}
      workspaceName="omni"
      workspaceId="ws1"
      onOpen={onOpen}
      {...props}
    />,
  )
  return { onOpen, ...view }
}

const list = () => screen.getByRole('navigation', { name: 'Sessions' })
const rows = () => within(list()).getAllByRole('button', { name: /OMNI|#\d/ })

describe('splitRuns', () => {
  it('puts the live and blocked runs first, then the settled, each newest first', () => {
    const { live, settled } = splitRuns(RUNS.slice().reverse())
    expect(live.map((r) => r.runId)).toEqual(['r1', 'r2'])
    expect(settled.map((r) => r.runId)).toEqual(['r3', 'r4', 'r5'])
  })
})

describe('shortAge', () => {
  it('reads an age as short as it goes', () => {
    expect(shortAge(new Date(NOW - 20_000).toISOString(), NOW)).toBe('now')
    expect(shortAge(new Date(NOW - 4 * 60_000).toISOString(), NOW)).toBe('4m')
    expect(shortAge(new Date(NOW - 18 * 3_600_000).toISOString(), NOW)).toBe('18h')
    expect(shortAge(new Date(NOW - 5 * 86_400_000).toISOString(), NOW)).toBe('5d')
    expect(shortAge('', NOW)).toBe('')
  })
})

describe('the sessions list', () => {
  it('is one flat list, live runs first, then the settled ones under a divider', () => {
    mount()
    expect(within(list()).queryByRole('group')).toBeNull()
    expect(rows().map((r) => r.getAttribute('aria-label'))).toEqual([
      `OMNI-2815, triage, ${STATE_WORDS.running}`,
      `OMNI-2, fix, ${STATE_WORDS.blocked}`,
      'OMNI-3, rca',
      'OMNI-4, triage',
      'OMNI-5, triage',
    ])
    const divider = within(list()).getByRole('button', { name: 'Settled' })
    expect(divider).toHaveAttribute('aria-expanded', 'true')
    // The divider sits between the two halves. The dots at each row's end
    // are buttons too, and not rows.
    const all = within(list())
      .getAllByRole('button')
      .filter((b) => b.getAttribute('aria-label') !== 'Session menu')
    expect(all.indexOf(divider)).toBe(2)
  })

  it('draws each row as the source tile, the number and the age, and nothing else', () => {
    mount()
    const first = rows()[0]
    expect(first.querySelector('.sd-session-row__number')).toHaveTextContent('OMNI-2815')
    expect(within(first).getByRole('img', { name: 'Janus' })).toHaveAttribute('data-size', 'xs')
    expect(first.querySelector('.sd-session-row__age')).toHaveTextContent('55m')
    expect(first).not.toHaveTextContent('Login loop')
    expect(first).not.toHaveTextContent('triage')
    expect(rows().map((r) => r.querySelector('.sd-session-row__age')?.textContent)).toEqual([
      '55m',
      '2h',
      '18h',
      '5d',
      '5d',
    ])
  })

  it('marks a live run with the accent dot over the tile and a blocked one in the blocked hue', () => {
    mount()
    const [live, blocked, settled] = rows()
    expect(live.querySelector('.sd-session-row__tile .sd-session-row__dot')).toHaveAttribute('data-live', 'true')
    expect(blocked.querySelector('.sd-session-row__dot')).toHaveAttribute('data-blocked', 'true')
    expect(settled.querySelector('.sd-session-row__dot')).toBeNull()
  })

  it('shows the helpdesk number under the helpdesk mark on that preference, and the key where a run has none', () => {
    setSessionsShow('helpdesk')
    mount()
    const [first, second, third] = rows()
    expect(first.querySelector('.sd-session-row__number')).toHaveTextContent('#25312')
    expect(within(first).getByRole('img', { name: 'Zoho Desk' })).toBeInTheDocument()
    expect(first).toHaveAccessibleName(`#25312, triage, ${STATE_WORDS.running}`)
    // No helpdesk number: the tracker key under the tracker mark, regardless.
    expect(second.querySelector('.sd-session-row__number')).toHaveTextContent('OMNI-2')
    expect(within(second).getByRole('img', { name: 'Janus' })).toBeInTheDocument()
    expect(third.querySelector('.sd-session-row__number')).toHaveTextContent('#25201')
  })

  it('draws the role itself as the tile when the config names no sources', () => {
    mount({ sources: undefined })
    expect(within(rows()[0]).getByRole('img', { name: 'tracker' })).toHaveTextContent('TR')
  })

  it('folds the settled rows away on the divider and remembers the fold', () => {
    mount()
    fireEvent.click(within(list()).getByRole('button', { name: 'Settled' }))
    expect(within(list()).getByRole('button', { name: 'Settled' })).toHaveAttribute('aria-expanded', 'false')
    expect(rows()).toHaveLength(2)
    expect(localStorage.getItem('sirdar.settledCollapsed')).toBe('1')
    fireEvent.click(within(list()).getByRole('button', { name: 'Settled' }))
    expect(rows()).toHaveLength(5)
    expect(localStorage.getItem('sirdar.settledCollapsed')).toBe('0')
  })

  it('opens folded when the fold was remembered', () => {
    localStorage.setItem('sirdar.settledCollapsed', '1')
    mount()
    expect(rows()).toHaveLength(2)
  })

  it('shows eight settled rows and the rest behind Show N more, with every live row shown', () => {
    const many = [
      ...Array.from({ length: 3 }, (_, i) =>
        run({ runId: `live${i}`, key: `LIVE-${i}`, status: 'running', updatedAt: new Date(NOW - i * 1000).toISOString() }),
      ),
      ...Array.from({ length: 11 }, (_, i) =>
        run({ runId: `r${i}`, key: `OMNI-${i}`, updatedAt: new Date(NOW - i * 3_600_000).toISOString() }),
      ),
    ]
    mount({ runs: many })
    expect(within(list()).getAllByRole('button', { name: /LIVE-/ })).toHaveLength(3)
    expect(within(list()).getAllByRole('button', { name: /OMNI-/ })).toHaveLength(SHOWN_LIMIT)
    fireEvent.click(within(list()).getByRole('button', { name: 'Show 3 more' }))
    expect(within(list()).getAllByRole('button', { name: /OMNI-/ })).toHaveLength(11)
    expect(within(list()).queryByRole('button', { name: /Show/ })).toBeNull()
  })

  it('says +N for the hidden settled rows in the rail, still named Show N more', () => {
    const many = Array.from({ length: 11 }, (_, i) =>
      run({ runId: `r${i}`, key: `OMNI-${i}`, updatedAt: new Date(NOW - i * 3_600_000).toISOString() }),
    )
    mount({ runs: many, rail: true })
    const more = within(list()).getByRole('button', { name: 'Show 3 more' })
    expect(more).toHaveTextContent('+3')
    fireEvent.click(more)
    expect(within(list()).getAllByRole('button', { name: /OMNI-/ })).toHaveLength(11)
  })

  it('marks the open run and opens another on click', () => {
    const { onOpen } = mount({ currentRunId: 'r2' })
    expect(within(list()).getByRole('button', { name: /OMNI-2,/ })).toHaveAttribute('aria-current', 'page')
    expect(within(list()).getByRole('button', { name: /OMNI-2815/ })).not.toHaveAttribute('aria-current')
    fireEvent.click(within(list()).getByRole('button', { name: /OMNI-3/ }))
    expect(onOpen).toHaveBeenCalledWith('r3')
  })

  it('draws nothing when the workspace has no runs', () => {
    mount({ runs: [] })
    expect(screen.queryByRole('navigation', { name: 'Sessions' })).toBeNull()
  })

  describe('the hover card', () => {
    /** Pointer onto a row, held still. */
    const hoverRow = (row: HTMLElement) => fireEvent.pointerEnter(row)
    const leaveRow = (row: HTMLElement) => fireEvent.pointerLeave(row)
    const card = () => screen.getByRole('tooltip')
    const noCard = () => expect(screen.queryByRole('tooltip')).toBeNull()

    it('opens beside a row after 120ms of hover, with the title, the other number, the kind and state, the model and the workspace', () => {
      vi.useFakeTimers()
      mount()
      const row = rows()[0]
      hoverRow(row)
      act(() => {
        vi.advanceTimersByTime(CARD_OPEN_MS - 1)
      })
      noCard()
      act(() => {
        vi.advanceTimersByTime(1)
      })
      expect(card().style.position).toBe('fixed')
      expect(row).toHaveAttribute('aria-describedby', card().id)
      expect(card().querySelector('.sd-session-card__title')).toHaveTextContent('Login loop after reset')
      const lines = [...card().querySelectorAll<HTMLElement>('.sd-session-card__row')]
      expect(lines[0]).toHaveTextContent('Zoho Desk #25312')
      expect(within(lines[0]).getByRole('img', { name: 'Zoho Desk' })).toBeInTheDocument()
      expect(lines[1]).toHaveTextContent(`triage${STATE_WORDS.running}`)
      expect(lines[2]).toHaveTextContent('claude-sonnet-5')
      expect(within(lines[2]).getByRole('img', { name: 'Claude' })).toBeInTheDocument()
      expect(lines[3]).toHaveTextContent('omni')
      // One card, drawn after the list rather than inside its scroll region.
      expect(card().closest('nav')).toBeNull()
      expect(screen.getAllByRole('tooltip')).toHaveLength(1)
    })

    it('is not cancelled by a re-render during the wait', () => {
      vi.useFakeTimers()
      const { rerender } = mount()
      hoverRow(rows()[0])
      act(() => {
        vi.advanceTimersByTime(60)
      })
      // The store emits: every run object is new, the ages are re-read.
      rerender(
        <SessionsList
          runs={RUNS.map((run) => ({ ...run }))}
          now={NOW + 1000}
          sources={SOURCES}
          workspaceName="omni"
          onOpen={() => {}}
        />,
      )
      noCard()
      act(() => {
        vi.advanceTimersByTime(60)
      })
      expect(card()).toBeInTheDocument()
    })

    it('moves to the next row with no wait while a card is open', () => {
      vi.useFakeTimers()
      mount()
      const [first, second] = rows()
      hoverRow(first)
      act(() => {
        vi.advanceTimersByTime(CARD_OPEN_MS)
      })
      expect(card().querySelector('.sd-session-card__title')).toHaveTextContent('Login loop after reset')
      leaveRow(first)
      hoverRow(second)
      // No timers advanced: the card is already the next row's.
      expect(card().querySelector('.sd-session-card__title')).toHaveTextContent('OMNI-2')
      expect(second).toHaveAttribute('aria-describedby', card().id)
      expect(first).not.toHaveAttribute('aria-describedby')
    })

    it('closes 150ms after the pointer leaves the row, unless it lands on the card', () => {
      vi.useFakeTimers()
      mount()
      const row = rows()[0]
      hoverRow(row)
      act(() => {
        vi.advanceTimersByTime(CARD_OPEN_MS)
      })
      leaveRow(row)
      act(() => {
        vi.advanceTimersByTime(CARD_CLOSE_MS - 1)
      })
      expect(card()).toBeInTheDocument()
      act(() => {
        vi.advanceTimersByTime(1)
      })
      noCard()

      hoverRow(row)
      act(() => {
        vi.advanceTimersByTime(CARD_OPEN_MS)
      })
      leaveRow(row)
      act(() => {
        vi.advanceTimersByTime(100)
      })
      fireEvent.pointerEnter(card())
      act(() => {
        vi.advanceTimersByTime(1000)
      })
      expect(card()).toBeInTheDocument()
      fireEvent.pointerLeave(card())
      act(() => {
        vi.advanceTimersByTime(CARD_CLOSE_MS)
      })
      noCard()
    })

    it('does not open when the pointer leaves before the delay', () => {
      vi.useFakeTimers()
      mount()
      const row = rows()[0]
      hoverRow(row)
      act(() => {
        vi.advanceTimersByTime(CARD_OPEN_MS - 50)
      })
      leaveRow(row)
      act(() => {
        vi.advanceTimersByTime(1000)
      })
      noCard()
    })

    it('opens on keyboard focus too, without focus gating the pointer', () => {
      vi.useFakeTimers()
      mount()
      const [first, second] = rows()
      // Nothing has focus; the pointer alone opens it.
      hoverRow(first)
      act(() => {
        vi.advanceTimersByTime(CARD_OPEN_MS)
      })
      expect(document.activeElement).not.toBe(first)
      expect(card()).toBeInTheDocument()
      leaveRow(first)
      act(() => {
        vi.advanceTimersByTime(CARD_CLOSE_MS)
      })
      noCard()

      // Focus alone opens it, and blur closes it.
      fireEvent.focus(second)
      act(() => {
        vi.advanceTimersByTime(CARD_OPEN_MS)
      })
      expect(card().querySelector('.sd-session-card__title')).toHaveTextContent('OMNI-2')
      fireEvent.blur(second)
      act(() => {
        vi.advanceTimersByTime(CARD_CLOSE_MS)
      })
      noCard()
    })

    it('names the run by its own number and source when it has no other number', () => {
      vi.useFakeTimers()
      mount()
      fireEvent.focus(rows()[1])
      act(() => {
        vi.advanceTimersByTime(CARD_OPEN_MS)
      })
      expect(card().querySelector('.sd-session-card__title')).toHaveTextContent('OMNI-2')
      const lines = [...card().querySelectorAll('.sd-session-card__row')]
      expect(lines[0]).toHaveTextContent('Janus OMNI-2')
      expect(lines[1]).toHaveTextContent(`fix${STATE_WORDS.blocked}`)
    })

    it('closes on Escape, on a scroll and on a press, and stays closed until the next intent', () => {
      vi.useFakeTimers()
      const { onOpen } = mount()
      const row = rows()[0]
      const open = () => {
        hoverRow(row)
        act(() => {
          vi.advanceTimersByTime(CARD_OPEN_MS)
        })
        expect(card()).toBeInTheDocument()
      }

      open()
      fireEvent.keyDown(document.body, { key: 'Escape' })
      noCard()
      // The pointer is still on the row; nothing re-opens by itself.
      act(() => {
        vi.advanceTimersByTime(1000)
      })
      noCard()
      leaveRow(row)

      open()
      fireEvent.scroll(list())
      noCard()
      leaveRow(row)

      open()
      fireEvent.pointerDown(row)
      fireEvent.click(row)
      noCard()
      expect(onOpen).toHaveBeenCalledWith('r1')
      leaveRow(row)

      // A quick click, before the card has opened: the pending card is
      // cancelled too, so nothing pops over the run the click opened.
      hoverRow(row)
      act(() => {
        vi.advanceTimersByTime(50)
      })
      fireEvent.pointerDown(row)
      fireEvent.click(row)
      act(() => {
        vi.advanceTimersByTime(1000)
      })
      noCard()
    })

    it('carries the row\'s own number as well as the other when the sidebar is the rail', () => {
      vi.useFakeTimers()
      mount({ rail: true })
      hoverRow(rows()[0])
      act(() => {
        vi.advanceTimersByTime(CARD_OPEN_MS)
      })
      const lines = [...card().querySelectorAll<HTMLElement>('.sd-session-card__row')]
      expect(lines[0]).toHaveTextContent('Janus OMNI-2815')
      expect(lines[1]).toHaveTextContent('Zoho Desk #25312')
      expect(lines[2]).toHaveTextContent(`triage${STATE_WORDS.running}`)
    })

    it('is drawn open in the flow for the gallery', () => {
      mount({ pinnedCard: 'r1' })
      expect(card()).toHaveAttribute('data-static', 'true')
      expect(card().style.position).toBe('')
      fireEvent.pointerLeave(rows()[0])
      expect(card()).toBeInTheDocument()
    })
  })
})

/* ---------- the menu and what it does ---------- */

const menu = () => screen.getByRole('menu', { name: /Session menu for/ })
const noMenu = () => expect(screen.queryByRole('menu')).toBeNull()
const item = (name: string | RegExp) => within(menu()).getByRole('menuitem', { name })
const row = (name: RegExp) => within(list()).getByRole('button', { name })
const rowNames = () => rows().map((r) => r.getAttribute('aria-label'))
const groupHead = (name: RegExp) => within(list()).getByRole('button', { name })

/** Every action wired, the way the desktop shell wires them. */
function actions(over: Partial<SessionActions> = {}): Required<SessionActions> & { calls: Record<string, unknown[][]> } {
  const calls: Record<string, unknown[][]> = {}
  const record =
    (name: string, answer?: unknown) =>
    (...args: unknown[]) => {
      ;(calls[name] ??= []).push(args)
      return answer
    }
  return {
    calls,
    onDelete: record('onDelete', Promise.resolve()) as SessionActions['onDelete'] & {},
    onOpenSettings: record('onOpenSettings'),
    onOpenNote: record('onOpenNote'),
    onOpenRunDir: record('onOpenRunDir'),
    loadLinks: record('loadLinks', Promise.resolve({ trackerUrl: 'https://janus.example.com/OMNI-3', helpdeskUrl: 'https://desk.zoho.com/t/25201' })) as SessionActions['loadLinks'] & {},
    onToast: record('onToast'),
    ...over,
  }
}

/** Waits for the promises a menu opening fans out. */
const settle = () => act(() => new Promise((resolve) => setTimeout(resolve, 0)))

describe('the session menu', () => {
  it('opens on right-click at the pointer, closes the hover card, and lists the actions in order', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    const withNote = RUNS.map((r) => (r.runId === 'r3' ? { ...r, notes: ['/notes/OMNI-3-rca.md'] } : r))
    mount({ runs: withNote, actions: actions() })
    const target = row(/OMNI-3/)
    fireEvent.pointerEnter(target)
    act(() => {
      vi.advanceTimersByTime(CARD_OPEN_MS)
    })
    expect(screen.getByRole('tooltip')).toBeInTheDocument()

    fireEvent.contextMenu(target, { clientX: 120, clientY: 300 })
    expect(screen.queryByRole('tooltip')).toBeNull()
    expect(menu()).toHaveAccessibleName('Session menu for OMNI-3')
    expect(menu().style.position).toBe('fixed')
    await settle()
    expect(within(menu()).getAllByRole('menuitem').map((i) => i.textContent)).toEqual([
      'Pin',
      'Un-settle',
      'Snooze',
      'Rename',
      'Mark unread',
      'Copy',
      'Open',
      'Workspace settings',
      'Archive',
      'Delete…',
    ])
    expect(within(menu()).getAllByRole('separator')).toHaveLength(2)
    expect(item('Delete…')).toHaveAttribute('data-tone', 'danger')
  })

  it('opens on double-click, on the dots at the row\'s end, and on the context-menu key', () => {
    mount()
    fireEvent.doubleClick(row(/OMNI-4/))
    expect(menu()).toHaveAccessibleName('Session menu for OMNI-4')
    fireEvent.keyDown(within(menu()).getAllByRole('menuitem')[0], { key: 'Escape' })
    noMenu()

    const dots = within(row(/OMNI-5/).parentElement!).getByRole('button', { name: 'Session menu' })
    expect(dots).toHaveAttribute('tabindex', '-1')
    expect(dots).toHaveAttribute('aria-haspopup', 'menu')
    fireEvent.click(dots)
    expect(menu()).toHaveAccessibleName('Session menu for OMNI-5')
    expect(dots).toHaveAttribute('aria-expanded', 'true')
    fireEvent.keyDown(within(menu()).getAllByRole('menuitem')[0], { key: 'Escape' })
    noMenu()

    fireEvent.keyDown(row(/OMNI-2,/), { key: 'ContextMenu' })
    expect(menu()).toHaveAccessibleName('Session menu for OMNI-2')
    fireEvent.keyDown(within(menu()).getAllByRole('menuitem')[0], { key: 'Escape' })
    fireEvent.keyDown(row(/OMNI-2,/), { key: 'F10', shiftKey: true })
    expect(menu()).toBeInTheDocument()
  })

  it('leaves out what it cannot do: no Open, no Workspace settings, no Delete without the actions', async () => {
    mount()
    fireEvent.contextMenu(row(/OMNI-4/))
    await settle()
    const names = within(menu()).getAllByRole('menuitem').map((i) => i.textContent)
    expect(names).not.toContain('Open')
    expect(names).not.toContain('Workspace settings')
    expect(names).not.toContain('Delete…')
    expect(names).toContain('Copy')
  })

  it('pins a row into a Pinned group above the live rows, in pin order, and unpins it', () => {
    mount()
    fireEvent.contextMenu(row(/OMNI-4/))
    fireEvent.click(item('Pin'))
    noMenu()
    expect(within(list()).getByText('Pinned')).toBeInTheDocument()
    expect(rowNames()).toEqual([
      'OMNI-4, triage',
      `OMNI-2815, triage, ${STATE_WORDS.running}`,
      `OMNI-2, fix, ${STATE_WORDS.blocked}`,
      'OMNI-3, rca',
      'OMNI-5, triage',
    ])
    // A second pin goes under the first, whatever its age.
    fireEvent.contextMenu(row(/OMNI-2815/))
    fireEvent.click(item('Pin'))
    expect(rowNames().slice(0, 2)).toEqual(['OMNI-4, triage', `OMNI-2815, triage, ${STATE_WORDS.running}`])
    fireEvent.contextMenu(row(/OMNI-4/))
    expect(item('Unpin')).toBeInTheDocument()
    fireEvent.click(item('Unpin'))
    expect(rowNames()[0]).toBe(`OMNI-2815, triage, ${STATE_WORDS.running}`)
    expect(rowNames()).toContain('OMNI-4, triage')
  })

  it('marks a live row settled and un-settles a finished one, under and over the divider', () => {
    mount()
    fireEvent.contextMenu(row(/OMNI-2815/))
    fireEvent.click(item('Mark settled'))
    // Only the blocked run is left above the divider.
    const divider = () => groupHead(/^Settled$/)
    let all = within(list()).getAllByRole('button').filter((b) => b.getAttribute('aria-label') !== 'Session menu')
    expect(all.indexOf(divider())).toBe(1)
    expect(rowNames()[1]).toBe(`OMNI-2815, triage, ${STATE_WORDS.running}`)

    fireEvent.contextMenu(row(/OMNI-5/))
    fireEvent.click(item('Un-settle'))
    all = within(list()).getAllByRole('button').filter((b) => b.getAttribute('aria-label') !== 'Session menu')
    expect(all.indexOf(divider())).toBe(2)
    expect(rowNames().slice(0, 2)).toEqual([`OMNI-2, fix, ${STATE_WORDS.blocked}`, 'OMNI-5, triage'])

    // The menu offers the way back, and the way back restores the state's own answer.
    fireEvent.contextMenu(row(/OMNI-2815/))
    fireEvent.click(item('Un-settle'))
    expect(rowNames()[0]).toBe(`OMNI-2815, triage, ${STATE_WORDS.running}`)
    expect(sessionPrefs('ws1').group).toEqual({ r5: 'live' })
  })

  it('snoozes a row into a folded Snoozed group until its time, or until the run changes state', () => {
    const { rerender } = mount()
    fireEvent.contextMenu(row(/OMNI-3/))
    fireEvent.click(item('Snooze'))
    const sub = screen.getByRole('menu', { name: 'Snooze' })
    expect(within(sub).getAllByRole('menuitem').map((i) => i.textContent)).toEqual([
      '1 hour',
      'Until tomorrow 9:00',
      'Next week',
      'Pick a time…',
    ])
    fireEvent.click(within(sub).getByRole('menuitem', { name: '1 hour' }))
    noMenu()
    expect(rowNames()).not.toContain('OMNI-3, rca')
    const head = groupHead(/Snoozed \(1\)/)
    expect(head).toHaveAttribute('aria-expanded', 'false')
    fireEvent.click(head)
    expect(rowNames()).toContain('OMNI-3, rca')
    expect(sessionPrefs('ws1').snoozed.r3).toEqual({ until: NOW + 3_600_000, status: 'completed' })

    // Snoozed, the menu offers Unsnooze instead of the choices.
    fireEvent.contextMenu(row(/OMNI-3/))
    expect(item('Unsnooze')).toBeInTheDocument()
    fireEvent.keyDown(item('Unsnooze'), { key: 'Escape' })

    // Time is up: the row is back and the record forgets it.
    rerender(
      <SessionsList runs={RUNS} now={NOW + 3_600_000} sources={SOURCES} workspaceName="omni" workspaceId="ws1" onOpen={() => {}} />,
    )
    expect(within(list()).queryByRole('button', { name: /Snoozed/ })).toBeNull()
    expect(rowNames()).toContain('OMNI-3, rca')
    expect(sessionPrefs('ws1').snoozed).toEqual({})

    // A steer puts the run back to running: the snooze lapses on the state change.
    fireEvent.contextMenu(row(/OMNI-3/))
    fireEvent.click(item('Snooze'))
    fireEvent.click(within(screen.getByRole('menu', { name: 'Snooze' })).getByRole('menuitem', { name: 'Next week' }))
    // The Snoozed group is still unfolded from before, so the row is listed under its head.
    expect(groupHead(/Snoozed \(1\)/)).toBeInTheDocument()
    expect(rowNames().at(-1)).toBe('OMNI-3, rca')
    const moved = RUNS.map((r) => (r.runId === 'r3' ? { ...r, status: 'running' as const } : r))
    rerender(
      <SessionsList runs={moved} now={NOW + 3_600_000} sources={SOURCES} workspaceName="omni" workspaceId="ws1" onOpen={() => {}} />,
    )
    expect(rowNames()).toContain(`OMNI-3, rca, ${STATE_WORDS.running}`)
    expect(sessionPrefs('ws1').snoozed).toEqual({})
  })

  it('lets a time be picked, and refuses one already past', () => {
    mount()
    fireEvent.contextMenu(row(/OMNI-4/))
    fireEvent.click(item('Snooze'))
    fireEvent.click(within(screen.getByRole('menu', { name: 'Snooze' })).getByRole('menuitem', { name: 'Pick a time…' }))
    const dialog = screen.getByRole('dialog', { name: 'Snooze OMNI-4 until' })
    const when = within(dialog).getByLabelText('Snooze until') as HTMLInputElement
    // Tomorrow at nine is offered.
    expect(when.value).toBe('2026-09-17T09:00')

    fireEvent.change(when, { target: { value: '2026-09-15T09:00' } })
    fireEvent.click(within(dialog).getByRole('button', { name: 'Snooze' }))
    expect(within(dialog).getByRole('alert')).toHaveTextContent('Pick a time later than now.')

    fireEvent.change(when, { target: { value: '2026-09-18T14:30' } })
    fireEvent.click(within(dialog).getByRole('button', { name: 'Snooze' }))
    expect(screen.queryByRole('dialog')).toBeNull()
    expect(sessionPrefs('ws1').snoozed.r4?.until).toBe(new Date(2026, 8, 18, 14, 30).getTime())
    expect(groupHead(/Snoozed \(1\)/)).toBeInTheDocument()
  })

  it('renames a row inline, names it by the alias, shows the real title under it on the card, and resets', () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    mount()
    fireEvent.contextMenu(row(/OMNI-2815/))
    fireEvent.click(item('Rename'))
    const field = within(list()).getByRole('textbox', { name: 'Rename OMNI-2815' }) as HTMLInputElement
    expect(document.activeElement).toBe(field)
    expect(field.placeholder).toBe('OMNI-2815')
    fireEvent.change(field, { target: { value: '  the login one ' } })
    fireEvent.keyDown(field, { key: 'Enter' })
    expect(within(list()).queryByRole('textbox')).toBeNull()
    const renamed = row(/the login one/)
    expect(renamed).toHaveAccessibleName(`the login one, triage, ${STATE_WORDS.running}`)
    expect(renamed.querySelector('.sd-session-row__number')).toHaveTextContent('the login one')
    expect(document.activeElement).toBe(renamed)

    fireEvent.focus(renamed)
    act(() => {
      vi.advanceTimersByTime(CARD_OPEN_MS)
    })
    const card = screen.getByRole('tooltip')
    expect(card.querySelector('.sd-session-card__title')).toHaveTextContent('the login one')
    expect(card.querySelector('.sd-session-card__subtitle')).toHaveTextContent('Login loop after reset')
    // The row's own number joins the card, since the row no longer shows it.
    expect(card.querySelectorAll('.sd-session-card__row')[0]).toHaveTextContent('Janus OMNI-2815')

    // Escape leaves the name as it was.
    fireEvent.contextMenu(renamed)
    fireEvent.click(item('Rename'))
    const again = within(list()).getByRole('textbox') as HTMLInputElement
    expect(again.value).toBe('the login one')
    fireEvent.change(again, { target: { value: 'nope' } })
    fireEvent.keyDown(again, { key: 'Escape' })
    expect(row(/the login one/)).toBeInTheDocument()

    fireEvent.contextMenu(row(/the login one/))
    fireEvent.click(item('Reset name'))
    expect(row(/OMNI-2815/)).toHaveAccessibleName(`OMNI-2815, triage, ${STATE_WORDS.running}`)
    expect(sessionPrefs('ws1').aliases).toEqual({})
  })

  it('shows a finished run as unread until it is opened, and marks unread and read from the menu', () => {
    // The record dates from four days ago: r3 (18h) is new, r4 and r5 (5d) are not.
    sessionPrefs('ws1', NOW - 4 * 86_400_000)
    const { rerender } = mount()
    const unread = row(/OMNI-3/)
    expect(unread).toHaveAccessibleName('OMNI-3, rca, unread')
    expect(unread.parentElement).toHaveAttribute('data-unread', 'true')
    expect(unread.querySelector('.sd-session-row__unread')).toBeInTheDocument()
    expect(row(/OMNI-4/).parentElement).not.toHaveAttribute('data-unread')
    // A live run is never unread.
    expect(row(/OMNI-2815/).parentElement).not.toHaveAttribute('data-unread')

    // Opening it reads it.
    rerender(
      <SessionsList runs={RUNS} now={NOW} sources={SOURCES} workspaceName="omni" workspaceId="ws1" currentRunId="r3" onOpen={() => {}} />,
    )
    expect(row(/OMNI-3/)).toHaveAccessibleName('OMNI-3, rca')

    fireEvent.contextMenu(row(/OMNI-4/))
    fireEvent.click(item('Mark unread'))
    expect(row(/OMNI-4/)).toHaveAccessibleName('OMNI-4, triage, unread')
    fireEvent.contextMenu(row(/OMNI-4/))
    fireEvent.click(item('Mark read'))
    expect(row(/OMNI-4/)).toHaveAccessibleName('OMNI-4, triage')

    fireEvent.contextMenu(row(/OMNI-2815/))
    expect(item('Mark unread')).toHaveAttribute('aria-disabled', 'true')
  })

  it('copies the key, the helpdesk number, the run id, the note path and the two URLs, and says so', async () => {
    const writeText = vi.fn(() => Promise.resolve())
    Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true })
    const a = actions()
    const withNote = RUNS.map((r) => (r.runId === 'r3' ? { ...r, notes: ['/notes/OMNI-3-rca.md'] } : r))
    mount({ runs: withNote, actions: a })
    fireEvent.contextMenu(row(/OMNI-3/))
    await settle()
    expect(a.calls.loadLinks).toEqual([['r3']])
    fireEvent.click(item('Copy'))
    const sub = screen.getByRole('menu', { name: 'Copy' })
    expect(within(sub).getAllByRole('menuitem').map((i) => i.textContent)).toEqual([
      'Ticket keyOMNI-3',
      'Helpdesk number#25201',
      'Run idr3',
      'Note path',
      'Tracker URL',
      'Helpdesk URL',
    ])
    fireEvent.click(within(sub).getByRole('menuitem', { name: /Helpdesk number/ }))
    expect(writeText).toHaveBeenCalledWith('25201')
    await settle()
    expect(a.calls.onToast).toEqual([['Copied helpdesk number.']])
    noMenu()

    for (const [name, text] of [
      [/Ticket key/, 'OMNI-3'],
      [/Run id/, 'r3'],
      [/Note path/, '/notes/OMNI-3-rca.md'],
      [/Tracker URL/, 'https://janus.example.com/OMNI-3'],
      [/Helpdesk URL/, 'https://desk.zoho.com/t/25201'],
    ] as [RegExp, string][]) {
      fireEvent.contextMenu(row(/OMNI-3/))
      await settle()
      fireEvent.click(item('Copy'))
      fireEvent.click(within(screen.getByRole('menu', { name: 'Copy' })).getByRole('menuitem', { name }))
      expect(writeText).toHaveBeenLastCalledWith(text)
    }

    // No helpdesk number, no note, no links: only the key and the run id.
    fireEvent.contextMenu(row(/OMNI-4/))
    await settle()
    fireEvent.click(item('Copy'))
    expect(
      within(screen.getByRole('menu', { name: 'Copy' })).getAllByRole('menuitem').map((i) => i.textContent),
    ).toEqual(['Ticket keyOMNI-4', 'Run idr4', 'Tracker URL', 'Helpdesk URL'])
  })

  it('says when the clipboard refuses', async () => {
    Object.defineProperty(navigator, 'clipboard', {
      value: { writeText: () => Promise.reject(new Error('denied')) },
      configurable: true,
    })
    const a = actions()
    mount({ actions: a })
    fireEvent.contextMenu(row(/OMNI-4/))
    fireEvent.click(item('Copy'))
    fireEvent.click(within(screen.getByRole('menu', { name: 'Copy' })).getByRole('menuitem', { name: /Ticket key/ }))
    await settle()
    expect(a.calls.onToast).toEqual([['Could not copy ticket key.', 'error']])
  })

  it('opens the ticket in its tracker or helpdesk, the note file, the run folder, and Settings › General', async () => {
    const opened = vi.spyOn(window, 'open').mockImplementation(() => null)
    const a = actions()
    const withNote = RUNS.map((r) => (r.runId === 'r3' ? { ...r, notes: ['/notes/OMNI-3-rca.md'] } : r))
    mount({ runs: withNote, actions: a })
    fireEvent.contextMenu(row(/OMNI-3/))
    await settle()
    fireEvent.click(item('Open'))
    const sub = () => screen.getByRole('menu', { name: 'Open' })
    expect(within(sub()).getAllByRole('menuitem').map((i) => i.textContent)).toEqual([
      'In Janus',
      'In Zoho Desk',
      'The note file',
      'The run folder',
    ])
    fireEvent.click(within(sub()).getByRole('menuitem', { name: 'In Janus' }))
    expect(opened).toHaveBeenCalledWith('https://janus.example.com/OMNI-3', '_blank', 'noreferrer')

    fireEvent.contextMenu(row(/OMNI-3/))
    await settle()
    fireEvent.click(item('Open'))
    fireEvent.click(within(sub()).getByRole('menuitem', { name: 'In Zoho Desk' }))
    expect(opened).toHaveBeenLastCalledWith('https://desk.zoho.com/t/25201', '_blank', 'noreferrer')

    fireEvent.contextMenu(row(/OMNI-3/))
    await settle()
    fireEvent.click(item('Open'))
    fireEvent.click(within(sub()).getByRole('menuitem', { name: 'The note file' }))
    expect(a.calls.onOpenNote).toEqual([['r3', '/notes/OMNI-3-rca.md']])

    fireEvent.contextMenu(row(/OMNI-3/))
    await settle()
    fireEvent.click(item('Open'))
    fireEvent.click(within(sub()).getByRole('menuitem', { name: 'The run folder' }))
    expect(a.calls.onOpenRunDir).toEqual([['r3']])

    fireEvent.contextMenu(row(/OMNI-3/))
    fireEvent.click(item('Workspace settings'))
    expect(a.calls.onOpenSettings).toHaveLength(1)

    // A browser: no note opener, no folder, and a run with no note has no note row.
    fireEvent.contextMenu(row(/OMNI-4/))
    await settle()
    fireEvent.click(item('Open'))
    expect(within(sub()).getAllByRole('menuitem').map((i) => i.textContent)).toEqual([
      'In Janus',
      'In Zoho Desk',
      'The run folder',
    ])
  })

  it('archives a row into a folded Archived group at the very bottom, and unarchives it', () => {
    mount()
    fireEvent.contextMenu(row(/OMNI-2815/))
    fireEvent.click(item('Archive'))
    expect(rowNames()).not.toContain(`OMNI-2815, triage, ${STATE_WORDS.running}`)
    const head = groupHead(/Archived \(1\)/)
    expect(head).toHaveAttribute('aria-expanded', 'false')
    const heads = within(list()).getAllByRole('button').filter((b) => /Settled|Archived/.test(b.textContent ?? ''))
    expect(heads.map((h) => h.textContent)).toEqual(['Settled', 'Archived (1)'])
    fireEvent.click(head)
    expect(rowNames().at(-1)).toBe(`OMNI-2815, triage, ${STATE_WORDS.running}`)
    fireEvent.contextMenu(row(/OMNI-2815/))
    fireEvent.click(item('Unarchive'))
    expect(within(list()).queryByRole('button', { name: /Archived/ })).toBeNull()
    expect(rowNames()[0]).toBe(`OMNI-2815, triage, ${STATE_WORDS.running}`)
  })

  it('asks before deleting, says what goes and what stays, and shows a refusal', async () => {
    let refuse = true
    const onDelete = vi.fn((_runId: string) =>
      refuse ? Promise.reject(new Error('conflict: run is live')) : Promise.resolve(),
    )
    const a = actions({ onDelete })
    mount({ actions: a })
    fireEvent.contextMenu(row(/OMNI-4/))
    fireEvent.click(item('Delete…'))
    const dialog = () => screen.getByRole('dialog', { name: `Delete run OMNI-4 · triage · ${runDate(RUNS[3])}?` })
    expect(dialog()).toHaveTextContent(
      'This removes its folder under .sirdar/runs. The register row and any filed note stay.',
    )
    expect(onDelete).not.toHaveBeenCalled()

    fireEvent.click(within(dialog()).getByRole('button', { name: 'Cancel' }))
    expect(screen.queryByRole('dialog')).toBeNull()

    fireEvent.contextMenu(row(/OMNI-4/))
    fireEvent.click(item('Delete…'))
    fireEvent.click(within(dialog()).getByRole('button', { name: 'Delete' }))
    await settle()
    expect(onDelete).toHaveBeenCalledWith('r4')
    expect(within(dialog()).getByRole('alert')).toHaveTextContent('conflict: run is live')

    refuse = false
    fireEvent.click(within(dialog()).getByRole('button', { name: 'Delete' }))
    await settle()
    expect(screen.queryByRole('dialog')).toBeNull()
  })

  it('forgets the record of a deleted run', async () => {
    const a = actions()
    mount({ actions: a })
    fireEvent.contextMenu(row(/OMNI-4/))
    fireEvent.click(item('Pin'))
    expect(sessionPrefs('ws1').pinned).toHaveProperty('r4')
    fireEvent.contextMenu(row(/OMNI-4/))
    fireEvent.click(item('Delete…'))
    fireEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Delete' }))
    await settle()
    expect(a.calls.onDelete).toEqual([['r4']])
    expect(sessionPrefs('ws1').pinned).toEqual({})
  })
})

/* ---------- the search ---------- */

describe('the filter', () => {
  it('shows the rows that answer the query, flat, with the match marked in the number', () => {
    mount({ query: 'omni-3' })
    expect(rowNames()).toEqual(['OMNI-3, rca'])
    expect(within(list()).queryByRole('button', { name: 'Settled' })).toBeNull()
    const mark = row(/OMNI-3/).querySelector('.sd-session-row__number mark')
    expect(mark).toHaveTextContent('OMNI-3')
    expect(row(/OMNI-3/).querySelector('.sd-session-row__match')).toBeNull()
  })

  it('marks a match on a field the row does not show on a line under the number', () => {
    mount({ query: 'after RESET' })
    expect(rowNames()).toEqual([`OMNI-2815, triage, ${STATE_WORDS.running}`])
    const line = row(/OMNI-2815/).querySelector('.sd-session-row__match')
    expect(line).toHaveTextContent('Login loop after reset')
    expect(line?.querySelector('mark')).toHaveTextContent('after reset')

    const { unmount } = render(<span />)
    unmount()
  })

  it('reaches the kind, the provider, the model and the state word, and the helpdesk number', () => {
    const { rerender } = mount({ query: 'codex' })
    expect(rowNames()).toEqual([`OMNI-2, fix, ${STATE_WORDS.blocked}`])
    const again = (query: string) =>
      rerender(<SessionsList runs={RUNS} now={NOW} sources={SOURCES} workspaceName="omni" workspaceId="ws1" onOpen={() => {}} query={query} />)
    again('rca')
    expect(rowNames()).toEqual(['OMNI-3, rca'])
    again(STATE_WORDS.failed)
    expect(rowNames()).toEqual(['OMNI-5, triage'])
    again('#252')
    expect(rowNames()).toEqual(['OMNI-3, rca'])
    again('sonnet-5')
    expect(rowNames()).toEqual([`OMNI-2815, triage, ${STATE_WORDS.running}`])
  })

  it('looks through every group, archived and snoozed rows included, and names the query when nothing answers', () => {
    const { rerender } = mount()
    fireEvent.contextMenu(row(/OMNI-4/))
    fireEvent.click(item('Archive'))
    rerender(<SessionsList runs={RUNS} now={NOW} sources={SOURCES} workspaceName="omni" workspaceId="ws1" onOpen={() => {}} query="omni-4" />)
    expect(rowNames()).toEqual(['OMNI-4, triage'])
    rerender(<SessionsList runs={RUNS} now={NOW} sources={SOURCES} workspaceName="omni" workspaceId="ws1" onOpen={() => {}} query="  zzz " />)
    expect(within(list()).queryAllByRole('button', { name: /OMNI/ })).toEqual([])
    expect(list()).toHaveTextContent('No sessions match “zzz”.')
  })
})

describe('the notes search', () => {
  const mountNotes = (props: Partial<React.ComponentProps<typeof SessionsList>>) =>
    mount({ mode: 'notes', query: 'export', ...props })

  it('lists one row per run the service found the query in, with the excerpt marked under it', () => {
    mountNotes({
      hits: [
        searchHit({ runId: 'r3', excerpt: '…the statement Export times out…' }),
        searchHit({ runId: 'r3', source: 'answer', excerpt: 'export pool exhausted' }),
        searchHit({ runId: 'r3', excerpt: 'a third that is not shown' }),
        searchHit({ runId: 'r5', excerpt: 'nothing about export here' }),
        searchHit({ runId: 'gone', excerpt: 'a run no longer listed' }),
      ],
    })
    expect(rowNames()).toEqual(['OMNI-3, rca', 'OMNI-5, triage'])
    const lines = [...row(/OMNI-3/).querySelectorAll('.sd-session-row__match')]
    expect(lines.map((l) => l.textContent)).toEqual(['…the statement Export times out…', 'export pool exhausted'])
    expect(lines[0].querySelector('mark')).toHaveTextContent('Export')
    expect(within(list()).queryByRole('button', { name: 'Settled' })).toBeNull()
  })

  it('says it is searching, says why it could not, says when nothing matched, and asks for a query', () => {
    const { rerender } = mountNotes({ hitsState: 'loading' })
    expect(list()).toHaveTextContent('Searching notes…')
    const again = (props: Partial<React.ComponentProps<typeof SessionsList>>) =>
      rerender(
        <SessionsList runs={RUNS} now={NOW} sources={SOURCES} workspaceName="omni" workspaceId="ws1" onOpen={() => {}} mode="notes" query="export" {...props} />,
      )
    again({ hitsState: 'error', hitsError: '500 Internal Server Error' })
    expect(list()).toHaveTextContent('Could not search notes. 500 Internal Server Error')
    again({ hits: [] })
    expect(list()).toHaveTextContent('No notes match “export”.')
    again({ query: '' })
    expect(list()).toHaveTextContent('Type to search the notes.')
  })

  it('opens the run from a hit row', () => {
    const { onOpen } = mountNotes({ hits: [searchHit({ runId: 'r5' })] })
    fireEvent.click(row(/OMNI-5/))
    expect(onOpen).toHaveBeenCalledWith('r5')
  })
})
