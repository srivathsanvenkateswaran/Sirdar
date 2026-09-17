export type RunState = 'preparing'|'running'|'completed'|'failed'|'blocked'|'over_budget';
export type RunKind = 'triage'|'rca'|'fix';
/** The providers a one-off override may name; '' is the workspace's own. */
export const PROVIDERS = ['claude', 'codex', 'openai', 'acp', 'qwen', 'cursor', 'agy'] as const;
export type Provider = (typeof PROVIDERS)[number];
/**
 * Which of a run's notes to read. The empty kind is the run's own note.md,
 * whatever the run's kind produced: it is how a fix run's note is reached,
 * since a fix run has no 'triage' note and asking it for one is a mismatch
 * the service refuses.
 */
export type NoteKind = ''|'triage'|'rca'|'resolution';
export interface Workspace { id: string; name: string; root: string; provider: Provider; model: string; notesDir: string; billing: string }
export interface Usage { turns: number; inputTokens: number; outputTokens: number; costUsd: number }
/**
 * `title` is the ticket's, read off the run directory by the service: the
 * bundle's tracker title or helpdesk subject, else the first note's own title,
 * else ''. A card shows it over the key; with no title the key is the title.
 * The wire always carries it; it is optional here so a literal built in a test
 * need not spell an empty one.
 *
 * `assignee` is who the bundle said the ticket belonged to when the run
 * gathered it, and `mine` says that person is the workspace's own account —
 * the same account `assignee: me` resolves to for a webhook filter or a queue
 * query. Both let a card draw an avatar and the board filter by owner without
 * a second call to the tracker. An `assignee` of '' draws nothing, and `mine`
 * is false for every run of a workspace whose credentials name nobody.
 *
 * `helpdeskKey` is the helpdesk's own number for the same ticket (`25312`),
 * read off the bundle: the helpdesk record's id, else the reference the
 * tracker carried. Absent or '' when the run has neither, in which case every
 * screen shows `key` under the tracker's mark whatever the reader's
 * "Sessions show" preference says.
 */
export interface RunSummary { runId: string; key: string; helpdeskKey?: string; title?: string; kind: RunKind; status: RunState; provider: string; model: string; startedAt: string; updatedAt: string; reason: string; assignee?: string; mine?: boolean; usage: Usage; notes: string[] }
export interface RunDetail extends RunSummary { promptPath: string; bundleDir: string; warnings: string[]; handle: string; budget: { maxTurns: number; maxMinutes: number; maxUsd: number }; fix?: FixInfo;
  /** What the operator asked for when they started the run, in their own words; absent when they asked for nothing in particular. */
  instruction?: string }
/**
 * Where a fix run's work went, read off the run's own state.json. `deviation`
 * is set when the agent reported doing something other than the note's
 * Proposed Fix: the commit is on the branch and has not been pushed until a
 * person reruns the fix with acceptDeviation.
 *
 * `pushed` is what says the work has left the machine, and is what the review
 * panel keys on. `prUrl` cannot do that job: a run started with `noPr`, and one
 * whose `gh` call failed, both push the branch and record no URL.
 */
export interface FixInfo { branch?: string; base?: string; commit?: string; prUrl?: string; pushed?: boolean; deviation?: string }
/**
 * A 'review' event's payload carries what a person did to the fix commit
 * after the session ended — `action: 'drop'` with the file and the 0-based
 * hunk index within that file. A 'steer' event carries the instruction as
 * `text` and how the run was continued (`resume` or `primed`). Every other
 * kind carries the agent's fields.
 *
 * An 'assistant_text' event says how its `text` joins the text around it:
 * `delta` is one fragment of a message the provider is still streaming, and
 * `replace` is the finished message, which stands in for the fragments that
 * preceded it rather than following them. A provider that reports a message
 * once sets neither.
 */
