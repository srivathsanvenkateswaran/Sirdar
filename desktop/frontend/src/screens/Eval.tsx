import { useCallback, useEffect, useMemo, useState, type FormEvent } from 'react'
import type {
  EvalReport,
  EvalResult,
  EvalStart,
  GoldenEntry,
  Quota,
  RetroReport,
  RetroResult,
  Transport,
} from '../api/types'
import ProviderFields from '../components/run/ProviderFields'
import { useProvidePrimaryAction } from '../components/shell/primaryAction'
import Badge from '../ui/badge'
import Button from '../ui/button'
import DataTable, { type DataColumn } from '../ui/data-table'
import Dialog from '../ui/dialog'
import PageHead from '../ui/page-head'
import ProviderMark from '../ui/provider-mark'
import SettingRow, { SettingCard } from '../ui/setting-row'
import StatusBadge, { type SdStatus } from '../ui/status-badge'
import Toggle from '../ui/toggle'
import './eval.css'

/** How long one key takes, for the estimate under the actions. */
export const MINUTES_PER_KEY = 5

/** A fraction rendered the way the CLI's table renders it, or a dash. */
export function pct(f?: { matched: number; total: number; score: number }): string {
  if (!f || f.total === 0) return '—'
  return `${f.matched}/${f.total} ${Math.round(f.score * 100)}%`
}

/**
 * The file overlap of two diffs, rendered as the CLI renders it. A union of
 * zero is two diffs that changed nothing between them, which is a dash
 * rather than a perfect score.
 */
export function jaccard(j?: { intersection: number; union: number; score: number }): string {
  if (!j || j.union === 0) return '—'
  return `${j.intersection}/${j.union} ${Math.round(j.score * 100)}%`
}

/**
 * "N selected · about M min", with the 5h clause when a quota reading exists.
 *
 * Five minutes a key is the estimate; the clause reads the provider's own
 * five-hour window, and says nothing when no provider has reported one, so
 * the line never claims a budget it cannot see.
 */
export function estimate(selected: number, quota: Quota | undefined, running: boolean): string {
  if (running) return `A suite is running · ${selected} selected`
  if (selected === 0) return 'Nothing selected'
  const parts = [`${selected} selected`, `about ${selected * MINUTES_PER_KEY} min`]
  if (quota?.fiveHour) {
    parts.push(
      quota.fiveHour.utilization < 1 ? 'within the 5h window' : 'the 5h window is used up',
    )
  }
  return parts.join(' · ')
}

const MONTHS = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec']

/** "15 Sep 09:33" in local time, or the raw stamp when it does not parse. */
export function when(at: string): string {
  const ms = Date.parse(at)
  if (Number.isNaN(ms)) return at
  const d = new Date(ms)
  const two = (n: number) => String(n).padStart(2, '0')
  return `${d.getDate()} ${MONTHS[d.getMonth()]} ${two(d.getHours())}:${two(d.getMinutes())}`
}

/** yes, no, or a dash for a session that said nothing legible about it. */
function yesNo(b?: boolean): string {
  if (b === undefined || b === null) return '—'
  return b ? 'yes' : 'no'
}

function money(usd: number): string {
  return `$${usd.toFixed(2)}`
}

function mins(m?: number): string {
  if (m === undefined || m === null) return '—'
  return m.toFixed(1)
}

/** The run states a report can carry, narrowed to the badge's own set. */
function asStatus(state: string): SdStatus {
  switch (state) {
    case 'queued':
    case 'preparing':
    case 'running':
    case 'blocked':
    case 'completed':
    case 'failed':
    case 'over_budget':
      return state
    default:
      return 'failed'
  }
}

/** The state word. A completed replay is done; the rest are the library's. */
function StateCell({ state }: { state: string }): JSX.Element {
  const status = asStatus(state)
  return <StatusBadge status={status}>{status === 'completed' ? 'Done' : undefined}</StatusBadge>
}

/**
 * One state for a retro row. A retro is up to three runs; the row is failed
 * if any of them failed, otherwise the least finished of them.
 */
