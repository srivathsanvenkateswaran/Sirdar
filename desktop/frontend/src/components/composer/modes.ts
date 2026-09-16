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
