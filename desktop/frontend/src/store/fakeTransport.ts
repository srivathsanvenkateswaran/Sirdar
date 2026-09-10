import type {
  AppEvent,
  Check,
  Quota,
  RegisterRow,
  RunDetail,
  RunEvent,
  RunSummary,
  Ticket,
  Transport,
  Usage,
  Workspace,
} from '../api/types'

/** Calls the fake recorded, so a test can assert what the UI asked for. */
export interface TransportCalls {
  startTriage: { ws: string; keys: string[]; opts?: unknown }[]
  startRCA: { ws: string; key: string }[]
  runs: string[]
  queue: string[]
}

export interface FakeTransport extends Transport {
  calls: TransportCalls
  /** Pushes an event to every live subscriber, as the watcher would. */
  emit(event: AppEvent): void
  /** Swaps what the next `runs()` call answers with. */
  setRuns(runs: RunSummary[]): void
  setTickets(tickets: Ticket[]): void
  /** Makes `queue()` reject, e.g. with the 501 a tracker-less workspace gives. */
  failQueue(err: Error | null): void
  subscriberCount(): number
}

export function workspace(over: Partial<Workspace> = {}): Workspace {
  return {
    id: 'ws1',
    name: 'omni',
    root: '/repos/omni',
    provider: 'claude',
    model: 'sonnet',
    notesDir: 'notes',
    ...over,
  }
}

export function usage(over: Partial<Usage> = {}): Usage {
  return { turns: 3, inputTokens: 1200, outputTokens: 400, costUsd: 0.12, ...over }
}

export function run(over: Partial<RunSummary> = {}): RunSummary {
  return {
    runId: 'r1',
    key: 'OMNI-1',
    kind: 'triage',
    status: 'completed',
    provider: 'claude',
    model: 'sonnet',
    startedAt: '2026-09-10T09:00:00Z',
    updatedAt: '2026-09-10T09:04:00Z',
    reason: '',
    usage: usage(),
    notes: [],
    ...over,
  }
}

export function ticket(over: Partial<Ticket> = {}): Ticket {
  return {
    key: 'OMNI-9',
    title: 'Statement export times out',
    priority: 'P2',
    status: 'Open',
    assignee: 'sri',
    url: '',
    helpdeskRef: '',
    updatedAt: '2026-09-10T08:00:00Z',
    ...over,
  }
}

/** An in-memory Transport for tests; every method resolves. */
export function createFakeTransport(seed: {
  workspaces?: Workspace[]
  runs?: RunSummary[]
  tickets?: Ticket[]
  quota?: Quota[]
} = {}): FakeTransport {
  let runList = seed.runs ?? []
  let ticketList = seed.tickets ?? []
  let queueError: Error | null = null
  const handlers = new Set<(e: AppEvent) => void>()
  const calls: TransportCalls = { startTriage: [], startRCA: [], runs: [], queue: [] }

  const fake: FakeTransport = {
    calls,
    emit(event) {
      for (const handler of [...handlers]) handler(event)
    },
    setRuns(next) {
      runList = next
    },
    setTickets(next) {
      ticketList = next
    },
    failQueue(err) {
      queueError = err
    },
    subscriberCount: () => handlers.size,

    workspaces: async () => seed.workspaces ?? [workspace()],
    addWorkspace: async (root) => workspace({ id: 'ws-new', root }),
    removeWorkspace: async () => {},
    queue: async (ws) => {
      calls.queue.push(ws)
      if (queueError) throw queueError
      return ticketList
    },
    runs: async (ws) => {
      calls.runs.push(ws)
      return runList
    },
    run: async (_ws, runId) =>
      ({
        ...run({ runId }),
        promptPath: '',
        bundleDir: '',
        warnings: [],
        handle: '',
        budget: { maxTurns: 20, maxMinutes: 20, maxUsd: 2 },
      }) as RunDetail,
    events: async () => ({ events: [] as RunEvent[], next: 0 }),
    note: async () => '',
    prompt: async () => '',
    startTriage: async (ws, keys, opts) => {
      calls.startTriage.push({ ws, keys, opts })
      return { jobId: `job-${calls.startTriage.length}` }
    },
    startRCA: async (ws, key) => {
      calls.startRCA.push({ ws, key })
      return { jobId: 'job-rca' }
    },
    resume: async () => ({ jobId: 'job-resume' }),
    cancel: async () => {},
    register: async () => [] as RegisterRow[],
    doctor: async () => [] as Check[],
    quota: async () => seed.quota ?? [],
    subscribe: (handler) => {
      handlers.add(handler)
      return () => {
        handlers.delete(handler)
      }
    },
  }

  return fake
}
