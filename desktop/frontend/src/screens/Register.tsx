import { useCallback, useEffect, useId, useMemo, useRef, useState } from 'react'
import type { RegisterRow, RunSummary, Transport } from '../api/types'
import NoteDots from '../components/register/NoteDots'
import OutlineChip, { VERDICT_TONES } from '../components/register/OutlineChip'
import { reasonOf, usd } from '../lib/format'
import {
  buildLedger,
  confirmedShare,
  dayOf,
  kindsLine,
  ledgerPerDay,
  runsThisWeek,
  spendLine,
  spent,
  toCSV,
  type LedgerRow,
} from '../lib/register'
import Button from '../ui/button'
import DataTable, { type DataColumn } from '../ui/data-table'
import Heatmap, { dayName } from '../ui/heatmap'
import PageHead from '../ui/page-head'
import ProviderMark from '../ui/provider-mark'
import Avatar from '../ui/run-card/Avatar'
import StatCard from '../ui/stat-card'
import StatusBadge from '../ui/status-badge'
import './register.css'

interface Filters {
  kind: string
  state: string
  provider: string
}

const EMPTY_FILTERS: Filters = { kind: '', state: '', provider: '' }

/** How many weeks the grid draws. The card's side label says the same number. */
const WEEKS = 26

const MONTHS = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec']

/**
 * "15 Sep 09:41" for a run with a start stamp, in the reader's own clock;
 * "14 Sep" for a register row that only knows its day. Anything else is
 * shown as written rather than guessed at.
 */
export function whenLabel(when: string): string {
  if (/^\d{4}-\d{2}-\d{2}$/.test(when)) {
    const m = Number(when.slice(5, 7))
    return `${Number(when.slice(8, 10))} ${MONTHS[m - 1] ?? m}`
  }
  const ms = Date.parse(when)
  if (Number.isNaN(ms)) return when
  const d = new Date(ms)
  const hh = String(d.getHours()).padStart(2, '0')
  const mm = String(d.getMinutes()).padStart(2, '0')
  return `${d.getDate()} ${MONTHS[d.getMonth()]} ${hh}:${mm}`
}

/** The name the CSV is saved under: the workspace and the day it was exported. */
export function csvFileName(workspaceId: string, now = Date.now()): string {
  const ws = workspaceId.replace(/[^\w.-]+/g, '-') || 'workspace'
  return `sirdar-register-${ws}-${dayOf(new Date(now).toISOString())}.csv`
}

/**
 * Hands a file to the browser as a download. The Wails shell binds no save
 * dialog today, so this is the one path on both surfaces; the day the bridge
 * gains one, this is the function that asks for it first.
 */
function download(name: string, text: string): void {
  const blob = new Blob([text], { type: 'text/csv;charset=utf-8' })
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = name
  a.rel = 'noopener'
  document.body.appendChild(a)
  a.click()
  a.remove()
  URL.revokeObjectURL(url)
}

function uniqueSorted(values: string[]): string[] {
  return [...new Set(values.filter(Boolean))].sort()
}

/** Replaces the run with the same id, or adds it: what a `run.updated` event means. */
function upsert(runs: RunSummary[], run: RunSummary): RunSummary[] {
  const i = runs.findIndex((r) => r.runId === run.runId)
  if (i === -1) return [...runs, run]
  const next = runs.slice()
  next[i] = run
  return next
}

function ExportIcon(): JSX.Element {
  return (
    <svg
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.5"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
      focusable="false"
    >
      <path d="M12 4v11M7 10l5 5 5-5M4 20h16" />
    </svg>
  )
}

function FilterIcon(): JSX.Element {
  return (
    <svg
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.5"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
      focusable="false"
    >
      <path d="M4 5h16l-6 8v5l-4 2v-7z" />
    </svg>
  )
}

/**
 * Every run this workspace has recorded, and the day it happened.
 *
 * The screen reads the register and the runs on disk and joins them: the
 * register is what was filed, the runs are what happened, and a run that
 * failed or is still going is only in the second. Three figures and the
 * runs-per-day grid sit over the table and are computed from the same rows
 * the table draws, so the card and the column under it cannot disagree.
 *
 * Read-only. Nothing here starts, answers or steers a run, so the screen
 * publishes no primary and the one control that acts on the sheet is Export
 * CSV, which changes nothing.
 */
