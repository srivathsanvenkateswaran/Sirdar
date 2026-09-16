import { useMemo, useState, type JSX, type ReactNode } from 'react'
import { duration } from '../../../lib/format'
import { bytesLabel, type ConsoleRow } from './model'

/**
 * Every call the run made as one sortable table — time, tool, input with
 * the model's one-line description, duration, output size, decision and the
 * rule — with a totals strip above it: calls, allows, denies, files read,
 * time in tools, bytes returned, and how long the model took to its first
 * answer and after a steer. It is the console under the `tools` filter with
 * wider columns, so every call's cost is compared in one place.
 *
 * A local adapter with the shared `ToolsTable`'s name.
 */
export interface ToolsTableProps {
  /** The console's call rows, in file order. */
  rows: ConsoleRow[]
  /** Every row, for the totals strip's model timings. */
  all: ConsoleRow[]
  startedMs: number
  expanded: ReadonlySet<string>
  onToggle: (id: string) => void
  /** Draws the expanded body under a row. */
  renderExpanded: (row: ConsoleRow) => ReactNode
}

type SortKey = 'i' | 'at' | 'tool' | 'in' | 'dur' | 'out' | 'dec'

const COLUMNS: { key: SortKey; label: string; right?: boolean }[] = [
  { key: 'i', label: '#' },
  { key: 'at', label: 'at' },
  { key: 'tool', label: 'tool' },
  { key: 'in', label: 'input' },
  { key: 'dur', label: 'duration', right: true },
  { key: 'out', label: 'output' },
  { key: 'dec', label: 'decision · rule' },
]

function value(row: ConsoleRow, i: number, key: SortKey): string | number {
  switch (key) {
    case 'i':
      return i
    case 'at':
      return row.atMs
    case 'tool':
      return row.tool
    case 'in':
      return row.summary
    case 'dur':
      return row.durationMs ?? -1
    case 'out':
      return row.outputBytes ?? -1
    case 'dec':
      return `${row.decision ?? ''} ${row.rule ?? ''}`
  }
}

/** The strip's figures, exported so the tests can pin them. */
export function toolTotals(rows: ConsoleRow[], all: ConsoleRow[], startedMs: number): { cells: { b: string; t: string }[]; model: string } {
  const allow = rows.filter((r) => r.decision !== 'deny').length
  const deny = rows.filter((r) => r.kind === 'deny').length
  const filesRead = rows.filter((r) => r.tool === 'Read').length
  const ms = rows.reduce((n, r) => n + (r.durationMs ?? 0), 0)
  const bytes = rows.reduce((n, r) => n + (r.outputBytes ?? 0), 0)
  const cells = [
    { b: String(rows.length), t: rows.length === 1 ? 'call' : 'calls' },
    { b: String(allow), t: 'allow' },
    { b: String(deny), t: 'deny' },
    { b: String(filesRead), t: 'files read' },
    { b: ms >= 10_000 ? duration(ms) : `${Math.round(ms).toLocaleString('en-US')} ms`, t: 'in tools' },
    { b: bytesLabel(bytes), t: 'returned' },
  ]
  const finals = all.filter((r) => r.kind === 'final')
  const steer = all.find((r) => r.kind === 'steer')
  const parts: string[] = []
  if (finals.length > 0 && Number.isFinite(startedMs)) parts.push(`${duration(finals[0].atMs - startedMs)} to first answer`)
  if (steer) {
    const after = finals.find((f) => f.atMs > steer.atMs)
    if (after) parts.push(`${duration(after.atMs - steer.atMs)} after steer`)
  }
  return { cells, model: parts.length > 0 ? `on the model · ${parts.join(' · ')}` : '' }
}

export default function ToolsTable({ rows, all, startedMs, expanded, onToggle, renderExpanded }: ToolsTableProps): JSX.Element {
  const [sort, setSort] = useState<{ key: SortKey; dir: 'asc' | 'desc' }>({ key: 'i', dir: 'asc' })
  const totals = useMemo(() => toolTotals(rows, all, startedMs), [rows, all, startedMs])

  const ordered = useMemo(() => {
    const withIndex = rows.map((row, i) => ({ row, i: i + 1 }))
    const cmp = (a: { row: ConsoleRow; i: number }, b: { row: ConsoleRow; i: number }) => {
      const va = value(a.row, a.i, sort.key)
      const vb = value(b.row, b.i, sort.key)
      const n = typeof va === 'number' && typeof vb === 'number' ? va - vb : String(va).localeCompare(String(vb))
      return sort.dir === 'asc' ? n : -n
    }
    return withIndex.sort(cmp)
  }, [rows, sort])

  const toggleSort = (key: SortKey) =>
    setSort((prev) => (prev.key === key ? { key, dir: prev.dir === 'asc' ? 'desc' : 'asc' } : { key, dir: key === 'i' || key === 'at' ? 'asc' : 'desc' }))

  return (
    <div className="wb-tools" data-testid="tools-table">
      <div className="wb-tstrip" aria-label="Totals">
        {totals.cells.map((c) => (
          <span key={c.t}>
            <b>{c.b}</b> {c.t}
          </span>
        ))}
        <span className="wb-tstrip__sp" />
        {totals.model ? <span>{totals.model}</span> : null}
      </div>
      <div className="wb-ttab-wrap" role="table" aria-label="Tool calls">
        <div className="wb-ttab wb-ttab--hd" role="row">
          {COLUMNS.map((c) => (
            <button
              key={c.key}
              type="button"
              role="columnheader"
              className="wb-ttab__sort"
              data-right={c.right ? 'true' : undefined}
              aria-sort={sort.key === c.key ? (sort.dir === 'asc' ? 'ascending' : 'descending') : 'none'}
              onClick={() => toggleSort(c.key)}
            >
              {c.label}
              {sort.key === c.key ? <span aria-hidden="true">{sort.dir === 'asc' ? ' ↑' : ' ↓'}</span> : null}
            </button>
          ))}
        </div>
        {ordered.map(({ row, i }) => (
          <div key={row.id}>
            <button
              type="button"
              className="wb-ttab"
              role="row"
              data-k={row.kind}
              data-open={expanded.has(row.id) ? 'true' : undefined}
              aria-expanded={expanded.has(row.id)}
              onClick={() => onToggle(row.id)}
            >
              <span className="wb-ttab__i" role="cell">{i}</span>
              <span className="wb-ttab__at" role="cell">{row.at}</span>
              <span className="wb-ttab__tool" role="cell">{row.tool}</span>
              <span className="wb-ttab__in" role="cell" title={row.summary}>
                {row.summary}
                {row.description ? <span className="wb-d"> — {row.description}</span> : null}
              </span>
              <span className="wb-ttab__dur" role="cell">{row.duration ?? ''}</span>
              <span className="wb-ttab__out" role="cell">{row.output ?? ''}</span>
              <span className="wb-ttab__dec" role="cell" title={row.reason}>
                <b>{row.decision || '—'}</b>
                {row.rule ? ` · ${row.rule}` : ''}
              </span>
            </button>
            {expanded.has(row.id) ? renderExpanded(row) : null}
          </div>
        ))}
        {rows.length === 0 ? <p className="wb-empty wb-empty--inline">No tool has been called yet.</p> : null}
      </div>
    </div>
  )
}