export interface RunEvent { t: string; kind: string; payload: { tool?: string; decision?: string; text?: string; turns?: number; costUsd?: number; raw?: unknown; model?: string; action?: string; path?: string; hunk?: number; continuation?: string; delta?: boolean; replace?: boolean } }
/** One file in a fix run's change. A renamed file is named by the path it now has. */
export interface DiffFile { path: string; status: 'added'|'modified'|'deleted'|'renamed'; additions: number; deletions: number }
/**
 * A fix run's change as a reviewer reads it. `worktreePresent` and `pushed`
 * are what decide whether a hunk can still be dropped: the change is readable
 * once the worktree is gone or the branch has been pushed, but not editable.
 *
 * `truncated` is set when the patch was cut at 2 MiB; the file list is whole
 * either way. `etag` hashes the patch and must be handed back to drop a hunk,
 * so an index read from one patch can never be applied to another.
 *
 * It is the body of `GET /api/workspaces/{id}/runs/{runId}/diff`, and of
 * `POST .../diff/drop`, which answers with the change as it stands after the
 * hunk it was given was reverted out of the commit. No screen reads it yet;
 * the shape is pinned here because the Go side's test pins it too.
 */
export interface RunDiff {
  base: string; head: string; branch: string;
  worktree: string; worktreePresent: boolean; pushed: boolean;
  files: DiffFile[]; patch: string; truncated?: boolean; etag: string
}
/** What `dropHunk` needs: the file, the 0-based hunk index within it, and the etag of the diff the index was read from. */
export interface DropHunkRequest { path: string; hunk: number; etag: string }
/**
 * What a steer answers with: the job that carries the session, and the run it
 * continues — the same id the caller passed, said back so a client that fired
 * the request off a list can tell which row to watch.
 */
export interface SteerStarted { jobId: string; runId: string }

// --- MCP inspection, mirrored from internal/app/mcp.go ---
/**
 * One configured MCP server as an operator reads it. The configuration half
 * is `internal/mcpclient.Entry`, which carries the rule that key names cross
 * and values never do; `connected` and the three after it are set only when
 * the listing was asked to connect.
 */
export interface MCPServer {
  name: string; scope: string; transport: string;
  command?: string; args?: string[]; url?: string;
  /** Names only: an Authorization header's value is the credential. */
  envKeys?: string[]; headerKeys?: string[];
  /** The file this entry was read from. */
  source: string;
  /** What an operator should know before trusting the row; empty when nothing. */
  note?: string;
  connected?: boolean; tools?: number; tookMs?: number;
  /** Why the connection failed, with the entry's own credentials taken out. */
  error?: string
}
/** The answer to "which MCP servers would a run here get", with the rule their tools are judged by. */
export interface MCPInventory { servers: MCPServer[]; warnings: string[]; workspaceOnly: boolean; permissions: string[] }
export type MCPVerdict = 'allowed' | 'denied'
/** One of a server's tools with the verdict a run would get for it. `fullName` is what permissions.mcp patterns are written against. */
export interface MCPTool { name: string; fullName: string; description?: string; verdict: MCPVerdict; rule: string; reason: string }
export interface MCPToolList { server: string; tools: MCPTool[]; tookMs: number; permissions: string[] }
/**
 * One hand-run tool call. `isError` is the server's own flag — the tool ran
 * and reported a failure — which is not the same as `error`, a call that did
 * not happen. `result` is the output flattened to text and capped at 64 KiB;
 * `truncated` says whether the cap bit.
 */
export interface MCPCallResult {
  server: string; tool: string; verdict: MCPVerdict; reason: string;
  result?: string; truncated?: boolean; isError?: boolean; error?: string; tookMs: number
}
/**
 * One place the sidebar's "Search notes" query was found: the run, which of
 * its files (`answer` is the agent's result.json, `note` a note), and a
 * line's worth of text around the first match, cut at 160 characters with an
 * ellipsis at each edge that was trimmed. `GET /api/workspaces/{id}/search?q=`
 * answers at most 50, newest run first.
 */