export default function Register(props: {
  transport: Transport
  workspaceId: string
  /** Opens a run's session. The key in each row is the link when this is given. */
  onOpenRun?: (runId: string) => void
}): JSX.Element {
  const { transport, workspaceId, onOpenRun } = props
  const [rows, setRows] = useState<RegisterRow[] | null>(null)
  const [runs, setRuns] = useState<RunSummary[]>([])
  const [error, setError] = useState<string | null>(null)
  const [filters, setFilters] = useState<Filters>(EMPTY_FILTERS)
  const [day, setDay] = useState('')
  const [sortDir, setSortDir] = useState<'asc' | 'desc'>('desc')
  const [filtersOpen, setFiltersOpen] = useState(false)
  /** The Filters button and its popover, so a click on either is not "outside". */
  const filtersBox = useRef<HTMLDivElement | null>(null)
  const popoverId = useId()
  // Which load is the current one. A read that started before a workspace
  // switch must not land on the screen after it.
  const generation = useRef(0)

  const load = useCallback(async () => {
    const mine = generation.current + 1
    generation.current = mine
    setError(null)
    try {
      const [register, onDisk] = await Promise.all([
        transport.register(workspaceId),
        transport.runs(workspaceId),
      ])
      if (mine !== generation.current) return
      setRows(register)
      setRuns(onDisk)
    } catch (err) {
      if (mine !== generation.current) return
      setError(reasonOf(err))
    }
  }, [transport, workspaceId])

  useEffect(() => {
    setRows(null)
    setRuns([])
    void load()
    return () => {
      generation.current += 1
    }
  }, [load])

  // A run that changes state changes its row; a job that ends may have
  // appended to the register, which only a re-read can show.
  useEffect(
    () =>
      transport.subscribe((e) => {
        if (e.kind === 'run.updated' && e.workspaceId === workspaceId) {
          setRuns((prev) => upsert(prev, e.run))
        } else if (e.kind === 'job.finished' && e.workspaceId === workspaceId) {
          void load()
        }
      }),
    [transport, workspaceId, load],
  )

  // The popover closes on Escape and on a click anywhere outside it.
  useEffect(() => {
    if (!filtersOpen) return
    function onKey(e: KeyboardEvent): void {
      if (e.key === 'Escape') {
        setFiltersOpen(false)
        filtersBox.current?.querySelector('button')?.focus()
      }
    }
    function onPointer(e: MouseEvent): void {
      if (filtersBox.current?.contains(e.target as Node)) return
      setFiltersOpen(false)
    }
    document.addEventListener('keydown', onKey)
    document.addEventListener('mousedown', onPointer)
    return () => {
      document.removeEventListener('keydown', onKey)
      document.removeEventListener('mousedown', onPointer)
    }
  }, [filtersOpen])

  const ledger = useMemo(() => buildLedger(rows ?? [], runs), [rows, runs])
  const week = useMemo(() => runsThisWeek(ledger), [ledger])
  const spend = useMemo(() => spent(ledger), [ledger])
  const confirmed = useMemo(() => confirmedShare(rows ?? []), [rows])
  const perDay = useMemo(() => ledgerPerDay(ledger), [ledger])
  const today = dayOf(new Date().toISOString())

  // The ticket's title, by run, for the key cell's tooltip: the table is
  // read by key and the title is what the key was about.
  const titles = useMemo(() => {
    const map = new Map<string, string>()
    for (const r of runs) if (r.title) map.set(r.runId, r.title)
    return map
  }, [runs])

  // Who the ticket belonged to, by run. A register row on its own does not
  // record it — the run does, off the bundle it gathered — so a row with no
  // run behind it any more shows an em dash rather than a guess.
  const assignees = useMemo(() => {
    const map = new Map<string, string>()
    for (const r of runs) if (r.assignee) map.set(r.runId, r.assignee)
    return map
  }, [runs])

  const kinds = useMemo(() => uniqueSorted(ledger.map((r) => r.kind)), [ledger])
  const states = useMemo(() => uniqueSorted(ledger.map((r) => r.state)), [ledger])
  const providers = useMemo(() => uniqueSorted(ledger.map((r) => r.provider)), [ledger])

  const visible = useMemo(() => {
    const kept = ledger.filter((r) => {
      if (filters.kind && r.kind !== filters.kind) return false
      if (filters.state && r.state !== filters.state) return false
      if (filters.provider && r.provider !== filters.provider) return false
      if (day && r.day !== day) return false
      return true
    })
    // The ledger is newest first; ascending is the same list turned over.
    return sortDir === 'desc' ? kept : kept.slice().reverse()
  }, [ledger, filters, day, sortDir])

  const activeFilters = Object.values(filters).filter(Boolean).length

  function setFilter(key: keyof Filters, value: string): void {
    setFilters((f) => ({ ...f, [key]: value }))
  }

  function exportCSV(): void {
    download(csvFileName(workspaceId), toCSV(visible))
  }

  const columns = useMemo<DataColumn<LedgerRow>[]>(
    () => [
      {
        id: 'key',
        header: 'Key',
        cell: (r) =>
          onOpenRun ? (
            <button
              type="button"
              className="register-key register-key--link"
              dir="ltr"
              onClick={() => onOpenRun(r.runId)}
              title={titles.get(r.runId) || 'Open this run'}
            >
              {r.key}
            </button>
          ) : (
            <span className="register-key" dir="ltr" title={titles.get(r.runId)}>
              {r.key}
            </span>
          ),
      },
      { id: 'kind', header: 'Kind', cell: (r) => <span className="register-kind">{r.kind}</span> },
      { id: 'state', header: 'State', cell: (r) => <StatusBadge status={r.state} /> },
      {
        id: 'provider',
        header: 'Provider',
        cell: (r) =>
          r.provider ? (
            <span className="register-provider">
              <ProviderMark provider={r.provider} size="sm" />
              <span>{r.provider}</span>
            </span>
          ) : (
            <span className="register-none">—</span>
          ),
      },
      {
        id: 'assignee',
        header: 'Assignee',
        cell: (r) => {
          const who = assignees.get(r.runId) ?? ''
          return who ? (
            <span className="sd-assignee">
              <Avatar name={who} />
              <span className="sd-assignee__name">{who}</span>
            </span>
          ) : (
            <span className="register-none">—</span>
          )
        },
      },
      { id: 'turns', header: 'Turns', cell: (r) => r.turns, numeric: true },
      { id: 'cost', header: 'Cost', cell: (r) => usd(r.costUsd), numeric: true },
      { id: 'mins', header: 'Mins', cell: (r) => (r.minutes === null ? '—' : r.minutes), numeric: true },
      { id: 'confidence', header: 'Confidence', cell: (r) => <OutlineChip value={r.confidence} /> },
      {
        id: 'verdict',
        header: 'Verdict',
        cell: (r) => <OutlineChip value={r.verdict} tone={VERDICT_TONES[r.verdict]} />,
      },
      { id: 'notes', header: 'Notes', cell: (r) => <NoteDots {...r.notes} /> },
      {
        id: 'when',
        header: 'When',
        cell: (r) => <span className="register-when">{whenLabel(r.when)}</span>,
        sortable: true,
      },
    ],
    [assignees, onOpenRun, titles],
  )

  /** Why a run stopped, under the row of a run that did not finish cleanly. */
  function detail(r: LedgerRow): string | null {
    if (r.state !== 'failed' && r.state !== 'over_budget') return null
    return r.reason || null
  }

  const loaded = rows !== null && !error

  return (
    <div className="register">
      <div className="register-head">
        <PageHead
          title="Register"
          lede="Every run, and the day it happened."
          actions={
            <Button icon={<ExportIcon />} onClick={exportCSV} disabled={visible.length === 0}>
              Export CSV
            </Button>
          }
        />
      </div>

      {error && (
        <p className="register-error" role="alert">
          {error}
        </p>
      )}
      {rows === null && !error && <p className="register-loading">Loading the register…</p>}

      {loaded && (
        <>
          <div className="register-band">
            <div className="register-figs">
              <StatCard
                label="Runs this week"
                value={String(week.total)}
                detail={week.total > 0 ? kindsLine(week.kinds) : 'None in the last seven days'}
              />
              <StatCard
                label="Spent"
                value={usd(spend.total)}
                valueTitle={`$${spend.total.toFixed(4)}`}
                detail={spend.providers.length > 0 ? spendLine(spend.providers, usd) : 'Nothing yet'}
              />
              <StatCard
                label="Confirmed"
                value={confirmed.recorded > 0 ? `${confirmed.percent}%` : '—'}
                detail={`of ${confirmed.recorded} ${confirmed.recorded === 1 ? 'verdict' : 'verdicts'} recorded`}
              />
            </div>
            <section className="register-heatcard" aria-labelledby="register-heat-title">
              <div className="register-heatcard__head">
                <h2 className="register-heatcard__title" id="register-heat-title">
                  Runs per day
                </h2>
                <span className="register-heatcard__side">{WEEKS} weeks</span>
              </div>
              <Heatmap
                days={perDay}
                weeks={WEEKS}
                endDate={today}
                onSelect={(date) => setDay((d) => (d === date ? '' : date))}
              />
            </section>
          </div>

          {ledger.length === 0 ? (
            <p className="register-empty">
              No runs recorded yet for this workspace. Start one from New session, or run{' '}
              <code>sirdar triage KEY</code>.
            </p>
          ) : (
            <>
              <div className="register-toolbar">
                <div className="register-filters" ref={filtersBox}>
                  <Button
                    variant="pale"
                    icon={<FilterIcon />}
                    aria-expanded={filtersOpen}
                    aria-controls={popoverId}
                    onClick={() => setFiltersOpen((o) => !o)}
                  >
                    {activeFilters > 0 ? `Filters (${activeFilters})` : 'Filters'}
                  </Button>
                  {filtersOpen && (
                    <div
                      className="register-popover"
                      id={popoverId}
                      role="group"
                      aria-label="Filters"
                    >
                      <label className="register-field">
                        Kind
                        <select value={filters.kind} onChange={(e) => setFilter('kind', e.target.value)}>
                          <option value="">All</option>
                          {kinds.map((k) => (
                            <option key={k} value={k}>
                              {k}
                            </option>
                          ))}
                        </select>
                      </label>
                      <label className="register-field">
                        State
                        <select value={filters.state} onChange={(e) => setFilter('state', e.target.value)}>
                          <option value="">All</option>
                          {states.map((s) => (
                            <option key={s} value={s}>
                              {s.replace('_', ' ')}
                            </option>
                          ))}
                        </select>
                      </label>
                      <label className="register-field">
                        Provider
                        <select
                          value={filters.provider}
                          onChange={(e) => setFilter('provider', e.target.value)}
                        >
                          <option value="">All</option>
                          {providers.map((p) => (
                            <option key={p} value={p}>
                              {p}
                            </option>
                          ))}
                        </select>
                      </label>
                      <Button
                        variant="ghost"
                        size="sm"
                        disabled={activeFilters === 0}
                        onClick={() => setFilters(EMPTY_FILTERS)}
                      >
                        Clear filters
                      </Button>
                    </div>
                  )}
                </div>
                {day && (
                  <p className="register-day">
                    Showing {dayName(day)} only.{' '}
                    <Button variant="ghost" size="sm" onClick={() => setDay('')}>
                      Show every day
                    </Button>
                  </p>
                )}
              </div>

              <DataTable
                caption="Every run"
                columns={columns}
                rows={visible}
                rowKey={(r) => r.runId}
                sort={{ columnId: 'when', direction: sortDir }}
                onSort={() => setSortDir((d) => (d === 'asc' ? 'desc' : 'asc'))}
                detail={detail}
                empty="Nothing in the register matches these filters."
              />
            </>
          )}
        </>
      )}
    </div>
  )
}
