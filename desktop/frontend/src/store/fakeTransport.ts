import type {
  AppEvent,
  Check,
  ComposedIntent,
  ConfigSummary,
  DropHunkRequest,
  EvalReport,
  RetroReport,
  GoldenEntry,
  MCPCallResult,
  MCPInventory,
  MCPToolList,
  PlaybookSummary,
  Quota,
  RegisterRow,
  RunDetail,
  RunDiff,
  RunEvent,
  RunSummary,
  SearchHit,
  HelpdeskLink,
  Ticket,
  Transport,
  Usage,
  Workspace,
} from '../api/types'
import type { SessionFixture } from './fakeSession'

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
  steer: { ws: string; runId: string; text: string; model: string }[]
  runDiff: { ws: string; runId: string }[]
  dropHunk: { ws: string; runId: string; req: DropHunkRequest }[]
  mcpServers: { ws: string; connect: boolean }[]
  mcpTools: { ws: string; server: string }[]
  mcpCall: { ws: string; server: string; tool: string; args?: unknown }[]
  deleteRun: { ws: string; runId: string }[]
  search: { ws: string; q: string }[]
  savePlaybook: { ws: string; name: string; body: string }[]
  addPlaybook: { ws: string; name: string; body: string }[]
  deletePlaybook: { ws: string; name: string }[]
  openPlaybook: { ws: string; name: string }[]
  scaffoldPlaybooks: string[]
  resolveHelpdesk: { ws: string; number: string }[]
  composeIntent: { ws: string; text: string }[]
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
  /** Makes `deleteRun()` reject, e.g. with the 409 a live run gives. */
  failDelete(err: Error | null): void
  /** Makes `search()` reject. */
  failSearch(err: Error | null): void
  /** Makes every playbook call reject, e.g. with the 501 a workspace that keeps them outside .sirdar gives. */
  failPlaybooks(err: Error | null): void
  /** The playbooks the fake holds right now, as filename to markdown. */
  playbookBodies(): Record<string, string>
  subscriberCount(): number
}

