import type { RunKind, SessionAccess } from '../api/types'
import { ACCESS, ACCESS_PHRASE, MODES, MODES_WITH_ACCESS, accessOf, modeChipTitle } from '../components/composer/modes'

/*
 * The Mode chip on a run that has already started. `MODES_WITH_ACCESS`
 * labels each mode with the posture it takes by default, which is the
 * whole story for triage, RCA and fix; a session's posture was the
 * operator's pick at the start and is on the run's record, so a worktree
 * session must not read "Session · read-only".
 */

/** The Mode chip's items, with the run's own mode carrying the posture it ran under. */
export function runModeItems(kind: RunKind, access?: SessionAccess): typeof MODES_WITH_ACCESS {
  const posture = accessOf(kind, access)
  if (posture === accessOf(kind)) return MODES_WITH_ACCESS
  const label = MODES.find((m) => m.id === kind)?.label ?? kind
  return MODES_WITH_ACCESS.map((m) => (m.id === kind ? { ...m, label: `${label} · ${ACCESS_PHRASE[posture]}` } : m))
}

/** The Mode chip's tooltip, explaining the posture the run actually has. */
export function runModeTitle(kind: RunKind, access?: SessionAccess): string {
  const title = modeChipTitle(kind)
  const posture = accessOf(kind, access)
  const fallback = accessOf(kind)
  if (posture === fallback) return title
  const noteOf = (id: SessionAccess) => ACCESS.find((a) => a.id === id)?.note ?? ''
  return title.replace(noteOf(fallback), noteOf(posture))
}
