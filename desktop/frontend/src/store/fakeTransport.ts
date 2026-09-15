import type {
  AppEvent,
  Check,
  ConfigSummary,
  EvalReport,
  GoldenEntry,
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
  startRCA: { ws: string; key: string; opts?: unknown }[]
  startFix: { ws: string; key: string; opts?: unknown }[]
  startEval: { ws: string; keys?: string[]; opts?: unknown }[]
  addGolden: { ws: string; key?: string; runId?: string }[]
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
    billing: 'subscription',
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
/** A workspace that notifies nowhere and serves no inbound hooks. */
export function emptyConfigSummary(): ConfigSummary {
  return {
    notify: { enabled: false, on: [], includeTitle: false, destinations: [] },
    webhooks: { enabled: false, cooldown: '10m0s', match: {}, sources: [] },
  }
}

export function createFakeTransport(seed: {
  workspaces?: Workspace[]
  runs?: RunSummary[]
  tickets?: Ticket[]
  quota?: Quota[]
  golden?: GoldenEntry[]
  reports?: EvalReport[]
  configSummary?: ConfigSummary
} = {}): FakeTransport {
  let runList = seed.runs ?? []
  let ticketList = seed.tickets ?? []
  let queueError: Error | null = null
  const handlers = new Set<(e: AppEvent) => void>()
  const calls: TransportCalls = {
    startTriage: [],
    startRCA: [],
    startFix: [],
    startEval: [],
    addGolden: [],
    runs: [],
    queue: [],
  }

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
    startRCA: async (ws, key, opts) => {
      calls.startRCA.push({ ws, key, opts })
      return { jobId: 'job-rca' }
    },
    startFix: async (ws, key, opts) => {
      calls.startFix.push({ ws, key, opts })
      return { jobId: `job-fix-${calls.startFix.length}` }
    },
    startEval: async (ws, keys, opts) => {
      calls.startEval.push({ ws, keys, opts })
      return { jobId: 'job-eval' }
    },
    evalReports: async () => seed.reports ?? ([] as EvalReport[]),
    golden: async () => seed.golden ?? ([] as GoldenEntry[]),
    addGolden: async (ws, o) => {
      calls.addGolden.push({ ws, ...o })
      return { key: o.key ?? 'OMNI-1', dir: '/golden/OMNI-1', bundleDir: '/golden/OMNI-1/bundle', assertions: 0, hasExpectedNote: false }
    },
    configSummary: async () => seed.configSummary ?? emptyConfigSummary(),
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
