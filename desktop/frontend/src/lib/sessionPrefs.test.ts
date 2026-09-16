import { afterEach, describe, expect, it, vi } from 'vitest'
import { run } from '../store/fakeTransport'
import {
  forceGroup,
  forgetRun,
  groupRuns,
  isSnoozed,
  isUnread,
  markRead,
  markUnread,
  PREFS_VERSION,
  pruneSnoozes,
  renameRun,
  resetName,
  resetSessionPrefs,
  sessionPrefs,
  setArchived,
  snoozeRun,
  snoozeUntil,
  subscribeSessionPrefs,
  togglePin,
  unsnoozeRun,
} from './sessionPrefs'

afterEach(() => {
  localStorage.clear()
  resetSessionPrefs()
})

/** A still clock: 2026-09-16 (a Wednesday) at 10:00 local. */
const NOW = new Date(2026, 8, 16, 10, 0, 0).getTime()
const at = (hoursAgo: number) => new Date(NOW - hoursAgo * 3_600_000).toISOString()

const WS = 'ws1'
const KEY = 'sirdar.sessionPrefs.ws1'

const RUNS = [
  run({ runId: 'r1', key: 'OMNI-1', status: 'running', updatedAt: at(1) }),
  run({ runId: 'r2', key: 'OMNI-2', status: 'blocked', updatedAt: at(2) }),
  run({ runId: 'r3', key: 'OMNI-3', status: 'completed', updatedAt: at(3) }),
  run({ runId: 'r4', key: 'OMNI-4', status: 'failed', updatedAt: at(4) }),
  run({ runId: 'r5', key: 'OMNI-5', status: 'completed', updatedAt: at(5) }),
]

const ids = (runs: { runId: string }[]) => runs.map((r) => r.runId)

describe('the record', () => {
  it('starts empty, versioned, stamped with now, and is the same object until it changes', () => {
    const first = sessionPrefs(WS, NOW)
    expect(first.v).toBe(PREFS_VERSION)
    expect(first.since).toBe(NOW)
    expect(sessionPrefs(WS)).toBe(first)
    togglePin(WS, 'r1', NOW)
    expect(sessionPrefs(WS)).not.toBe(first)
  })

  it('is written per workspace and read back, with unknown shapes dropped', () => {
    togglePin(WS, 'r1', NOW)
    renameRun(WS, 'r2', 'the login one')
    expect(JSON.parse(localStorage.getItem(KEY) ?? '{}')).toMatchObject({
      v: PREFS_VERSION,
      pinned: { r1: NOW },
      aliases: { r2: 'the login one' },
    })
    expect(localStorage.getItem('sirdar.sessionPrefs.ws2')).toBeNull()

    localStorage.setItem(
      KEY,
      JSON.stringify({
        v: 1,
        since: 5,
        pinned: { ok: 7, bad: 'x' },
        group: { ok: 'live', bad: 'sideways' },
        snoozed: { ok: { until: 9, status: 'completed' }, bad: 3 },
        aliases: { ok: 'name', bad: '' },
        opened: { ok: 0 },
        archived: { ok: true, bad: 'yes' },
        stray: 1,
      }),
    )
    resetSessionPrefs()
    expect(sessionPrefs(WS, NOW)).toEqual({
      v: 1,
      since: 5,
      pinned: { ok: 7 },
      group: { ok: 'live' },
      snoozed: { ok: { until: 9, status: 'completed' } },
      aliases: { ok: 'name' },
      opened: { ok: 0 },
      archived: { ok: true },
    })

    localStorage.setItem(KEY, 'not json')
    resetSessionPrefs()
    expect(sessionPrefs(WS, NOW).since).toBe(NOW)
  })

  it('notifies subscribers on every change and not on a no-op', () => {
    const listener = vi.fn()
    const off = subscribeSessionPrefs(listener)
    forceGroup(WS, 'r1', null) // nothing to remove
    expect(listener).not.toHaveBeenCalled()
    forceGroup(WS, 'r1', 'settled')
    expect(listener).toHaveBeenCalledTimes(1)
    off()
    forceGroup(WS, 'r1', null)
    expect(listener).toHaveBeenCalledTimes(1)
  })
})

