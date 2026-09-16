import type { RunSummary, SourcesSummary } from '../api/types'
import { stateWord } from '../ui/status-badge'
import { helpdeskNumber, shownNumber, type SessionsShow } from './sessionsShow'

/**
 * The sidebar's live filter over the sessions list: which runs answer a
 * query, and where, so the row can mark the match.
 *
 * Every field a row or its card can say is searched, case folded: the number
 * the row shows, the tracker key and the helpdesk number, the local alias,
 * the title (the ticket's, or the note's when the run has no ticket title),
 * the kind, the provider, the model, and the state word. The first field
 * that matches is the one reported; the number leads so the highlight lands
 * on the row's own text whenever it can.
 */

export type MatchField =
  | 'number'
  | 'key'
  | 'helpdesk'
  | 'alias'
  | 'title'
  | 'kind'
  | 'provider'
  | 'model'
  | 'state'

export interface RunMatch {
  field: MatchField
  /** The field's whole text, as drawn. */
  text: string
  /** The match inside `text`, as a half-open range of characters. */
  start: number
  end: number
}

/** The query as it is compared: trimmed and case folded. Empty means "no filter". */
export function normalizeQuery(query: string): string {
  return query.trim().toLowerCase()
}

/** Where `needle` (already folded) sits in `text`, or -1. */
function indexIn(text: string, needle: string): number {
  return text.toLowerCase().indexOf(needle)
}

export function matchRun(
  run: RunSummary,
  query: string,
  opts: { alias?: string; show: SessionsShow; sources?: SourcesSummary },
): RunMatch | null {
  const needle = normalizeQuery(query)
  if (!needle) return null
  const shown = shownNumber(run, opts.show, opts.sources)
  const helpdesk = run.helpdeskKey?.trim() ? helpdeskNumber(run.helpdeskKey) : ''
  const fields: [MatchField, string][] = [
    ['number', shown.text],
    ['key', run.key],
    ['helpdesk', helpdesk],
    ['alias', opts.alias ?? ''],
    ['title', run.title ?? ''],
    ['kind', run.kind],
    ['provider', run.provider],
    ['model', run.model],
    ['state', stateWord(run.status)],
  ]
  for (const [field, text] of fields) {
    if (!text) continue
    const at = indexIn(text, needle)
    if (at >= 0) return { field, text, start: at, end: at + needle.length }
  }
  return null
}

/** `text` cut into the part before the match, the match, and the part after. */
export function splitMatch(m: RunMatch): [string, string, string] {
  return [m.text.slice(0, m.start), m.text.slice(m.start, m.end), m.text.slice(m.end)]
}

/**
 * The first place `query` appears in `text`, case folded, split the same
 * way, so a search hit's excerpt can carry a mark; the whole text as the
 * first part when it does not appear.
 */
export function splitText(text: string, query: string): [string, string, string] {
  const needle = normalizeQuery(query)
  const at = needle ? indexIn(text, needle) : -1
  if (at < 0) return [text, '', '']
  return [text.slice(0, at), text.slice(at, at + needle.length), text.slice(at + needle.length)]
}