export interface SearchHit { runId: string; key: string; kind: string; status: string; source: 'answer'|'note'; path: string; excerpt: string }
/** `type` is the canonical ticket type — 'bug', 'story', 'subtask' — or '' when the tracker names none. */
export interface Ticket { key: string; title: string; type: string; priority: string; status: string; assignee: string; url: string; helpdeskRef: string; updatedAt: string; latestRun?: RunSummary }
export interface Quota { provider: string; observedAt: string; fiveHour?: { utilization: number; resetsAt: string }; sevenDay?: { utilization: number; resetsAt: string }; usedPercent?: number; resetsAt?: string }
export interface RegisterRow { key: string; kind: string; runId: string; date: string; provider: string; model: string; service: string; classification: string; confidence: string; severity: string; turns: number; costUsd: number; triageVerdict: string; notePath: string; title: string; company: string }
/** A doctor row. 'warn' is advisory: ok stays true and no exit code moves. */
export type CheckLevel = 'ok'|'warn'|'fail'
export interface Check { name: string; ok: boolean; level?: CheckLevel; detail: string }

// --- eval and the golden set ---
/** One key in the golden set. The bundle itself never crosses to the UI. */
export interface GoldenEntry {
  key: string; dir: string; bundleDir: string; assertions: number; hasExpectedNote: boolean;
  /** The entry carries a retro.json, so a suite can replay it against the merged fix. */
  hasRetro?: boolean
}
export interface EvalCheck { key: string; kind: 'equals'|'contains'|'min'; pass: boolean; detail?: string }
export interface EvalFraction { matched: number; total: number; score: number }
/** How much of the human-written note the produced one covered. */
export interface EvalOverlap { refs: EvalFraction; headings: EvalFraction; missingRefs?: string[]; missingHeadings?: string[] }
export interface EvalResult {
  key: string; runId: string; state: string; reason?: string;
  turns: number; costUsd: number; minutes: number;
  schemaValid: boolean; checks: EvalCheck[]; passed: number; total: number;
  overlap?: EvalOverlap
}
export interface EvalReport { path: string; at: string; provider: string; model: string; goldenDir: string; results: EvalResult[] }

// --- the retro report ---
/**
 * A retro replays a ticket at the commit its fix branched from and scores
 * what came back against the pull request a human merged. Every number here
 * is arithmetic on two diffs, except `rubric`, which is one model's opinion
 * and is off unless it was asked for.
 */
export interface EvalJaccard { intersection: number; union: number; score: number }
/** One measure taken on both sides. There is no score: the two numbers are the point. */
export interface EvalLinePair { agent: number; pr: number }
export interface RetroStage {
  runId?: string; state?: string; reason?: string;
  turns?: number; costUsd?: number; minutes?: number;
  docPath?: string; notePath?: string
}
export interface RetroFixStage extends RetroStage { diffPath?: string; commit?: string; buildPassed?: boolean }
export interface RetroTriageScore {
  classification: string; confidence: string;
  codeRefsPathOverlap: EvalFraction; prFilesHit: EvalFraction;
  missedFiles?: string[]; strayRefs?: string[]
}
export interface RetroFixScore {
  filesJaccard: EvalJaccard; hunkOverlap: EvalFraction;
  linesAdded: EvalLinePair; linesRemoved: EvalLinePair;
  buildPassed?: boolean; diffPath?: string; agentFiles?: string[]
}
export interface RetroRubric { sameRootCause: boolean; sameFix: boolean; verdict: string; reasoning: string }
export interface RetroResult {
  key: string; baseCommit?: string; asOf?: string; prUrls?: string[]; reason?: string;
  triage?: RetroStage; fix?: RetroFixStage; rca?: RetroStage;
  triageScore?: RetroTriageScore; fixScore?: RetroFixScore; rubric?: RetroRubric;
  costUsd: number; rubricCostUsd?: number
}
export interface RetroReport {
  path: string; at: string; provider: string; model: string; goldenDir: string;
  withRca: boolean; rubric: boolean; results: RetroResult[]
}