describe('groupRuns', () => {
  it('splits live from settled with no record, newest first', () => {
    const g = groupRuns(RUNS.slice().reverse(), sessionPrefs(WS, NOW), NOW)
    expect(ids(g.live)).toEqual(['r1', 'r2'])
    expect(ids(g.settled)).toEqual(['r3', 'r4', 'r5'])
    expect(g.pinned).toEqual([])
    expect(g.snoozed).toEqual([])
    expect(g.archived).toEqual([])
  })

  it('pins in pin order, above everything, and unpins on the second toggle', () => {
    togglePin(WS, 'r4', NOW)
    togglePin(WS, 'r1', NOW + 1)
    let g = groupRuns(RUNS, sessionPrefs(WS), NOW)
    expect(ids(g.pinned)).toEqual(['r4', 'r1'])
    expect(ids(g.live)).toEqual(['r2'])
    expect(ids(g.settled)).toEqual(['r3', 'r5'])
    togglePin(WS, 'r4', NOW + 2)
    g = groupRuns(RUNS, sessionPrefs(WS), NOW)
    expect(ids(g.pinned)).toEqual(['r1'])
    expect(ids(g.settled)).toEqual(['r3', 'r4', 'r5'])
  })

  it('holds a row in the group it was forced into, whatever its state', () => {
    forceGroup(WS, 'r1', 'settled')
    forceGroup(WS, 'r5', 'live')
    const g = groupRuns(RUNS, sessionPrefs(WS), NOW)
    expect(ids(g.live)).toEqual(['r2', 'r5'])
    expect(ids(g.settled)).toEqual(['r1', 'r3', 'r4'])
    forceGroup(WS, 'r1', null)
    expect(ids(groupRuns(RUNS, sessionPrefs(WS), NOW).live)).toEqual(['r1', 'r2', 'r5'])
  })

  it('hides a snoozed row until its time, or until the run changes state', () => {
    snoozeRun(WS, 'r3', NOW + 3_600_000, 'completed')
    let prefs = sessionPrefs(WS)
    expect(ids(groupRuns(RUNS, prefs, NOW).snoozed)).toEqual(['r3'])
    expect(ids(groupRuns(RUNS, prefs, NOW).settled)).toEqual(['r4', 'r5'])
    // Time is up.
    expect(groupRuns(RUNS, prefs, NOW + 3_600_000).snoozed).toEqual([])
    // Or the run moved on: a steer put it back to running.
    const moved = RUNS.map((r) => (r.runId === 'r3' ? { ...r, status: 'running' as const } : r))
    expect(groupRuns(moved, prefs, NOW).snoozed).toEqual([])
    expect(isSnoozed(prefs, moved[2], NOW)).toBe(false)
    expect(ids(groupRuns(moved, prefs, NOW).live)).toEqual(['r1', 'r2', 'r3'])

    unsnoozeRun(WS, 'r3')
    prefs = sessionPrefs(WS)
    expect(prefs.snoozed).toEqual({})
  })

  it('prunes snoozes that no longer hold and keeps the ones that do', () => {
    snoozeRun(WS, 'r3', NOW + 3_600_000, 'completed')
    snoozeRun(WS, 'r4', NOW - 1, 'failed')
    snoozeRun(WS, 'r5', NOW + 3_600_000, 'running') // the run is completed now
    snoozeRun(WS, 'gone', NOW + 3_600_000, 'completed') // not in the list right now
    expect(pruneSnoozes(WS, RUNS, NOW)).toBe(true)
    expect(Object.keys(sessionPrefs(WS).snoozed).sort()).toEqual(['gone', 'r3'])
    expect(pruneSnoozes(WS, RUNS, NOW)).toBe(false)
  })

  it('archives over every other arrangement, and unarchives', () => {
    togglePin(WS, 'r1', NOW)
    snoozeRun(WS, 'r3', NOW + 3_600_000, 'completed')
    setArchived(WS, 'r1', true)
    setArchived(WS, 'r3', true)
    let g = groupRuns(RUNS, sessionPrefs(WS), NOW)
    expect(ids(g.archived)).toEqual(['r1', 'r3'])
    expect(g.pinned).toEqual([])
    expect(g.snoozed).toEqual([])
    setArchived(WS, 'r1', false)
    g = groupRuns(RUNS, sessionPrefs(WS), NOW)
    expect(ids(g.archived)).toEqual(['r3'])
    expect(ids(g.pinned)).toEqual(['r1'])
  })
})

