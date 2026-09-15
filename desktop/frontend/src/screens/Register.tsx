import { useEffect, useMemo, useRef, useState } from 'react'
import type { RegisterRow, Transport } from '../api/types'
import ConfidenceBadge from '../components/register/ConfidenceBadge'
import NoteDots from '../components/register/NoteDots'
import VerdictBadge from '../components/register/VerdictBadge'
import Button from '../ui/button'
import DataTable, { type DataColumn } from '../ui/data-table'
import Heatmap from '../ui/heatmap'
import '../components/panels.css'
import './register.css'
import {
  computeAccuracy,
  formatHeld,
  groupDate,
  groupRegisterRows,
  runsPerDay,
  sumUsage,
  toMarkdownTable,
  type RegisterGroup,
} from '../lib/register'

interface Filters {
  service: string
  confidence: string
  verdict: string
}

const EMPTY_FILTERS: Filters = { service: '', confidence: '', verdict: '' }

/** How long the copy button reports what happened before it says its name. */
const COPY_LABEL_MS = 1500
const VERDICT_OPTIONS = ['confirmed', 'partial', 'wrong']

const COPY_LABEL = 'Copy as Markdown table'

function uniqueSorted(values: (string | undefined)[]): string[] {
  return [...new Set(values.filter((v): v is string => Boolean(v)))].sort()
}

/**
 * Every run this workspace has recorded, as a table, with the runs-per-day
 * grid above it.
 *
 * The grid is on this screen rather than on the Board because the Board is
 * about what is happening now and the Register is about what happened. Picking
 * a day filters the table to it, which is what keeps the colour from being the
 * only copy of the count.
 */