// --- the read-only configuration summary ---
/**
 * What Settings shows of the workspace's notify and webhooks blocks. Every
 * credential is reported as the scheme of its reference — 'env', 'keychain',
 * 'file', 'cmd' — and never as the reference, the variable name, or the
 * secret.
 */
export interface ConfigSummary {
  general: GeneralSummary; budget: BudgetSummary; permissions: PermissionsSummary;
  notes: NotesSummary; mcp: MCPSummary; notify: NotifySummary; webhooks: WebhooksSummary;
  sources: SourcesSummary; me: MeSummary
}
/**
 * Who the workspace thinks the reader is, and which rule said so. `email` is
 * the one address, `names` every other spelling the config named (display
 * names and usernames together), and `source` names the rule: 'me' for the
 * config's own `me:` block, 'webhooks' for `webhooks.match.assignee`,
 * 'sources' for the tracker or helpdesk account email, 'git' for the
 * repository's own committer. An empty `source` is a workspace that can name
 * nobody, and then no run is anyone's.
 */
export type IdentitySource = ''|'me'|'webhooks'|'sources'|'git'
export interface MeSummary { email: string; names: string[]; source: IdentitySource }
/**
 * The tracker and the helpdesk the workspace reads, so a ticket number can be
 * drawn under its own product's mark. A role the workspace has not configured
 * is absent.
 */
export interface SourcesSummary {
  tracker?: SourceSummary; helpdesk?: SourceSummary
  /**
   * The tracker queue's resolved type filter: the configured list, or `['bug']`
   * when the workspace names none. `[]` or `['*']` means every type. The
   * service has already applied it — a screen reads it only to say why a lane
   * is empty. Optional only because a server older than the filter sends
   * nothing; treat an absent value as the default.
   */
  queueTypes?: string[]
}
/**
 * One source as the UI names it. `adapter` is the config value (`jira`,
 * `zohodesk`, `exec`); `name` is the product's name, or for an exec adapter
 * the first label of its ticket URL's host, title-cased ("Janus"); `host` is
 * where its tickets live, '' when no run has recorded a URL yet.
 */
export interface SourceSummary { adapter: string; name: string; host: string }
/** The top of config.yaml: the workspace, where it is, and the provider a new session gets. `configPath` is the file the Settings rows point at. */
export interface GeneralSummary {
  workspace: string; root: string; configPath: string; provider: string; model: string; billing: string;
  notesLanguage: string; customerLanguage: string; rtlMarkup: boolean
}
/** The budget block, with `stallMinutes` resolved: 0 is off, an unset key is the default. */
export interface BudgetSummary { maxTurns: number; maxMinutes: number; maxUsd: number; stallMinutes: number }
/** Every allow-list a session is judged by. Patterns an operator wrote, never credentials. */
export interface PermissionsSummary { bash: string[]; fixBash: string[]; fetch: string[]; readAlso: string[]; mcp: string[] }
/** Where notes go and what they are called. `templates` is absent on the embedded defaults. */
export interface NotesSummary { dir: string; templates?: string; filenames: { triage: string; rca: string; resolution: string } }
export interface MCPSummary { workspaceOnly: boolean }
export interface NotifySummary { enabled: boolean; on: string[]; includeTitle: boolean; destinations: NotifyDestination[] }
export interface NotifyDestination { type: 'slack'|'teams'|'generic'; credential?: string; target?: string; headers?: string[]; signed?: boolean }
export interface WebhooksSummary { enabled: boolean; cooldown: string; match: WebhookMatchSummary; sources: WebhookSourceSummary[] }
export interface WebhookMatchSummary { assignee?: string; statuses?: string[]; labels?: string[] }
export interface WebhookSourceSummary { name: string; auth: 'secret'|'basic'; credential?: string; proxy?: string }
// What an inbound webhook delivery did. 'key' is absent when the delivery named no ticket.
export type HookOutcome = 'started'|'skipped'|'filtered'|'ignored'|'rejected';
export type AppEvent =
  | { kind: 'run.updated'; workspaceId: string; run: RunSummary }
  | { kind: 'run.event'; workspaceId: string; runId: string; index: number; event: RunEvent }
  /** A run directory `deleteRun` took off disk; every window drops the row. */
  | { kind: 'run.removed'; workspaceId: string; runId: string }
  | { kind: 'quota.updated'; quota: Quota }
  | { kind: 'job.finished'; jobId: string; workspaceId: string; outcomes: { key: string; status: RunState; runId: string }[] }
  | { kind: 'hook.received'; source: string; key?: string; outcome: HookOutcome }
  | { kind: 'log'; text: string }
  /**
   * The transport's own word on the stream, never sent by the service: 'lost'
   * when the event source drops (it retries on its own), 'open' when it is
   * back. The store resyncs runs and queue on the way back, since whatever
   * happened in between was never delivered.
   */
  | { kind: 'live'; state: 'open' | 'lost' };
