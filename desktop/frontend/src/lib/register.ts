import type { RegisterRow, RunState, RunSummary } from '../api/types'

/**
 * One line of the Register screen: every `RegisterRow` sharing a tracker key,
 * folded into whichever of the three stages (triage, RCA, resolution) exist.
 */
export interface RegisterGroup {
  key: string
  service: string
  /** The note's own title and the company its frontmatter named — the newest row of this
   *  key that recorded either wins, mirroring `groupRegister` in cmd/sirdar/cmd_register.go. */
  title: string
  company: string
  rows: RegisterRow[]
  triage?: RegisterRow
  rca?: RegisterRow
  resolution?: RegisterRow
  /** The date of the most recent 'fix' row. A fix writes no note of its own — what the
   *  register records is that one happened, and when — mirroring registerEntry.fixDate. */
  fixDate?: string
}

/** Groups register rows by tracker key, keeping first-seen order. */
export function groupRegisterRows(rows: RegisterRow[]): RegisterGroup[] {
  const byKey = new Map<string, RegisterGroup>()
  for (const row of rows) {
    let group = byKey.get(row.key)
    if (!group) {
      group = { key: row.key, service: row.service, title: '', company: '', rows: [] }
      byKey.set(row.key, group)
    }
    group.rows.push(row)
    if (!group.service && row.service) group.service = row.service
    if (row.title) group.title = row.title
    if (row.company) group.company = row.company
    if (row.kind === 'triage') group.triage = row
    else if (row.kind === 'rca') group.rca = row
    else if (row.kind === 'resolution') group.resolution = row
    else if (row.kind === 'fix') group.fixDate = row.date
  }
  return [...byKey.values()]
}

/**
 * Where the ticket has got to: resolved once the rca run has filed its notes, fix-pushed
 * once a fix went out, triaged before either — mirroring registerEntry.status() in
 * cmd/sirdar/cmd_register.go.
 */
export function groupStatus(group: RegisterGroup): string {
  if (group.resolution?.notePath || group.rca?.notePath) return 'resolved'
  if (group.fixDate) return 'fix-pushed'
  return 'triaged'
}

/** Escapes the one character that would break a markdown table row. */
function cell(s: string): string {
  return s.replaceAll('|', '\\|')
}

/**
 * Turns a note path into the `[[stem]]` link the vault uses, or an empty cell when that
 * note was never written — mirroring wikiLink in cmd/sirdar/cmd_register.go.
 */
function wikiLink(notePath: string | undefined): string {
  if (!notePath) return ''
  const base = notePath.split(/[/\\]/).pop() ?? notePath
  const stem = base.replace(/\.md$/, '')
  return `[[${cell(stem)}]]`
}

/** Date used to sort a group: the triage date, else whichever stage has one. */
export function groupDate(group: RegisterGroup): string {
  return group.triage?.date ?? group.rca?.date ?? group.resolution?.date ?? ''
}

const VERDICT_WEIGHT: Record<string, number> = { confirmed: 1, partial: 0.5, wrong: 0 }

export interface AccuracySummary {
  /** Weighted count of held hypotheses (confirmed = 1, partial = 0.5, wrong = 0). */
  held: number
  /** Number of groups carrying a triage verdict. */
  reviewed: number
  /** held / reviewed as a whole-number percentage, 0 when nothing is reviewed. */
  percent: number
}

/** "Hypothesis held N of M reviewed" — confirmed counts full, partial half, wrong none. */
export function computeAccuracy(groups: RegisterGroup[]): AccuracySummary {
  let held = 0
  let reviewed = 0
  for (const group of groups) {
    const verdict = group.triage?.triageVerdict
    if (verdict && verdict in VERDICT_WEIGHT) {
      held += VERDICT_WEIGHT[verdict]
      reviewed += 1
    }
  }
  const percent = reviewed === 0 ? 0 : Math.round((held / reviewed) * 100)
  return { held, reviewed, percent }
}

/** Renders a possibly-fractional held count without a trailing ".0". */
export function formatHeld(held: number): string {
  return Number.isInteger(held) ? String(held) : held.toFixed(1)
}

/** Sum of cost and turns across every underlying row of the given groups. */
export function sumUsage(groups: RegisterGroup[]): { costUsd: number; turns: number } {
  let costUsd = 0
  let turns = 0
  for (const group of groups) {
    for (const row of group.rows) {
      costUsd += row.costUsd
      turns += row.turns
    }
  }
  return { costUsd, turns }
}

const MARKDOWN_HEADER =
  '| # | Issue | Title | Company | Helpdesk | Tracker | Triage | RCA | Resolution | Status |'
