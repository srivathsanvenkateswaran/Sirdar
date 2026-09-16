import type { FixInfo } from '../../api/types'
import Button from '../../ui/button'

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
 *
 * What says the work is done is `pushed`, not the pull request URL. A run
 * started with "open no pull request", and one whose `gh` call failed after the
 * push went through, both record a deviation and no URL — and reading the URL
 * alone put this panel back into "waiting for you" for work that had already
 * left the machine.
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
  // Published is the push, or a pull request URL for the runs recorded
  // before the push itself was written down.
  const published = Boolean(fix.pushed) || Boolean(fix.prUrl)
  const blocked = Boolean(fix.deviation) && !published

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
        {published && !fix.prUrl ? (
          <>
            <dt>Pushed</dt>
            <dd>
              {fix.branch || 'The branch'} is on the remote, with no pull request opened for it.
              Either this run was started with “open no pull request”, or the GitHub
              CLI could not open one and said why in the activity pane. Open it from the branch.
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
            agent session is started. If the branch has moved on since — somebody committed on
            top of it, or deleted it — accepting is refused rather than quietly starting a fresh
            session, and you start that yourself from Start fix.
          </p>
          <div className="form-row">
            <Button variant="pale" onClick={onAccept} disabled={pending}>
              {pending ? 'Publishing…' : 'Accept and publish'}
            </Button>
          </div>
          {error ? <div className="form-error">{error}</div> : null}
        </div>
      ) : null}

      {fix.deviation && published ? (
        <p className="form-note">
          The agent reported deviating from the note, and the commit was published after a person
          accepted it: {fix.deviation}
        </p>
      ) : null}
    </section>
  )
}
