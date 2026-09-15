import { useCallback, useEffect, useState } from 'react'
import type { EvalReport, EvalResult, GoldenEntry, Transport } from '../api/types'
import ProviderFields from '../components/run/ProviderFields'
import '../components/panels.css'

/** A fraction rendered the way the CLI's table renders it, or a dash. */
export function pct(f?: { matched: number; total: number; score: number }): string {
  if (!f || f.total === 0) return '—'
  return `${f.matched}/${f.total} ${Math.round(f.score * 100)}%`
}

/** The local time a report was written, or its raw stamp when unparseable. */
function when(at: string): string {
  const ms = Date.parse(at)
  return Number.isNaN(ms) ? at : new Date(ms).toLocaleString()
}

function ResultRow({ result }: { result: EvalResult }): JSX.Element {
  const failed = result.checks.filter((c) => !c.pass)
  return (
    <>
      <tr className="eval-row" data-state={result.state}>
        <th scope="row" className="mono">
          {result.key}
        </th>
        <td>{result.state.replace('_', ' ')}</td>
        <td>{result.turns}</td>
        <td>${result.costUsd.toFixed(2)}</td>
        <td>{result.minutes.toFixed(1)}</td>
        <td>{result.schemaValid ? 'yes' : 'no'}</td>
        <td>
          {result.passed}/{result.total}
        </td>
        <td>{pct(result.overlap?.refs)}</td>
        <td>{pct(result.overlap?.headings)}</td>
      </tr>
      {failed.length > 0 || result.reason ? (
        <tr className="eval-row eval-row--why">
          <td colSpan={9}>
            {result.reason && result.state !== 'completed' ? (
              <p className="eval-why">{result.reason}</p>
            ) : null}
            {failed.map((c) => (
              <p className="eval-why" key={c.key}>
                <span className="mono">{c.key}</span> — {c.detail || 'did not hold'}
              </p>
            ))}
          </td>
        </tr>
      ) : null}
    </>
  )
}

/**
 * The golden set and what the last eval scored against it.
 *
 * An eval replays stored bundles through real triage runs, so starting one
 * spends the provider exactly as a triage does. The table is read back from
 * the report the run wrote, not held in the window: a report survives the app
 * being closed, and the CLI writes the same file.
 */
export default function Eval(props: {
  transport: Transport
  workspaceId: string
  defaultProvider?: string
  onStartEval: (keys?: string[], opts?: { provider?: string; model?: string }) => Promise<void> | void
}): JSX.Element {
  const { transport, workspaceId, defaultProvider, onStartEval } = props
  const [golden, setGolden] = useState<GoldenEntry[] | null>(null)
  const [reports, setReports] = useState<EvalReport[]>([])
  const [selected, setSelected] = useState<Set<string>>(new Set())
  const [provider, setProvider] = useState('')
  const [model, setModel] = useState('')
  const [pending, setPending] = useState(false)
  const [error, setError] = useState('')

  const load = useCallback(async () => {
    if (!workspaceId) return
    setError('')
    try {
      const [entries, found] = await Promise.all([
        transport.golden(workspaceId),
        transport.evalReports(workspaceId),
      ])
      setGolden(entries)
      setReports(found)
    } catch (err) {
      setGolden([])
      setError(err instanceof Error ? err.message : String(err))
    }
  }, [transport, workspaceId])

  useEffect(() => {
    void load()
  }, [load])

  // An eval writes its report when the job ends, so the table is reloaded
  // then rather than leaving the reader to press Refresh.
  useEffect(() => {
    return transport.subscribe((e) => {
      if (e.kind === 'job.finished') void load()
    })
  }, [transport, load])

  const toggle = (key: string) => {
    setSelected((prev) => {
      const next = new Set(prev)
      if (next.has(key)) next.delete(key)
      else next.add(key)
      return next
    })
  }

  async function start(): Promise<void> {
    setPending(true)
    setError('')
    try {
      const keys = [...selected]
      await onStartEval(keys.length > 0 ? keys : undefined, {
        provider: provider || undefined,
        model: model.trim() || undefined,
      })
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setPending(false)
    }
  }

  const latest = reports[0]
  const entries = golden ?? []

  return (
    <div className="panel eval">
      <section className="eval-golden">
        <h2 className="panel-heading">Golden set</h2>
        {golden === null ? (
          <p className="empty-state">Loading the golden set…</p>
        ) : entries.length === 0 ? (
          <p className="empty-state">
            No golden bundles yet. Open a completed triage run and add it to the golden set, or
            run <code>sirdar golden add KEY</code>.
          </p>
        ) : (
          <ul className="golden-list">
            {entries.map((entry) => (
              <li key={entry.key} className="golden-row">
                <label className="golden-row__pick">
                  <input
                    type="checkbox"
                    checked={selected.has(entry.key)}
                    onChange={() => toggle(entry.key)}
                  />
                  <span className="mono">{entry.key}</span>
                </label>
                <span className="golden-row__meta">
                  {entry.assertions} {entry.assertions === 1 ? 'assertion' : 'assertions'}
                </span>
                <span className="golden-row__meta">
                  {entry.hasExpectedNote ? 'expected.md' : 'no expected.md'}
                </span>
              </li>
            ))}
          </ul>
        )}

        <ProviderFields
          idPrefix="eval"
          provider={provider}
          model={model}
          defaultProvider={defaultProvider}
          disabled={pending}
          onProvider={setProvider}
          onModel={setModel}
        />
        <div className="form-row" style={{ marginTop: 8 }}>
          <button
            type="button"
            className="run-btn run-btn--primary"
            onClick={() => void start()}
            disabled={pending || entries.length === 0}
          >
            {pending
              ? 'Starting…'
              : selected.size > 0
                ? `Run eval on ${selected.size} ${selected.size === 1 ? 'key' : 'keys'}`
                : 'Run eval on the whole set'}
          </button>
          <button type="button" className="run-btn" onClick={() => void load()} disabled={pending}>
            Refresh
          </button>
        </div>
        <p className="about-note">
          An eval replays each bundle through a real triage run, so it spends the provider the
          way a triage does. Its runs are marked eval: they never file a note and never reach the
          register.
        </p>
        {error ? <p className="form-error">{error}</p> : null}
      </section>

      <section className="eval-report">
        <h2 className="panel-heading">Last eval</h2>
        {!latest ? (
          <p className="empty-state">
            No eval has been recorded for this workspace yet. Reports are written to{' '}
            <code>.sirdar/eval</code>.
          </p>
        ) : (
          <>
            <p className="eval-meta">
              {when(latest.at)} · {latest.provider}
              {latest.model ? ` ${latest.model}` : ''} ·{' '}
              <span className="mono">{latest.path}</span>
            </p>
            <table className="eval-table">
              <caption className="visually-hidden">Score per key</caption>
              <thead>
                <tr>
                  <th scope="col">Key</th>
                  <th scope="col">State</th>
                  <th scope="col">Turns</th>
                  <th scope="col">Cost</th>
                  <th scope="col">Mins</th>
                  <th scope="col">Valid</th>
                  <th scope="col">Assertions</th>
                  <th scope="col">Refs</th>
                  <th scope="col">Headings</th>
                </tr>
              </thead>
              <tbody>
                {latest.results.map((r) => (
                  <ResultRow key={r.key} result={r} />
                ))}
              </tbody>
            </table>
          </>
        )}
      </section>
    </div>
  )
}
