import type { RunKind } from '../../api/types'

/** What a session does to a ticket, in the order the Mode chip lists them. */
export type SessionMode = RunKind

/** The modes as the Mode chip lists them, each with what it does. */
export const MODES: { id: SessionMode; label: string; note: string }[] = [
  { id: 'triage', label: 'Triage', note: 'Read the ticket and the code; write a triage note' },
  { id: 'rca', label: 'RCA', note: 'A root-cause note, from the triage note' },
  { id: 'fix', label: 'Fix', note: 'A fix on a branch, from the triage note' },
]

export type Access = 'read-only' | 'worktree'

/**
 * What a mode may do to the tree: the Access chip's word and its
 * explanation. The words are HANDOFF.md's: reads are confined to the
 * workspace, the run directory and its bundle; a fix session stands in a
 * linked worktree under `.sirdar/worktrees/<run-id>`, and Sirdar, not the
 * agent, commits and pushes.
 */
export const ACCESS: { id: Access; label: string; note: string }[] = [
  {
    id: 'read-only',
    label: 'Read-only',
    note: 'Triage and RCA read the workspace, the run directory and its bundle. Nothing is written.',
  },
  {
    id: 'worktree',
    label: 'Worktree',
    note: 'Fix writes in a linked worktree under .sirdar/worktrees; the tree you work in is untouched. Sirdar commits and pushes, never the agent.',
  },
]

/** The posture a mode runs with. */
export function accessOf(mode: SessionMode): Access {
  return mode === 'fix' ? 'worktree' : 'read-only'
}

/**
 * What the posture reads as inside the Mode chip, after the mode's own word.
 *
 * Access is derived from Mode and was never a control of its own, so on the
 * session composer it is the Mode chip's secondary text — "Triage ·
 * read-only", "Fix · writes in worktree" — rather than a second chip that
 * pushed the row onto a second line. `ACCESS`'s own `note` is still what the
 * chip's tooltip and its popover say; only the chip's word is shortened.
 */
export const ACCESS_PHRASE: Record<Access, string> = {
  'read-only': 'read-only',
  worktree: 'writes in worktree',
}

/** The Mode chip's whole word: the mode, then what it may do to the tree. */
export function modeWithAccess(mode: SessionMode): string {
  const label = MODES.find((m) => m.id === mode)?.label ?? mode
  return `${label} · ${ACCESS_PHRASE[accessOf(mode)]}`
}

/**
 * The modes as the session composer's one Mode chip lists them: each label
 * carries its posture, so the row is model · mode · the button and nothing
 * else. New session keeps `MODES`, where Mode is still a live choice.
 */
export const MODES_WITH_ACCESS: { id: SessionMode; label: string; note: string }[] = MODES.map((m) => ({
  id: m.id,
  label: modeWithAccess(m.id),
  note: m.note,
}))

/**
 * The Mode chip's tooltip on a run that has already started: the kind cannot
 * change, and `accessOf` says what the posture it carries means.
 */
export function modeChipTitle(mode: SessionMode): string {
  const note = ACCESS.find((a) => a.id === accessOf(mode))?.note ?? ''
  return `The run's kind does not change; start another session for a different one. ${note}`.trim()
}

/**
 * The providers that cannot continue a run at all. `internal/provider`'s
 * `Steerable` half returns `ContinueNone` for cursor and agy — neither hands
 * Sirdar a tool call to judge before it runs, so an open-ended follow-up
 * cannot be held to the read-only guarantee. Every other provider resumes by
 * handle, or (acp) is primed with the run's own note.
 */
const UNSTEERABLE = new Set(['cursor', 'agy'])

/** Whether a follow-up reaches this provider at all. */
export function steerable(provider: string): boolean {
  return !UNSTEERABLE.has(provider.trim().toLowerCase())
}

/**
 * The one line the box says while the run works. It says what typing here
 * does and nothing else: the status sentence that used to sit beside the
 * button said the same thing a second time, and the button itself now reads
 * Stop, which is the whole of what the reader can do.
 */
export function runningPlaceholder(provider: string): string {
  return steerable(provider) ? 'Steer the run — it picks this up at its next turn' : 'Waiting for the run to finish'
}
