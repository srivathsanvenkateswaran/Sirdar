import type {
  AppEvent,
  HookOutcome,
  Quota,
  RunSummary,
  SourcesSummary,
  Ticket,
  Transport,
  Workspace,
} from '../api/types'
import { parseTime, reasonOf } from '../lib/format'
import { clearJob, setRunJob } from '../lib/jobs'

/**
 * Which screen the window is showing. Run detail and Change review carry the
 * run they opened; Settings carries the page inside the modal, when a link
 * named one.
 */
export type Screen =
  | { name: 'board' }
  /** The New session screen: a key or URL, a mode, and Start. */
  | { name: 'new' }
  | { name: 'run'; runId: string }
  /** The run's change, full width, before a branch is made of it. */
  | { name: 'review'; runId: string }
  | { name: 'register' }
  | { name: 'eval' }
  | { name: 'settings'; page?: string }
  /** The design library. Only reachable while the Settings switch is on. */
  | { name: 'library' }

export interface Toast {
  id: number
  tone: 'info' | 'error'
  text: string
}

/**
 * One inbound webhook delivery, as the Board's Inbound panel lists it. The
 * tracker fires on every field change, so most deliveries are skipped or
 * filtered; seeing them is how a hook that never arrives is told apart from
 * one that arrives and is dropped.
 */
export interface InboundDelivery {
  id: number
  source: string
  key: string
  outcome: HookOutcome
  at: string
}

/** How many deliveries the Inbound panel keeps. */
export const INBOUND_LIMIT = 50

/**
 * A job the screen that started it can cancel.
 *
 * A triage, RCA or fix is paired with a run as its first `run.updated`
 * arrives, and Run detail cancels it from there. An eval is different: over
 * the whole set it names no key at all, so no run can ever claim it, and over
 * named keys it is many runs on a screen that shows none of them. Every eval
 * job is kept on the state, so the Eval screen can offer Cancel for it; the
 * name is what the list was first for.
 */
export interface KeylessJob {
  jobId: string
  workspaceId: string
  /** What the job is, for the button beside it. */
  label: string
}

/**
 * Whether the window is still hearing from the service. 'lost' is the HTTP
 * event stream dropping; the browser retries on its own, and the store
 * resyncs when it is back. The desktop bridge never drops.
 */
export type LiveUpdates = 'live' | 'lost'

export interface AppState {
  transport: Transport
  workspaces: Workspace[]
  currentWorkspaceId: string
  screen: Screen
  runsByWorkspace: Record<string, RunSummary[]>
  ticketsByWorkspace: Record<string, Ticket[]>
  /**
   * The tracker and helpdesk each workspace reads, from its config summary,
   * so a ticket number can be drawn under its own product's mark. Absent
   * until the summary has been read, and for a workspace whose config
   * cannot be loaded (Settings says why).
   */
  sourcesByWorkspace: Record<string, SourcesSummary>
  /** Workspaces whose tracker cannot list a queue (the API answers 501). */
  queueUnsupported: Record<string, boolean>
  quota: Quota[]
  toasts: Toast[]
  /** Inbound webhook deliveries, newest first. */
  inbound: InboundDelivery[]
  /** Jobs the screen that started them can cancel; see KeylessJob. */
  keylessJobs: KeylessJob[]
  /** True until `init()` has finished its first pass, so the board can say so. */
  loading: boolean
  /** Whether the event stream is up; see LiveUpdates. */
  liveUpdates: LiveUpdates
}

export interface TriageOptions {
  provider?: string
  model?: string
  dryRun?: boolean
}

export interface RCAOptions {
  prUrl?: string
  resolution?: string
  provider?: string
  model?: string
}

export interface FixOptions {
  dryRun?: boolean
  noPr?: boolean
  base?: string
  acceptDeviation?: boolean
  provider?: string
  model?: string
}

export interface EvalOptions {
  provider?: string
  model?: string
  concurrency?: number
  /** Replay each key at the commit its fix branched from and score against the merged change. */
  retro?: boolean
  /** With `retro`: add the blind RCA run between triage and fix. */
  withRca?: boolean
  /** With `retro`: one provider call grading root cause and files against the human fix. */
  rubric?: boolean
}

/**
 * A job this window started, held until its runs are known.
 *
 * `startTriage` and `startRCA` answer with a job id straight away; the runs
 * that job produces only exist once the runner has written their state, and
 * arrive as `run.updated`. Pairing the two is what makes Cancel usable, so the
 * store keeps the keys it asked for and claims the first run reported for each.
 */
