import { useCallback, useEffect, useMemo, useRef, useState, type JSX } from 'react'
import type { RunDetail, RunDiff, Transport } from '../../../api/types'
import { reasonOf } from '../../../lib/format'
import { changeTotals, pushCommand, rekeyAfterDrop, type FixReport, type RunCheck } from '../../../lib/review'
import Button from '../../../ui/button'
import { hunkKey, parsePatch, type HunkDecision } from '../../../ui/diff-view'
import Document, { type OutlineItem } from './Document'
import { BranchIcon, CheckIcon, XIcon } from './icons'
import { Prose } from './AnswerCard'

/** The reason without the code the transport prefixes it with. */
function withoutCode(err: unknown): string {
  return reasonOf(err).replace(/^(?:conflict|not_found|no_diff|forbidden|internal|unsupported):\s*/, '')
}

/**
 * The fix run's change as the Diff document: a facts strip (commit subject,
 * files and counts, deviation from the note), then every file hunk by hunk
 * with Keep and Drop while the change can still be edited, the risks the
 * report named, and under the page a two-row decision bar — the checks the
 * run ran, with the pre-fix failure in the failed hue, then the branch, its
 * base and commit, and Push.
 *
 * A local adapter with the shared `ChangesView`'s name. Drop hands the hunk
 * and the diff's etag to the service, as the Changes pane does today; Push
 * shows the CLI line, since no route pushes yet. There is no Discard: no
 * route deletes a fix branch, and a button that can never act is noise.
 */
export interface ChangesViewProps {
  transport: Transport
  workspaceId: string
  runId: string
  detail: RunDetail
  checks: RunCheck[]
  report?: FixReport
  /** Hunks the reviewer dropped, off the run's `review` events, for the outline. */
  dropped: { path: string; hunk: number }[]
  reload?: number
  onLoaded?: (diff: RunDiff | null) => void
  onOpenReview: () => void
  onAcceptDeviation?: () => void
  acceptPending?: boolean
}

