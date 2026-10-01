import { useCallback, useSyncExternalStore } from 'react'

/**
 * The model a reader picked for a run's next answer or steer.
 *
 * It used to be the session layout's own state, which is how it got lost:
 * switching layout unmounts the one that held it, and coming back showed the
 * run's model again while the reader believed the next turn was on the one
 * they had picked. It lives here instead, for the life of the window, keyed
 * by run — so it survives a layout switch and is never carried to another
 * run — and stamped with the run's status at the moment of the pick: a pick
 * is about the send it was made for, so once the run moves on (the steer
 * started, the run finished, it blocked again) the pick reads as empty
 * without anyone having to clear it.
 */

interface Pick {
  model: string
  /** The run's status when it was picked; a different status means the pick is spent. */
  status: string
}

const picks = new Map<string, Pick>()
const listeners = new Set<() => void>()

function keyOf(workspaceId: string, runId: string): string {
  return `${workspaceId}/${runId}`
}

function subscribe(listener: () => void): () => void {
  listeners.add(listener)
  return () => listeners.delete(listener)
}

/** The pick for a run in the status it has now, or '' when there is none or it is spent. */
export function readPickedModel(workspaceId: string, runId: string, status: string): string {
  const pick = picks.get(keyOf(workspaceId, runId))
  return pick && pick.status === status ? pick.model : ''
}

/** Records a pick; '' takes it back. */
export function writePickedModel(workspaceId: string, runId: string, status: string, model: string): void {
  const key = keyOf(workspaceId, runId)
  if (model) picks.set(key, { model, status })
  else picks.delete(key)
  for (const l of [...listeners]) l()
}

/** For tests: forget every pick. */
export function resetPickedModels(): void {
  picks.clear()
  for (const l of [...listeners]) l()
}

/** The run's pick and its setter, as `useState` would give them. */
export function usePickedModel(workspaceId: string, runId: string, status: string): [string, (model: string) => void] {
  const model = useSyncExternalStore(
    subscribe,
    () => readPickedModel(workspaceId, runId, status),
    () => readPickedModel(workspaceId, runId, status),
  )
  const set = useCallback(
    (next: string) => writePickedModel(workspaceId, runId, status, next),
    [workspaceId, runId, status],
  )
  return [model, set]
}
