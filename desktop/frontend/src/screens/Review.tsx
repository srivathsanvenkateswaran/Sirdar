import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import type { RunDiff, Ticket, Transport } from '../api/types'
import { elapsed } from '../lib/events'
import { costOrUnknown, reasonOf } from '../lib/format'
import { changeTotals, checksFromEvents, fixReport, noteLabel, OUTCOME_WORDS, pushCommand } from '../lib/review'
import { LIVE, useRunFeed } from '../components/run/useRunFeed'
import Button from '../ui/button'
import DiffView, { hunkKey, parsePatch, type DiffMode, type HunkDecision } from '../ui/diff-view'
import KindChip from '../ui/kind-chip'
import ProviderMark, { providerName } from '../ui/provider-mark'
import SegmentedControl from '../ui/segmented-control'
import StatusBadge, { type SdStatus } from '../ui/status-badge'
import './review.css'

/** How long Copy reads "Copied" before it says its name again. */
const COPIED_MS = 1500

const MODES = [
  { id: 'unified', label: 'Unified' },
  { id: 'split', label: 'Split' },
]

/** The word the rail prints beside a file: its status, or `reviewed` once every hunk of it is kept. */
function fileWord(status: RunDiff['files'][number]['status'], reviewed: boolean): string {
  if (reviewed) return 'reviewed'
  return status === 'added' ? 'new' : status
}

/**
 * A hunk's decision survives a drop only if it still names the same hunk:
 * the hunks after the dropped one move up by one, and the dropped one is
 * gone.
 */
function rekeyAfterDrop(
  decisions: Record<string, HunkDecision>,
  path: string,
  dropped: number,
): Record<string, HunkDecision> {
  const next: Record<string, HunkDecision> = {}
  for (const [key, decision] of Object.entries(decisions)) {
    const at = key.lastIndexOf('\n')
    const keyPath = key.slice(0, at)
    const index = Number(key.slice(at + 1))
    if (keyPath !== path) {
      next[key] = decision
    } else if (index < dropped) {
      next[key] = decision
    } else if (index > dropped) {
      next[hunkKey(path, index - 1)] = decision
    }
  }
  return next
}

/** The note a fix is filed against: the resolution note when there is one, the last note otherwise. */
function notePathOf(notes: string[] | undefined): string {
  if (!notes || notes.length === 0) return ''
  return notes.find((p) => noteLabel(p) === 'Resolution note') ?? notes[notes.length - 1]
}

/**
 * The Change review, at `#/runs/<workspace>/<run>/review`.
 *
 * The run's change full width: the file rail with the checks the run ran, the
 * diff with Keep and Drop per hunk, and what the agent said beside it. The
 * footer names the worktree and the base, and — since no API pushes a fix
 * branch yet — the branch and the one command that pushes it, with Copy.
 *
 * Everything comes through the transport: the run for its facts, its events
 * for the report and the checks, and `runDiff` for the change. A drop goes to
 * `dropHunk` with the etag the diff was read under and the screen shows the
 * change as the service answers it afterwards.
 */
