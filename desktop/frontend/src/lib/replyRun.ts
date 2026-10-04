import type { RunEvent, RunSummary } from '../api/types'

/**
 * A run that answers in chat: every session run, and a triage or RCA run
 * the backend marked `replyFirst`. Its `final` lines are markdown replies,
 * not a structured answer, and the note (when there is one) is filed after
 * the reply by a turn of its own.
 */
export function isReplyRun(run?: Pick<RunSummary, 'kind' | 'replyFirst'> | null): boolean {
  if (!run) return false
  return run.kind === 'session' || run.replyFirst === true
}

/**
 * The text of the newest reply in a reply run's log: the last `final` that
 * is not the note turn's and says something. A steer that stopped without
 * answering ends with an empty `final`, which does not replace the reply
 * before it; the note turn's `final` is the note's JSON, never a reply.
 */
export function latestReply(events: RunEvent[]): string {
  for (let i = events.length - 1; i >= 0; i -= 1) {
    const e = events[i]
    if (e.kind !== 'final' || e.payload?.phase === 'note') continue
    const text = (e.payload?.text ?? '').trim()
    if (text) return text
  }
  return ''
}

/** `/notes/OMNI-1 refund-stuck.md` → `OMNI-1 refund-stuck`: the name a vault shows a note by. */
export function noteStem(path: string): string {
  const base = path.split(/[\\/]/).pop() ?? path
  return base.replace(/\.md$/i, '')
}