export default function Register(props: { transport: Transport; workspaceId: string }): JSX.Element {
  const { transport, workspaceId } = props
  const [rows, setRows] = useState<RegisterRow[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [filters, setFilters] = useState<Filters>(EMPTY_FILTERS)
  const [day, setDay] = useState('')
  const [sortDir, setSortDir] = useState<'asc' | 'desc'>('desc')
  const [copyLabel, setCopyLabel] = useState(COPY_LABEL)
  const copyTimer = useRef<ReturnType<typeof setTimeout> | null>(null)

  // The copy button restores its label on a timer; a screen that closes first
  // must not leave that timer behind to fire into an unmounted component.
  useEffect(
    () => () => {
      if (copyTimer.current) clearTimeout(copyTimer.current)
      copyTimer.current = null
    },
    [],
  )

  useEffect(() => {
    let cancelled = false
    setRows(null)
    setError(null)
    transport
      .register(workspaceId)
      .then((r) => {
        if (!cancelled) setRows(r)
      })
      .catch((err) => {
        if (!cancelled) setError(err instanceof Error ? err.message : String(err))
      })
    return () => {
      cancelled = true
    }
  }, [transport, workspaceId])

  const groups = useMemo(() => groupRegisterRows(rows ?? []), [rows])
  const perDay = useMemo(() => runsPerDay(groups), [groups])

  const services = useMemo(() => uniqueSorted(groups.map((g) => g.service)), [groups])
  const confidences = useMemo(
    () => uniqueSorted(groups.map((g) => g.triage?.confidence)),
    [groups],
  )

  const filtered = useMemo(
    () =>
      groups.filter((g) => {
        if (filters.service && g.service !== filters.service) return false
        if (filters.confidence && g.triage?.confidence !== filters.confidence) return false
        if (filters.verdict && g.triage?.triageVerdict !== filters.verdict) return false
        if (day && !g.rows.some((row) => (row.date ?? '').slice(0, 10) === day)) return false
        return true
      }),
    [groups, filters, day],
  )

  const sorted = useMemo(() => {
    const copy = [...filtered]
    copy.sort((a, b) => {
      const cmp = groupDate(a).localeCompare(groupDate(b))
      return sortDir === 'asc' ? cmp : -cmp
    })
    return copy
  }, [filtered, sortDir])

  const accuracy = useMemo(() => computeAccuracy(filtered), [filtered])
  const totals = useMemo(() => sumUsage(filtered), [filtered])

  function setFilter(key: keyof Filters, value: string) {
    setFilters((f) => ({ ...f, [key]: value }))
  }

  async function handleCopy() {
    const table = toMarkdownTable(sorted)
    try {
      await navigator.clipboard.writeText(table)
      setCopyLabel('Copied')
    } catch {
      setCopyLabel('Copy failed')
    }
    if (copyTimer.current) clearTimeout(copyTimer.current)
    copyTimer.current = setTimeout(() => setCopyLabel(COPY_LABEL), COPY_LABEL_MS)
  }

  const columns = useMemo<DataColumn<RegisterGroup>[]>(
    () => [
      { id: 'key', header: 'Key', cell: (g) => g.key, numeric: true },
      { id: 'service', header: 'Service', cell: (g) => g.service || '—' },
      {
        id: 'triageDate',
        header: 'Triage date',
        cell: (g) => g.triage?.date || '—',
        numeric: true,
        sortable: true,
      },
      { id: 'confidence', header: 'Confidence', cell: (g) => <ConfidenceBadge value={g.triage?.confidence} /> },
      { id: 'classification', header: 'Classification', cell: (g) => g.triage?.classification || '—' },
      { id: 'rcaDate', header: 'RCA date', cell: (g) => g.rca?.date || '—', numeric: true },
      { id: 'resolution', header: 'Resolution', cell: (g) => g.resolution?.classification || '—' },
      { id: 'verdict', header: 'Verdict', cell: (g) => <VerdictBadge value={g.triage?.triageVerdict} /> },
      {
        id: 'notes',
        header: 'Notes',
        cell: (g) => (
          <NoteDots
            triage={Boolean(g.triage?.notePath)}
            rca={Boolean(g.rca?.notePath)}
            resolution={Boolean(g.resolution?.notePath)}
          />
        ),
      },
    ],
    [],
  )

  return (
    <div className="panel register">
      <div className="register-toolbar">
        <div className="register-filters">
          <label>
            Service
            <select value={filters.service} onChange={(e) => setFilter('service', e.target.value)}>
              <option value="">All</option>
              {services.map((s) => (
                <option key={s} value={s}>
                  {s}
                </option>
              ))}
            </select>
          </label>
          <label>
            Confidence
            <select
              value={filters.confidence}
              onChange={(e) => setFilter('confidence', e.target.value)}
            >
              <option value="">All</option>
              {confidences.map((c) => (
                <option key={c} value={c}>
                  {c}
                </option>
              ))}
            </select>
          </label>
          <label>
            Verdict
            <select value={filters.verdict} onChange={(e) => setFilter('verdict', e.target.value)}>
              <option value="">All</option>
              {VERDICT_OPTIONS.map((v) => (
                <option key={v} value={v}>
                  {v}
                </option>
              ))}
            </select>
          </label>
        </div>
        {/*
          The Register is read-only and has no commit, so it has no filled
          button — which is the proof that "one primary per screen" is a rule
          rather than a decoration.
        */}
        <Button onClick={handleCopy} disabled={sorted.length === 0}>
          {copyLabel}
        </Button>
      </div>

      {error && <p className="form-error">{error}</p>}
      {rows === null && !error && <p className="loading-state">Loading register…</p>}
      {rows !== null && groups.length === 0 && (
        <p className="empty-state">No triage runs recorded yet for this workspace.</p>
      )}

      {groups.length > 0 && (
        <>
          <div className="register-activity">
            <Heatmap days={perDay} onSelect={(date) => setDay((d) => (d === date ? '' : date))} />
            {day && (
              <p className="register-day">
                Showing {day} only.{' '}
                <button type="button" className="register-day__clear" onClick={() => setDay('')}>
                  Show every day
                </button>
              </p>
            )}
          </div>

          <p className="register-summary">
            Hypothesis held {formatHeld(accuracy.held)} of {accuracy.reviewed} reviewed (
            {accuracy.percent}%)
          </p>
          <p className="register-totals">
            Total cost ${totals.costUsd.toFixed(2)} across {totals.turns} turns.
          </p>
          <DataTable
            caption="Register"
            columns={columns}
            rows={sorted}
            rowKey={(g) => g.key}
            sort={{ columnId: 'triageDate', direction: sortDir }}
            onSort={() => setSortDir((d) => (d === 'asc' ? 'desc' : 'asc'))}
            empty="Nothing in the register matches these filters."
          />
        </>
      )}
    </div>
  )
}