export function retroState(r: RetroResult): SdStatus {
  const stages = [r.triage, r.rca, r.fix].filter((s): s is NonNullable<typeof s> => Boolean(s))
  const states = stages.map((s) => s.state ?? '')
  for (const s of ['failed', 'over_budget', 'blocked', 'running', 'preparing', 'queued'] as const) {
    if (states.includes(s)) return s
  }
  if (stages.length === 0) return r.reason ? 'failed' : 'queued'
  return 'completed'
}

/**
 * What the rubric said about the root cause, as an outlined chip. A retro run
 * without a rubric has no opinion to show, so the cell is a dash rather than
 * a guess from the overlap numbers.
 */
function RootCauseChip({ r }: { r: RetroResult }): JSX.Element {
  if (!r.rubric) return <span className="eval-none">—</span>
  if (r.rubric.sameRootCause) {
    return (
      <span className="eval-chip" data-tone="done">
        matched
      </span>
    )
  }
  if (r.rubric.verdict === 'partial') {
    return (
      <span className="eval-chip" data-tone="blocked">
        partial
      </span>
    )
  }
  return (
    <span className="eval-chip" data-tone="failed">
      different
    </span>
  )
}

function sumStages(r: RetroResult, field: 'turns' | 'minutes'): number | undefined {
  const values = [r.triage, r.rca, r.fix].map((s) => s?.[field]).filter((v): v is number => typeof v === 'number')
  if (values.length === 0) return undefined
  return values.reduce((a, b) => a + b, 0)
}

const RETRO_COLUMNS: DataColumn<RetroResult>[] = [
  { id: 'key', header: 'Key', cell: (r) => r.key, numeric: true },
  { id: 'state', header: 'State', cell: (r) => <StateCell state={retroState(r)} /> },
  { id: 'rootCause', header: 'Root cause', cell: (r) => <RootCauseChip r={r} /> },
  { id: 'files', header: 'File overlap', cell: (r) => jaccard(r.fixScore?.filesJaccard), numeric: true },
  { id: 'rubric', header: 'Rubric', cell: (r) => r.rubric?.verdict ?? '—' },
  { id: 'turns', header: 'Turns', cell: (r) => sumStages(r, 'turns') ?? '—', numeric: true },
  { id: 'cost', header: 'Cost', cell: (r) => money(r.costUsd), numeric: true },
  { id: 'minutes', header: 'Mins', cell: (r) => mins(sumStages(r, 'minutes')), numeric: true },
]

/** What the agent missed, and what the rubric said about it. */
function retroDetail(result: RetroResult): JSX.Element | null {
  const missed = result.triageScore?.missedFiles ?? []
  if (!result.reason && missed.length === 0 && !result.rubric?.reasoning) return null
  return (
    <>
      {result.reason ? <p className="eval-why">{result.reason}</p> : null}
      {missed.length > 0 ? (
        <p className="eval-why">
          the note never named <span className="mono">{missed.join(', ')}</span>
        </p>
      ) : null}
      {result.rubric?.reasoning ? (
        <p className="eval-why">
          rubric {result.rubric.verdict} — {result.rubric.reasoning}
        </p>
      ) : null}
    </>
  )
}

const PLAIN_COLUMNS: DataColumn<EvalResult>[] = [
  { id: 'key', header: 'Key', cell: (r) => r.key, numeric: true },
  { id: 'state', header: 'State', cell: (r) => <StateCell state={r.state} /> },
  { id: 'assertions', header: 'Assertions', cell: (r) => `${r.passed}/${r.total}`, numeric: true },
  { id: 'refs', header: 'Refs', cell: (r) => pct(r.overlap?.refs), numeric: true },
  { id: 'headings', header: 'Headings', cell: (r) => pct(r.overlap?.headings), numeric: true },
  { id: 'valid', header: 'Valid', cell: (r) => yesNo(r.schemaValid) },
  { id: 'turns', header: 'Turns', cell: (r) => r.turns, numeric: true },
  { id: 'cost', header: 'Cost', cell: (r) => money(r.costUsd), numeric: true },
  { id: 'minutes', header: 'Mins', cell: (r) => mins(r.minutes), numeric: true },
]

