import { useEffect, useMemo, useRef, useState } from 'react'
import type { RegisterRow, Transport } from '../api/types'
import ConfidenceBadge from '../components/register/ConfidenceBadge'
import NoteDots from '../components/register/NoteDots'
import VerdictBadge from '../components/register/VerdictBadge'
import '../components/panels.css'
import {
  computeAccuracy,
  formatHeld,
  groupDate,
  groupRegisterRows,
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

export default function Register(props: { transport: Transport; workspaceId: string }): JSX.Element {
  const { transport, workspaceId } = props
  const [rows, setRows] = useState<RegisterRow[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [filters, setFilters] = useState<Filters>(EMPTY_FILTERS)
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
        return true
      }),
    [groups, filters],
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
        <button type="button" onClick={handleCopy} disabled={sorted.length === 0}>
          {copyLabel}
        </button>
      </div>

      {error && <p className="form-error">{error}</p>}
      {rows === null && !error && <p className="loading-state">Loading register…</p>}
      {rows !== null && groups.length === 0 && (
        <p className="empty-state">No triage runs recorded yet for this workspace.</p>
      )}

      {groups.length > 0 && (
        <>
          <p className="register-summary">
            Hypothesis held {formatHeld(accuracy.held)} of {accuracy.reviewed} reviewed (
            {accuracy.percent}%)
          </p>
          <p className="register-totals">
            Total cost ${totals.costUsd.toFixed(2)} across {totals.turns} turns.
          </p>
          <table className="register-table">
            <thead>
              <tr>
                <th>Key</th>
                <th>Service</th>
                <th>
                  <button
                    type="button"
                    className="sort-btn"
                    onClick={() => setSortDir((d) => (d === 'asc' ? 'desc' : 'asc'))}
                  >
                    Triage date {sortDir === 'asc' ? '▲' : '▼'}
                  </button>
                </th>
                <th>Confidence</th>
                <th>Classification</th>
                <th>RCA date</th>
                <th>Resolution</th>
                <th>Verdict</th>
                <th>Notes</th>
              </tr>
            </thead>
            <tbody>
              {sorted.map((group: RegisterGroup) => (
                <tr key={group.key}>
                  <td className="mono">{group.key}</td>
                  <td>{group.service || '—'}</td>
                  <td className="mono">{group.triage?.date || '—'}</td>
                  <td>
                    <ConfidenceBadge value={group.triage?.confidence} />
                  </td>
                  <td>{group.triage?.classification || '—'}</td>
                  <td className="mono">{group.rca?.date || '—'}</td>
                  <td>{group.resolution?.classification || '—'}</td>
                  <td>
                    <VerdictBadge value={group.triage?.triageVerdict} />
                  </td>
                  <td>
                    <NoteDots
                      triage={Boolean(group.triage?.notePath)}
                      rca={Boolean(group.rca?.notePath)}
                      resolution={Boolean(group.resolution?.notePath)}
                    />
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </>
      )}
    </div>
  )
}
