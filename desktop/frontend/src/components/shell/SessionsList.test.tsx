import { act, fireEvent, render, screen, within } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { SourcesSummary } from '../../api/types'
import { resetSessionsShow, setSessionsShow } from '../../lib/sessionsShow'
import { run } from '../../store/fakeTransport'
import { STATE_WORDS } from '../../ui/status-badge'
import SessionsList, { CARD_CLOSE_MS, CARD_OPEN_MS, SHOWN_LIMIT, shortAge, splitRuns } from './SessionsList'

afterEach(() => {
  localStorage.clear()
  resetSessionsShow()
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
    <SessionsList runs={RUNS} now={NOW} sources={SOURCES} workspaceName="omni" onOpen={onOpen} {...props} />,
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
    // The divider sits between the two halves.
    const all = within(list()).getAllByRole('button')
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
