import { useCallback, useEffect, useMemo, useRef, useState, type FormEvent } from 'react'
import type { ShadowReport, ShadowRow, ShadowStatus, Transport } from '../api/types'
import { useProvidePrimaryAction } from '../components/shell/primaryAction'
import { reasonOf, usd } from '../lib/format'
import { useNow } from '../lib/useNow'
import Badge from '../ui/badge'
import Button from '../ui/button'
import DataTable, { type DataColumn } from '../ui/data-table'
import Drawer from '../ui/drawer'
import PageHead from '../ui/page-head'
import StatCard from '../ui/stat-card'
import './shadow.css'

/**
 * How long the screen waits after a shadow event before it reads the
 * scorecard again. A run's progress arrives as a run.updated per state
 * change, and each one would otherwise be a read of the whole ledger.
 */
export const SHADOW_RELOAD_MS = 400

const MONTHS = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec']

/**
 * "10:45" for a time later today, "7 Oct 09:05" for one on another day, in
 * the reader's own clock. An unreadable stamp is shown as written.
 */
function clock(iso: string, now: number): string {
  const ms = Date.parse(iso)
  if (Number.isNaN(ms)) return iso
  const d = new Date(ms)
  const hhmm = `${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}`
  return d.toDateString() === new Date(now).toDateString()
    ? hhmm
    : `${d.getDate()} ${MONTHS[d.getMonth()]} ${hhmm}`
}

/** A cap with no cents reads as "$10", the way it was written in the config. */
function cap(n: number): string {
  return Number.isInteger(n) ? `$${n}` : usd(n)
}

/** Today's use of the two daily caps; a cap of 0 is no cap, and says only the use. */
function todayLine(t: ShadowStatus['today']): string {
  const runs = t.maxPerDay > 0 ? `${t.runs} of ${t.maxPerDay} today` : `${t.runs} today`
  const spend = t.maxUSDPerDay > 0 ? `${usd(t.usd)} of ${cap(t.maxUSDPerDay)}` : usd(t.usd)
  return `${runs} · ${spend}`
}

/**
 * The page head's one line about the watcher: what it is doing, when it
 * next acts, and how much of today's caps it has used. A watcher another
 * process holds, or one that stopped, says why instead, since the caps are
 * not what is holding it.
 */
export function statusLine(status: ShadowStatus | undefined, now: number): string {
  if (!status) return ''
  switch (status.state) {
    case 'watching': {
      const next = status.nextPoll ? `Watching · next poll ${clock(status.nextPoll, now)}` : 'Watching'
      return `${next} · ${todayLine(status.today)}`
    }
    case 'paused': {
      const until = status.pausedUntil ? `Paused until ${clock(status.pausedUntil, now)}` : 'Paused'
      return `${until} · ${todayLine(status.today)}`
    }
    case 'gated':
      return `Gated: ${status.reason} · ${todayLine(status.today)}`
    case 'locked-elsewhere':
      return `Watched by another process · ${status.reason}`
    case 'stopped':
      return `Not watching · ${status.reason}`
    default:
      return 'Off'
  }
}

/** A run's length as the Median time card reads it. */
function elapsed(sec: number): string {
  if (sec < 60) return `${Math.round(sec)} s`
  if (sec < 3600) return `${Math.round(sec / 60)} min`
  return `${(sec / 3600).toFixed(1)} h`
}

function pct(fraction: number): string {
  return `${Math.round(fraction * 100)}%`
}

/** "2 of 3 PR files named · 1 of 2 code refs in the PR", leaving out a measure the row lacks. */
function overlapLine(row: ShadowRow): string {
  const parts: string[] = []
  if (row.prFilesHit) parts.push(`${row.prFilesHit.matched} of ${row.prFilesHit.total} PR files named`)
  if (row.codeRefsPathOverlap) {
    parts.push(`${row.codeRefsPathOverlap.matched} of ${row.codeRefsPathOverlap.total} code refs in the PR`)
  }
  return parts.join(' · ')
}

/** What shadow said: the classification and the hypothesis's first sentence. */
function saidLine(row: ShadowRow): string {
  const said = [row.classification, row.hypothesis].filter(Boolean).join(' · ')
  return said || row.status
}

