/**
 * The run → job pairing the Cancel button needs.
 *
 * Cancel only works for a job this window started, and the job id comes back
 * from `startTriage`/`startRCA` before the run it produces exists on disk. The
 * store keeps the pairing here as the first `run.updated` for one of the job's
 * keys arrives, and Run detail reads it, so neither has to know about the
 * other. A `job.finished` clears the job's runs again: its id is no longer one
 * the service will cancel.
 */

const jobs = new Map<string, string>()
const watchers = new Set<() => void>()

function notify(): void {
  for (const watcher of [...watchers]) watcher()
}

export function setRunJob(runId: string, jobId: string): void {
  if (!runId || !jobId || jobs.get(runId) === jobId) return
  jobs.set(runId, jobId)
  notify()
}

export function clearRunJob(runId: string): void {
  if (!jobs.delete(runId)) return
  notify()
}

/** Drops every run paired with this job, as `job.finished` reports it. */
export function clearJob(jobId: string): void {
  let changed = false
  for (const [runId, id] of [...jobs]) {
    if (id !== jobId) continue
    jobs.delete(runId)
    changed = true
  }
  if (changed) notify()
}

export function getRunJob(runId: string): string | undefined {
  return jobs.get(runId)
}

/** Notifies on every change; the return value unsubscribes. */
export function subscribeRunJobs(listener: () => void): () => void {
  watchers.add(listener)
  return () => {
    watchers.delete(listener)
  }
}

/** Empties the map. Tests use it so one case cannot enable another's Cancel. */
export function resetRunJobs(): void {
  if (jobs.size === 0) return
  jobs.clear()
  notify()
}