interface PendingJob {
  jobId: string
  workspaceId: string
  /** The keys still waiting for a run of their own. */
  keys: Set<string>
  /** When the job was asked for, so an older run for the same key is ignored. */
  startedAt: number
}

/**
 * How far before the request a run may claim to have started and still be
 * taken for this job's. The service writes the timestamp after answering, so
 * the allowance is only there to absorb a clock that ticks the other way.
 */
const CLOCK_GRACE_MS = 2000

export interface AppStore {
  subscribe(listener: () => void): () => void
  getState(): AppState
  init(): Promise<void>
  setWorkspace(id: string): void
  navigate(screen: Screen): void
  /**
   * Opens what a deep link names, in one step. `setWorkspace` sends the window
   * back to the board, which is right for the switcher and wrong for a link
   * that names both a workspace and a screen inside it.
   */
  openRoute(screen: Screen, workspaceId?: string): void
  /**
   * Each start answers with the job id the service gave it, or '' when
   * nothing was started (no workspace, no key — both toasted). A screen that
   * wants the run the job produces watches `lib/jobs` for a run paired with
   * that id, which is how New session opens the session it just started.
   */
  startTriage(keys: string[], opts?: TriageOptions): Promise<string>
  startRCA(key: string, opts?: RCAOptions): Promise<string>
  startFix(key: string, opts?: FixOptions): Promise<string>
  startEval(keys?: string[], opts?: EvalOptions): Promise<void>
  /** Stops a job this window started, by id. */
  cancelJob(jobId: string): Promise<void>
  refresh(): Promise<void>
  toast(text: string, tone?: Toast['tone']): void
  dismissToast(id: number): void
  dispose(): void
}

const WORKSPACE_KEY = 'sirdar.workspaceId'

/** localStorage is absent in tests and can throw in a locked-down webview. */
function readStoredWorkspace(): string {
  try {
    return globalThis.localStorage?.getItem(WORKSPACE_KEY) ?? ''
  } catch {
    return ''
  }
}

function writeStoredWorkspace(id: string): void {
  try {
    globalThis.localStorage?.setItem(WORKSPACE_KEY, id)
  } catch {
    // A workspace that cannot be remembered is still usable this session.
  }
}

/**
 * The queue endpoint answers 501 for a workspace with no tracker configured.
 * Both transports surface that as an Error, so match on what they can carry:
 * the status line from the HTTP client or the `ErrUnsupported` code from the
 * JSON error body.
 */
export function isQueueUnsupported(err: unknown): boolean {
  return /\b501\b|unsupported|not implemented/i.test(reasonOf(err ?? ''))
}

/** What a delivery's toast says: who sent it, about what, and what came of it. */
export function inboundText(entry: { source: string; key: string; outcome: HookOutcome }): string {
  const what = entry.key ? `${entry.source} · ${entry.key}` : entry.source
  switch (entry.outcome) {
    case 'started':
      return `Webhook from ${what} started a triage.`
    case 'skipped':
      return `Webhook from ${what} was skipped; that key is busy or in its cooldown.`
    case 'filtered':
      return `Webhook from ${what} did not match this workspace's filter.`
    case 'ignored':
      return `Webhook from ${entry.source} named no ticket.`
    default:
      return `Webhook from ${what} was rejected.`
  }
}

/** The reason for a toast; a rejection that says nothing still says something. */
function errorText(err: unknown): string {
  return reasonOf(err ?? '') || 'Something went wrong'
}

/** Replaces the entry with the same runId, or prepends it when it is new. */
export function upsertRun(runs: RunSummary[], run: RunSummary): RunSummary[] {
  const index = runs.findIndex((r) => r.runId === run.runId)
  if (index < 0) return [run, ...runs]
  const next = runs.slice()
  next[index] = run
  return next
}

/** One quota reading per provider; the newest observation wins. */
export function upsertQuota(quota: Quota[], entry: Quota): Quota[] {
  const index = quota.findIndex((q) => q.provider === entry.provider)
  if (index < 0) return [...quota, entry]
  const next = quota.slice()
  next[index] = entry
  return next
}

/**
 * A failed start is reported twice on purpose: the toast tells the window, and
 * the rejection tells the form that asked. Swallowing it here left every form's
 * `error` prop permanently empty — a fix refused for a note nobody approved
 * would toast once and leave the dialog looking as though it had worked.
 */
