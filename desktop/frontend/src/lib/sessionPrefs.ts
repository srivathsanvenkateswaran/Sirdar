import type { RunState, RunSummary } from '../api/types'
import { parseTime } from './format'

/**
 * What this person has done to the rows of one workspace's sessions list:
 * pinned, un-settled, snoozed, renamed, marked unread, archived.
 *
 * None of it is about the run. A run is a directory on disk and the service
 * says what it is; these are a reader's arrangements of the list in this
 * browser, like the theme and the Settled fold, so they live in localStorage
 * and never near the workspace. One record per workspace, under
 * `sirdar.sessionPrefs.<workspaceId>`, versioned so a later shape can read
 * an earlier one rather than trip over it.
 *
 * The module is a store the way `sessionsShow` is: `sessionPrefs(ws)` is the
 * current record (the same object until something changes, so
 * `useSyncExternalStore` can hold it), the mutators write and notify, and
 * `groupRuns` is the one pure function that applies the whole record to a
 * list of runs.
 */

export const PREFS_VERSION = 1

/** A snooze remembers the state the run had, so a run that moves on wakes up. */
export interface SnoozeRecord {
  until: number
  status: RunState
}

export type ForcedGroup = 'settled' | 'live'

export interface SessionPrefs {
  v: typeof PREFS_VERSION
  /**
   * When the record was first written. A run finished before this is not
   * unread: on the first day every settled run would otherwise be bold.
   */
  since: number
  /** Run id to the time it was pinned; the group is ordered by it. */
  pinned: Record<string, number>
  /** A row held in a group its state does not put it in. */
  group: Record<string, ForcedGroup>
  snoozed: Record<string, SnoozeRecord>
  /** A local name for the row; the card shows the real title beneath it. */
  aliases: Record<string, string>
  /** Run id to when it was last opened, in ms; 0 is "marked unread". */
  opened: Record<string, number>
  archived: Record<string, true>
}

const KEY_PREFIX = 'sirdar.sessionPrefs.'

function key(workspaceId: string): string {
  return KEY_PREFIX + workspaceId
}

