import type { FixInfo } from '../../api/types'

/** The short sha a branch line shows; the full one is under State. */
function short(commit: string): string {
  return commit.length > 10 ? commit.slice(0, 10) : commit
}

/**
 * What a fix run produced, and the review it may be waiting on.
 *
 * A deviation means the agent did something other than the note's proposed
 * fix. The commit exists, on the branch, and has not been pushed. Accepting it
 * does not start another session: the rerun pushes the commit shown here, which
 * is the one the reader just read.
 */
export default function FixPanel({
  fix,
  pending,
  error,
  onAccept,
}: {
  fix: FixInfo
  pending: boolean
  error: string
  onAccept: () => void
}) {
  const blocked = Boolean(fix.deviation) && !fix.prUrl

  return (
    <section className="form form--fix-review" aria-label="Fix result">
      <dl className="fix-facts">
        {fix.branch ? (
          <>
            <dt>Branch</dt>
            <dd className="mono">
              {fix.branch}
              {fix.base ? ` (from origin/${fix.base})` : ''}
            </dd>
          </>
        ) : null}
        {fix.commit ? (
          <>
            <dt>Commit</dt>
            <dd className="mono">{short(fix.commit)}</dd>
          </>
        ) : null}
        {fix.prUrl ? (
          <>
            <dt>Pull request</dt>
            <dd>
              <a href={fix.prUrl} target="_blank" rel="noreferrer noopener">
                {fix.prUrl}
              </a>
            </dd>
          </>
        ) : null}
      </dl>

      {blocked ? (
        <div className="fix-deviation">
          <p className="form-question">
            The agent did not implement the note's proposed fix as written
          </p>
          <blockquote className="fix-deviation__text">{fix.deviation}</blockquote>
          <p className="form-note">
            The commit is on {fix.branch || 'the fix branch'} and has not been pushed. Read the
            diff. Accepting pushes that same commit and opens the pull request for it; no second
            agent session is started.
          </p>
          <div className="form-row">
            <button
              type="button"
              className="run-btn run-btn--primary"
              onClick={onAccept}
              disabled={pending}
            >
              {pending ? 'Publishing…' : 'Accept and publish'}
            </button>
          </div>
          {error ? <div className="form-error">{error}</div> : null}
        </div>
      ) : null}
    </section>
  )
}
