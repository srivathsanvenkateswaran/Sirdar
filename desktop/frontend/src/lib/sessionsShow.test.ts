// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { SourcesSummary } from '../api/types'
import {
  helpdeskNumber,
  resetSessionsShow,
  sessionsShow,
  setSessionsShow,
  shownNumber,
  subscribeSessionsShow,
} from './sessionsShow'

afterEach(() => {
  localStorage.clear()
  resetSessionsShow()
})

const SOURCES: SourcesSummary = {
  tracker: { adapter: 'exec', name: 'Janus', host: 'janus.example.com' },
  helpdesk: { adapter: 'zohodesk', name: 'Zoho Desk', host: 'desk.zoho.com' },
}

describe('the sessions-show preference', () => {
  it('starts on the tracker number with nothing stored', () => {
    expect(sessionsShow()).toBe('tracker')
    expect(localStorage.getItem('sirdar.sessionsShow')).toBeNull()
  })

  it('remembers a choice and tells its watchers', () => {
    const watcher = vi.fn()
    const stop = subscribeSessionsShow(watcher)
    setSessionsShow('helpdesk')
    expect(watcher).toHaveBeenCalledTimes(1)
    expect(localStorage.getItem('sirdar.sessionsShow')).toBe('helpdesk')
    resetSessionsShow()
    expect(sessionsShow()).toBe('helpdesk')
    // The same value again is not a change.
    setSessionsShow('helpdesk')
    expect(watcher).toHaveBeenCalledTimes(2)
    stop()
  })

  it('ignores a stored value that is not a choice', () => {
    localStorage.setItem('sirdar.sessionsShow', 'both')
    resetSessionsShow()
    expect(sessionsShow()).toBe('tracker')
  })
})

describe('shownNumber', () => {
  const run = { key: 'OMNI-2815', helpdeskKey: '25312' }

  it('shows the tracker key with the helpdesk number in the other slot', () => {
    expect(shownNumber(run, 'tracker', SOURCES)).toEqual({
      text: 'OMNI-2815',
      role: 'tracker',
      source: SOURCES.tracker,
      other: 'Zoho Desk #25312',
    })
  })

  it('shows the helpdesk number with a hash, and the key in the other slot', () => {
    expect(shownNumber(run, 'helpdesk', SOURCES)).toEqual({
      text: '#25312',
      role: 'helpdesk',
      source: SOURCES.helpdesk,
      other: 'Janus OMNI-2815',
    })
  })

  it('falls back to the tracker key when the run has no helpdesk number, whatever the preference', () => {
    expect(shownNumber({ key: 'OMNI-1', helpdeskKey: '' }, 'helpdesk', SOURCES)).toEqual({
      text: 'OMNI-1',
      role: 'tracker',
      source: SOURCES.tracker,
      other: '',
    })
    expect(shownNumber({ key: 'OMNI-1' }, 'helpdesk').other).toBe('')
  })

  it('writes the other number bare when the config names no source', () => {
    expect(shownNumber(run, 'tracker').other).toBe('#25312')
    expect(shownNumber(run, 'helpdesk').other).toBe('OMNI-2815')
  })

  it('does not double a hash the helpdesk already wrote', () => {
    expect(helpdeskNumber('#25312')).toBe('#25312')
    expect(helpdeskNumber(' 25312 ')).toBe('#25312')
  })
})