function empty(now: number): SessionPrefs {
  return {
    v: PREFS_VERSION,
    since: now,
    pinned: {},
    group: {},
    snoozed: {},
    aliases: {},
    opened: {},
    archived: {},
  }
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

/** Takes whatever the stored JSON holds and answers with a record of the current shape. */
function coerce(raw: unknown, now: number): SessionPrefs {
  const out = empty(now)
  if (!isRecord(raw)) return out
  if (typeof raw.since === 'number' && Number.isFinite(raw.since)) out.since = raw.since
  const pick = <T>(name: keyof SessionPrefs, ok: (v: unknown) => v is T): Record<string, T> => {
    const src = raw[name]
    const dst: Record<string, T> = {}
    if (!isRecord(src)) return dst
    for (const [id, value] of Object.entries(src)) if (ok(value)) dst[id] = value
    return dst
  }
  out.pinned = pick(
    'pinned',
    (v): v is number => typeof v === 'number' && Number.isFinite(v),
  )
  out.group = pick('group', (v): v is ForcedGroup => v === 'settled' || v === 'live')
  out.snoozed = pick(
    'snoozed',
    (v): v is SnoozeRecord =>
      isRecord(v) && typeof v.until === 'number' && typeof v.status === 'string',
  )
  out.aliases = pick('aliases', (v): v is string => typeof v === 'string' && v !== '')
  out.opened = pick(
    'opened',
    (v): v is number => typeof v === 'number' && Number.isFinite(v),
  )
  out.archived = pick('archived', (v): v is true => v === true)
  return out
}

/** localStorage is absent in some tests and can throw in a locked-down webview. */
function read(workspaceId: string, now: number): SessionPrefs {
  try {
    const stored = globalThis.localStorage?.getItem(key(workspaceId))
    if (!stored) return empty(now)
    return coerce(JSON.parse(stored), now)
  } catch {
    return empty(now)
  }
}

function write(workspaceId: string, prefs: SessionPrefs): void {
  try {
    globalThis.localStorage?.setItem(key(workspaceId), JSON.stringify(prefs))
  } catch {
    // An arrangement that cannot be remembered still holds this session.
  }
}

const cache = new Map<string, SessionPrefs>()
const watchers = new Set<() => void>()

/** The workspace's record, the same object until something in it changes. */
export function sessionPrefs(workspaceId: string, now = Date.now()): SessionPrefs {
  let prefs = cache.get(workspaceId)
  if (!prefs) {
    prefs = read(workspaceId, now)
    cache.set(workspaceId, prefs)
  }
  return prefs
}

/** Notifies on every change to any workspace's record; the return value unsubscribes. */
export function subscribeSessionPrefs(listener: () => void): () => void {
  watchers.add(listener)
  return () => {
    watchers.delete(listener)
  }
}

/** Drops the cache, so one test cannot leak into the next. */
export function resetSessionPrefs(): void {
  cache.clear()
  for (const watcher of [...watchers]) watcher()
}

function update(workspaceId: string, change: (prefs: SessionPrefs) => SessionPrefs): void {
  const before = sessionPrefs(workspaceId)
  const after = change(before)
  if (after === before) return
  cache.set(workspaceId, after)
  write(workspaceId, after)
  for (const watcher of [...watchers]) watcher()
}

function without<T>(map: Record<string, T>, id: string): Record<string, T> {
  if (!(id in map)) return map
  const next = { ...map }
  delete next[id]
  return next
}

/** The record with one map swapped, or the same record when the map did not change. */
function patch<K extends keyof SessionPrefs>(p: SessionPrefs, name: K, next: SessionPrefs[K]): SessionPrefs {
  return next === p[name] ? p : { ...p, [name]: next }
}

// --- pin ---

export function togglePin(workspaceId: string, runId: string, now = Date.now()): void {
  update(workspaceId, (p) => {
    if (runId in p.pinned) return patch(p, 'pinned', without(p.pinned, runId))
    // A pin made in the same instant as the last one still goes under it.
    const stamp = Math.max(now, ...Object.values(p.pinned).map((t) => t + 1))
    return patch(p, 'pinned', { ...p.pinned, [runId]: stamp })
  })
}

// --- settled / live ---

/** Holds the row in `group` whatever its state says; `null` lets the state decide again. */
export function forceGroup(workspaceId: string, runId: string, group: ForcedGroup | null): void {
  update(workspaceId, (p) =>
    patch(
      p,
      'group',
      group === null
        ? without(p.group, runId)
        : p.group[runId] === group
          ? p.group
          : { ...p.group, [runId]: group },
    ),
  )
}

// --- snooze ---

export type SnoozeChoice = 'hour' | 'tomorrow' | 'nextWeek'

/** The menu's three fixed snoozes, as instants: an hour on; tomorrow at 9:00; the coming Monday at 9:00. */
export function snoozeUntil(choice: SnoozeChoice, now = Date.now()): number {
  const at = new Date(now)
  switch (choice) {
    case 'hour':
      return now + 60 * 60_000
    case 'tomorrow': {
      const t = new Date(at.getFullYear(), at.getMonth(), at.getDate() + 1, 9, 0, 0, 0)
      return t.getTime()
    }
    case 'nextWeek': {
      // Monday is 1; from a Monday it is the Monday after.
      const ahead = ((8 - at.getDay()) % 7) || 7
      const t = new Date(at.getFullYear(), at.getMonth(), at.getDate() + ahead, 9, 0, 0, 0)
      return t.getTime()
    }
  }
}

export function snoozeRun(workspaceId: string, runId: string, until: number, status: RunState): void {
  update(workspaceId, (p) => patch(p, 'snoozed', { ...p.snoozed, [runId]: { until, status } }))
}

export function unsnoozeRun(workspaceId: string, runId: string): void {
  update(workspaceId, (p) => patch(p, 'snoozed', without(p.snoozed, runId)))
}

/** Whether the row is hidden right now: the snooze has not run out, and the run has not moved on. */
export function isSnoozed(prefs: SessionPrefs, run: Pick<RunSummary, 'runId' | 'status'>, now: number): boolean {
  const rec = prefs.snoozed[run.runId]
  return Boolean(rec && rec.until > now && rec.status === run.status)
}

/**
 * Forgets snoozes that have run out or whose run changed state, so the
 * record does not keep rows that are already back in the list. Answers
 * whether anything was dropped.
 */
export function pruneSnoozes(workspaceId: string, runs: RunSummary[], now = Date.now()): boolean {
  let dropped = false
  update(workspaceId, (p) => {
    const byId = new Map(runs.map((r) => [r.runId, r]))
    let next = p.snoozed
    for (const id of Object.keys(p.snoozed)) {
      const run = byId.get(id)
      if (run && isSnoozed(p, run, now)) continue
      if (!run) continue // a run not listed right now is left alone
      next = without(next, id)
      dropped = true
    }
    return patch(p, 'snoozed', next)
  })
  return dropped
}

// --- rename ---

export function renameRun(workspaceId: string, runId: string, alias: string): void {
  const name = alias.trim()
  update(workspaceId, (p) =>
    patch(
      p,
      'aliases',
      name === ''
        ? without(p.aliases, runId)
        : p.aliases[runId] === name
          ? p.aliases
          : { ...p.aliases, [runId]: name },
    ),
  )
}

export function resetName(workspaceId: string, runId: string): void {
  renameRun(workspaceId, runId, '')
}

// --- read / unread ---

export function markRead(workspaceId: string, runId: string, now = Date.now()): void {
  update(workspaceId, (p) => patch(p, 'opened', { ...p.opened, [runId]: now }))
}

export function markUnread(workspaceId: string, runId: string): void {
  update(workspaceId, (p) => patch(p, 'opened', p.opened[runId] === 0 ? p.opened : { ...p.opened, [runId]: 0 }))
}

/** A run still happening or waiting on a person: the two the list keeps above the fold. */
export function isLiveStatus(status: RunState): boolean {
  return status === 'preparing' || status === 'running' || status === 'blocked'
}

/** When the run last changed, as ms; its start when it has not; 0 when neither is a time. */
export function stampOf(run: Pick<RunSummary, 'updatedAt' | 'startedAt'>): number {
  const updated = parseTime(run.updatedAt)
  if (!Number.isNaN(updated)) return updated
  const started = parseTime(run.startedAt)
  return Number.isNaN(started) ? 0 : started
}

/**
 * A finished run this person has not opened since it finished. A live run
 * is never unread — its dot says live — and a run that finished before the
 * record existed is not either, or the first day would be all bold.
 */
export function isUnread(prefs: SessionPrefs, run: RunSummary): boolean {
  if (isLiveStatus(run.status)) return false
  const opened = prefs.opened[run.runId]
  const since = opened === undefined ? prefs.since : opened
  return stampOf(run) > since
}

// --- archive ---

export function setArchived(workspaceId: string, runId: string, archived: boolean): void {
  update(workspaceId, (p) =>
    patch(
      p,
      'archived',
      archived ? (p.archived[runId] ? p.archived : { ...p.archived, [runId]: true }) : without(p.archived, runId),
    ),
  )
}

// --- a deleted run ---

/** Drops every record of a run whose directory is gone. */
export function forgetRun(workspaceId: string, runId: string): void {
  update(workspaceId, (p) => {
    let next = p
    next = patch(next, 'pinned', without(p.pinned, runId))
    next = patch(next, 'group', without(p.group, runId))
    next = patch(next, 'snoozed', without(p.snoozed, runId))
    next = patch(next, 'aliases', without(p.aliases, runId))
    next = patch(next, 'opened', without(p.opened, runId))
    next = patch(next, 'archived', without(p.archived, runId))
    return next
  })
}

// --- the list ---

export interface GroupedRuns {
  /** In the order they were pinned. */
  pinned: RunSummary[]
  live: RunSummary[]
  settled: RunSummary[]
  snoozed: RunSummary[]
  archived: RunSummary[]
}

/** Newest first, by last change. */
export function newestFirst(runs: RunSummary[]): RunSummary[] {
  return runs.slice().sort((a, b) => stampOf(b) - stampOf(a))
}

/**
 * The list's groups with the whole record applied, in the order the list
 * draws them. Archived wins over everything, then a snooze that still
 * holds, then a pin; the rest go live or settled by state, unless the
 * record holds the row in the other group.
 */
export function groupRuns(runs: RunSummary[], prefs: SessionPrefs, now = Date.now()): GroupedRuns {
  const out: GroupedRuns = { pinned: [], live: [], settled: [], snoozed: [], archived: [] }
  for (const run of newestFirst(runs)) {
    if (prefs.archived[run.runId]) out.archived.push(run)
    else if (isSnoozed(prefs, run, now)) out.snoozed.push(run)
    else if (run.runId in prefs.pinned) out.pinned.push(run)
    else {
      const forced = prefs.group[run.runId]
      const live = forced ? forced === 'live' : isLiveStatus(run.status)
      if (live) out.live.push(run)
      else out.settled.push(run)
    }
  }
  out.pinned.sort((a, b) => (prefs.pinned[a.runId] ?? 0) - (prefs.pinned[b.runId] ?? 0))
  return out
}
