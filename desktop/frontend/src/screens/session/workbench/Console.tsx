import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, type JSX, type PointerEvent, type RefObject } from 'react'
import { tokens } from '../../../lib/format'
import SegmentedControl from '../../../ui/segmented-control'
import Toggle from '../../../ui/toggle'
import { ChevronDownIcon, ChevronRightIcon, CollapseIcon, MaximiseIcon, SearchIcon } from './icons'
import { CONSOLE_FILTERS, matchesConsoleFilter, matchesSearch, offsetTenths, type ConsoleFilter, type ConsoleRow, type Question } from './model'
import ToolStep from './ToolStep'
import ToolsTable from './ToolsTable'

/** The console's height is remembered across sessions, like the folded pane. */
export const CONSOLE_HEIGHT_KEY = 'sirdar.workbenchConsoleHeight'
export const CONSOLE_COLLAPSED_KEY = 'sirdar.workbenchConsoleCollapsed'
export const CONSOLE_DEFAULT_HEIGHT = 312
export const CONSOLE_MIN_HEIGHT = 120
/** The header alone. */
export const CONSOLE_HEAD_HEIGHT = 40

export interface ConsoleProps {
  rows: ConsoleRow[]
  countsLabel: string
  /** `837.7k in · 14.8k out`, when the run reports tokens. */
  usage?: { inputTokens: number; outputTokens: number }
  filter: ConsoleFilter
  onFilter: (f: ConsoleFilter) => void
  query: string
  onQuery: (q: string) => void
  searchRef: RefObject<HTMLInputElement | null>
  follow: boolean
  onFollow: (on: boolean) => void
  live: boolean
  collapsed: boolean
  onCollapsed: (v: boolean) => void
  maximised: boolean
  onMaximised: (v: boolean) => void
  height: number
  onHeight: (h: number) => void
  expanded: ReadonlySet<string>
  onToggleRow: (id: string) => void
  question?: Question
  startedAt: string | undefined
  startedMs: number
  onPickOption?: (text: string) => void
  /** The rail cell each row sits in, and its label, for the expanded body's metadata. */
  turnLabelOf: (row: ConsoleRow) => string | undefined
  /** A rail click: scroll to the first row of this cell. `n` changes on each click. */
  scrollTo?: { key: string; n: number }
  turnOf: (row: ConsoleRow) => string | undefined
  /** Reports which rail cells have rows in view. */
  onVisibleTurns?: (keys: Set<string>) => void
}

const TOOL_KINDS = new Set(['tool', 'deny', 'write', 'final'])