/**
 * The one-off overrides every start accepts; empty means the workspace's own.
 *
 * `instruction` is what the operator typed around the ticket key in the
 * composer: what they most want this session to answer. It reaches the
 * session as an Operator's request section at the top of the prompt and is
 * recorded on the run. It is not the note, and nothing filed carries it.
 */
export interface Overrides { provider?: string; model?: string; instruction?: string }
export interface TriageStart extends Overrides { dryRun?: boolean }
export interface RCAStart extends Overrides { prUrl?: string; resolution?: string }
/**
 * `noPr` pushes the branch and opens no pull request; `local` stops at the
 * commit — nothing is pushed, the worktree is kept, and the change is read
 * in the Review screen. The two are `sirdar fix --no-pr` and `--local`.
 */
export interface FixStart extends Overrides { dryRun?: boolean; noPr?: boolean; local?: boolean; base?: string; acceptDeviation?: boolean }
/**
 * What a helpdesk number resolved to. `key` empty is an ordinary answer:
 * `reason` then says which of the three it was — the workspace reads no
 * helpdesk, the record does not exist, or the record names no tracker issue.
 */
export interface HelpdeskLink { number: string; key: string; subject?: string; reason?: string }
/**
 * How one ambiguous composer line was read. It is a suggestion: the composer
 * draws it as chips and starts nothing until a person says so.
 */
