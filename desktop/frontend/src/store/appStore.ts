import type {
  AppEvent,
  Quota,
  RunSummary,
  Ticket,
  Transport,
  Workspace,
} from '../api/types'

/** Which screen the window is showing. Run detail carries the run it opened. */
export type Screen =
  | { name: 'board' }
  | { name: 'run'; runId: string }
  | { name: 'register' }
  | { name: 'settings' }

export interface Toast {
  id: number
  tone: 'info' | 'error'
  text: string
}

export interface AppState {
  transport: Transport
  workspaces: Workspace[]
  currentWorkspaceId: string
  screen: Screen
  runsByWorkspace: Record<string, RunSummary[]>
  ticketsByWorkspace: Record<string, Ticket[]>
  /** Workspaces whose tracker cannot list a queue (the API answers 501). */
  queueUnsupported: Record<string, boolean>
  quota: Quota[]
  toasts: Toast[]
  /** True until `init()` has finished its first pass, so the board can say so. */
  loading: boolean
}

export interface TriageOptions {
  provider?: string
  model?: string
  dryRun?: boolean
}

export interface AppStore {
  subscribe(listener: () => void): () => void
  getState(): AppState
  init(): Promise<void>
  setWorkspace(id: string): void
  navigate(screen: Screen): void
  startTriage(keys: string[], opts?: TriageOptions): Promise<void>
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
  const message = err instanceof Error ? err.message : String(err ?? '')
  return /\b501\b|unsupported|not implemented/i.test(message)
}

function errorText(err: unknown): string {
  if (err instanceof Error && err.message) return err.message
  const text = String(err ?? '')
  return text || 'Something went wrong'
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

export function createAppStore(transport: Transport): AppStore {
  let state: AppState = {
    transport,
    workspaces: [],
    currentWorkspaceId: '',
    screen: { name: 'board' },
    runsByWorkspace: {},
    ticketsByWorkspace: {},
    queueUnsupported: {},
    quota: [],
    toasts: [],
    loading: true,
  }

  const listeners = new Set<() => void>()
  let unsubscribe: (() => void) | null = null
  let toastSeq = 0
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

  async function loadWorkspace(workspaceId: string): Promise<void> {
    if (!workspaceId) return
    await Promise.all([loadRuns(workspaceId), loadQueue(workspaceId)])
  }

  function apply(event: AppEvent): void {
    if (disposed) return
    switch (event.kind) {
      case 'run.updated': {
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
      let workspaces: Workspace[] = []
      try {
        workspaces = await transport.workspaces()
      } catch (err) {
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

      unsubscribe = transport.subscribe(apply)
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

    async startTriage(keys, opts) {
      const workspaceId = state.currentWorkspaceId
      if (!workspaceId) {
        toast('Add a workspace before starting a run.', 'error')
        return
      }
      if (keys.length === 0) {
        toast('Enter at least one ticket key.', 'error')
        return
      }
      try {
        await transport.startTriage(workspaceId, keys, opts)
        if (disposed) return
        toast(`Triage started for ${keys.length === 1 ? keys[0] : `${keys.length} keys`}.`)
        void loadRuns(workspaceId)
      } catch (err) {
        if (!disposed) toast(`Triage did not start. ${errorText(err)}`, 'error')
      }
    },

    async refresh() {
      await Promise.all([loadWorkspace(state.currentWorkspaceId), loadQuota()])
    },

    toast,
    dismissToast,

    dispose() {
      disposed = true
      unsubscribe?.()
      unsubscribe = null
      listeners.clear()
    },
  }
}