export default function ChangesView({
  transport,
  workspaceId,
  runId,
  detail,
  checks,
  report,
  dropped,
  reload = 0,
  onLoaded,
  onOpenReview,
  onAcceptDeviation,
  acceptPending = false,
}: ChangesViewProps): JSX.Element {
  const [diff, setDiff] = useState<RunDiff | null>(null)
  const [loadError, setLoadError] = useState('')
  const [loading, setLoading] = useState(true)
  const [decisions, setDecisions] = useState<Record<string, HunkDecision>>({})
  const [dropping, setDropping] = useState<string | undefined>()
  const [refusal, setRefusal] = useState('')
  const [pushShown, setPushShown] = useState(false)
  const [copied, setCopied] = useState(false)
  const generation = useRef(0)

  const load = useCallback(async () => {
    const mine = ++generation.current
    setLoading(true)
    setLoadError('')
    try {
      const d = await transport.runDiff(workspaceId, runId)
      if (mine !== generation.current) return
      setDiff(d)
      setDecisions({})
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
    setRefusal('')
    void load()
    return () => {
      generation.current += 1
    }
  }, [load, reload])

  const keep = (path: string, index: number) => {
    setDecisions((prev) => {
      const key = hunkKey(path, index)
      const next = { ...prev }
      if (next[key] === 'kept') delete next[key]
      else next[key] = 'kept'
      return next
    })
  }

  const drop = async (path: string, index: number) => {
    if (!diff) return
    const key = hunkKey(path, index)
    setDropping(key)
    setRefusal('')
    try {
      const after = await transport.dropHunk(workspaceId, runId, { path, hunk: index, etag: diff.etag })
      setDiff(after)
      onLoaded?.(after)
      setDecisions((prev) => rekeyAfterDrop(prev, path, index))
    } catch (err: unknown) {
      setRefusal(`${path} hunk ${index + 1}: ${withoutCode(err)}`)
      void load()
    } finally {
      setDropping(undefined)
    }
  }

  const files = useMemo(() => {
    if (!diff) return []
    const parsed = parsePatch(diff.patch)
    return parsed.map((file) => {
      const own = diff.files.find((f) => f.path === file.path)
      return own ? { ...file, status: own.status, additions: own.additions, deletions: own.deletions } : file
    })
  }, [diff])

  const editable = Boolean(diff && diff.worktreePresent && !diff.pushed)
  const blocked = detail.status === 'blocked'
  const totals = diff ? changeTotals(diff.files) : undefined
  const subject = report?.summary.split('\n')[0] ?? ''

  const outline = useMemo<OutlineItem[]>(() => {
    const out: OutlineItem[] = [{ id: 'summary', title: 'Summary', n: blocked ? 'pending' : undefined }]
    for (const f of files) {
      const counts = [f.additions > 0 ? `+${f.additions}` : '', f.deletions > 0 ? `−${f.deletions}` : ''].filter(Boolean).join(' ')
      out.push({ id: `file-${f.path}`, title: f.path, n: counts || undefined })
      for (const h of f.hunks) out.push({ id: `hunk-${f.path}-${h.index}`, title: h.header.replace(/ @@.*$/, ' @@'), sub: true })
      for (const d of dropped.filter((d) => d.path === f.path)) {
        out.push({ id: `dropped-${f.path}-${d.hunk}`, title: `hunk ${d.hunk + 1} · dropped`, sub: true, tone: 'dropped' })
      }
    }
    // A file whose every hunk was dropped is gone from the patch but not from the review.
    for (const d of dropped.filter((d) => !files.some((f) => f.path === d.path))) {
      out.push({ id: `file-${d.path}`, title: d.path, n: 'dropped' })
      out.push({ id: `dropped-${d.path}-${d.hunk}`, title: `hunk ${d.hunk + 1} · dropped`, sub: true, tone: 'dropped' })
    }
    if (report?.risks) out.push({ id: 'risks', title: 'Risks' })
    return out
  }, [files, dropped, report, blocked])

  const pushLine = diff ? pushCommand(diff.worktree, diff.branch) : ''
  const branch = diff?.branch || detail.fix?.branch || ''
  const base = diff?.base || detail.fix?.base || ''
  const commit = (diff?.head && !diff.head.startsWith(branch) ? diff.head : detail.fix?.commit) ?? ''
  const pushed = diff?.pushed ?? detail.fix?.pushed ?? false

  return (
    <div className="wb-changes">
      <Document outline={outline} label="Diff" padTop="tight">
        <section data-sec="summary">
          <div className="wb-facts">
            {subject ? (
              <span>
                commit subject <b dir="auto">{subject}</b>
              </span>
            ) : blocked ? (
              <span>
                working tree{' '}
                <b>{totals ? `${totals.files} ${totals.files === 1 ? 'file' : 'files'} changed` : 'unknown'}</b>
              </span>
            ) : null}
            {totals ? (
              <span>
                <span className="wb-mono">
                  {totals.files} {totals.files === 1 ? 'file' : 'files'} · +{totals.additions} −{totals.deletions}
                </span>
              </span>
            ) : null}
            {blocked ? (
              <span>not yet summarised — the run is waiting on you</span>
            ) : report ? (
              <span>
                deviation from note <b>{report.deviationFromNote ? report.deviationFromNote : 'none'}</b>
              </span>
            ) : detail.fix?.deviation ? (
              <span>
                deviation from note <b>{detail.fix.deviation}</b>
              </span>
            ) : null}
          </div>
        </section>

        {refusal ? (
          <p className="wb-refused" role="alert">
            {refusal}
          </p>
        ) : null}
        {diff && !editable ? (
          <p className="wb-empty wb-empty--inline">
            {diff.pushed ? 'The branch has been pushed; the change can be read but not edited.' : 'The worktree is gone; the change can be read but not edited.'}
          </p>
        ) : null}
        {loading && !diff ? (
          <p className="wb-empty">Reading the change…</p>
        ) : loadError && !diff ? (
          <p className="wb-empty">{loadError}</p>
        ) : null}

        {files.map((file) => (
          <section key={file.path} data-sec={`file-${file.path}`} className="wb-file" aria-label={file.path} dir="ltr">
            {file.hunks.map((hunk) => {
              const key = hunkKey(file.path, hunk.index)
              const kept = decisions[key] === 'kept'
              const busy = dropping === key
              return (
                <div key={key} data-sec={`hunk-${file.path}-${hunk.index}`}>
                  <div className="wb-dh">
                    <span className="wb-dh__f">{file.path}</span>
                    <span className="wb-dh__h">{hunk.header}</span>
                    {editable ? (
                      <span className="wb-dh__acts">
                        <Button variant="pale" size="sm" aria-pressed={kept} disabled={busy} onClick={() => keep(file.path, hunk.index)}>
                          {kept ? 'Kept' : 'Keep'}
                        </Button>
                        <Button variant="ghost" size="sm" busy={busy} onClick={() => void drop(file.path, hunk.index)}>
                          {busy ? 'Dropping…' : 'Drop'}
                        </Button>
                      </span>
                    ) : null}
                  </div>
                  <div className="wb-dlines" role="table" aria-label={`${file.path} hunk ${hunk.index + 1}`}>
                    {hunk.lines.map((line, i) => (
                      <div key={i} className="wb-dline" data-t={line.type === 'context' ? 'ctx' : line.type} role="row">
                        <span className="wb-dline__n" role="cell">{line.oldNo ?? ''}</span>
                        <span className="wb-dline__n" role="cell">{line.newNo ?? ''}</span>
                        <span className="wb-dline__s" role="cell" aria-hidden="true">
                          {line.type === 'add' ? '+' : line.type === 'del' ? '−' : ' '}
                        </span>
                        <span className="wb-dline__c" role="cell">{line.text}</span>
                      </div>
                    ))}
                  </div>
                </div>
              )
            })}
          </section>
        ))}
        {diff?.truncated ? <p className="wb-empty wb-empty--inline">The patch was cut at its size limit. Every file is listed; not every line is shown.</p> : null}

        {report?.risks ? (
          <section className="wb-sec" data-sec="risks">
            <div className="wb-sec-h">Risks</div>
            <Prose text={report.risks} />
          </section>
        ) : null}
      </Document>

      <div className="wb-decide" aria-label="Decision">
        <div className="wb-decide__r">
          {checks.length === 0 ? <span className="wb-decide__note wb-decide__note--start">No checks ran yet.</span> : null}
          {checks.map((c, i) => (
            <span key={i} className="wb-chk" data-ok={c.outcome === 'ok' ? 'true' : c.outcome === 'failed' ? 'false' : undefined} title={c.result}>
              {c.outcome === 'failed' ? <XIcon /> : <CheckIcon />}
              <span className="visually-hidden">{c.outcome} </span>
              {c.command}
              {c.result && c.result !== 'ok' ? ` · ${c.result}` : ''}
            </span>
          ))}
          {dropped.length > 0 ? (
            <span className="wb-decide__note">
              {dropped.length} {dropped.length === 1 ? 'hunk' : 'hunks'} dropped in review
            </span>
          ) : null}
        </div>
        <div className="wb-decide__r">
          <span className="wb-br">
            <span className="wb-br__lab">branch</span>
            <BranchIcon />
            {branch ? (
              <>
                <span className="wb-br__name">{branch}</span>
                {base ? (
                  <>
                    <span className="wb-br__lab">from</span> {base}
                  </>
                ) : null}
                {commit ? (
                  <>
                    <span className="wb-br__lab">·</span> {commit.slice(0, 7)}
                  </>
                ) : null}
                <span className="wb-br__lab">·</span> {pushed ? 'pushed' : commit ? 'local, not pushed' : 'no commit yet'}
              </>
            ) : (
              <span className="wb-br__name">{blocked ? 'not committed yet' : 'no branch recorded'}</span>
            )}
          </span>
          <span className="wb-decide__acts">
            {detail.fix?.deviation && onAcceptDeviation ? (
              <Button variant="pale" size="sm" busy={acceptPending} onClick={onAcceptDeviation}>
                {acceptPending ? 'Rerunning…' : 'Accept deviation and rerun'}
              </Button>
            ) : null}
            <Button variant="ghost" size="sm" onClick={onOpenReview}>
              Open review
            </Button>
            {branch && !pushed && diff ? (
              <Button variant="secondary" size="sm" aria-expanded={pushShown} onClick={() => setPushShown((v) => !v)}>
                Push branch
              </Button>
            ) : null}
          </span>
        </div>
        {pushShown && pushLine ? (
          <div className="wb-decide__r wb-decide__push">
            <span className="wb-decide__note wb-decide__note--start">No route pushes yet; run this where the worktree is:</span>
            <code className="wb-mono">{pushLine}</code>
            <Button
              variant="pale"
              size="sm"
              onClick={() => {
                void navigator.clipboard?.writeText(pushLine).then(
                  () => {
                    setCopied(true)
                    setTimeout(() => setCopied(false), 2000)
                  },
                  () => {},
                )
              }}
            >
              {copied ? 'Copied' : 'Copy'}
            </Button>
          </div>
        ) : null}
      </div>
    </div>
  )
}