export interface ComposedIntent { key: string; mode: '' | RunKind; instruction: string; confidence: number }
export interface EvalStart extends Overrides { concurrency?: number; retro?: boolean; withRca?: boolean; rubric?: boolean }
export interface Transport {
  workspaces(): Promise<Workspace[]>; addWorkspace(root: string): Promise<Workspace>; removeWorkspace(id: string): Promise<void>;
  queue(ws: string, f?: { assignee?: string; status?: string; limit?: number }): Promise<Ticket[]>;
  /**
   * Which tracker issue a helpdesk number belongs to. Read-only: it reads
   * one helpdesk record and starts nothing. A number with no tracker issue
   * behind it resolves with `key: ''` and the reason on it, because the
   * composer asks this while somebody is still typing.
   */
  resolveHelpdesk(ws: string, number: string): Promise<HelpdeskLink>;
  /**
   * Reads one ambiguous composer line with a single short provider call and
   * answers what it was understood as. Starts nothing: the answer is drawn
   * as chips a person confirms.
   */
  composeIntent(ws: string, text: string): Promise<ComposedIntent>;
  runs(ws: string, key?: string): Promise<RunSummary[]>; run(ws: string, runId: string): Promise<RunDetail>;
  /**
   * Removes a run's directory under .sirdar/runs. The register row and any
   * filed note stay. A live run is refused (409); `run.removed` follows on
   * the stream.
   */
  deleteRun(ws: string, runId: string): Promise<void>;
  /** The sidebar's "Search notes": a case-folded substring over every run's answer JSON and notes. */
  search(ws: string, q: string): Promise<SearchHit[]>;
  events(ws: string, runId: string, after: number): Promise<{ events: RunEvent[]; next: number }>;
  note(ws: string, runId: string, kind: NoteKind): Promise<string>; prompt(ws: string, runId: string): Promise<string>;
  startTriage(ws: string, keys: string[], o?: TriageStart): Promise<{ jobId: string }>;
  startRCA(ws: string, key: string, o?: RCAStart): Promise<{ jobId: string }>;
  startFix(ws: string, key: string, o?: FixStart): Promise<{ jobId: string }>;
  startEval(ws: string, keys?: string[], o?: EvalStart): Promise<{ jobId: string }>;
  evalReports(ws: string): Promise<EvalReport[]>;
  /** The newest retro report, or null when the workspace has run none. */
  latestRetro(ws: string): Promise<RetroReport | null>;
  golden(ws: string): Promise<GoldenEntry[]>;
  addGolden(ws: string, o: { key?: string; runId?: string }): Promise<GoldenEntry>;
  configSummary(ws: string): Promise<ConfigSummary>;
  resume(ws: string, runId: string, answer?: string): Promise<{ jobId: string }>; cancel(jobId: string): Promise<void>;
  /** Continues a finished run with a follow-up instruction, on the same run. */
  steer(ws: string, runId: string, text: string): Promise<SteerStarted>;
  /** A fix run's change, file by file, with the unified patch. Starts nothing. */
  runDiff(ws: string, runId: string): Promise<RunDiff>;
  /** Reverts one hunk out of the fix commit and answers with the change as it stands after. */
  dropHunk(ws: string, runId: string, req: DropHunkRequest): Promise<RunDiff>;
  /** The MCP servers a run here would be offered; `connect` reaches each one and counts its tools. */
  mcpServers(ws: string, connect?: boolean): Promise<MCPInventory>;
  /** Every tool one server lists, with the verdict a run would get for it. */
  mcpTools(ws: string, server: string): Promise<MCPToolList>;
  /**
   * Runs one tool by hand. A tool the workspace's permissions would refuse a
   * run resolves (not rejects) with `verdict: 'denied'` and the reason, and
   * nothing is started — the same body the HTTP route answers 403 with.
   */
  mcpCall(ws: string, server: string, tool: string, args?: unknown): Promise<MCPCallResult>;
  register(ws: string): Promise<RegisterRow[]>; doctor(ws: string): Promise<Check[]>; quota(): Promise<Quota[]>;
  subscribe(handler: (e: AppEvent) => void): () => void;
  /** Desktop build version, e.g. "1.2.3" or "dev". Only the Wails transport implements it. */
  version?(): Promise<string>;
  /**
   * Opens the workspace's `.sirdar/config.yaml` in whatever the desktop
   * associates with it. Only the Wails transport implements it: a browser
   * served by `sirdar serve` cannot open a file on the operator's machine,
   * and Settings copies the path there instead.
   */
  openConfig?(ws: string): Promise<void>;
  /**
   * Opens one of a run's notes — a path the run itself recorded in
   * `notes` — in whatever the desktop associates with Markdown, which on
   * a machine with a vault is Obsidian. Only the Wails transport
   * implements it, for the same reason as `openConfig`; a browser copies
   * the path instead.
   */
  openNote?(ws: string, runId: string, path: string): Promise<void>;
  /**
   * Reveals a run's directory under .sirdar/runs in the desktop's file
   * manager. Only the Wails transport implements it; the sessions menu
   * leaves the item out in a browser.
   */
  openRunDir?(ws: string, runId: string): Promise<void>;
}