export default function Review({
  transport,
  workspaceId,
  runId,
  tickets,
  onBack,
  onOpenNote,
}: {
  transport: Transport
  workspaceId: string
  runId: string
  /** The board's tickets, for the title of the one this run is for. */
  tickets?: Ticket[]
  /** Back to the session the change belongs to. */
  onBack: () => void
  /** Opens the session's note. */
  onOpenNote: () => void
}): JSX.Element {
  const { detail, events, loadError, finished } = useRunFeed(transport, workspaceId, runId)
  const [diff, setDiff] = useState<RunDiff | null>(null)
  const [diffError, setDiffError] = useState('')
  const [diffLoading, setDiffLoading] = useState(true)
  const [mode, setMode] = useState<DiffMode>('unified')
  const [activePath, setActivePath] = useState('')
  const [decisions, setDecisions] = useState<Record<string, HunkDecision>>({})
  const [dropping, setDropping] = useState<string | undefined>(undefined)
  const [dropError, setDropError] = useState('')
  const [copied, setCopied] = useState(false)
  const [copyError, setCopyError] = useState('')
  const [now, setNow] = useState(() => Date.now())
  const copyTimer = useRef<ReturnType<typeof setTimeout> | null>(null)

  const status = detail?.status ?? ''
  const live = LIVE.has(status)

  const loadDiff = useCallback(() => {
    let cancelled = false
    setDiffLoading(true)
    transport
      .runDiff(workspaceId, runId)
      .then((d) => {
        if (cancelled) return
        setDiff(d)
        setDiffError('')
        // The hunks are numbered afresh in what came back, so a decision
        // made against the old numbering would name the wrong hunk.
        setDecisions({})
        setActivePath((path) => (path && d.files.some((f) => f.path === path) ? path : d.files[0]?.path ?? ''))
      })
      .catch((err: unknown) => {
        if (cancelled) return
        setDiff(null)
        setDiffError(reasonOf(err))
      })
      .finally(() => {
        if (!cancelled) setDiffLoading(false)
      })
    return () => {
      cancelled = true
    }
  }, [transport, workspaceId, runId])

  useEffect(() => {
    setDropError('')
    setDiffError('')
    return loadDiff()
  }, [loadDiff])

  // A run opened while it was still working: the moment it ends, the change
  // and the fix facts exist, so ask for the change again (the feed re-reads
  // the run).
  useEffect(() => {
    if (finished === 0) return
    return loadDiff()
  }, [finished, loadDiff])

  useEffect(() => {
    if (!live) return
    const id = setInterval(() => setNow(Date.now()), 1000)
    return () => clearInterval(id)
  }, [live])

  useEffect(
    () => () => {
      if (copyTimer.current) clearTimeout(copyTimer.current)
      copyTimer.current = null
    },
    [],
  )

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onBack()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [onBack])

  const runEvents = useMemo(() => events.map((e) => e.event), [events])
  const report = useMemo(() => fixReport(runEvents), [runEvents])
  const checks = useMemo(() => checksFromEvents(runEvents), [runEvents])

  const editable = Boolean(diff && diff.worktreePresent && !diff.pushed)

  /** How many hunks the patch has per file, so the rail can say when every one is kept. */
  const hunkCounts = useMemo(() => {
    const counts: Record<string, number> = {}
    for (const file of parsePatch(diff?.patch ?? '')) counts[file.path] = file.hunks.length
    return counts
  }, [diff?.patch])

  const keep = useCallback((path: string, hunk: number) => {
    setDecisions((prev) => ({ ...prev, [hunkKey(path, hunk)]: 'kept' }))
  }, [])

  const drop = useCallback(
    async (path: string, hunk: number) => {
      if (!diff) return
      const key = hunkKey(path, hunk)
      setDropping(key)
      setDropError('')
      try {
        const next = await transport.dropHunk(workspaceId, runId, { path, hunk, etag: diff.etag })
        setDiff(next)
        setDecisions((prev) => rekeyAfterDrop(prev, path, hunk))
        setActivePath((current) =>
          current && next.files.some((f) => f.path === current) ? current : next.files[0]?.path ?? '',
        )
      } catch (err: unknown) {
        const message = reasonOf(err)
        setDropError(message)
        // A stale etag means the change moved under this screen; read it
        // again so the next drop names the hunk that is really there.
        if (/conflict/i.test(message)) loadDiff()
      } finally {
        setDropping(undefined)
      }
    },
    [diff, transport, workspaceId, runId, loadDiff],
  )

  const cli = diff ? pushCommand(diff.worktree, diff.branch) : ''

  const copy = useCallback(async () => {
    if (!cli) return
    setCopyError('')
    try {
      await navigator.clipboard?.writeText(cli)
      setCopied(true)
      if (copyTimer.current) clearTimeout(copyTimer.current)
      copyTimer.current = setTimeout(() => setCopied(false), COPIED_MS)
    } catch {
      setCopyError('Could not reach the clipboard. Select the line and copy it.')
    }
  }, [cli])

  if (loadError || !detail) {
    return (
      <div className="review">
        <header className="review-top">
          <Button variant="ghost" onClick={onBack}>
            Back to session
          </Button>
        </header>
        <p className={loadError ? 'review-failed' : 'review-loading'} role={loadError ? 'alert' : undefined}>
          {loadError || 'Loading run…'}
        </p>
      </div>
    )
  }

  const title = tickets?.find((t) => t.key === detail.key)?.title ?? ''
  const totals = changeTotals(diff?.files ?? [])
  const notDone = detail.fix?.deviation || report?.deviationFromNote || ''
  const notePath = notePathOf(detail.notes)
  const summary = report?.summary ?? ''
  const paragraphs = summary
    .split(/\n\s*\n/)
    .map((p) => p.trim())
    .filter(Boolean)
  const worktree = diff?.worktree ?? ''
  const base = diff?.base ?? detail.fix?.base ?? ''
  const branch = diff?.branch ?? detail.fix?.branch ?? ''
  const pushed = diff ? diff.pushed : Boolean(detail.fix?.pushed)
  const keptAll = (path: string): boolean => {
    const hunks = hunkCounts[path] ?? 0
    if (hunks === 0) return false
    for (let i = 0; i < hunks; i += 1) if (decisions[hunkKey(path, i)] !== 'kept') return false
    return true
  }

  return (
    <div className="review">
      <header className="review-top">
        <h2 className="review-key">{detail.key}</h2>
        <KindChip kind={detail.kind} />
        <StatusBadge status={detail.status as SdStatus} />
        <span className="review-title" title={title || undefined}>
          {title}
        </span>
        <div className="review-stats">
          <span className="review-stat review-provider">
            <ProviderMark provider={detail.provider} size="sm" />
            <span className="review-provider__name">{providerName(detail.provider)}</span>
            <span className="review-provider__model">{detail.model}</span>
          </span>
          <span className="review-stat">
            <b>{elapsed(detail, now)}</b>
          </span>
          <span className="review-stat">
            <b>{detail.usage?.turns ?? 0}</b> {detail.usage?.turns === 1 ? 'turn' : 'turns'}
          </span>
          <span className="review-stat">
            <b>{costOrUnknown(detail.usage?.costUsd, live)}</b>
          </span>
        </div>
        <Button variant="ghost" onClick={onBack}>
          Back to session
        </Button>
      </header>

      <div className="review-body">
        <nav className="review-rail" aria-label="Files">
          <div className="review-railhead">
            {diff ? (
              <>
                {totals.files} {totals.files === 1 ? 'file' : 'files'} ·{' '}
                <span className="review-mono">
                  <span className="review-add">+{totals.additions}</span>{' '}
                  <span className="review-del">−{totals.deletions}</span>
                </span>
              </>
            ) : diffLoading ? (
              'Loading change…'
            ) : (
              'No files'
            )}
          </div>
          {diff?.files.map((file) => (
            <button
              key={file.path}
              type="button"
              className="review-file"
              aria-current={file.path === activePath ? 'true' : undefined}
              onClick={() => setActivePath(file.path)}
            >
              <span className="review-file__path">{file.path}</span>
              <span className="review-add">+{file.additions}</span>
              <span className="review-del">−{file.deletions}</span>
              <span className="review-file__word">{fileWord(file.status, keptAll(file.path))}</span>
            </button>
          ))}
          <h3 className="review-railsec">Checks</h3>
          {checks.length === 0 ? (
            <p className="review-checks review-checks--empty">
              {live ? 'None yet.' : 'The run recorded no checks.'}
            </p>
          ) : (
            <ul className="review-checks" aria-label="Checks">
              {checks.map((check, i) => (
                <li key={`${check.command}-${i}`} title={check.result || undefined}>
                  <span className="review-check__word" data-outcome={check.outcome}>
                    {OUTCOME_WORDS[check.outcome]}
                  </span>{' '}
                  {check.command}
                </li>
              ))}
            </ul>
          )}
        </nav>

        <section className="review-diffcol" aria-label="Change">
          <div className="review-dtool">
            <SegmentedControl
              options={MODES}
              value={mode}
              onChange={(id) => setMode(id as DiffMode)}
              label="Diff layout"
            />
            <span className="review-mono review-dtool__path" dir="ltr">
              {activePath}
            </span>
          </div>
          {diff && !editable ? (
            <p className="review-readonly" role="status">
              {diff.pushed
                ? 'The branch is pushed, so the change is read-only here.'
                : 'The worktree is gone, so the change is read-only here.'}
            </p>
          ) : null}
          {dropError ? (
            <p className="review-droperror" role="alert">
              {dropError}
            </p>
          ) : null}
          <div className="review-diff">
            {diff ? (
              <DiffView
                patch={diff.patch}
                files={diff.files}
                mode={mode}
                editable={editable}
                decisions={decisions}
                dropping={dropping}
                onKeep={keep}
                onDrop={(path, hunk) => void drop(path, hunk)}
                activePath={activePath}
                truncated={diff.truncated}
              />
            ) : diffLoading ? (
              <p className="review-nodiff">Loading change…</p>
            ) : (
              <div className="review-nodiff" role="status">
                <p className="review-nodiff__title">No change to review</p>
                <p className="review-nodiff__why">{diffError}</p>
              </div>
            )}
          </div>
        </section>

        <aside className="review-pane" aria-label="What the agent said">
          <h3 className="review-pane__title">What the agent said</h3>
          {paragraphs.length > 0 ? (
            paragraphs.map((p, i) => (
              <p key={i} dir="auto">
                {p}
              </p>
            ))
          ) : (
            <p className="review-pane__empty">
              {live
                ? 'The run is still working; its summary arrives when it ends.'
                : detail.reason || 'The run recorded no summary.'}
            </p>
          )}
          {notDone ? (
            <p className="review-notdone" dir="auto">
              <b>Not done:</b> {notDone}
            </p>
          ) : null}
          <div className="review-note">
            <div className="review-note__row">
              <span className="review-note__label">{notePath ? noteLabel(notePath) : 'Note'}</span>
              <Button variant="pale" size="sm" onClick={onOpenNote} disabled={!notePath}>
                Open
              </Button>
            </div>
            <div className="review-mono review-note__path" dir="ltr">
              {notePath || 'No note filed yet.'}
            </div>
          </div>
        </aside>
      </div>

      <footer className="review-foot">
        <div className="review-stats">
          {worktree ? (
            <span className="review-stat">
              worktree <b dir="ltr">{worktree}</b>
            </span>
          ) : null}
          {base ? (
            <span className="review-stat">
              base <b dir="ltr">{base}</b>
            </span>
          ) : null}
        </div>
        {branch ? (
          <div className="review-push">
            {pushed ? (
              <span className="review-push__done">
                <span className="review-mono" dir="ltr">
                  {branch}
                </span>{' '}
                is on origin
                {detail.fix?.prUrl ? (
                  <>
                    {' · '}
                    <a href={detail.fix.prUrl} target="_blank" rel="noreferrer noopener">
                      pull request
                    </a>
                  </>
                ) : null}
              </span>
            ) : (
              <>
                <span className="review-push__branch">
                  Branch{' '}
                  <span className="review-mono" dir="ltr">
                    {branch}
                  </span>
                </span>
                {cli ? (
                  <>
                    <code className="review-cli" dir="ltr">
                      {cli}
                    </code>
                    <Button size="sm" onClick={() => void copy()} title="Copy the push command">
                      {copied ? 'Copied' : 'Copy'}
                    </Button>
                  </>
                ) : null}
                {copyError ? (
                  <span className="review-droperror" role="alert">
                    {copyError}
                  </span>
                ) : null}
              </>
            )}
          </div>
        ) : null}
      </footer>
    </div>
  )
}