/** A search hit as the service answers one: the note of the sample run, matched on "export". */
export function searchHit(over: Partial<SearchHit> = {}): SearchHit {
  return {
    runId: 'r1',
    key: 'OMNI-1',
    kind: 'triage',
    status: 'completed',
    source: 'note',
    path: '/repos/omni/notes/OMNI-1-triage.md',
    excerpt: '…the statement export times out on the second page of the report…',
    ...over,
  }
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

/**
 * Who the fake workspace's own credentials belong to: the account
 * `assignee: me` resolves to, the way `SelfOf` resolves it in the service.
 * It is what `queue(ws, { assignee: 'me' })` answers against.
 */
export const SELF = 'sri@acme.com'

/**
 * The service's rule for "is this person me", so the fake queue answers the
 * `me` filter the way a tracker would: case never matters, and a bare local
 * part matches the address it is the local part of.
 */
export function isSelf(assignee: string): boolean {
  const who = assignee.trim().toLowerCase()
  if (!who) return false
  return who === SELF || who === SELF.split('@')[0]
}

/**
 * A run as the service lists it. The title is empty by default, the way a
 * run whose bundle and note have gone reads, so a test that wants the card's
 * titled shape passes one in. The assignee is the reader's own, since the
 * sample workspace is the reader's; a test about somebody else's run passes
 * `assignee` and `mine` in. The helpdesk key is empty too, the way a
 * tracker-only ticket reads; a test about the number preference passes one.
 */
export function run(over: Partial<RunSummary> = {}): RunSummary {
  return {
    runId: 'r1',
    key: 'OMNI-1',
    helpdeskKey: '',
    title: '',
    kind: 'triage',
    status: 'completed',
    provider: 'claude',
    model: 'sonnet',
    startedAt: '2026-09-10T09:00:00Z',
    updatedAt: '2026-09-10T09:04:00Z',
    reason: '',
    assignee: SELF,
    mine: true,
    usage: usage(),
    notes: [],
    ...over,
  }
}

export function ticket(over: Partial<Ticket> = {}): Ticket {
  return {
    key: 'OMNI-9',
    title: 'Statement export times out',
    type: 'bug',
    priority: 'P2',
    status: 'Open',
    assignee: 'sri',
    url: '',
    helpdeskRef: '',
    updatedAt: '2026-09-10T08:00:00Z',
    ...over,
  }
}

/** The name a new playbook must have, as internal/app spells it. */
const PLAYBOOK_NAME = /^[0-9]{2}-[a-z0-9-]+\.md$/

/** The playbooks a scaffolded workspace has, as filename to markdown. */
export const SAMPLE_PLAYBOOKS: Record<string, string> = {
  '10-helpdesk.md':
    '# Helpdesk\n\nThe helpdesk thread is the customer’s own words. Read all of it before the tracker ticket.\n',
  '20-logs.md':
    '# Logs\n\nLoki keeps 30 days. A query with no result over a longer window proves nothing.\n',
}

/** One playbook's row, derived from its body the way the service derives it. */
export function playbookRow(name: string, body: string): PlaybookSummary {
  const lines = body.split('\n').map((l) => l.trim())
  const heading = lines.find((l) => l.startsWith('# '))
  const lede = lines.find((l) => l !== '' && !l.startsWith('#') && !l.startsWith('- ')) ?? ''
  return {
    name,
    file: `.sirdar/playbooks/${name}`,
    title: heading ? heading.slice(2).trim() : name.replace(/\.md$/, ''),
    lede: lede.length > 160 ? `${lede.slice(0, 160)}…` : lede,
    order: /^[0-9]{2}-/.test(name) ? name.slice(0, 2) : '',
    bytes: body.length,
    modifiedAt: '2026-09-16T09:12:00Z',
  }
}

/** A workspace that notifies nowhere and serves no inbound hooks. */
export function emptyConfigSummary(): ConfigSummary {
  return {
    ...configSummary(),
    notify: { enabled: false, on: [], includeTitle: false, destinations: [] },
    webhooks: { enabled: false, cooldown: '10m0s', match: {}, sources: [] },
  }
}

/** The config summary as the sample workspace's config.yaml reads: every page of Settings has a value. */
export function configSummary(over: Partial<ConfigSummary> = {}): ConfigSummary {
  return {
    general: {
      workspace: 'omni',
      root: '/repos/omni',
      configPath: '/repos/omni/.sirdar/config.yaml',
      provider: 'claude',
      model: 'sonnet',
      billing: 'subscription',
      notesLanguage: 'en',
      customerLanguage: 'auto',
      rtlMarkup: true,
    },
    budget: { maxTurns: 20, maxMinutes: 20, maxUsd: 2, stallMinutes: 6 },
    permissions: {
      bash: ['git status*', 'git diff*', 'git log*'],
      fixBash: ['git status*', 'git diff*', 'npm test*'],
      fetch: [],
      readAlso: [],
      mcp: ['mcp__filesystem__read_*', 'mcp__filesystem__list_*'],
    },
    notes: {
      dir: '/repos/omni/notes',
      filenames: {
        triage: '{{key}}-triage.md',
        rca: '{{key}}-rca.md',
        resolution: '{{key}}-resolution.md',
      },
    },
    mcp: { workspaceOnly: true },
    notify: {
      enabled: true,
      on: ['completed', 'failed'],
      includeTitle: false,
      destinations: [{ type: 'slack', credential: 'env' }],
    },
    webhooks: {
      enabled: true,
      cooldown: '10m0s',
      match: { assignee: 'me' },
      sources: [{ name: 'jira', auth: 'secret', credential: 'keychain' }],
    },
    sources: {
      tracker: { adapter: 'jira', name: 'Jira', host: 'acme.atlassian.net' },
      helpdesk: { adapter: 'zohodesk', name: 'Zoho Desk', host: 'desk.zoho.com' },
    },
    me: { email: 'sri@acme.com', names: ['Sri Venkateswaran', 'sri'], source: 'me' },
    ...over,
  }
}

/** An in-memory Transport for tests; every method resolves. */

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
  /** What `search()` picks from: the hits whose excerpt contains the query, case folded. */
  hits?: SearchHit[]
  /** What `resolveHelpdesk()` answers, by helpdesk number; a number not named here has no tracker issue. */
  helpdesk?: Record<string, HelpdeskLink>
  /** What `composeIntent()` answers. Absent makes the call reject, as a workspace with no provider does. */
  composed?: ComposedIntent
  /**
   * The playbooks the workspace has, as filename to markdown. `{}` is the
   * empty state; leaving it out gives the two-file sample set.
   */
  playbooks?: Record<string, string>
  /**
   * Whole runs by run id — the detail, the log, the note, the prompt and
   * the change — for the session screen: `run`, `events`, `note`, `prompt`
   * and `runDiff` answer from the fixture when asked for one of these ids.
   * `store/fakeSession.ts` builds them.
   */
  sessions?: Record<string, SessionFixture>
} = {}): FakeTransport {
  let runList = seed.runs ?? []
  let ticketList = seed.tickets ?? []
  let queueError: Error | null = null
  let deleteError: Error | null = null
  let searchError: Error | null = null
  let playbookError: Error | null = null
  const playbookBodies: Record<string, string> = { ...(seed.playbooks ?? SAMPLE_PLAYBOOKS) }
  let currentDiff: RunDiff | null = seed.diff === undefined ? diff() : seed.diff
  /** A fixture run's change, edited in place by `dropHunk` like the shared one. */
  const sessionDiffs = new Map<string, RunDiff | null>(
    Object.entries(seed.sessions ?? {}).map(([runId, f]) => [runId, f.diff]),
  )
  const diffFor = (runId: string): RunDiff | null =>
    sessionDiffs.has(runId) ? (sessionDiffs.get(runId) as RunDiff | null) : currentDiff
  const setDiffFor = (runId: string, next: RunDiff) => {
    if (sessionDiffs.has(runId)) sessionDiffs.set(runId, next)
    else currentDiff = next
  }
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
    deleteRun: [],
    search: [],
    savePlaybook: [],
    addPlaybook: [],
    deletePlaybook: [],
    openPlaybook: [],
    scaffoldPlaybooks: [],
    resolveHelpdesk: [],
    composeIntent: [],
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
    failDelete(err) {
      deleteError = err
    },
    failSearch(err) {
      searchError = err
    },
    failPlaybooks(err) {
      playbookError = err
    },
    playbookBodies: () => ({ ...playbookBodies }),
    subscriberCount: () => handlers.size,

    workspaces: async () => seed.workspaces ?? [workspace()],
    addWorkspace: async (root) => workspace({ id: 'ws-new', root }),
    removeWorkspace: async () => {},
    resolveHelpdesk: async (ws, number) => {
      calls.resolveHelpdesk.push({ ws, number })
      return (
        seed.helpdesk?.[number] ?? {
          number,
          key: '',
          reason: `the helpdesk record for ${number} names no tracker issue`,
        }
      )
    },
    composeIntent: async (ws, text) => {
      calls.composeIntent.push({ ws, text })
      if (!seed.composed) throw new Error('this workspace has no provider to ask')
      return seed.composed
    },
    queue: async (ws, filter) => {
      calls.queue.push(ws)
      if (queueError) throw queueError
      // The one filter the tracker really applies here: `me` narrows the
      // queue to the reader's own keys, which is what the board's Queue lane
      // asks for.
      if (filter?.assignee === 'me') return ticketList.filter((t) => isSelf(t.assignee))
      return ticketList
    },
    runs: async (ws) => {
      calls.runs.push(ws)
      return runList
    },
    run: async (_ws, runId) => {
      const fixture = seed.sessions?.[runId]
      if (fixture) return fixture.detail
      return {
        ...run({ runId }),
        promptPath: '',
        bundleDir: '',
        warnings: [],
        handle: '',
        budget: { maxTurns: 20, maxMinutes: 20, maxUsd: 2 },
      } as RunDetail
    },
    deleteRun: async (ws, runId) => {
      calls.deleteRun.push({ ws, runId })
      if (deleteError) throw deleteError
      const target = runList.find((r) => r.runId === runId)
      if (!target) throw new Error(`not_found: no run ${runId}`)
      if (target.status === 'preparing' || target.status === 'running') {
        throw new Error(`conflict: run is live: ${runId} is ${target.status}`)
      }
      runList = runList.filter((r) => r.runId !== runId)
      // The service publishes run.removed once the directory is gone.
      fake.emit({ kind: 'run.removed', workspaceId: ws, runId })
    },
    search: async (ws, q) => {
      calls.search.push({ ws, q })
      if (searchError) throw searchError
      const needle = q.trim().toLowerCase()
      if (!needle) return []
      return (seed.hits ?? []).filter((h) => h.excerpt.toLowerCase().includes(needle))
    },
    events: async (_ws, runId) => {
      const fixture = seed.sessions?.[runId]
      if (fixture) return { events: fixture.events, next: fixture.events.length }
      return { events: [] as RunEvent[], next: 0 }
    },
    note: async (_ws, runId, kind) => {
      const fixture = seed.sessions?.[runId]
      if (!fixture) return ''
      // A note the run did not write, or a kind it does not have, is the
      // service's not-found — the ordinary answer for a fix run.
      const mismatch = (kind === 'rca' || kind === 'resolution') && fixture.detail.kind !== 'rca'
      if (!fixture.note || mismatch) throw new Error(`not_found: no ${kind || 'note'} for ${runId}`)
      return fixture.note
    },
    prompt: async (_ws, runId) => seed.sessions?.[runId]?.prompt ?? '',
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
    playbooks: async () => {
      if (playbookError) throw playbookError
      return Object.keys(playbookBodies)
        .sort()
        .map((name) => playbookRow(name, playbookBodies[name]))
    },
    playbook: async (_ws, name) => {
      if (playbookError) throw playbookError
      const body = playbookBodies[name]
      if (body === undefined) throw new Error(`not_found: no playbook named ${name}`)
      return body
    },
    savePlaybook: async (ws, name, body) => {
      calls.savePlaybook.push({ ws, name, body })
      if (playbookError) throw playbookError
      if (playbookBodies[name] === undefined && !PLAYBOOK_NAME.test(name)) {
        throw new Error(`not_found: ${name} is not a playbook filename`)
      }
      playbookBodies[name] = body
      return playbookRow(name, body)
    },
    addPlaybook: async (ws, name, body) => {
      calls.addPlaybook.push({ ws, name, body })
      if (playbookError) throw playbookError
      if (!PLAYBOOK_NAME.test(name)) {
        throw new Error('bad_request: name must be two digits, a hyphen, a lowercase slug and .md')
      }
      if (playbookBodies[name] !== undefined) {
        throw new Error(`conflict: a playbook named ${name} is already there`)
      }
      playbookBodies[name] = body
      return playbookRow(name, body)
    },
    deletePlaybook: async (ws, name) => {
      calls.deletePlaybook.push({ ws, name })
      if (playbookError) throw playbookError
      if (playbookBodies[name] === undefined) throw new Error(`not_found: no playbook named ${name}`)
      delete playbookBodies[name]
    },
    scaffoldPlaybooks: async (ws) => {
      calls.scaffoldPlaybooks.push(ws)
      if (playbookError) throw playbookError
      // The service never overwrites a file that is already there.
      for (const [name, body] of Object.entries(SAMPLE_PLAYBOOKS)) {
        if (playbookBodies[name] === undefined) playbookBodies[name] = body
      }
      return Object.keys(playbookBodies)
        .sort()
        .map((name) => playbookRow(name, playbookBodies[name]))
    },
    openPlaybook: async (ws, name) => {
      calls.openPlaybook.push({ ws, name })
      if (playbookError) throw playbookError
      if (playbookBodies[name] === undefined) throw new Error(`not_found: no playbook named ${name}`)
    },
    configSummary: async () => seed.configSummary ?? emptyConfigSummary(),
    resume: async () => ({ jobId: 'job-resume' }),
    steer: async (ws, runId, text, model) => {
      calls.steer.push({ ws, runId, text, model: model ?? '' })
      if (!text.trim()) throw new Error('steer refused: the instruction is empty')
      return { jobId: `job-steer-${calls.steer.length}`, runId }
    },
    runDiff: async (ws, runId) => {
      calls.runDiff.push({ ws, runId })
      const current = diffFor(runId)
      if (!current) throw new Error('not_found: the run has no change to show')
      return current
    },
    dropHunk: async (ws, runId, req) => {
      calls.dropHunk.push({ ws, runId, req })
      const current = diffFor(runId)
      if (!current) throw new Error('not_found: the run has no change to show')
      if (req.etag !== current.etag) {
        throw new Error('conflict: the diff has changed since it was read; read it again')
      }
      if (current.pushed || !current.worktreePresent) {
        throw new Error('conflict: the change can no longer be edited')
      }
      // The hunk is gone: the file loses its lines and the etag moves, so a
      // second drop with the old etag is refused the way the service refuses it.
      dropSeq += 1
      const next: RunDiff = {
        ...current,
        files: current.files.map((f) =>
          f.path === req.path ? { ...f, additions: Math.max(0, f.additions - 3) } : f,
        ),
        patch: current.patch.replace(/@@[^\n]*\n(?:[^@][^\n]*\n)*/, ''),
        etag: `etag-${dropSeq + 1}`,
      }
      setDiffFor(runId, next)
      return next
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
