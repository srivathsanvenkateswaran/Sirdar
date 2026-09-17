import { useMemo, useRef, useState } from 'react'
import type { Marker as MarkerModel } from '../../lib/evidence'
import { markersForStep } from '../../lib/evidence'
import ContextMenu, { type MenuEntry } from '../../ui/context-menu'
import Drawer from '../../ui/drawer'
import Marker from '../../ui/marker'
import { MoreIcon } from './inspectorIcons'
import './inspector.css'

/*
 * Every call the run made, three columns wide.
 *
 * The pane it lives in is 30% of a window, which an eight-column table
 * cannot be read in: the `#`, the clock, the input, the decision, the
 * cites, the duration and the output size were seven things competing for
 * about four hundred pixels, and the totals line above them spent a whole
 * row on "41 calls · 38 by policy · 0 denied · 3 asked you".
 *
 * So the header says the two numbers a support engineer acts on — how many
 * calls, and how many needed them — and a row is the tool, its one-line
 * description, the decision when it was not the policy's own, and how long
 * it took. Everything else is in the drawer the row opens: the input, the
 * output, what it cited, the timing. Sorting is in the pane's menu, because
 * a chevron on every heading is four more things drawn at rest.
 */

export type ToolsSort = 'order' | 'tool' | 'took'

/** A call as this pane draws it. Each layout maps its own model onto this. */
export interface ToolRow {
  /** The step's id across the layouts: the `tool_started` event's index. */
  index: number
  tool: string
  /** The model's one-line description of the call, or the input in one line. */
  summary: string
  /**
   * What decided it. `policy` is the ordinary case — the workspace's rules
   * answered without anyone being asked — and draws no chip at all.
   */
  decision: 'policy' | 'approved' | 'denied' | 'asked' | 'waiting'
  tookMs?: number
  pending?: boolean
  /** The drawer's contents. */
  at?: string
  input?: string
  output?: string
  outputBytes?: number
}

const DECISION_TONE: Record<ToolRow['decision'], string> = {
  policy: '',
  approved: 'allow',
  denied: 'deny',
  asked: 'ask',
  waiting: 'ask',
}

/** `1.2 s`, `840 ms`. */
export function took(ms: number | undefined): string {
  // A call that finished inside the log's resolution says nothing useful by
  // saying "0 ms", so it says nothing.
  if (ms === undefined || ms <= 0) return ''
  return ms >= 1000 ? `${(ms / 1000).toFixed(1)} s` : `${Math.round(ms)} ms`
}

/** `41 calls · 3 needed your approval`; the second clause only when there were any. */
export function toolsHeadline(rows: ToolRow[]): string {
  const asked = rows.filter((r) => r.decision === 'approved' || r.decision === 'denied' || r.decision === 'asked').length
  const calls = `${rows.length} ${rows.length === 1 ? 'call' : 'calls'}`
  return asked > 0 ? `${calls} · ${asked} needed your approval` : calls
}

function compare(a: ToolRow, b: ToolRow, sort: ToolsSort): number {
  switch (sort) {
    case 'tool':
      return a.tool.localeCompare(b.tool) || a.index - b.index
    case 'took':
      return (b.tookMs ?? -1) - (a.tookMs ?? -1) || a.index - b.index
    default:
      return a.index - b.index
  }
}

export interface ToolsPaneProps {
  rows: ToolRow[]
  /** The evidence markers the answer produced, so a row says what it is cited for. */
  markers?: MarkerModel[]
  /** Takes the reader to the call on the transcript or the path. */
  onLocate?: (index: number) => void
  /** The row tinted as the transcript's twin. */
  highlighted?: number
  /** Where the drawer is drawn against; the caller's column must be positioned. */
  drawerWidth?: number
}