const MARKDOWN_DIVIDER = '|---|---|---|---|---|---|---|---|---|---|'

/**
 * `| # | Issue | Title | Company | Helpdesk | Tracker | Triage | RCA | Resolution | Status |`
 * Column for column with `printRegisterMarkdown` in cmd/sirdar/cmd_register.go: Issue holds
 * the tracker key, Title and Company come from the register rows — the note's own title, and
 * the company its frontmatter named — the Triage/RCA/Resolution cells are `[[wiki links]]` to
 * whichever note was written, and Status is groupStatus's resolved/fix-pushed/triaged. Helpdesk
 * and Tracker stay blank: the register carries no helpdesk reference, and the tracker key
 * already has a cell in Issue.
 */
export function toMarkdownTable(groups: RegisterGroup[]): string {
  const rows = groups.map((group, i) => {
    const cells = [
      String(i + 1),
      cell(group.key),
      cell(group.title),
      cell(group.company),
      '',
      '',
      wikiLink(group.triage?.notePath),
      wikiLink(group.rca?.notePath),
      wikiLink(group.resolution?.notePath),
      groupStatus(group),
    ]
    return `| ${cells.join(' | ')} |`
  })
  return [MARKDOWN_HEADER, MARKDOWN_DIVIDER, ...rows].join('\n')
}

/**
 * How many runs happened on each day, for the Register's heatmap.
 *
 * Every underlying row counts, not every group: a key that was triaged, fixed
 * and then had its root cause written is three runs on three days, and the
 * question the grid answers is how much the machine worked, not how many
 * tickets were closed. A row with no date is not a day and is dropped rather
 * than counted against today.
 */
export function runsPerDay(groups: RegisterGroup[]): { date: string; count: number }[] {
  const byDay = new Map<string, number>()
  for (const group of groups) {
    for (const row of group.rows) {
      const day = (row.date ?? '').slice(0, 10)
      if (!/^\d{4}-\d{2}-\d{2}$/.test(day)) continue
      byDay.set(day, (byDay.get(day) ?? 0) + 1)
    }
  }
  return [...byDay.entries()]
    .map(([date, count]) => ({ date, count }))
    .sort((a, b) => a.date.localeCompare(b.date))
}

// ---------------------------------------------------------------------------
// The ledger: one line per run, which is what the Register screen draws.
// ---------------------------------------------------------------------------

/**
 * One run as the Register lists it.
 *
 * A register row is written only when a run has something to file — a note,
 * or a fix that landed — so a run that is still going, is waiting on an
 * answer, or failed is not in the register at all. The ledger joins the two:
 * every register row, with its run's state and timing when the run is still
 * on disk, and every run the register has no line for. Without the join the
 * screen could not show the one row a reader is most likely looking for,
 * which is the run that went wrong.
 */
export interface LedgerRow {
  /** The register row's run id, or the run's. Unique down the table. */
  runId: string
  key: string
  kind: string
  state: RunState
  provider: string
  model: string
  turns: number
  costUsd: number
  /** Whole minutes the run took, or has taken so far. Null when the start is unknown. */
  minutes: number | null
  /** The triage confidence recorded for this key, if a triage was. */
  confidence: string
  /** The verdict a person recorded on this key's triage, if any. */
  verdict: string
  /** Which of this key's notes exist. */
  notes: { triage: boolean; rca: boolean; resolution: boolean }
  /** RFC 3339 when the run is on disk, else the register's `YYYY-MM-DD`. */
  when: string
  /** `YYYY-MM-DD`, the day the run happened, for the grid and the day filter. */
  day: string
  /** Why the run stopped where it did; empty for a run that ended cleanly. */
  reason: string
}

const VERDICTS = new Set(['confirmed', 'partial', 'wrong'])

/** `YYYY-MM-DD` in the reader's own timezone, or '' when the stamp is not one. */
export function dayOf(stamp: string | undefined): string {
  if (!stamp) return ''
  if (/^\d{4}-\d{2}-\d{2}$/.test(stamp)) return stamp
  const ms = Date.parse(stamp)
  if (Number.isNaN(ms)) return stamp.slice(0, 10)
  const d = new Date(ms)
  const y = d.getFullYear()
  const m = String(d.getMonth() + 1).padStart(2, '0')
  const day = String(d.getDate()).padStart(2, '0')
  return `${y}-${m}-${day}`
}

/** True while a run can still change: it is preparing, running or waiting on an answer. */
export function isLive(state: RunState): boolean {
  return state === 'preparing' || state === 'running' || state === 'blocked'
}

/**
 * Whole minutes between a run's start and its last change — or, while it is
 * still going, between its start and `now`. Null when the start is unknown.
 */