/** The grade as a badge with its reason on hover, and the row's tags after it. */
function GradeCell({ row }: { row: ShadowRow }): JSX.Element {
  return (
    <span className="shadow-grade">
      <Badge title={row.gradeReason || undefined}>{row.grade || 'Pending'}</Badge>
      {row.tags.map((tag) => (
        <span key={tag} className="shadow-tag">
          {tag}
        </span>
      ))}
    </span>
  )
}

/**
 * One row's drawer: the grade, what the operator's side came to, how far
 * shadow's verdict overlapped it, what the read-only guard refused, what
 * shadow would have asked, and a form for recording an outcome by hand.
 * The form is always there: an outcome entered here is merged with what
 * the tracker gave, so it can add a PR the collector missed.
 */
function RowDrawer(props: {
  row: ShadowRow
  onClose: () => void
  onSave: (prUrls: string[], resolution: string) => Promise<void>
}): JSX.Element {
  const { row, onClose, onSave } = props
  const [prs, setPrs] = useState('')
  const [resolution, setResolution] = useState('')
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')
  const overlap = overlapLine(row)

  async function submit(e: FormEvent): Promise<void> {
    e.preventDefault()
    const urls = prs
      .split('\n')
      .map((line) => line.trim())
      .filter(Boolean)
    const said = resolution.trim()
    // An outcome with neither would be graded against nothing; the service
    // refuses it too, but the reader should not wait for a round trip to
    // learn the form was empty.
    if (urls.length === 0 && !said) {
      setError('Add a pull request URL or a resolution before saving.')
      return
    }
    setSaving(true)
    setError('')
    try {
      await onSave(urls, said)
      setPrs('')
      setResolution('')
    } catch (err) {
      setError(reasonOf(err))
    } finally {
      setSaving(false)
    }
  }

  return (
    <Drawer open title={row.key} meta={usd(row.costUSD)} onClose={onClose} width={480}>
      <div className="shadow-drawer">
        <section className="shadow-drawer__section">
          <h3 className="shadow-drawer__head">Grade</h3>
          <p className="shadow-drawer__line">
            <GradeCell row={row} />
          </p>
          {row.gradeReason ? (
            <p className="shadow-drawer__text" dir="auto">
              {row.gradeReason}
            </p>
          ) : null}
        </section>

        <section className="shadow-drawer__section">
          <h3 className="shadow-drawer__head">Outcome</h3>
          {row.outcome ? (
            <>
              <p className="shadow-drawer__meta">{row.outcome.sources.join(' · ')}</p>
              {row.outcome.prUrls.length > 0 ? (
                <ul className="shadow-drawer__list">
                  {row.outcome.prUrls.map((url) => (
                    <li key={url}>
                      <a href={url} target="_blank" rel="noreferrer" dir="ltr">
                        {url}
                      </a>
                    </li>
                  ))}
                </ul>
              ) : null}
              {row.outcome.resolution ? (
                <p className="shadow-drawer__text" dir="auto">
                  {row.outcome.resolution}
                </p>
              ) : null}
            </>
          ) : (
            <p className="shadow-drawer__meta">No outcome collected yet.</p>
          )}
        </section>

        {overlap || row.reproduction ? (
          <section className="shadow-drawer__section">
            <h3 className="shadow-drawer__head">Overlap</h3>
            {overlap ? <p className="shadow-drawer__text">{overlap}</p> : null}
            {row.reproduction ? (
              <p className="shadow-drawer__meta">
                Reproduction: <span>{row.reproduction}</span>
              </p>
            ) : null}
          </section>
        ) : null}

        {row.denied.length > 0 ? (
          <section className="shadow-drawer__section">
            <h3 className="shadow-drawer__head">Denied calls</h3>
            <ul className="shadow-drawer__list">
              {row.denied.map((d, i) => (
                <li key={i}>
                  <code dir="ltr">{`${d.tool} ${d.summary}`}</code>
                </li>
              ))}
            </ul>
          </section>
        ) : null}

        {row.questions.length > 0 ? (
          <section className="shadow-drawer__section">
            <h3 className="shadow-drawer__head">Questions</h3>
            <ul className="shadow-drawer__list">
              {row.questions.map((q, i) => (
                <li key={i} dir="auto">
                  {q}
                </li>
              ))}
            </ul>
          </section>
        ) : null}

        <form className="shadow-drawer__section shadow-form" onSubmit={(e) => void submit(e)}>
          <h3 className="shadow-drawer__head">Record the outcome</h3>
          <label className="shadow-form__field">
            Pull requests
            <textarea
              rows={2}
              dir="ltr"
              value={prs}
              placeholder="One URL per line"
              onChange={(e) => setPrs(e.target.value)}
            />
          </label>
          <label className="shadow-form__field">
            Resolution
            <textarea rows={3} dir="auto" value={resolution} onChange={(e) => setResolution(e.target.value)} />
          </label>
          {error ? (
            <p className="shadow-error" role="alert">
              {error}
            </p>
          ) : null}
          <div className="shadow-form__actions">
            <Button type="submit" variant="primary" busy={saving}>
              Save
            </Button>
          </div>
        </form>
      </div>
    </Drawer>
  )
}

