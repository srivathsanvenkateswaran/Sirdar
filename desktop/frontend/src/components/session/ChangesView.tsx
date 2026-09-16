import { useState, type MouseEvent } from 'react'
import type { RunDetail, RunDiff } from '../../api/types'
import type { Marker as MarkerModel } from '../../lib/evidence'
import { changeTotals, OUTCOME_WORDS, pushCommand, type FixReport, type RunCheck } from '../../lib/review'
import Button from '../../ui/button'
import DiffView, { type HunkDecision } from '../../ui/diff-view'
import Marker from '../../ui/marker'
import StatusBadge from '../../ui/status-badge'
import { clock } from './model'
import { MetaCell } from './NoteDocument'
import { CopyButton, RefProse, Section } from './Prose'
import Stamp from './Stamp'

export interface DropRecord {
  path: string
  hunk: number
  /** `00:49` */
  at: string
}

export interface ChangesViewProps {
  detail: RunDetail
  diff: RunDiff | null
  loading: boolean
  loadError: string
  decisions: Record<string, HunkDecision>
  dropping?: string
  refusal: string
  onKeep: (path: string, hunk: number) => void
  onDrop: (path: string, hunk: number) => void
  report?: FixReport
  checks: RunCheck[]
  markers: MarkerModel[]
  hotMarker?: string
  onMarker?: (id: string, event: MouseEvent<HTMLElement>) => void
  /** The hunks a person dropped after the session, off the run's `review` events. */
  drops?: DropRecord[]
  onAcceptDeviation?: () => void
  acceptPending?: boolean
  acceptError?: string
  onOpenReview?: () => void
  /** The change is still growing: the run is live. */
  live?: boolean
  /** The pending step's command, for the "next step needs your answer" line while blocked. */
  pendingCommand?: string
}

/** The commit subject and body the agent reported, split at the first blank line. */
export function splitSummary(summary: string): { subject: string; body: string } {
  const [subject, ...rest] = summary.split(/\n\s*\n/)
  return { subject: (subject ?? '').trim(), body: rest.join('\n\n').trim() }
}

/**
 * A fix run's change as the document: a metadata strip (branch, base,
 * worktree, commit, files, checks, deviation), the commit subject and body,
 * the checks the agent ran with the pre-fix FAIL kept, the diff per file with
 * Keep and Drop and C markers on the hunks, the agent's risks, and the push
 * decision at the end where the reviewer arrives after reading the diff.
 * Push shows the CLI line, since there is no route for it yet; Discard has
 * none either and says so.
 */
