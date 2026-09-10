import type { RegisterRow } from '../api/types'

/**
 * One line of the Register screen: every `RegisterRow` sharing a tracker key,
 * folded into whichever of the three stages (triage, RCA, resolution) exist.
 */
export interface RegisterGroup {
  key: string
  service: string
  rows: RegisterRow[]
  triage?: RegisterRow
  rca?: RegisterRow
  resolution?: RegisterRow
}

/** Groups register rows by tracker key, keeping first-seen order. */
export function groupRegisterRows(rows: RegisterRow[]): RegisterGroup[] {
  const byKey = new Map<string, RegisterGroup>()
  for (const row of rows) {
    let group = byKey.get(row.key)
    if (!group) {
      group = { key: row.key, service: row.service, rows: [] }
      byKey.set(row.key, group)
    }
    group.rows.push(row)
    if (!group.service && row.service) group.service = row.service
    if (row.kind === 'triage') group.triage = row
    else if (row.kind === 'rca') group.rca = row
    else if (row.kind === 'resolution') group.resolution = row
  }
  return [...byKey.values()]
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
  '| # | Issue | Company | Helpdesk | Tracker | Triage | RCA | Resolution | Status |'
const MARKDOWN_DIVIDER = '|---|---|---|---|---|---|---|---|---|'

/**
 * `| # | Issue | Company | Helpdesk | Tracker | Triage | RCA | Resolution | Status |`
 * Issue/Company/Helpdesk are blank — the register carries no ticket title,
 * company, or helpdesk reference, only the tracker key.
 */
export function toMarkdownTable(groups: RegisterGroup[]): string {
  const rows = groups.map((group, i) => {
    const cells = [
      String(i + 1),
      '',
      '',
      '',
      group.key,
      group.triage?.date ?? '',
      group.rca?.date ?? '',
      group.resolution?.classification ?? '',
      group.triage?.triageVerdict ?? '',
    ]
    return `| ${cells.join(' | ')} |`
  })
  return [MARKDOWN_HEADER, MARKDOWN_DIVIDER, ...rows].join('\n')
}
