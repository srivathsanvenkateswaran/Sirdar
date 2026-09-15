import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import type { FixInfo, RunDiff, Transport } from '../../api/types'
import type { Check } from '../../lib/checks'
import { parsePatch } from '../../lib/diff'
import Button from '../../ui/button'
import DiffView, { hunkKey } from '../../ui/diff-view'
import FixPanel from './FixPanel'

/** Strips the HTTP code the transport prefixes, so the reader gets the reason alone. */
export function reasonOf(err: unknown): string {
  const text = err instanceof Error ? err.message : String(err)
  return text.replace(/^(?:conflict|not_found|no_diff|forbidden|internal|unsupported):\s*/, '')
}

/**
 * The fix run's change: the diff with Keep and Drop, the checks the agent
 * ran, and the branch the commit is on.
 *
 * Drop hands the hunk and the diff's etag to the service, which reverts it
 * out of the commit and answers with the change as it stands after; that
 * answer replaces what is drawn. A refusal — the etag moved, the branch was
 * pushed — is shown under the hunk it was for, and the diff is read again so
 * the next Drop is judged against what is there now.
 */
export default function ChangesPane({
  transport,
  workspaceId,
  runId,
  checks,
  fix,
  reload = 0,
  onLoaded,
  onOpenReview,
  onAcceptDeviation,
  acceptPending = false,
  acceptError = '',
}: {
  transport: Transport
  workspaceId: string
  runId: string
  /** The build, vet and test commands read off the run's events. */
  checks: Check[]
  /** Where the commit went, off the run's state, for the deviation gate. */
  fix?: FixInfo
  /** Bumped when the run finishes, so the diff written at the end is read. */
  reload?: number
  /** Tells the tab strip how many files the change touches. */
  onLoaded?: (diff: RunDiff | null) => void
  onOpenReview: () => void
  onAcceptDeviation?: () => void
  acceptPending?: boolean
  acceptError?: string
}) {
  const [diff, setDiff] = useState<RunDiff | null>(null)
  const [loadError, setLoadError] = useState('')
  const [loading, setLoading] = useState(true)
  const [kept, setKept] = useState<Set<string>>(() => new Set())
  const [dropping, setDropping] = useState<string | undefined>()
  const [refusals, setRefusals] = useState<Record<string, string>>({})

  /** Which read is the current one; an older read that lands late is ignored. */
  const generation = useRef(0)

  const load = useCallback(async () => {
    const mine = ++generation.current
    setLoading(true)
    setLoadError('')
    try {
      const d = await transport.runDiff(workspaceId, runId)
      if (mine !== generation.current) return
      setDiff(d)
      onLoaded?.(d)
    } catch (err: unknown) {
      if (mine !== generation.current) return
      setDiff(null)
      setLoadError(reasonOf(err))
      onLoaded?.(null)
    } finally {
      if (mine === generation.current) setLoading(false)
    }
  }, [transport, workspaceId, runId, onLoaded])

  useEffect(() => {
    setKept(new Set())
    setRefusals({})
    void load()
    return () => {
      // Whatever is in flight belongs to a run this pane no longer shows.
      generation.current += 1
    }
  }, [load, reload])

  const files = useMemo(() => (diff ? parsePatch(diff.patch) : []), [diff])

  const keep = useCallback((path: string, index: number) => {
    setKept((prev) => {
      const next = new Set(prev)
      const key = hunkKey(path, index)
      if (next.has(key)) next.delete(key)
      else next.add(key)
      return next
    })
  }, [])

  const drop = useCallback(
    async (path: string, index: number) => {
      if (!diff) return
      const key = hunkKey(path, index)
      setDropping(key)
      setRefusals((prev) => {
        const next = { ...prev }
        delete next[key]
        return next
      })
      try {
        const after = await transport.dropHunk(workspaceId, runId, { path, hunk: index, etag: diff.etag })
        setDiff(after)
        onLoaded?.(after)
        // The hunks below the dropped one move up by one, so a mark made
        // against the old numbering would name the wrong hunk.
        setKept(new Set())
      } catch (err: unknown) {
        setRefusals((prev) => ({ ...prev, [key]: reasonOf(err) }))
        void load()
      } finally {
        setDropping(undefined)
      }
    },
    [diff, transport, workspaceId, runId, onLoaded, load],
  )

  const editable = Boolean(diff && diff.worktreePresent && !diff.pushed)
  const readOnlyReason = !diff
    ? undefined
    : diff.pushed
      ? 'The branch has been pushed; the change can be read but not edited.'
      : !diff.worktreePresent
        ? 'The worktree is gone; the change can be read but not edited.'
        : undefined

  return (
    <div className="changes">
      {loading && !diff ? (
        <p className="pane-empty changes-note">Reading the change…</p>
      ) : loadError && !diff ? (
        <p className="pane-empty changes-note">{loadError}</p>
      ) : (
        <DiffView
          files={files}
          meta={diff?.files}
          kept={kept}
          dropping={dropping}
          refusals={refusals}
          editable={editable}
          readOnlyReason={readOnlyReason}
          truncated={diff?.truncated}
          onKeep={keep}
          onDrop={(path, index) => void drop(path, index)}
        />
      )}

      {checks.length > 0 ? (
        <div className="checks" role="list" aria-label="Checks">
          {checks.map((check, i) => (
            <span key={i} role="listitem" className="check" data-ok={check.ok === undefined ? undefined : String(check.ok)}>
              <span className="check-word">
                {check.ok === undefined ? '…' : check.ok ? 'ok' : 'FAIL'}
              </span>
              <span className="check-command">{check.command}</span>
              {check.detail && check.detail !== 'ok' ? (
                <span className="check-detail"> · {check.detail}</span>
              ) : null}
            </span>
          ))}
        </div>
      ) : null}

      {fix ? (
        <FixPanel
          fix={fix}
          pending={acceptPending}
          error={acceptError}
          onAccept={() => onAcceptDeviation?.()}
        />
      ) : null}

      {diff ? (
        <div className="branch">
          <span>Branch</span>
          <span className="branch-name">{diff.branch}</span>
          {diff.pushed ? <span className="branch-word">pushed</span> : null}
          <Button onClick={onOpenReview}>Open review</Button>
        </div>
      ) : null}
    </div>
  )
}