describe('snoozeUntil', () => {
  it('is an hour on, tomorrow at nine, and the coming Monday at nine', () => {
    expect(snoozeUntil('hour', NOW)).toBe(NOW + 3_600_000)
    expect(new Date(snoozeUntil('tomorrow', NOW))).toEqual(new Date(2026, 8, 17, 9, 0, 0))
    // From a Wednesday the coming Monday is the 21st.
    expect(new Date(snoozeUntil('nextWeek', NOW))).toEqual(new Date(2026, 8, 21, 9, 0, 0))
    // From a Monday it is the Monday after, not the same morning.
    const monday = new Date(2026, 8, 21, 10, 0, 0).getTime()
    expect(new Date(snoozeUntil('nextWeek', monday))).toEqual(new Date(2026, 8, 28, 9, 0, 0))
  })
})

describe('names', () => {
  it('keeps a trimmed alias, and an empty one is a reset', () => {
    renameRun(WS, 'r1', '  login loop  ')
    expect(sessionPrefs(WS).aliases).toEqual({ r1: 'login loop' })
    renameRun(WS, 'r1', '   ')
    expect(sessionPrefs(WS).aliases).toEqual({})
    renameRun(WS, 'r1', 'again')
    resetName(WS, 'r1')
    expect(sessionPrefs(WS).aliases).toEqual({})
  })
})

describe('unread', () => {
  it('is a finished run that changed after it was last opened, never a live one, and not one from before the record', () => {
    const prefs = sessionPrefs(WS, NOW - 3.5 * 3_600_000)
    // r3 finished 3h ago, after the record; r4 and r5 before it.
    expect(isUnread(prefs, RUNS[2])).toBe(true)
    expect(isUnread(prefs, RUNS[3])).toBe(false)
    expect(isUnread(prefs, RUNS[0])).toBe(false)
    expect(isUnread(prefs, RUNS[1])).toBe(false)

    markRead(WS, 'r3', NOW)
    expect(isUnread(sessionPrefs(WS), RUNS[2])).toBe(false)
    // A later change makes it new again.
    expect(isUnread(sessionPrefs(WS), { ...RUNS[2], updatedAt: at(-1) })).toBe(true)

    markUnread(WS, 'r5')
    expect(isUnread(sessionPrefs(WS), RUNS[4])).toBe(true)
    markRead(WS, 'r5', NOW)
    expect(isUnread(sessionPrefs(WS), RUNS[4])).toBe(false)
  })
})

describe('forgetRun', () => {
  it('drops every record of a run', () => {
    togglePin(WS, 'r1', NOW)
    forceGroup(WS, 'r1', 'settled')
    snoozeRun(WS, 'r1', NOW + 1, 'completed')
    renameRun(WS, 'r1', 'x')
    markUnread(WS, 'r1')
    setArchived(WS, 'r1', true)
    togglePin(WS, 'r2', NOW)
    forgetRun(WS, 'r1')
    const p = sessionPrefs(WS)
    expect(p.pinned).toEqual({ r2: NOW })
    expect(p.group).toEqual({})
    expect(p.snoozed).toEqual({})
    expect(p.aliases).toEqual({})
    expect(p.opened).toEqual({})
    expect(p.archived).toEqual({})
  })
})