export function createAppStore(transport: Transport): AppStore {
  let state: AppState = {
    transport,
    workspaces: [],
    currentWorkspaceId: '',
    screen: { name: 'board' },
    runsByWorkspace: {},
    ticketsByWorkspace: {},
    sourcesByWorkspace: {},
    queueUnsupported: {},
    quota: [],
    toasts: [],
    inbound: [],
    keylessJobs: [],
    loading: true,
    liveUpdates: 'live',
  }

  const listeners = new Set<() => void>()
  const pending: PendingJob[] = []
  let unsubscribe: (() => void) | null = null
  let toastSeq = 0
  let inboundSeq = 0
  let disposed = false
  let started = false

  function emit(): void {
    for (const listener of listeners) listener()
  }

  function set(patch: Partial<AppState>): void {
    state = { ...state, ...patch }
    emit()
  }

  function toast(text: string, tone: Toast['tone'] = 'info'): void {
    toastSeq += 1
    const entry: Toast = { id: toastSeq, tone, text }
    set({ toasts: [...state.toasts, entry] })
  }

  function dismissToast(id: number): void {
    set({ toasts: state.toasts.filter((t) => t.id !== id) })
  }

  async function loadRuns(workspaceId: string): Promise<void> {
    try {
      const runs = await transport.runs(workspaceId)
      if (disposed) return
      set({ runsByWorkspace: { ...state.runsByWorkspace, [workspaceId]: runs } })
    } catch (err) {
      if (!disposed) toast(`Could not load runs. ${errorText(err)}`, 'error')
    }
  }

  async function loadQueue(workspaceId: string): Promise<void> {
    try {
      const tickets = await transport.queue(workspaceId)
      if (disposed) return
      set({
        ticketsByWorkspace: { ...state.ticketsByWorkspace, [workspaceId]: tickets },
        queueUnsupported: { ...state.queueUnsupported, [workspaceId]: false },
      })
    } catch (err) {
      if (disposed) return
      if (isQueueUnsupported(err)) {
        set({
          ticketsByWorkspace: { ...state.ticketsByWorkspace, [workspaceId]: [] },
          queueUnsupported: { ...state.queueUnsupported, [workspaceId]: true },
        })
        return
      }
      toast(`Could not load the queue. ${errorText(err)}`, 'error')
    }
  }

  async function loadQuota(): Promise<void> {
    try {
      const quota = await transport.quota()
      if (disposed) return
      set({ quota })
    } catch {
      // Quota is advisory; a workspace with no recorded rate limits has none.
    }
  }

  /**
   * The sources block of the config summary. A summary that cannot be read
   * is not reported here: the lists fall back to bare numbers, and Settings
   * is where the reason is shown.
   */
  async function loadSources(workspaceId: string): Promise<void> {
    try {
      const summary = await transport.configSummary(workspaceId)
      if (disposed) return
      set({
        sourcesByWorkspace: { ...state.sourcesByWorkspace, [workspaceId]: summary.sources ?? {} },
      })
    } catch {
      // Left absent on purpose.
    }
  }

  async function loadWorkspace(workspaceId: string): Promise<void> {
    if (!workspaceId) return
    await Promise.all([loadRuns(workspaceId), loadQueue(workspaceId), loadSources(workspaceId)])
  }

  /**
   * Remembers a job until every key it was given has a run, so each run can
   * be cancelled from its own screen. A job given a label is also kept on the
   * state, for the screen that started it to cancel as a whole: every eval,
   * whether it names keys or not.
   */
  function track(
    jobId: string,
    workspaceId: string,
    keys: string[],
    startedAt: number,
    label?: string,
  ): void {
    if (!jobId) return
    if (label !== undefined) {
      set({ keylessJobs: [...state.keylessJobs, { jobId, workspaceId, label }] })
    }
    if (keys.length > 0) pending.push({ jobId, workspaceId, keys: new Set(keys), startedAt })
  }

  /** The Eval screen's name for a job: what it was run over. */
  function evalLabel(keys: string[] | undefined): string {
    if (!keys || keys.length === 0) return 'Eval of the whole golden set'
    return keys.length === 1 ? `Eval of ${keys[0]}` : `Eval of ${keys.length} keys`
  }

  /**
   * Reads the workspace and the quota again after the stream was down: an
   * event that fired in between was never delivered, and a card left on
   * "running" for a run that finished is the wrong thing to show.
   */
  function resync(): void {
    void Promise.all([loadWorkspace(state.currentWorkspaceId), loadQuota()])
  }

  /**
   * Pairs a newly reported run with the job that asked for it: the oldest
   * pending job for this workspace that is still waiting on the run's key.
   */
  function claim(workspaceId: string, run: RunSummary): void {
    const startedAt = parseTime(run.startedAt)
    for (const job of pending) {
      if (job.workspaceId !== workspaceId) continue
      if (!job.keys.has(run.key)) continue
      if (!Number.isNaN(startedAt) && startedAt < job.startedAt - CLOCK_GRACE_MS) continue
      job.keys.delete(run.key)
      setRunJob(run.runId, job.jobId)
      return
    }
  }

  /** Forgets a finished job: its id is no longer one the service will cancel. */
  function release(jobId: string): void {
    const at = pending.findIndex((job) => job.jobId === jobId)
    if (at >= 0) pending.splice(at, 1)
    if (state.keylessJobs.some((job) => job.jobId === jobId)) {
      set({ keylessJobs: state.keylessJobs.filter((job) => job.jobId !== jobId) })
    }
    clearJob(jobId)
  }

  function apply(event: AppEvent): void {
    if (disposed) return
    switch (event.kind) {
      case 'run.updated': {
        claim(event.workspaceId, event.run)
        const existing = state.runsByWorkspace[event.workspaceId] ?? []
        set({
          runsByWorkspace: {
            ...state.runsByWorkspace,
            [event.workspaceId]: upsertRun(existing, event.run),
          },
        })
        return
      }
      case 'quota.updated': {
        set({ quota: upsertQuota(state.quota, event.quota) })
        return
      }
      case 'job.finished': {
        release(event.jobId)
        const done = event.outcomes ?? []
        const failed = done.filter(
          (o) => o.status === 'failed' || o.status === 'over_budget',
        ).length
        const summary =
          done.length === 0
            ? 'Job finished.'
            : failed > 0
              ? `Finished ${done.length} ${done.length === 1 ? 'run' : 'runs'}, ${failed} failed.`
              : `Finished ${done.length} ${done.length === 1 ? 'run' : 'runs'}.`
        toast(summary, failed > 0 ? 'error' : 'info')
        void loadRuns(event.workspaceId)
        return
      }
      case 'hook.received': {
        inboundSeq += 1
        const entry: InboundDelivery = {
          id: inboundSeq,
          source: event.source,
          key: event.key ?? '',
          outcome: event.outcome,
          at: new Date().toISOString(),
        }
        set({ inbound: [entry, ...state.inbound].slice(0, INBOUND_LIMIT) })
        toast(inboundText(entry), event.outcome === 'rejected' ? 'error' : 'info')
        return
      }
      case 'live': {
        // The browser retries a dropped stream by itself and reports every
        // attempt; only the change of state is worth a word.
        if (event.state === 'lost' && state.liveUpdates !== 'lost') {
          set({ liveUpdates: 'lost' })
          toast('Live updates lost; reconnecting.', 'error')
        } else if (event.state === 'open' && state.liveUpdates === 'lost') {
          set({ liveUpdates: 'live' })
          toast('Live updates are back.')
          resync()
        }
        return
      }
      // `run.event` is a per-line firehose; Run detail subscribes for itself.
      default:
        return
    }
  }

  return {
    subscribe(listener) {
      listeners.add(listener)
      return () => {
        listeners.delete(listener)
      }
    },

    getState() {
      return state
    },

    async init() {
      // StrictMode mounts effects twice in development; one load is enough.
      if (started) return
      started = true
      // Subscribed before anything is read, so a workspace list that cannot
      // be read leaves the stream up, and an event that lands during the
      // first read is not lost to it. One subscription outlives a retry.
      unsubscribe ??= transport.subscribe(apply)
      let workspaces: Workspace[] = []
      try {
        workspaces = await transport.workspaces()
      } catch (err) {
        // The latch is let go so the next init() can try again, which is
        // what the board's own retry does.
        started = false
        if (!disposed) {
          set({ loading: false })
          toast(`Could not load workspaces. ${errorText(err)}`, 'error')
        }
        return
      }
      if (disposed) return

      const stored = readStoredWorkspace()
      const current =
        workspaces.find((w) => w.id === stored)?.id ?? workspaces[0]?.id ?? ''
      set({ workspaces, currentWorkspaceId: current, loading: false })
      if (current) writeStoredWorkspace(current)

      await Promise.all([loadWorkspace(current), loadQuota()])
    },

    setWorkspace(id) {
      if (!id || id === state.currentWorkspaceId) return
      writeStoredWorkspace(id)
      set({ currentWorkspaceId: id, screen: { name: 'board' } })
      void loadWorkspace(id)
    },

    navigate(screen) {
      set({ screen })
    },

    openRoute(screen, workspaceId) {
      // A link into a workspace this install does not have is followed as far
      // as it can be: the screen opens on whichever workspace is current.
      const known = workspaceId && state.workspaces.some((w) => w.id === workspaceId)
      const next = known ? (workspaceId as string) : state.currentWorkspaceId
      const moved = next !== state.currentWorkspaceId
      if (moved) writeStoredWorkspace(next)
      set({ currentWorkspaceId: next, screen })
      if (moved) void loadWorkspace(next)
    },

    async startTriage(keys, opts) {
      const workspaceId = state.currentWorkspaceId
      if (!workspaceId) {
        toast('Add a workspace before starting a run.', 'error')
        return ''
      }
      if (keys.length === 0) {
        toast('Enter at least one ticket key.', 'error')
        return ''
      }
      const askedAt = Date.now()
      try {
        const started = await transport.startTriage(workspaceId, keys, opts)
        if (disposed) return ''
        const jobId = started?.jobId ?? ''
        track(jobId, workspaceId, keys, askedAt)
        toast(`Triage started for ${keys.length === 1 ? keys[0] : `${keys.length} keys`}.`)
        void loadRuns(workspaceId)
        return jobId
      } catch (err) {
        if (!disposed) toast(`Triage did not start. ${errorText(err)}`, 'error')
        throw err
      }
    },

    async startFix(key, opts) {
      const workspaceId = state.currentWorkspaceId
      if (!workspaceId) {
        toast('Add a workspace before starting a run.', 'error')
        return ''
      }
      if (!key) {
        toast('Enter a ticket key.', 'error')
        return ''
      }
      const askedAt = Date.now()
      try {
        const started = await transport.startFix(workspaceId, key, opts)
        if (disposed) return ''
        const jobId = started?.jobId ?? ''
        track(jobId, workspaceId, [key], askedAt)
        toast(
          opts?.acceptDeviation
            ? `Publishing the reviewed commit for ${key}.`
            : `Fix started for ${key}.`,
        )
        void loadRuns(workspaceId)
        return jobId
      } catch (err) {
        if (!disposed) toast(`Fix did not start. ${errorText(err)}`, 'error')
        throw err
      }
    },

    async startEval(keys, opts) {
      const workspaceId = state.currentWorkspaceId
      if (!workspaceId) {
        toast('Add a workspace before starting a run.', 'error')
        return
      }
      const askedAt = Date.now()
      try {
        const started = await transport.startEval(workspaceId, keys, opts)
        if (disposed) return
        track(started?.jobId ?? '', workspaceId, keys ?? [], askedAt, evalLabel(keys))
        toast(
          keys && keys.length > 0
            ? `Eval started for ${keys.length === 1 ? keys[0] : `${keys.length} keys`}.`
            : 'Eval started for the whole golden set.',
        )
        void loadRuns(workspaceId)
      } catch (err) {
        if (!disposed) toast(`Eval did not start. ${errorText(err)}`, 'error')
        throw err
      }
    },

    async startRCA(key, opts) {
      const workspaceId = state.currentWorkspaceId
      if (!workspaceId) {
        toast('Add a workspace before starting a run.', 'error')
        return ''
      }
      if (!key) {
        toast('Enter a ticket key.', 'error')
        return ''
      }
      const askedAt = Date.now()
      try {
        const started = await transport.startRCA(workspaceId, key, opts)
        if (disposed) return ''
        const jobId = started?.jobId ?? ''
        track(jobId, workspaceId, [key], askedAt)
        toast(`Root cause analysis started for ${key}.`)
        void loadRuns(workspaceId)
        return jobId
      } catch (err) {
        if (!disposed) toast(`Root cause analysis did not start. ${errorText(err)}`, 'error')
        throw err
      }
    },

    async cancelJob(jobId) {
      if (!jobId) return
      try {
        await transport.cancel(jobId)
        if (disposed) return
        // The job's own `job.finished` will arrive and release it; dropping
        // it here as well keeps the button from lingering if it does not.
        release(jobId)
      } catch (err) {
        if (!disposed) toast(`Could not cancel the job. ${errorText(err)}`, 'error')
        throw err
      }
    },

    async refresh() {
      await Promise.all([loadWorkspace(state.currentWorkspaceId), loadQuota()])
    },

    toast,
    dismissToast,

    dispose() {
      disposed = true
      pending.length = 0
      unsubscribe?.()
      unsubscribe = null
      listeners.clear()
    },
  }
}