export default function Console(props: ConsoleProps): JSX.Element {
  const {
    rows,
    countsLabel,
    usage,
    filter,
    onFilter,
    query,
    onQuery,
    searchRef,
    follow,
    onFollow,
    live,
    collapsed,
    onCollapsed,
    maximised,
    onMaximised,
    height,
    onHeight,
    expanded,
    onToggleRow,
    question,
    startedAt,
    startedMs,
    onPickOption,
    turnLabelOf,
    scrollTo,
    turnOf,
    onVisibleTurns,
  } = props
  const log = useRef<HTMLDivElement | null>(null)
  const drag = useRef<{ y: number; h: number } | null>(null)

  const shown = useMemo(
    () => rows.filter((r) => matchesConsoleFilter(r, filter) && matchesSearch(r, query)),
    [rows, filter, query],
  )
  const callRows = useMemo(() => rows.filter((r) => r.call && matchesSearch(r, query)), [rows, query])

  // Follow live: the newest row stays in view while the run works.
  useLayoutEffect(() => {
    if (!follow || !live || !log.current) return
    log.current.scrollTop = log.current.scrollHeight
  }, [rows, follow, live])

  // A rail click brings its turn's first row into view and flashes it.
  useEffect(() => {
    if (!scrollTo || !log.current) return
    const target = log.current.querySelector<HTMLElement>(`[data-turn="${CSS.escape(scrollTo.key)}"]`)
    if (!target) return
    onFollow(false)
    target.scrollIntoView?.({ block: 'start' })
    target.dataset.flash = 'true'
    const t = setTimeout(() => {
      delete target.dataset.flash
    }, 1200)
    return () => clearTimeout(t)
  }, [scrollTo, onFollow])

  const reportVisible = useCallback(() => {
    const el = log.current
    if (!el || !onVisibleTurns) return
    const keys = new Set<string>()
    const items = el.querySelectorAll<HTMLElement>('[data-turn]')
    if (el.clientHeight === 0) {
      const last = items[items.length - 1]
      if (last?.dataset.turn) keys.add(last.dataset.turn)
      onVisibleTurns(keys)
      return
    }
    const top = el.scrollTop
    const bottom = top + el.clientHeight
    for (const item of items) {
      const y = item.offsetTop - el.offsetTop
      if (y + item.offsetHeight >= top && y <= bottom && item.dataset.turn) keys.add(item.dataset.turn)
    }
    onVisibleTurns(keys)
  }, [onVisibleTurns])

  useEffect(() => {
    reportVisible()
  }, [shown, reportVisible, collapsed, maximised, height])

  const onGrip = (e: PointerEvent<HTMLDivElement>) => {
    e.preventDefault()
    drag.current = { y: e.clientY, h: collapsed ? CONSOLE_DEFAULT_HEIGHT : height }
    const move = (ev: globalThis.PointerEvent) => {
      if (!drag.current) return
      const next = Math.max(CONSOLE_MIN_HEIGHT, drag.current.h + (drag.current.y - ev.clientY))
      if (collapsed) onCollapsed(false)
      onHeight(next)
    }
    const up = () => {
      drag.current = null
      window.removeEventListener('pointermove', move)
      window.removeEventListener('pointerup', up)
    }
    window.addEventListener('pointermove', move)
    window.addEventListener('pointerup', up)
  }

  const onGripKey = (e: React.KeyboardEvent<HTMLDivElement>) => {
    if (e.key === 'ArrowUp') {
      e.preventDefault()
      onCollapsed(false)
      onHeight(Math.max(CONSOLE_MIN_HEIGHT, (collapsed ? CONSOLE_DEFAULT_HEIGHT : height) + 24))
    } else if (e.key === 'ArrowDown') {
      e.preventDefault()
      onHeight(Math.max(CONSOLE_MIN_HEIGHT, height - 24))
    }
  }

  const style = maximised ? undefined : { blockSize: collapsed ? `${CONSOLE_HEAD_HEIGHT}px` : `${height}px` }

  return (
    <section
      className="wb-console"
      data-collapsed={collapsed ? 'true' : undefined}
      data-maximised={maximised ? 'true' : undefined}
      style={style}
      aria-label="Transcript"
    >
      <div
        className="wb-grip"
        role="separator"
        aria-orientation="horizontal"
        aria-label="Resize the transcript"
        aria-valuenow={collapsed ? CONSOLE_HEAD_HEIGHT : height}
        tabIndex={0}
        onPointerDown={onGrip}
        onKeyDown={onGripKey}
      />
      <div className="wb-chead">
        <span className="wb-chead__t">Transcript</span>
        <span className="wb-chead__cnt">{countsLabel}</span>
        <div className="wb-chead__seg">
          <SegmentedControl label="Show" options={CONSOLE_FILTERS} value={filter} onChange={(id) => onFilter(id as ConsoleFilter)} />
        </div>
        <label className="wb-csearch">
          <SearchIcon />
          <span className="visually-hidden">Search the transcript</span>
          <input
            ref={searchRef}
            type="search"
            value={query}
            placeholder="Search"
            onChange={(e) => onQuery(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Escape' && query) {
                e.stopPropagation()
                onQuery('')
              }
            }}
          />
          {query ? null : <kbd className="kbd">⌘F</kbd>}
        </label>
        <span className="wb-chead__right">
          {usage && (usage.inputTokens > 0 || usage.outputTokens > 0) ? (
            <span className="wb-chead__cnt">
              {tokens(usage.inputTokens)} in · {tokens(usage.outputTokens)} out
            </span>
          ) : null}
          <span className="wb-follow" data-off={follow ? undefined : 'true'}>
            <Toggle checked={follow} onChange={onFollow} label="Follow live" disabled={!live} />
            <span aria-hidden="true">Follow live</span>
          </span>
          <button
            type="button"
            className="wb-ib"
            aria-label={maximised ? 'Show the documents' : 'Maximise the transcript'}
            aria-pressed={maximised}
            title={maximised ? 'Show the documents' : 'Maximise the transcript'}
            onClick={() => {
              onMaximised(!maximised)
              if (!maximised) onCollapsed(false)
            }}
          >
            <MaximiseIcon />
          </button>
          <button
            type="button"
            className="wb-ib"
            aria-label={collapsed ? 'Expand the transcript' : 'Collapse the transcript to its header'}
            aria-pressed={collapsed}
            title={collapsed ? 'Expand the transcript' : 'Collapse the transcript to its header'}
            onClick={() => {
              onCollapsed(!collapsed)
              if (!collapsed) onMaximised(false)
            }}
          >
            <CollapseIcon />
          </button>
        </span>
      </div>

      {collapsed ? null : (
        <div className="wb-clog" ref={log} onScroll={reportVisible} data-testid="console-log">
          {filter === 'tools' ? (
            <ToolsTable
              rows={callRows}
              all={rows}
              startedMs={startedMs}
              expanded={expanded}
              onToggle={onToggleRow}
              renderExpanded={(row) => <ToolStep row={row} turnLabel={turnLabelOf(row)} live={live} />}
            />
          ) : (
            <>
              {shown.length === 0 ? (
                <p className="wb-empty wb-empty--inline">
                  {query ? `Nothing in the transcript matches “${query}”.` : filter === 'denied' ? 'No call was denied.' : 'Nothing yet.'}
                </p>
              ) : null}
              {shown.map((row) => {
                const open = expanded.has(row.id)
                const isCall = Boolean(row.call)
                const Tag = isCall ? 'button' : 'div'
                return (
                  <div key={row.id} data-turn={turnOf(row)} data-row={row.id}>
                    <Tag
                      type={isCall ? 'button' : undefined}
                      className="wb-row"
                      data-k={row.kind}
                      data-open={open ? 'true' : undefined}
                      aria-expanded={isCall ? open : undefined}
                      onClick={isCall ? () => onToggleRow(row.id) : undefined}
                    >
                      <span className="wb-row__at">{row.at}</span>
                      <span className="wb-row__tool">
                        {isCall && TOOL_KINDS.has(row.kind) && row.kind !== 'final' ? (
                          <span className="wb-row__tw">
                            {open ? <ChevronDownIcon /> : <ChevronRightIcon />}
                            {row.tool}
                          </span>
                        ) : (
                          row.tool
                        )}
                      </span>
                      <span className="wb-row__sum" title={row.summary} dir="auto">
                        {row.kind === 'final' ? <span className="wb-d">answer › </span> : null}
                        {row.kind === 'steer' ? `“${row.summary}”` : row.summary}
                        {row.description ? <span className="wb-d"> — {row.description}</span> : null}
                      </span>
                      <span className="wb-row__dur">{row.duration ?? ''}</span>
                      <span className="wb-row__out">{row.output ?? ''}</span>
                      <span className="wb-row__dec">{row.decision ?? ''}</span>
                      {row.kind === 'deny' && row.reason && !open ? <span className="wb-row__reason">{row.reason}</span> : null}
                    </Tag>
                    {open && isCall ? (
                      <ToolStep row={row} turnLabel={turnLabelOf(row)} live={live} onOpenInTools={() => onFilter('tools')} />
                    ) : null}
                  </div>
                )
              })}
            </>
          )}
          {question ? (
            <div className="wb-ask" role="region" aria-label="The agent is asking">
              <div className="wb-ask__h">
                <b>AskUserQuestion</b>
                <span>{offsetTenths(question.since, startedAt)}</span>
                <span>waiting on you · the run is paused, the budget clock is not</span>
              </div>
              <div className="wb-ask__q" dir="auto">
                {question.text}
              </div>
              {question.options.length > 0 ? (
                <div className="wb-ask__opts">
                  {question.options.map((o, i) => (
                    <button key={i} type="button" className="wb-opt" onClick={() => onPickOption?.(o)}>
                      <kbd className="kbd">{i + 1}</kbd>
                      {o}
                    </button>
                  ))}
                </div>
              ) : null}
            </div>
          ) : null}
        </div>
      )}
    </section>
  )
}
