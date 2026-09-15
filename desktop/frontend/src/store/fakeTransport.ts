import type {
  AppEvent,
  Check,
  ConfigSummary,
  DropHunkRequest,
  EvalReport,
  RetroReport,
  GoldenEntry,
  MCPCallResult,
  MCPInventory,
  MCPToolList,
  Quota,
  RegisterRow,
  RunDetail,
  RunDiff,
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
  cancel: string[]
  runs: string[]
  queue: string[]
  steer: { ws: string; runId: string; text: string }[]
  runDiff: { ws: string; runId: string }[]
  dropHunk: { ws: string; runId: string; req: DropHunkRequest }[]
  mcpServers: { ws: string; connect: boolean }[]
  mcpTools: { ws: string; server: string }[]
  mcpCall: { ws: string; server: string; tool: string; args?: unknown }[]
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
  /** Swaps what `runDiff()` answers with; `dropHunk()` edits it in place. */
  setDiff(diff: RunDiff | null): void
  subscriberCount(): number
}

/** The unified patch `diff()` answers with: two files, three hunks, the shape a review screen has to lay out. */
export const SAMPLE_PATCH = `diff --git a/internal/export/statement.go b/internal/export/statement.go
--- a/internal/export/statement.go
+++ b/internal/export/statement.go
@@ -41,7 +41,9 @@ func (e *Exporter) page(ctx context.Context, n int) error {
-	conn := e.pool.Get()
-	defer conn.Close()
+	conn, err := e.pool.Acquire(ctx)
+	if err != nil {
+		return err
+	}
+	defer conn.Release()
 	rows, err := conn.Query(ctx, statementPage, n)
@@ -88,3 +90,6 @@ func (e *Exporter) close() {
 	e.pool.Close()
+	if e.log != nil {
+		e.log.Printf("export: pool closed after %d pages", e.pages)
+	}
 }
diff --git a/internal/export/statement_test.go b/internal/export/statement_test.go
--- a/internal/export/statement_test.go
+++ b/internal/export/statement_test.go
@@ -12,0 +13,9 @@ func TestPageReleasesConnection(t *testing.T) {
+	pool := newFakePool(1)
+	e := &Exporter{pool: pool}
+	for n := 0; n < 4; n++ {
+		if err := e.page(context.Background(), n); err != nil {
+			t.Fatalf("page %d: %v", n, err)
+		}
+	}
+	if pool.open != 0 {
+		t.Fatalf("open connections after export = %d, want 0", pool.open)
`

/** A fix run's change as `runDiff()` answers it: editable, unpushed, in its worktree. */
export function diff(over: Partial<RunDiff> = {}): RunDiff {
  return {
    base: 'main',
    head: 'sirdar/OMNI-1-fix',
    branch: 'sirdar/OMNI-1-fix',
    worktree: '/repos/omni/.sirdar/worktrees/OMNI-1',
    worktreePresent: true,
    pushed: false,
    files: [
      { path: 'internal/export/statement.go', status: 'modified', additions: 9, deletions: 2 },
      { path: 'internal/export/statement_test.go', status: 'added', additions: 9, deletions: 0 },
    ],
    patch: SAMPLE_PATCH,
    etag: 'etag-1',
    ...over,
  }
}

/** The MCP servers a workspace lists: one stdio server that answers, one http server that does not. */
export function mcpInventory(over: Partial<MCPInventory> = {}): MCPInventory {
  return {
    servers: [
      {
        name: 'filesystem',
        scope: 'workspace',
        transport: 'stdio',
        command: 'npx',
        args: ['-y', '@modelcontextprotocol/server-filesystem', '/repos/omni'],
        source: '/repos/omni/.mcp.json',
        connected: true,
        tools: 3,
        tookMs: 412,
      },
      {
        name: 'zoho',
        scope: 'global',
        transport: 'http',
        url: 'https://mcp.example.test/zoho',
        headerKeys: ['Authorization'],
        source: '/Users/me/.claude.json',
        connected: false,
        error: 'dial tcp: connection refused',
      },
    ],
    warnings: [],
    workspaceOnly: false,
    permissions: ['mcp__filesystem__read_*', 'mcp__filesystem__list_*'],
    ...over,
  }
}