export default function ChangesView({
  detail,
  diff,
  loading,
  loadError,
  decisions,
  dropping,
  refusal,
  onKeep,
  onDrop,
  report,
  checks,
  markers,
  hotMarker,
  onMarker,
  drops = [],
  onAcceptDeviation,
  acceptPending = false,
  acceptError = '',
  onOpenReview,
  live = false,
  pendingCommand,
}: ChangesViewProps): JSX.Element {
  const [showPush, setShowPush] = useState(false)
  const fix = detail.fix
  const { subject, body } = splitSummary(report?.summary ?? '')
  const totals = diff ? changeTotals(diff.files) : undefined
  const editable = Boolean(diff && diff.worktreePresent && !diff.pushed)
  const blocked = detail.status === 'blocked'
  const published = Boolean(fix?.pushed) || Boolean(fix?.prUrl)
  const deviationBlocks = Boolean(fix?.deviation) && !published
  const okChecks = checks.filter((c) => c.outcome === 'ok')
  const lastCheck = checks[checks.length - 1]
  const worktreeShort = diff?.worktree ? diff.worktree.replace(/^.*?(\.sirdar\/worktrees\/)/, '$1') : ''
  const cMarkers = markers.filter((m) => m.kind === 'C')
  const markersForHunk = (path: string, hunk: number) => cMarkers.filter((m) => m.path === path && m.hunk === hunk)
  const dropFor = (path: string, hunk: number) => drops.find((d) => d.path === path && d.hunk === hunk)
  const elapsed = clock(detail.updatedAt, detail.startedAt)

  return (
    <article className="sn-note sn-change" data-testid="changes-view">
      <div className="sn-meta" data-cols="3">
        {fix?.branch || diff?.branch ? <MetaCell k="Branch" v={fix?.branch || diff?.branch} mono span2 /> : null}
        <MetaCell k="Base" v={[fix?.base || diff?.base || 'main', live || blocked ? 'cut at start' : ''].filter(Boolean).join(' · ')} mono />
        {worktreeShort ? (
          <MetaCell k="Worktree" v={[worktreeShort, diff?.worktreePresent ? (live || blocked ? '' : 'kept') : 'gone'].filter(Boolean).join(' · ')} mono span2 />
        ) : null}
        {blocked || live ? (
          <MetaCell k="Budget" v={`${detail.usage?.turns ?? 0}/${detail.budget.maxTurns} turns · ${elapsed}/${detail.budget.maxMinutes}:00`} mono />
        ) : (
          <MetaCell k="Commit" v={fix?.commit ? `${fix.commit.slice(0, 7)} · ${published ? 'pushed' : 'not pushed'}` : 'not yet'} mono />
        )}
        {totals ? (
          <MetaCell k={live || blocked ? 'Files so far' : 'Files'} v={`${totals.files} · +${totals.additions} −${totals.deletions}`} mono />
        ) : null}
        <MetaCell
          k="Checks"
          v={
            checks.length === 0 ? (
              'not yet run'
            ) : lastCheck?.outcome === 'ok' ? (
              <>
                <StatusBadge status="done">ok</StatusBadge> {okChecks.map((c) => c.command.split(/\s+/).slice(0, 2).join(' ').replace(/^go /, '')).join(' · ')}
                {lastCheck.result && lastCheck.result !== 'ok' ? ` ${lastCheck.result.replace(/^ok\s+\S+\s*/, '')}` : ''}
              </>
            ) : (
              <>
                <StatusBadge status="failed">{OUTCOME_WORDS[lastCheck.outcome]}</StatusBadge> {lastCheck.command}
              </>
            )
          }
        />
        {!live && !blocked ? (
          <MetaCell k="Deviation from note" v={fix?.deviation ? (published ? 'reported · accepted' : 'reported · waiting on you') : report ? 'none · as proposed' : 'not reported yet'} />
        ) : (
          <MetaCell k="Implements" v="the note's Proposed fix" />
        )}
      </div>

      <h1 className="sn-note__h1" dir="auto">
        {subject || (live || blocked ? 'The change so far' : 'The change')}
      </h1>
      <div className="sn-note__sub">
        <span>
          {report
            ? `Commit subject and body as the agent reported them${detail.updatedAt ? ` at ${elapsed}` : ''}. Sirdar committed on the branch; pushing is yours.`
            : blocked
              ? `The change, as it stands at ${elapsed}. The agent is waiting on your answer before it goes on.`
              : live
                ? 'The change, as it grows. The report arrives when the agent finishes.'
                : 'The run ended without a report.'}
        </span>
      </div>
      {body ? <RefProse text={body} markers={markers} hotMarker={hotMarker} onMarker={onMarker} /> : null}

      {deviationBlocks ? (
        <div className="sn-deviation" role="region" aria-label="Deviation from the note">
          <p style={{ margin: 0 }}>The agent did not implement the note's proposed fix as written.</p>
          <blockquote>{fix?.deviation}</blockquote>
          <p style={{ margin: 0 }}>
            The commit is on {fix?.branch || 'the fix branch'} and has not been pushed. Read the diff. Accepting pushes that same commit and opens the
            pull request for it; no second agent session is started.
          </p>
          {onAcceptDeviation ? (
            <div className="sn-decide">
              <Button variant="pale" onClick={onAcceptDeviation} disabled={acceptPending}>
                {acceptPending ? 'Publishing…' : 'Accept and publish'}
              </Button>
            </div>
          ) : null}
          {acceptError ? <p className="form-error">{acceptError}</p> : null}
        </div>
      ) : null}

      <Section title="Checks" tag={checks.length > 0 ? `${checks.length} ${checks.length === 1 ? 'command' : 'commands'}` : undefined}>
        {checks.length === 0 ? (
          <p className="sn-empty">
            None yet.
            {pendingCommand ? (
              <>
                {' '}
                The agent's next step is <span className="sn-mono">{pendingCommand}</span>, which needs your answer below.
              </>
            ) : null}
          </p>
        ) : (
          <div className="sn-checks" role="list" aria-label="Checks">
            {checks.map((c, i) => (
              <span key={i} role="listitem" style={{ display: 'contents' }}>
                <span className={c.outcome === 'ok' ? 'sn-checks__ok' : c.outcome === 'failed' ? 'sn-checks__fail' : 'sn-checks__ran'}>
                  {c.outcome === 'failed' ? 'FAIL' : OUTCOME_WORDS[c.outcome]}
                </span>
                <span>
                  {c.command}
                  {c.result && c.result !== 'ok' ? <span className="sn-checks__r"> · {c.result}</span> : null}
                </span>
              </span>
            ))}
          </div>
        )}
      </Section>

      <Section title={live || blocked ? 'Change so far' : 'Files'} tag={totals ? `${totals.files} ${totals.files === 1 ? 'file' : 'files'}` : undefined}>
        {loading && !diff ? (
          <p className="sn-empty">Reading the change…</p>
        ) : loadError && !diff ? (
          <p className="sn-empty">{loadError}</p>
        ) : diff ? (
          <>
            {refusal ? (
              <p className="form-error" role="alert">
                {refusal}
              </p>
            ) : null}
            {!editable ? (
              <p className="sn-empty" style={{ marginBlockEnd: 10 }}>
                {diff.pushed ? 'The branch has been pushed; the change can be read but not edited.' : 'The worktree is gone; the change can be read but not edited.'}
              </p>
            ) : null}
            {drops.filter((d) => !diff.files.some((f) => f.path === d.path)).map((d) => (
              <p key={`${d.path}-${d.hunk}`} className="sn-empty" style={{ marginBlockEnd: 10 }}>
                <Stamp tone="you">{`dropped by you · ${d.at}`}</Stamp> {d.path} hunk {d.hunk + 1} is no longer in the commit; it stays in the worktree.
              </p>
            ))}
            <div className="sn-change__diff">
              <DiffView
                patch={diff.patch}
                files={diff.files}
                editable={editable}
                decisions={decisions}
                dropping={dropping}
                truncated={diff.truncated}
                onKeep={onKeep}
                onDrop={onDrop}
                hunkExtra={(file, hunk) => {
                  const own = markersForHunk(file.path, hunk.index)
                  const dropped = dropFor(file.path, hunk.index)
                  return (
                    <span className="sn-hunk-extra">
                      {dropped ? <Stamp tone="you">{`dropped by you · ${dropped.at}`}</Stamp> : null}
                      {own.map((m) => (
                        <Marker key={m.id} id={m.id} hot={m.id === hotMarker} onClick={onMarker} title={`${file.path} ${hunk.header}`} />
                      ))}
                    </span>
                  )
                }}
              />
            </div>
          </>
        ) : null}
      </Section>

      {report?.risks ? (
        <Section title="Risks" tag="what to look at hardest">
          <RefProse text={report.risks} markers={markers} hotMarker={hotMarker} onMarker={onMarker} />
        </Section>
      ) : null}

      {diff && !live && !blocked ? (
        <>
          <div className="sn-decide">
            <Button onClick={() => setShowPush((v) => !v)} disabled={diff.pushed} aria-expanded={showPush}>
              {diff.pushed ? 'Pushed' : `Push ${diff.branch.length > 24 ? `${diff.branch.slice(0, 12)}…${diff.branch.slice(-8)}` : diff.branch}`}
            </Button>
            <Button variant="ghost" disabled title="There is no route to discard a worktree yet; remove .sirdar/worktrees/<run> by hand.">
              Discard worktree
            </Button>
            {onOpenReview ? (
              <Button variant="ghost" onClick={onOpenReview}>
                Open review
              </Button>
            ) : null}
            <span className="sn-decide__hint">
              {editable ? 'Pushing includes only kept hunks. A dropped hunk stays in the worktree.' : diff.pushed ? 'The branch is on the remote.' : ''}
            </span>
          </div>
          {showPush && !diff.pushed ? (
            <div className="sn-push" data-testid="push-command">
              <code className="sn-push__cmd">{pushCommand(diff.worktree, diff.branch)}</code>
              <CopyButton text={pushCommand(diff.worktree, diff.branch)} />
            </div>
          ) : null}
        </>
      ) : null}
    </article>
  )
}