/** Why a key scored what it did: the run's own reason, then each failed check. */
function plainDetail(result: EvalResult): JSX.Element | null {
  const failed = result.checks.filter((c) => !c.pass)
  if (failed.length === 0 && !result.reason) return null
  return (
    <>
      {result.reason && result.state !== 'completed' ? (
        <p className="eval-why">{result.reason}</p>
      ) : null}
      {failed.map((c) => (
        <p className="eval-why" key={c.key}>
          <span className="mono">{c.key}</span> — {c.detail || 'did not hold'}
        </p>
      ))}
    </>
  )
}

/** The newest report the workspace has, whichever shape it is. */
type Latest = { kind: 'retro'; report: RetroReport } | { kind: 'plain'; report: EvalReport }

export function latestOf(reports: EvalReport[], retro: RetroReport | null): Latest | null {
  const plain = reports[0]
  if (!plain && !retro) return null
  if (!retro) return { kind: 'plain', report: plain }
  if (!plain) return { kind: 'retro', report: retro }
  return Date.parse(retro.at) >= Date.parse(plain.at)
    ? { kind: 'retro', report: retro }
    : { kind: 'plain', report: plain }
}

function reportMeta(latest: Latest): string {
  const parts = [when(latest.report.at), latest.kind]
  if (latest.kind === 'retro') {
    if (latest.report.rubric) parts.push('rubric')
    if (latest.report.withRca) parts.push('rca')
  }
  parts.push(
    latest.report.model ? `${latest.report.provider} ${latest.report.model}` : latest.report.provider,
  )
  return parts.join(' · ')
}

function PlusIcon(): JSX.Element {
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
      <path d="M12 5v14M5 12h14" />
    </svg>
  )
}

function CheckIcon(): JSX.Element {
  return (
    <svg
      className="eval-cb__mark"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="3"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
      focusable="false"
    >
      <path d="m5 12 5 5 9-10" />
    </svg>
  )
}

type Open = 'golden' | 'json' | 'provider' | null

/**
 * The golden set and what the last suite scored against it.
 *
 * A suite replays stored bundles through real runs, so starting one spends
 * the provider exactly as a triage does. The table is read back from the
 * report the run wrote, not held in the window: a report survives the app
 * being closed, and the CLI writes the same file.
 */