/** The tools one server lists, judged: two reads allowed, one write denied. */
export function mcpTools(server = 'filesystem', over: Partial<MCPToolList> = {}): MCPToolList {
  return {
    server,
    tools: [
      {
        name: 'read_file',
        fullName: `mcp__${server}__read_file`,
        description: 'Read the complete contents of a file',
        verdict: 'allowed',
        rule: 'permissions.mcp',
        reason: `matches mcp__${server}__read_*`,
      },
      {
        name: 'list_directory',
        fullName: `mcp__${server}__list_directory`,
        description: 'List the entries of a directory',
        verdict: 'allowed',
        rule: 'permissions.mcp',
        reason: `matches mcp__${server}__list_*`,
      },
      {
        name: 'write_file',
        fullName: `mcp__${server}__write_file`,
        description: 'Create or overwrite a file',
        verdict: 'denied',
        rule: 'name',
        reason: 'the tool name reads as a write and no permissions.mcp pattern allows it',
      },
    ],
    tookMs: 388,
    permissions: [`mcp__${server}__read_*`, `mcp__${server}__list_*`],
    ...over,
  }
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
  retro?: RetroReport | null
  configSummary?: ConfigSummary
  /** What `runDiff()` answers; `null` makes it reject as a run with no change does. */
  diff?: RunDiff | null
  mcp?: MCPInventory
  /** Per-server tool lists; a server not named here gets the sample list under its own name. */
  mcpTools?: Record<string, MCPToolList>
  /** What `register()` answers: the rows the workspace's register.jsonl holds. */
  register?: RegisterRow[]
} = {}): FakeTransport {
  let runList = seed.runs ?? []
  let ticketList = seed.tickets ?? []
  let queueError: Error | null = null
  let currentDiff: RunDiff | null = seed.diff === undefined ? diff() : seed.diff
  let dropSeq = 0
  const handlers = new Set<(e: AppEvent) => void>()
  const calls: TransportCalls = {
    startTriage: [],
    startRCA: [],
    startFix: [],
    startEval: [],
    addGolden: [],
    cancel: [],
    runs: [],
    queue: [],
    steer: [],
    runDiff: [],
    dropHunk: [],
    mcpServers: [],
    mcpTools: [],
    mcpCall: [],
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
    setDiff(next) {
      currentDiff = next
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
    latestRetro: async () => seed.retro ?? null,
    golden: async () => seed.golden ?? ([] as GoldenEntry[]),
    addGolden: async (ws, o) => {
      calls.addGolden.push({ ws, ...o })
      return { key: o.key ?? 'OMNI-1', dir: '/golden/OMNI-1', bundleDir: '/golden/OMNI-1/bundle', assertions: 0, hasExpectedNote: false }
    },
    configSummary: async () => seed.configSummary ?? emptyConfigSummary(),
    resume: async () => ({ jobId: 'job-resume' }),
    steer: async (ws, runId, text) => {
      calls.steer.push({ ws, runId, text })
      if (!text.trim()) throw new Error('steer refused: the instruction is empty')
      return { jobId: `job-steer-${calls.steer.length}`, runId }
    },
    runDiff: async (ws, runId) => {
      calls.runDiff.push({ ws, runId })
      if (!currentDiff) throw new Error('not_found: the run has no change to show')
      return currentDiff
    },
    dropHunk: async (ws, runId, req) => {
      calls.dropHunk.push({ ws, runId, req })
      if (!currentDiff) throw new Error('not_found: the run has no change to show')
      if (req.etag !== currentDiff.etag) {
        throw new Error('conflict: the diff has changed since it was read; read it again')
      }
      if (currentDiff.pushed || !currentDiff.worktreePresent) {
        throw new Error('conflict: the change can no longer be edited')
      }
      // The hunk is gone: the file loses its lines and the etag moves, so a
      // second drop with the old etag is refused the way the service refuses it.
      dropSeq += 1
      currentDiff = {
        ...currentDiff,
        files: currentDiff.files.map((f) =>
          f.path === req.path ? { ...f, additions: Math.max(0, f.additions - 3) } : f,
        ),
        patch: currentDiff.patch.replace(/@@[^\n]*\n(?:[^@][^\n]*\n)*/, ''),
        etag: `etag-${dropSeq + 1}`,
      }
      return currentDiff
    },
    mcpServers: async (ws, connect) => {
      calls.mcpServers.push({ ws, connect: Boolean(connect) })
      const inv = seed.mcp ?? mcpInventory()
      if (connect) return inv
      // Without --connect nothing was reached, so the connection fields are absent.
      return {
        ...inv,
        servers: inv.servers.map(({ connected, tools, tookMs, error, ...entry }) => {
          void connected
          void tools
          void tookMs
          void error
          return entry
        }),
      }
    },
    mcpTools: async (ws, server) => {
      calls.mcpTools.push({ ws, server })
      return seed.mcpTools?.[server] ?? mcpTools(server)
    },
    mcpCall: async (ws, server, tool, args) => {
      calls.mcpCall.push({ ws, server, tool, args })
      const listed = (seed.mcpTools?.[server] ?? mcpTools(server)).tools.find((t) => t.name === tool)
      if (!listed) throw new Error(`not_found: ${server} lists no tool named ${tool}`)
      if (listed.verdict === 'denied') {
        return {
          server,
          tool,
          verdict: 'denied',
          reason: listed.reason,
          tookMs: 0,
        } satisfies MCPCallResult
      }
      return {
        server,
        tool,
        verdict: 'allowed',
        reason: listed.reason,
        result: JSON.stringify({ tool, args: args ?? {}, ok: true }, null, 2),
        tookMs: 57,
      } satisfies MCPCallResult
    },
    cancel: async (jobId) => {
      calls.cancel.push(jobId)
    },
    register: async () => seed.register ?? ([] as RegisterRow[]),
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
