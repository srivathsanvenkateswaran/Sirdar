import { useMemo, useState, type MouseEvent } from 'react'
import type { RunDetail } from '../../api/types'
import { markersForStep, type Marker as MarkerModel } from '../../lib/evidence'
import { tokens, usd } from '../../lib/format'
import { bytes } from '../../lib/toolOutput'
import Marker from '../../ui/marker'
import { took, type SessionCounts, type SessionStep } from './model'
import Stamp, { stepStamp } from './Stamp'

export type ToolsSort = 'time' | 'tool' | 'took' | 'out'

export interface ToolsTableProps {
  steps: SessionStep[]
  markers: MarkerModel[]
  counts: SessionCounts
  detail: RunDetail
  hotMarker?: string
  onMarker?: (id: string, event: MouseEvent<HTMLElement>) => void
  /** Clicking a row goes to the step on the path (and opens it). */
  onOpenStep?: (index: number) => void
  /** The row to highlight: the step the reader came from. */
  selected?: number
  /** Wider columns — the model's description and the decision on their own — for a column that has the room. */
  wide?: boolean
}

/** The rule a step's decision is written under: `allow-list`, `go test *`, `denied`, `asked`. */
export function decisionWord(step: SessionStep): string {
  if (step.state === 'denied') return 'denied'
  if (step.state === 'waiting') return 'waiting on you'
  if (step.decision === 'allow') return step.rule && step.rule !== 'allowed' ? `allowed · ${step.rule}` : 'allowed'
  if (step.rule === 'allow-list') return 'allow-list'
  return step.kind === 'read' || step.kind === 'output' ? 'read-only' : '—'
}

function sortSteps(steps: SessionStep[], sort: ToolsSort, dir: 1 | -1): SessionStep[] {
  const out = steps.slice()
  out.sort((a, b) => {
    switch (sort) {
      case 'tool':
        return a.tool.localeCompare(b.tool) * dir || a.index - b.index
      case 'took':
        return ((a.durationMs ?? -1) - (b.durationMs ?? -1)) * dir || a.index - b.index
      case 'out':
        return (a.outputBytes - b.outputBytes) * dir || a.index - b.index
      default:
        return (a.index - b.index) * dir
    }
  })
  return out
}

/**
 * Every call in one table: when, the tool with the policy's stamp and the
 * evidence it produced, the input, the model's one-line description, the
 * decision and its rule, how long it took and how much came back, with a
 * totals row and sortable columns. Clicking a row goes to the step; the
 * markers are clickable here too.
 */