export default function ToolsPane({ rows, markers, onLocate, highlighted, drawerWidth }: ToolsPaneProps): JSX.Element {
  const [sort, setSort] = useState<ToolsSort>('order')
  const [menu, setMenu] = useState(false)
  const [open, setOpen] = useState<number | null>(null)
  const menuButton = useRef<HTMLButtonElement | null>(null)

  const sorted = useMemo(() => [...rows].sort((a, b) => compare(a, b, sort)), [rows, sort])
  const shown = open === null ? undefined : rows.find((r) => r.index === open)

  const items: MenuEntry[] = (
    [
      ['order', 'Sort by order'],
      ['tool', 'Sort by tool'],
      ['took', 'Sort by duration'],
    ] as const
  ).map(([id, label]) => ({
    id,
    label,
    detail: sort === id ? 'on' : undefined,
    onSelect: () => setSort(id),
  }))

  if (rows.length === 0) return <p className="si-empty">No tool has been called yet.</p>

  return (
    <div className="si-tools" data-testid="tools-table">
      <div className="si-tools__head">
        <h3 className="si-block__h si-block__h--flush">
          <bdi>{toolsHeadline(rows)}</bdi>
        </h3>
        <button
          ref={menuButton}
          type="button"
          className="si-iconbtn"
          aria-label="Tools options"
          aria-haspopup="menu"
          aria-expanded={menu}
          onClick={() => setMenu((v) => !v)}
        >
          <MoreIcon />
        </button>
        <ContextMenu open={menu} anchor={menuButton} items={items} label="Tool call options" onClose={() => setMenu(false)} />
      </div>
      <ul className="si-rows">
        {sorted.map((r) => {
          const cites = markers ? markersForStep(r.index, markers) : []
          return (
            <li key={r.index}>
              <button
                type="button"
                className="si-row"
                data-on={highlighted === r.index ? 'true' : undefined}
                data-deny={r.decision === 'denied' ? 'true' : undefined}
                onClick={() => setOpen(r.index)}
                aria-label={`${r.tool}: ${r.summary || 'no description'}`}
              >
                <span className="si-row__what">
                  <b className="si-row__tool">
                    <bdi>{r.tool}</bdi>
                  </b>
                  {r.summary ? (
                    <span className="si-row__why sd-bidi" dir="auto">
                      {r.summary}
                    </span>
                  ) : null}
                </span>
                {r.decision === 'policy' ? null : (
                  <span className="si-stamp" data-tone={DECISION_TONE[r.decision]}>
                    {r.decision === 'waiting' ? 'asked' : r.decision}
                  </span>
                )}
                <span className="si-row__took">
                  <bdi>{took(r.tookMs) || (r.pending ? '—' : '')}</bdi>
                </span>
                {cites.length > 0 ? (
                  <span className="si-row__cites">
                    {cites.map((m) => (
                      <Marker key={m.id} id={m.id} title={m.query} />
                    ))}
                  </span>
                ) : null}
              </button>
            </li>
          )
        })}
      </ul>
      <Drawer
        open={shown !== undefined}
        title={shown?.tool ?? 'Call'}
        meta={shown ? [shown.at, took(shown.tookMs)].filter(Boolean).join(' · ') : undefined}
        width={drawerWidth}
        onClose={() => setOpen(null)}
      >
        {shown ? (
          <div className="si-call" data-testid="tool-detail">
            {shown.summary ? (
              <p className="si-call__why sd-bidi" dir="auto">
                {shown.summary}
              </p>
            ) : null}
            {shown.decision === 'policy' ? null : (
              <p className="si-call__line">
                <span className="si-card__label">Decision</span>
                <span className="si-stamp" data-tone={DECISION_TONE[shown.decision]}>
                  {shown.decision === 'waiting' ? 'asked' : shown.decision}
                </span>
              </p>
            )}
            {markers && markersForStep(shown.index, markers).length > 0 ? (
              <p className="si-call__line">
                <span className="si-card__label">Cites</span>
                {markersForStep(shown.index, markers).map((m) => (
                  <Marker key={m.id} id={m.id} title={m.query} />
                ))}
              </p>
            ) : null}
            {shown.input ? (
              <>
                <h4 className="si-call__h">Input</h4>
                <pre className="si-call__pre" dir="ltr">
                  {shown.input}
                </pre>
              </>
            ) : null}
            {shown.output ? (
              <>
                <h4 className="si-call__h">Output</h4>
                <pre className="si-call__pre" dir="ltr">
                  {shown.output}
                </pre>
              </>
            ) : null}
            {onLocate ? (
              <button type="button" className="si-more" onClick={() => onLocate(shown.index)}>
                Show it in the transcript
              </button>
            ) : null}
          </div>
        ) : null}
      </Drawer>
    </div>
  )
}