export function minutesOf(run: RunSummary, now: number): number | null {
  const started = Date.parse(run.startedAt ?? '')
  if (Number.isNaN(started)) return null
  const ended = isLive(run.status) ? now : Date.parse(run.updatedAt ?? '')
  const end = Number.isNaN(ended) ? now : ended
  return Math.max(0, Math.round((end - started) / 60_000))
}

/** A sortable number for a stamp that is RFC 3339 or a bare date. */
function stampOf(when: string): number {
  const ms = Date.parse(when)
  return Number.isNaN(ms) ? 0 : ms
}

/**
 * Builds the ledger from the register and the runs on disk.
 *
 * Newest first, by the run's start when it is known and the register's date
 * otherwise. A register row whose run has been cleaned up is still a run that
 * finished: the row exists because it did.
 */
export function buildLedger(
  rows: RegisterRow[],
  runs: RunSummary[],
  now = Date.now(),
): LedgerRow[] {
  const byRun = new Map(runs.map((r) => [r.runId, r]))
  const groups = new Map(groupRegisterRows(rows).map((g) => [g.key, g]))

  function keyFacts(key: string): Pick<LedgerRow, 'confidence' | 'verdict' | 'notes'> {
    const g = groups.get(key)
    return {
      confidence: g?.triage?.confidence ?? '',
      verdict: g?.triage?.triageVerdict ?? '',
      notes: {
        triage: Boolean(g?.triage?.notePath),
        rca: Boolean(g?.rca?.notePath),
        resolution: Boolean(g?.resolution?.notePath),
      },
    }
  }

  const out: LedgerRow[] = []
  const seen = new Set<string>()
  for (const row of rows) {
    const run = byRun.get(row.runId)
    if (row.runId) seen.add(row.runId)
    const when = run?.startedAt || row.date
    out.push({
      runId: row.runId || `${row.key}:${row.kind}:${row.date}`,
      key: row.key,
      kind: row.kind,
      state: run?.status ?? 'completed',
      provider: row.provider || run?.provider || '',
      model: row.model || run?.model || '',
      turns: row.turns,
      costUsd: row.costUsd,
      minutes: run ? minutesOf(run, now) : null,
      ...keyFacts(row.key),
      when,
      day: dayOf(when),
      reason: run?.reason ?? '',
    })
  }
  for (const run of runs) {
    if (seen.has(run.runId)) continue
    out.push({
      runId: run.runId,
      key: run.key,
      kind: run.kind,
      state: run.status,
      provider: run.provider,
      model: run.model,
      turns: run.usage?.turns ?? 0,
      costUsd: run.usage?.costUsd ?? 0,
      minutes: minutesOf(run, now),
      ...keyFacts(run.key),
      when: run.startedAt,
      day: dayOf(run.startedAt),
      reason: run.reason ?? '',
    })
  }
  out.sort((a, b) => stampOf(b.when) - stampOf(a.when))
  return out
}

export interface WeekSummary {
  total: number
  /** Counts by kind, most common first, so the line under the figure reads in order. */
  kinds: { kind: string; count: number }[]
}

/** The seven days ending today: today and the six before it, as `YYYY-MM-DD`. */
export function lastSevenDays(now = Date.now()): Set<string> {
  const out = new Set<string>()
  for (let i = 0; i < 7; i += 1) {
    out.add(dayOf(new Date(now - i * 86_400_000).toISOString()))
  }
  return out
}

/** Runs in the seven days ending today, with the kinds they were. */
export function runsThisWeek(ledger: LedgerRow[], now = Date.now()): WeekSummary {
  const days = lastSevenDays(now)
  const byKind = new Map<string, number>()
  let total = 0
  for (const row of ledger) {
    if (!days.has(row.day)) continue
    total += 1
    byKind.set(row.kind, (byKind.get(row.kind) ?? 0) + 1)
  }
  const kinds = [...byKind.entries()]
    .map(([kind, count]) => ({ kind, count }))
    .sort((a, b) => b.count - a.count || a.kind.localeCompare(b.kind))
  return { total, kinds }
}

const KIND_WORDS: Record<string, [string, string]> = {
  triage: ['triage', 'triages'],
  rca: ['RCA', 'RCAs'],
  fix: ['fix', 'fixes'],
  resolution: ['resolution', 'resolutions'],
}

/** "12 fixes · 18 triages · 12 RCAs", from a week summary. Empty when nothing ran. */
export function kindsLine(kinds: WeekSummary['kinds']): string {
  return kinds
    .map(({ kind, count }) => {
      const [one, many] = KIND_WORDS[kind] ?? [kind, kind]
      return `${count} ${count === 1 ? one : many}`
    })
    .join(' · ')
}