export default function Eval(props: {
  transport: Transport
  workspaceId: string
  defaultProvider?: string
  defaultModel?: string
  /** The store's quota readings; the estimate's 5h clause reads them. */
  quota?: Quota[]
  /**
   * Whole-set eval jobs this window started. Such a job names no key, so no
   * run claims it and this screen is the only place its Cancel can live.
   */
  jobs?: { jobId: string; label: string }[]
  onStartEval: (keys?: string[], opts?: EvalStart) => Promise<void> | void
  onCancelJob?: (jobId: string) => Promise<void> | void
}): JSX.Element {
  const { transport, workspaceId, defaultProvider, defaultModel, quota, jobs, onStartEval, onCancelJob } =
    props
  const [golden, setGolden] = useState<GoldenEntry[] | null>(null)
  const [reports, setReports] = useState<EvalReport[] | null>(null)
  const [retro, setRetro] = useState<RetroReport | null>(null)
  const [selected, setSelected] = useState<Set<string>>(new Set())
  const [rubric, setRubric] = useState(true)
  const [withRca, setWithRca] = useState(false)
  const [provider, setProvider] = useState('')
  const [model, setModel] = useState('')
  const [pending, setPending] = useState(false)
  const [started, setStarted] = useState(false)
  const [error, setError] = useState('')
  const [open, setOpen] = useState<Open>(null)

  // The add-golden dialog's own state.
  const [goldenKey, setGoldenKey] = useState('')
  const [goldenPending, setGoldenPending] = useState(false)
  const [goldenError, setGoldenError] = useState('')

  // The provider dialog edits a draft and commits it on Save.
  const [draftProvider, setDraftProvider] = useState('')
  const [draftModel, setDraftModel] = useState('')

  const load = useCallback(async () => {
    if (!workspaceId) return
    setError('')
    try {
      const [entries, found, lastRetro] = await Promise.all([
        transport.golden(workspaceId),
        transport.evalReports(workspaceId),
        // A workspace that has never run a retro answers null.
        transport.latestRetro(workspaceId),
      ])
      setGolden(entries)
      setReports(found)
      setRetro(lastRetro)
    } catch (err) {
      setGolden([])
      setReports([])
      setError(err instanceof Error ? err.message : String(err))
    }
  }, [transport, workspaceId])

  useEffect(() => {
    void load()
  }, [load])

  // A suite writes its report when the job ends, so the table is reloaded
  // then rather than leaving the reader to press anything.
  useEffect(() => {
    return transport.subscribe((e) => {
      if (e.kind === 'job.finished' && e.workspaceId === workspaceId) {
        setStarted(false)
        void load()
      }
    })
  }, [transport, workspaceId, load])

  const entries = golden ?? []

  // A key that left the set leaves the selection with it.
  useEffect(() => {
    if (golden === null) return
    setSelected((prev) => {
      const keep = new Set([...prev].filter((k) => golden.some((g) => g.key === k)))
      return keep.size === prev.size ? prev : keep
    })
  }, [golden])

  const toggle = (key: string) => {
    setSelected((prev) => {
      const next = new Set(prev)
      if (next.has(key)) next.delete(key)
      else next.add(key)
      return next
    })
  }

  const running = (jobs?.length ?? 0) > 0 || started
  const effectiveProvider = provider || defaultProvider || ''
  const effectiveModel = model.trim() || (provider ? '' : defaultModel || '')

  /*
   * A suite is a retro when every picked key can be replayed as one. A plain
   * key in the selection makes the whole suite a plain replay, which every
   * key can do, rather than a retro half the keys would fail.
   */
  const picked = useMemo(() => entries.filter((g) => selected.has(g.key)), [entries, selected])
  const isRetro = picked.length > 0 && picked.every((g) => Boolean(g.hasRetro))

  const quotaFor = useMemo(() => {
    if (!quota || quota.length === 0) return undefined
    return quota.find((q) => q.provider === effectiveProvider && q.fiveHour) ?? quota.find((q) => q.fiveHour)
  }, [quota, effectiveProvider])

  async function start(): Promise<void> {
    if (selected.size === 0 || running || pending) return
    setPending(true)
    setError('')
    try {
      const opts: EvalStart = {
        provider: provider || undefined,
        model: model.trim() || undefined,
        retro: isRetro,
      }
      if (isRetro) {
        opts.rubric = rubric
        opts.withRca = withRca
      }
      await onStartEval([...selected], opts)
      setStarted(true)
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setPending(false)
    }
  }

  async function cancel(jobId: string): Promise<void> {
    if (!onCancelJob) return
    setError('')
    try {
      await onCancelJob(jobId)
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    }
  }

  async function addGolden(event: FormEvent): Promise<void> {
    event.preventDefault()
    const key = goldenKey.trim()
    if (!key) {
      setGoldenError('Enter a ticket key.')
      return
    }
    setGoldenPending(true)
    setGoldenError('')
    try {
      await transport.addGolden(workspaceId, { key })
      setGoldenKey('')
      setOpen(null)
      await load()
    } catch (err) {
      setGoldenError(err instanceof Error ? err.message : String(err))
    } finally {
      setGoldenPending(false)
    }
  }

  function openProvider(): void {
    setDraftProvider(provider)
    setDraftModel(model)
    setOpen('provider')
  }

  function saveProvider(): void {
    setProvider(draftProvider)
    setModel(draftModel)
    setOpen(null)
  }

  const latest = useMemo(() => (reports === null ? null : latestOf(reports, retro)), [reports, retro])

  const disabledReason = running
    ? 'A suite is already running'
    : selected.size === 0
      ? 'Select at least one key'
      : entries.length === 0
        ? 'The golden set is empty'
        : ''

  /*
   * Run suite is drawn in the page head, and published so the sidebar's New
   * session steps down while this screen is up: one filled button, and it
   * is the one that spends the provider.
   */
  useProvidePrimaryAction({
    label: 'Run suite',
    onRun: () => void start(),
    disabled: Boolean(disabledReason) || pending,
    busy: pending,
    title: disabledReason || 'Replay the picked keys through real runs',
    placement: 'screen',
  })

  const json = latest ? JSON.stringify(latest.report, null, 2) : ''

  return (
    <div className="eval">
      <div className="eval-head">
        <PageHead
          title="Eval"
          lede="Run the golden set again and compare with what a person did."
          actions={
            <div className="eval-actions">
              <div className="eval-actions__row">
                {jobs && onCancelJob
                  ? jobs.map((job) => (
                      <Button
                        key={job.jobId}
                        variant="ghost"
                        onClick={() => void cancel(job.jobId)}
                        title="Stop the suite this window started"
                      >
                        Cancel suite
                      </Button>
                    ))
                  : null}
                <Button icon={<PlusIcon />} onClick={() => setOpen('golden')}>
                  Add golden
                </Button>
                <Button
                  variant="pale"
                  disabled={!latest}
                  title={latest ? latest.report.path : 'No report yet'}
                  onClick={() => setOpen('json')}
                >
                  Open JSON
                </Button>
                <Button
                  variant="primary"
                  disabled={Boolean(disabledReason)}
                  busy={pending}
                  title={disabledReason || 'Replay the picked keys through real runs'}
                  onClick={() => void start()}
                >
                  Run suite
                </Button>
              </div>
              <p className="eval-estimate" aria-live="polite">
                {estimate(selected.size, quotaFor, running)}
              </p>
            </div>
          }
        />
        {error ? <p className="eval-error">{error}</p> : null}
      </div>

      <div className="eval-body">
        <div className="eval-col-l">
          <SettingCard heading="Golden set">
            <div className="eval-golden">
              {golden === null ? (
                <p className="eval-golden__note">Loading the golden set…</p>
              ) : entries.length === 0 ? (
                <p className="eval-golden__note">
                  No golden bundles yet. Add golden copies a completed triage run's bundle here, or
                  run <code>sirdar golden add KEY</code>.
                </p>
              ) : (
                entries.map((entry) => (
                  <label key={entry.key} className="eval-grow">
                    <span className="eval-cb">
                      <input
                        type="checkbox"
                        className="eval-cb__input"
                        aria-label={entry.key}
                        checked={selected.has(entry.key)}
                        disabled={running}
                        onChange={() => toggle(entry.key)}
                      />
                      <CheckIcon />
                    </span>
                    <span className="eval-grow__key">{entry.key}</span>
                    <span className="eval-grow__title">
                      {entry.assertions} {entry.assertions === 1 ? 'assertion' : 'assertions'} ·{' '}
                      {entry.hasExpectedNote ? 'expected.md' : 'no expected.md'}
                    </span>
                    <Badge title={entry.hasRetro ? 'Replays against the merged fix' : 'Scores assertions and the note'}>
                      {entry.hasRetro ? 'retro' : 'plain'}
                    </Badge>
                  </label>
                ))
              )}
            </div>
          </SettingCard>

          <SettingCard heading="Options">
            <SettingRow
              label="Score with a rubric"
              help="The provider grades root cause and files against the human fix"
              control={
                <Toggle
                  label="Score with a rubric"
                  checked={rubric}
                  disabled={running || (picked.length > 0 && !isRetro)}
                  onChange={setRubric}
                />
              }
            />
            <SettingRow
              label="Include the RCA step"
              help="Off runs triage and fix only; the note is not scored"
              control={
                <Toggle
                  label="Include the RCA step"
                  checked={withRca}
                  disabled={running || (picked.length > 0 && !isRetro)}
                  onChange={setWithRca}
                />
              }
            />
            <SettingRow
              label="Provider"
              value={
                effectiveProvider ? (
                  <span className="eval-provider">
                    <ProviderMark provider={effectiveProvider} size="sm" />
                    <span className="eval-provider__pair">
                      {effectiveProvider}
                      {effectiveModel ? ` · ${effectiveModel}` : ''}
                    </span>
                  </span>
                ) : (
                  'Workspace default'
                )
              }
              control={
                <Button variant="pale" disabled={running} onClick={openProvider}>
                  Change
                </Button>
              }
            />
          </SettingCard>
        </div>

        <div className="eval-col-r">
          <div className="eval-report__head">
            <h2 className="eval-report__title">
              Last report
              {latest ? <span className="eval-report__meta">{reportMeta(latest)}</span> : null}
            </h2>
          </div>
          {reports === null ? (
            <p className="eval-empty">Loading the last report…</p>
          ) : !latest ? (
            <p className="eval-empty">
              No report yet. Run suite writes one to <code>.sirdar/eval</code>, and so does{' '}
              <code>sirdar eval</code>.
            </p>
          ) : latest.kind === 'retro' ? (
            <>
              <DataTable
                caption="Each key against the change a human merged"
                columns={RETRO_COLUMNS}
                rows={latest.report.results}
                rowKey={(r) => r.key}
                detail={retroDetail}
                empty="This report scored no keys."
              />
              <div className="eval-read">
                <div className="eval-read__label">Reading the report</div>
                <p>
                  File overlap counts the files touched by both diffs, so 1/3 means the agent
                  changed one of the three files the person changed.
                </p>
                <p>
                  A matched root cause with low overlap usually means the agent fixed it somewhere
                  else, which is worth a look before you call it wrong.
                </p>
              </div>
            </>
          ) : (
            <>
              <DataTable
                caption="Score per key"
                columns={PLAIN_COLUMNS}
                rows={latest.report.results}
                rowKey={(r) => r.key}
                detail={plainDetail}
                empty="This report scored no keys."
              />
              <div className="eval-read">
                <div className="eval-read__label">Reading the report</div>
                <p>
                  Assertions are the checks in expected.json; a row that failed one names it
                  underneath.
                </p>
                <p>
                  Refs and headings count what the human's note named that the produced note also
                  named, so 4/5 means the agent pointed at four of the five.
                </p>
              </div>
            </>
          )}
        </div>
      </div>

      <Dialog
        open={open === 'golden'}
        title="Add golden"
        onClose={() => {
          setOpen(null)
          setGoldenError('')
        }}
        actions={
          <>
            <Button onClick={() => setOpen(null)}>Cancel</Button>
            <Button variant="primary" form="eval-add-golden" type="submit" busy={goldenPending}>
              Add
            </Button>
          </>
        }
      >
        <form id="eval-add-golden" onSubmit={(e) => void addGolden(e)}>
          <div className="eval-dialog__field">
            <label className="eval-dialog__label" htmlFor="eval-golden-key">
              Ticket key
            </label>
            <input
              id="eval-golden-key"
              className="eval-dialog__input"
              value={goldenKey}
              disabled={goldenPending}
              autoComplete="off"
              onChange={(e) => setGoldenKey(e.target.value)}
            />
            <p className="eval-dialog__help">
              The key's newest completed triage run is copied into the golden set with an
              expected.json skeleton beside it, as <code>sirdar golden add</code> does.
            </p>
            {goldenError ? <p className="eval-error">{goldenError}</p> : null}
          </div>
        </form>
      </Dialog>

      <Dialog
        open={open === 'json'}
        title="Report JSON"
        onClose={() => setOpen(null)}
        actions={
          <>
            <Button onClick={() => void navigator.clipboard?.writeText(json)}>Copy</Button>
            <Button variant="pale" onClick={() => setOpen(null)}>
              Close
            </Button>
          </>
        }
      >
        {latest ? <p className="eval-dialog__path">{latest.report.path}</p> : null}
        <pre className="eval-dialog__json" tabIndex={0}>
          {json}
        </pre>
      </Dialog>

      <Dialog
        open={open === 'provider'}
        title="Provider"
        onClose={() => setOpen(null)}
        actions={
          <>
            <Button onClick={() => setOpen(null)}>Cancel</Button>
            <Button variant="primary" onClick={saveProvider}>
              Save
            </Button>
          </>
        }
      >
        <ProviderFields
          idPrefix="eval"
          provider={draftProvider}
          model={draftModel}
          defaultProvider={defaultProvider}
          onProvider={setDraftProvider}
          onModel={setDraftModel}
        />
      </Dialog>
    </div>
  )
}