/**
 * The shadow scorecard: how often the verdicts shadow reached unattended
 * matched what the operator did, how soon, and at what cost.
 *
 * The headline is four figures; the table under it is one row per key, the
 * key opening the shadow run in the Session screen and the title opening
 * the row's drawer, where the rest of what is known about it sits. Nothing
 * here starts a shadow run: the only things it changes are the watcher's
 * pause and an outcome the operator types in.
 */
export default function Shadow(props: {
  transport: Transport
  workspaceId: string
  /** The watcher's state, from the store. */
  status?: ShadowStatus
  /** The clock the status line is read against; tests hold it still. */
  now?: number
  onOpenRun: (runId: string) => void
}): JSX.Element {
  const { transport, workspaceId, onOpenRun } = props
  const ticking = useNow(30_000, props.now === undefined)
  const now = props.now ?? ticking
  const [report, setReport] = useState<ShadowReport | null>(null)
  const [error, setError] = useState('')
  const [actionError, setActionError] = useState('')
  const [acting, setActing] = useState(false)
  const [openKey, setOpenKey] = useState('')
  /** A row the drawer shows as the save answered it, until the report catches up. */
  const [saved, setSaved] = useState<ShadowRow | null>(null)
  // The answer to Pause or Resume, which can land before the store hears
  // the shadow.status event that says the same.
  const [answered, setAnswered] = useState<ShadowStatus | undefined>(undefined)
  const generation = useRef(0)

  useEffect(() => setAnswered(undefined), [props.status])
  const status = answered ?? props.status

  const load = useCallback(async () => {
    const mine = (generation.current += 1)
    try {
      const got = await transport.shadowReport(workspaceId)
      if (mine !== generation.current) return
      setReport(got)
      setError('')
      // The row as saved stood in until the scorecard had it; from here the
      // scorecard's own is newer, and a later regrade must show through.
      setSaved((prev) => (prev && got.rows.some((r) => r.key === prev.key) ? null : prev))
    } catch (err) {
      if (mine !== generation.current) return
      setError(reasonOf(err))
    }
  }, [transport, workspaceId])

  useEffect(() => {
    setReport(null)
    void load()
    return () => {
      generation.current += 1
    }
  }, [load])

  // While the screen is open, a shadow run moving, a shadow job ending or
  // the watcher's status changing can each change the scorecard. They come
  // in bursts, so they are gathered into one read a moment after the last.
  useEffect(() => {
    let timer: ReturnType<typeof setTimeout> | null = null
    const off = transport.subscribe((e) => {
      const ours =
        (e.kind === 'shadow.status' && e.workspaceId === workspaceId) ||
        (e.kind === 'run.updated' && e.workspaceId === workspaceId && e.run.shadow === true) ||
        (e.kind === 'job.finished' && e.workspaceId === workspaceId && e.shadowJob === true)
      if (!ours) return
      if (timer) clearTimeout(timer)
      timer = setTimeout(() => {
        timer = null
        void load()
      }, SHADOW_RELOAD_MS)
    })
    return () => {
      if (timer) clearTimeout(timer)
      off()
    }
  }, [transport, workspaceId, load])

  const act = useCallback(
    async (call: () => Promise<ShadowStatus>) => {
      setActing(true)
      setActionError('')
      try {
        setAnswered(await call())
      } catch (err) {
        setActionError(reasonOf(err))
      } finally {
        setActing(false)
        void load()
      }
    },
    [load],
  )

  const state = status?.state
  const action =
    state === 'watching' || state === 'gated'
      ? { label: 'Pause', run: () => act(() => transport.shadowPause(workspaceId)) }
      : state === 'paused'
        ? { label: 'Resume', run: () => act(() => transport.shadowResume(workspaceId)) }
        : null

  // The drawer's Save is the filled button while it is open, so the head's
  // steps down then and the window still has only one.
  const drawerOpen = openKey !== ''
  useProvidePrimaryAction(
    action ? { label: action.label, onRun: action.run, busy: acting, placement: 'screen' } : null,
  )

  // Held still: the drawer moves focus to its close button whenever this
  // changes, which a new function on every render would do mid-sentence.
  const closeDrawer = useCallback(() => setOpenKey(''), [])
  const rows = report?.rows ?? []
  const open = saved?.key === openKey ? saved : rows.find((r) => r.key === openKey)

  async function saveOutcome(prUrls: string[], resolution: string): Promise<void> {
    const row = await transport.shadowOutcome(workspaceId, { key: openKey, prUrls, resolution })
    setSaved(row)
    void load()
  }

  const columns = useMemo<DataColumn<ShadowRow>[]>(
    () => [
      {
        id: 'key',
        header: 'Key',
        cell: (r) => (
          <button type="button" className="shadow-key" title="Open this shadow run" onClick={() => onOpenRun(r.runId)}>
            <bdi dir="ltr">{r.key}</bdi>
          </button>
        ),
      },
      {
        id: 'title',
        header: 'Title',
        cell: (r) => (
          <button
            type="button"
            className="shadow-title"
            dir="auto"
            title="Outcome, overlap and the outcome form"
            onClick={() => {
              setSaved(null)
              setOpenKey(r.key)
            }}
          >
            {r.title || r.key}
          </button>
        ),
      },
      {
        id: 'said',
        header: 'Shadow said',
        cell: (r) => (
          <span className="shadow-said" dir="auto">
            {saidLine(r)}
          </span>
        ),
      },
      { id: 'grade', header: 'Grade', cell: (r) => <GradeCell row={r} /> },
      { id: 'cost', header: 'Cost', cell: (r) => usd(r.costUSD), numeric: true },
    ],
    [onOpenRun],
  )

  const h = report?.headline
  const line = statusLine(status, now)

  return (
    <div className="shadow">
      <div className="shadow-head">
        <PageHead
          title="Shadow"
          lede={line || undefined}
          actions={
            action ? (
              <Button variant={drawerOpen ? 'secondary' : 'primary'} busy={acting} onClick={action.run}>
                {action.label}
              </Button>
            ) : undefined
          }
        />
        {actionError ? (
          <p className="shadow-error" role="alert">
            {actionError}
          </p>
        ) : null}
      </div>

      {error ? (
        <p className="shadow-error" role="alert">
          {error}
        </p>
      ) : null}
      {!report && !error ? <p className="shadow-loading">Loading the scorecard…</p> : null}

      {h ? (
        <>
          <div className="shadow-strip">
            <StatCard size="compact" label="Graded" value={String(h.graded)} detail={`of ${h.started} started`} />
            <StatCard
              size="compact"
              label="Confirmed"
              value={h.graded > 0 ? pct(h.confirmed) : '—'}
              detail={h.graded > 0 ? `${pct(h.confirmedOrPartial)} confirmed or partial` : 'Nothing graded yet'}
            />
            <StatCard
              size="compact"
              label="Median time"
              value={h.medianElapsedSec > 0 ? elapsed(h.medianElapsedSec) : '—'}
              detail={
                h.readyOf > 0
                  ? `Ready before you started: ${h.readyBefore} of ${h.readyOf}`
                  : 'No lead time measured yet'
              }
            />
            <StatCard
              size="compact"
              label="Cost"
              value={usd(h.costUSD)}
              valueTitle={`$${h.costUSD.toFixed(4)}`}
              detail={`${usd(h.medianCostUSD)} median per ticket`}
            />
          </div>

          <div className="shadow-table">
            <DataTable
              caption="Shadow runs"
              columns={columns}
              rows={rows}
              rowKey={(r) => r.key}
              empty="No shadow runs yet. New tickets assigned to you are triaged here once shadow mode is on."
            />
          </div>
        </>
      ) : null}

      {open ? (
        <RowDrawer
          key={open.key}
          row={open}
          onClose={closeDrawer}
          onSave={saveOutcome}
        />
      ) : null}
    </div>
  )
}