export interface SpendSummary {
  total: number
  /** Cost by provider, largest first. */
  providers: { provider: string; costUsd: number }[]
}

/** Every dollar the ledger records, and which provider took it. */
export function spent(ledger: LedgerRow[]): SpendSummary {
  const byProvider = new Map<string, number>()
  let total = 0
  for (const row of ledger) {
    const cost = Number.isFinite(row.costUsd) ? row.costUsd : 0
    total += cost
    const p = row.provider || 'unknown'
    byProvider.set(p, (byProvider.get(p) ?? 0) + cost)
  }
  const providers = [...byProvider.entries()]
    .map(([provider, costUsd]) => ({ provider, costUsd }))
    .sort((a, b) => b.costUsd - a.costUsd || a.provider.localeCompare(b.provider))
  return { total, providers }
}

/**
 * "claude $14.10 · codex $2.60 · others $1.50": the two largest by name, the
 * rest folded together so the line stays one line. A third provider on its
 * own is named rather than called "others".
 */
export function spendLine(
  providers: SpendSummary['providers'],
  money: (n: number) => string,
): string {
  if (providers.length === 0) return ''
  const named = providers.slice(0, 2).map((p) => `${p.provider} ${money(p.costUsd)}`)
  const rest = providers.slice(2)
  if (rest.length === 1) named.push(`${rest[0].provider} ${money(rest[0].costUsd)}`)
  else if (rest.length > 1) {
    named.push(`others ${money(rest.reduce((sum, p) => sum + p.costUsd, 0))}`)
  }
  return named.join(' · ')
}

export interface ConfirmedSummary {
  /** Register rows carrying a verdict a person recorded. */
  recorded: number
  /** Of those, how many said the hypothesis held. */
  confirmed: number
  /** confirmed / recorded as a whole percentage; 0 when nothing is recorded. */
  percent: number
}

/**
 * The share of recorded verdicts that were "confirmed". Only the register
 * carries verdicts, and only a triage row carries one; a partial or a wrong
 * verdict counts as recorded and not as confirmed.
 */
export function confirmedShare(rows: RegisterRow[]): ConfirmedSummary {
  let recorded = 0
  let confirmed = 0
  for (const row of rows) {
    const v = (row.triageVerdict ?? '').toLowerCase()
    if (!VERDICTS.has(v)) continue
    recorded += 1
    if (v === 'confirmed') confirmed += 1
  }
  return {
    recorded,
    confirmed,
    percent: recorded === 0 ? 0 : Math.round((confirmed / recorded) * 100),
  }
}

/** Runs per day for the grid: every ledger row on a day it can name. */
export function ledgerPerDay(ledger: LedgerRow[]): { date: string; count: number }[] {
  const byDay = new Map<string, number>()
  for (const row of ledger) {
    if (!/^\d{4}-\d{2}-\d{2}$/.test(row.day)) continue
    byDay.set(row.day, (byDay.get(row.day) ?? 0) + 1)
  }
  return [...byDay.entries()]
    .map(([date, count]) => ({ date, count }))
    .sort((a, b) => a.date.localeCompare(b.date))
}

/** Quotes a CSV field when it needs it: a comma, a quote, or a line break. */
function csvField(value: string | number | null): string {
  const s = value === null || value === undefined ? '' : String(value)
  return /[",\r\n]/.test(s) ? `"${s.replaceAll('"', '""')}"` : s
}

export const CSV_HEADER = [
  'key',
  'kind',
  'state',
  'provider',
  'model',
  'turns',
  'cost_usd',
  'minutes',
  'confidence',
  'verdict',
  'triage_note',
  'rca_note',
  'resolution_note',
  'when',
  'reason',
] as const

/**
 * The visible rows as CSV, one line per run, in the order they are shown.
 * Every column the table draws is here, as the value rather than as the
 * chip: a spreadsheet wants `confirmed`, not a green outline. CRLF line
 * ends, which is what RFC 4180 asks for and what every spreadsheet reads.
 */
export function toCSV(ledger: LedgerRow[]): string {
  const lines = [CSV_HEADER.join(',')]
  for (const row of ledger) {
    lines.push(
      [
        row.key,
        row.kind,
        row.state,
        row.provider,
        row.model,
        row.turns,
        row.costUsd.toFixed(2),
        row.minutes ?? '',
        row.confidence,
        row.verdict,
        row.notes.triage ? 'yes' : 'no',
        row.notes.rca ? 'yes' : 'no',
        row.notes.resolution ? 'yes' : 'no',
        row.when,
        row.reason,
      ]
        .map(csvField)
        .join(','),
    )
  }
  return lines.join('\r\n') + '\r\n'
}