export default function ToolsTable({ steps, markers, counts, detail, hotMarker, onMarker, onOpenStep, selected, wide = false }: ToolsTableProps): JSX.Element {
  const [sort, setSort] = useState<ToolsSort>('time')
  const [dir, setDir] = useState<1 | -1>(1)
  const rows = useMemo(() => sortSteps(steps, sort, dir), [steps, sort, dir])
  const totalMs = steps.reduce((n, s) => n + (s.durationMs ?? 0), 0)

  const toggle = (col: ToolsSort) => {
    if (sort === col) setDir((d) => (d === 1 ? -1 : 1))
    else {
      setSort(col)
      setDir(1)
    }
  }
  const ariaSort = (col: ToolsSort) => (sort === col ? (dir === 1 ? 'ascending' : 'descending') : undefined)
  const arrow = (col: ToolsSort) => (sort === col ? (dir === 1 ? ' ↑' : ' ↓') : '')

  return (
    <div data-testid="tools-table">
      <div className="sn-tsum">
        {counts.byTool.map((t) => (
          <span key={t.tool}>
            <b>{t.tool}</b> {t.n}
            {t.denied > 0 ? ` · ${t.denied} denied` : ''}
          </span>
        ))}
        {detail.usage ? (
          <span>
            <b>Tokens</b> {tokens(detail.usage.inputTokens)} in · {tokens(detail.usage.outputTokens)} out
          </span>
        ) : null}
        {detail.usage?.costUsd ? (
          <span>
            <b>Cost</b> {usd(detail.usage.costUsd)} of ${detail.budget.maxUsd}
          </span>
        ) : null}
        <span>
          <b>Turns</b> {detail.usage?.turns ?? 0} of {detail.budget.maxTurns}
        </span>
      </div>
      <table className={wide ? 'sn-ttbl sn-ttbl--wide' : 'sn-ttbl'}>
        <thead>
          <tr>
            <th className="r" scope="col" aria-sort={ariaSort('time')}>
              <button type="button" onClick={() => toggle('time')}>#{arrow('time')}</button>
            </th>
            <th scope="col" aria-sort={ariaSort('tool')}>
              <button type="button" onClick={() => toggle('tool')}>
                {wide ? 'Call · input' : 'Call · decision · cites'}
                {arrow('tool')}
              </button>
            </th>
            {wide ? (
              <>
                <th scope="col">Why</th>
                <th scope="col">Decision · rule</th>
                <th scope="col">Cites</th>
              </>
            ) : null}
            <th className="r" scope="col" aria-sort={ariaSort('took')}>
              <button type="button" onClick={() => toggle('took')}>
                Took{wide ? '' : ' · output'}
                {arrow('took')}
              </button>
            </th>
            {wide ? (
              <th className="r" scope="col" aria-sort={ariaSort('out')}>
                <button type="button" onClick={() => toggle('out')}>Output{arrow('out')}</button>
              </th>
            ) : null}
          </tr>
        </thead>
        <tbody>
          {rows.map((s) => {
            const own = markersForStep(s.index, markers)
            const stamp = stepStamp(s)
            const n = steps.indexOf(s) + 1
            const out = s.outputBytes > 0 ? `${bytes(s.outputBytes)}${s.output ? ` · ${s.output.split('\n').filter(Boolean).length} L` : ''}` : s.state === 'denied' ? 'denied' : '—'
            return (
              <tr
                key={s.index}
                data-on={selected === s.index ? 'true' : undefined}
                data-index={s.index}
                onClick={() => onOpenStep?.(s.index)}
                tabIndex={onOpenStep ? 0 : undefined}
                onKeyDown={(e) => {
                  if (e.key === 'Enter' || e.key === ' ') {
                    e.preventDefault()
                    onOpenStep?.(s.index)
                  }
                }}
              >
                <td className="dim r">{n}</td>
                <td>
                  <span className="sn-ttbl__call">
                    {s.tool}
                    {!wide && stamp ? <Stamp tone={stamp.tone}>{stamp.word}</Stamp> : null}
                    {!wide
                      ? own.map((m) => (
                          <Marker key={m.id} id={m.id} hot={m.id === hotMarker} onClick={onMarker} title={m.query || m.path} />
                        ))
                      : null}
                  </span>
                  <span className="sn-ttbl__inp" title={s.input} dir="ltr">
                    {s.at} · {s.object}
                  </span>
                </td>
                {wide ? (
                  <>
                    <td className="dim" title={s.description}>
                      {s.description ?? ''}
                    </td>
                    <td>{decisionWord(s)}</td>
                    <td>
                      {own.map((m) => (
                        <Marker key={m.id} id={m.id} hot={m.id === hotMarker} onClick={onMarker} title={m.query || m.path} />
                      ))}
                    </td>
                  </>
                ) : null}
                <td className="r">
                  <span className="dim">{took(s.durationMs) || (s.state === 'waiting' ? 'waiting' : '—')}</span>
                  {!wide ? <span className="sn-ttbl__out">{out}</span> : null}
                </td>
                {wide ? <td className="r">{out}</td> : null}
              </tr>
            )
          })}
        </tbody>
        <tfoot>
          <tr>
            <td className="dim r" />
            <td>
              {counts.calls} {counts.calls === 1 ? 'call' : 'calls'}
              {counts.denied > 0 ? ` · ${counts.denied} denied` : ''}
            </td>
            {wide ? <td colSpan={3} /> : null}
            <td className="r">
              {took(totalMs) || '—'}
              {!wide ? <span className="sn-ttbl__out">{bytes(counts.outBytes)} out</span> : null}
            </td>
            {wide ? <td className="r">{bytes(counts.outBytes)}</td> : null}
          </tr>
        </tfoot>
      </table>
    </div>
  )
}
