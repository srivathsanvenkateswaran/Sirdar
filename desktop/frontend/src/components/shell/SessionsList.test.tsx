import { act, fireEvent, render, screen, within } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { SourcesSummary } from '../../api/types'
import { resetSessionsShow, setSessionsShow } from '../../lib/sessionsShow'
import { run } from '../../store/fakeTransport'
import { STATE_WORDS } from '../../ui/status-badge'
import SessionsList, { CARD_DELAY_MS, SHOWN_LIMIT, shortAge, splitRuns } from './SessionsList'

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
    it('opens beside a row after a moment, with the title, the other number, the kind and state, the model and the workspace', () => {
      vi.useFakeTimers()
      mount()
      const row = rows()[0]
      fireEvent.mouseEnter(row)
      expect(screen.queryByRole('tooltip')).toBeNull()
      act(() => {
        vi.advanceTimersByTime(CARD_DELAY_MS)
      })
      const card = screen.getByRole('tooltip')
      expect(card.style.position).toBe('fixed')
      expect(row).toHaveAttribute('aria-describedby', card.id)
      expect(card.querySelector('.sd-session-card__title')).toHaveTextContent('Login loop after reset')
      const lines = [...card.querySelectorAll<HTMLElement>('.sd-session-card__row')]
      expect(lines[0]).toHaveTextContent('Zoho Desk #25312')
      expect(within(lines[0]).getByRole('img', { name: 'Zoho Desk' })).toBeInTheDocument()
      expect(lines[1]).toHaveTextContent(`triage${STATE_WORDS.running}`)
      expect(lines[2]).toHaveTextContent('claude-sonnet-5')
      expect(within(lines[2]).getByRole('img', { name: 'Claude' })).toBeInTheDocument()
      expect(lines[3]).toHaveTextContent('omni')

      // Leaving closes it.
      fireEvent.mouseLeave(row)
      expect(screen.queryByRole('tooltip')).toBeNull()
    })

    it('names the run by its own number and source when it has no other number, and says model unknown', () => {
      vi.useFakeTimers()
      mount()
      const row = rows()[1]
      fireEvent.focus(row)
      act(() => {
        vi.advanceTimersByTime(CARD_DELAY_MS)
      })
      const card = screen.getByRole('tooltip')
      expect(card.querySelector('.sd-session-card__title')).toHaveTextContent('OMNI-2')
      const lines = [...card.querySelectorAll('.sd-session-card__row')]
      expect(lines[0]).toHaveTextContent('Janus OMNI-2')
      expect(lines[1]).toHaveTextContent(`fix${STATE_WORDS.blocked}`)
      // The fake run names a model; a run that has not reported one says so.
      fireEvent.blur(row)
      expect(screen.queryByRole('tooltip')).toBeNull()
    })

    it('closes on Escape and does not open when the pointer leaves before the delay', () => {
      vi.useFakeTimers()
      mount()
      const row = rows()[0]
      fireEvent.mouseEnter(row)
      act(() => {
        vi.advanceTimersByTime(CARD_DELAY_MS - 50)
      })
      fireEvent.mouseLeave(row)
      act(() => {
        vi.advanceTimersByTime(100)
      })
      expect(screen.queryByRole('tooltip')).toBeNull()

      fireEvent.focus(row)
      act(() => {
        vi.advanceTimersByTime(CARD_DELAY_MS)
      })
      expect(screen.getByRole('tooltip')).toBeInTheDocument()
      fireEvent.keyDown(row, { key: 'Escape' })
      expect(screen.queryByRole('tooltip')).toBeNull()
    })

    it('is drawn open in the flow for the gallery', () => {
      mount({ pinnedCard: 'r1' })
      const card = screen.getByRole('tooltip')
      expect(card).toHaveAttribute('data-static', 'true')
      expect(card.style.position).toBe('')
      fireEvent.mouseLeave(rows()[0])
      expect(screen.getByRole('tooltip')).toBeInTheDocument()
    })
  })
})
