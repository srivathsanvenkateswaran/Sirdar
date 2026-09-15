import { useCallback, useEffect, useRef, useState } from 'react'
import type { FixInfo, RunDiff, Transport } from '../../api/types'
import { reasonOf } from '../../lib/format'
import { OUTCOME_WORDS, type RunCheck } from '../../lib/review'
import Button from '../../ui/button'
import DiffView, { hunkKey, type HunkDecision } from '../../ui/diff-view'
import FixPanel from './FixPanel'

/** The reason without the code the transport prefixes it with, so the reader gets the sentence alone. */
export function withoutCode(err: unknown): string {
  return reasonOf(err).replace(/^(?:conflict|not_found|no_diff|forbidden|internal|unsupported):\s*/, '')
}

/**
 * The fix run's change: the diff with Keep and Drop, the checks the agent
 * ran, and the branch the commit is on.
 *
 * Drop hands the hunk and the diff's etag to the service, which reverts it
 * out of the commit and answers with the change as it stands after; that
 * answer replaces what is drawn. A refusal — the etag moved, the branch was
 * pushed — is shown above the diff naming the hunk it was for, and the diff
 * is read again so the next Drop is judged against what is there now.
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
  checks: RunCheck[]
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
  const [decisions, setDecisions] = useState<Record<string, HunkDecision>>({})
  const [dropping, setDropping] = useState<string | undefined>()
  const [refusal, setRefusal] = useState('')
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
      setLoadError(withoutCode(err))
      onLoaded?.(null)
    } finally {
      if (mine === generation.current) setLoading(false)
    }
  }, [transport, workspaceId, runId, onLoaded])

  useEffect(() => {
    setDecisions({})
    setRefusal('')
    void load()
    return () => {
      // Whatever is in flight belongs to a run this pane no longer shows.
      generation.current += 1
    }
  }, [load, reload])

  const keep = useCallback((path: string, index: number) => {
    setDecisions((prev) => {
      const key = hunkKey(path, index)
      const next = { ...prev }
      if (next[key] === 'kept') delete next[key]
      else next[key] = 'kept'
      return next
    })
  }, [])

  const drop = useCallback(
    async (path: string, index: number) => {
      if (!diff) return
      const key = hunkKey(path, index)
      setDropping(key)
      setRefusal('')
      try {
        const after = await transport.dropHunk(workspaceId, runId, { path, hunk: index, etag: diff.etag })
        setDiff(after)
        onLoaded?.(after)
        // The hunks below the dropped one move up by one, so a mark made
        // against the old numbering would name the wrong hunk.
        setDecisions({})
      } catch (err: unknown) {
        setRefusal(`${path} hunk ${index + 1}: ${withoutCode(err)}`)
        void load()
      } finally {
        setDropping(undefined)
      }
    },
    [diff, transport, workspaceId, runId, onLoaded, load],
  )

  const editable = Boolean(diff && diff.worktreePresent && !diff.pushed)
  const readOnlyReason = !diff
    ? ''
    : diff.pushed
      ? 'The branch has been pushed; the change can be read but not edited.'
      : !diff.worktreePresent
        ? 'The worktree is gone; the change can be read but not edited.'
        : ''

  return (
    <div className="changes">
      {loading && !diff ? (
        <p className="pane-empty changes-note">Reading the change…</p>
      ) : loadError && !diff ? (
        <p className="pane-empty changes-note">{loadError}</p>
      ) : diff ? (
        <div className="changes-diff">
          {refusal ? (
            <p className="changes-refused" role="alert">
              {refusal}
            </p>
          ) : null}
          {readOnlyReason ? <p className="pane-empty changes-note">{readOnlyReason}</p> : null}
          <DiffView
            patch={diff.patch}
            files={diff.files}
            editable={editable}
            decisions={decisions}
            dropping={dropping}
            truncated={diff.truncated}
            onKeep={keep}
            onDrop={(path, index) => void drop(path, index)}
          />
        </div>
      ) : null}

      {checks.length > 0 ? (
        <div className="checks" role="list" aria-label="Checks">
          {checks.map((check, i) => (
            <span key={i} role="listitem" className="check" data-outcome={check.outcome}>
              <span className="check-word">{OUTCOME_WORDS[check.outcome]}</span>
              <span className="check-command">{check.command}</span>
              {check.result && check.result !== 'ok' ? (
                <span className="check-detail"> · {check.result}</span>
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
