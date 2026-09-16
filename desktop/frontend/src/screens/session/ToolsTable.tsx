import { useMemo, useState, type MouseEvent } from 'react'
import { markersForStep, type Marker as MarkerModel } from '../../lib/evidence'
import Marker from '../../ui/marker'
import { SortIcon } from './icons'
import type { Decision, StepCall } from './model'
import { formatBytes, formatMs } from './shape'
import { Stamp, type StampTone } from './ToolStep'

/*
 * Every call the run made, as a table: when, which tool, what it was asked,
 * what the policy said, how long it took, how much came back. A totals line
 * heads it. Sortable by the columns a reader compares on; clicking a row
 * scrolls the transcript to that call's card and tints it, so the table and
 * the conversation are one set of facts read two ways.
 */

export type SortKey = 'n' | 'at' | 'tool' | 'took' | 'out'

const DECISION_WORDS: Record<Decision, { tone: StampTone; text: string }> = {
  policy: { tone: 'allow', text: 'policy' },
  approved: { tone: 'allow', text: 'approved' },
  accepted: { tone: 'allow', text: 'accepted' },
  denied: { tone: 'deny', text: 'denied' },
}

function compare(a: StepCall, b: StepCall, key: SortKey): number {
  switch (key) {
    case 'at':
      return a.startedAt.localeCompare(b.startedAt)
    case 'tool':
      return a.tool.localeCompare(b.tool) || a.n - b.n
    case 'took':
      return (a.tookMs ?? -1) - (b.tookMs ?? -1)
    case 'out':
      return a.size.bytes - b.size.bytes
    default:
      return a.n - b.n
  }
}

export interface ToolsTableProps {
  calls: StepCall[]
  /** The call tinted as the transcript's twin, by its event index. */
  highlighted?: number
  onLocate?: (step: StepCall) => void
  /** The evidence markers (E1…En) derived from the answer, so the table says which call produced which item. */
  markers?: MarkerModel[]
  hotMarker?: string
  onMarker?: (id: string, event: MouseEvent<HTMLElement>) => void
}

export default function ToolsTable({ calls, highlighted, onLocate, markers, hotMarker, onMarker }: ToolsTableProps): JSX.Element {
  const [sort, setSort] = useState<{ key: SortKey; desc: boolean }>({ key: 'n', desc: false })

  const rows = useMemo(() => {
    const sorted = [...calls].sort((a, b) => compare(a, b, sort.key))
    return sort.desc ? sorted.reverse() : sorted
  }, [calls, sort])

  const denied = calls.filter((c) => c.decision === 'denied').length
  const asked = calls.filter((c) => c.decision === 'approved' || c.decision === 'accepted').length
  const policy = calls.length - denied - asked
  const out = calls.reduce((n, c) => n + c.size.bytes, 0)

  function toggle(key: SortKey): void {
    setSort((prev) => (prev.key === key ? { key, desc: !prev.desc } : { key, desc: false }))
  }

  function head(key: SortKey, label: string, align?: 'end'): JSX.Element {
    const on = sort.key === key
    return (
      <th scope="col" aria-sort={on ? (sort.desc ? 'descending' : 'ascending') : 'none'} data-sort={on ? 'true' : undefined} data-align={align}>
        <button type="button" className="sc-tt__sort" onClick={() => toggle(key)}>
          {label}
          <SortIcon />
        </button>
      </th>
    )
  }

  if (calls.length === 0) return <div className="sc-pane-empty">No tool has been called yet.</div>

  return (
    <div className="sc-tools" dir="ltr">
      <div className="sc-ttsum">
        <span>
          <b>{calls.length}</b> {calls.length === 1 ? 'call' : 'calls'}
        </span>
        <span>
          <b>{policy}</b> by policy
        </span>
        <span data-tone={denied > 0 ? 'blocked' : undefined}>
          <b>{denied}</b> denied
        </span>
        <span>
          <b>{asked}</b> asked you
        </span>
        <span className="sc-ttsum__out">
          <b>{formatBytes(out)}</b> out
        </span>
      </div>
      <table className="sc-tt" aria-label="Tool calls">
        <thead>
          <tr>
            {head('n', '#')}
            {head('at', 'at')}
            {head('tool', 'tool')}
            <th scope="col">input</th>
            <th scope="col">decision</th>
            {markers ? <th scope="col">cites</th> : null}
            {head('took', 'took', 'end')}
            {head('out', 'output', 'end')}
          </tr>
        </thead>
        <tbody>
          {rows.map((c) => {
            const word = DECISION_WORDS[c.decision]
            return (
              <tr
                key={c.index}
                data-on={highlighted === c.index ? 'true' : undefined}
                data-deny={c.decision === 'denied' ? 'true' : undefined}
                onClick={onLocate ? () => onLocate(c) : undefined}
                tabIndex={onLocate ? 0 : undefined}
                onKeyDown={
                  onLocate
                    ? (e) => {
                        if (e.key === 'Enter' || e.key === ' ') {
                          e.preventDefault()
                          onLocate(c)
                        }
                      }
                    : undefined
                }
                aria-label={onLocate ? `Show call ${c.n} in the transcript` : undefined}
              >
                <td className="sc-tt__n">{c.n}</td>
                <td className="sc-tt__n">{c.at}</td>
                <td className="sc-tt__tool">{c.tool}</td>
                <td title={c.summary}>{c.summary}</td>
                <td>
                  <Stamp tone={word.tone}>{word.text}</Stamp>
                </td>
                {markers ? (
                  <td className="sc-tt__cites">
                    {markersForStep(c.index, markers).map((m) => (
                      <Marker key={m.id} id={m.id} hot={m.id === hotMarker} onClick={onMarker} title={m.query} />
                    ))}
                  </td>
                ) : null}
                <td className="sc-tt__r">{c.tookMs !== undefined ? formatMs(c.tookMs) : c.pending ? '—' : ''}</td>
                <td className="sc-tt__r">{c.size.bytes > 0 ? `${formatBytes(c.size.bytes)} · ${c.size.lines} ln` : c.pending ? '—' : '0 B'}</td>
              </tr>
            )
          })}
        </tbody>
      </table>
    </div>
  )
}
