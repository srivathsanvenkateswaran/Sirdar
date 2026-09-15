import type { RegisterRow } from '../api/types'

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
