export type RunState = 'preparing'|'running'|'completed'|'failed'|'blocked'|'over_budget';
export type RunKind = 'triage'|'rca'|'fix';
/** The providers a one-off override may name; '' is the workspace's own. */
export const PROVIDERS = ['claude', 'codex', 'openai', 'acp', 'qwen'] as const;
export type Provider = (typeof PROVIDERS)[number];
export type NoteKind = 'triage'|'rca'|'resolution';
export interface Workspace { id: string; name: string; root: string; provider: Provider; model: string; notesDir: string; billing: string }
export interface Usage { turns: number; inputTokens: number; outputTokens: number; costUsd: number }
export interface RunSummary { runId: string; key: string; kind: RunKind; status: RunState; provider: string; model: string; startedAt: string; updatedAt: string; reason: string; usage: Usage; notes: string[] }
export interface RunDetail extends RunSummary { promptPath: string; bundleDir: string; warnings: string[]; handle: string; budget: { maxTurns: number; maxMinutes: number; maxUsd: number }; fix?: FixInfo }
/**
 * Where a fix run's work went, read off the run's own state.json. `deviation`
 * is set when the agent reported doing something other than the note's
 * Proposed Fix: the commit is on the branch and has not been pushed until a
 * person reruns the fix with acceptDeviation.
 */
export interface FixInfo { branch?: string; base?: string; commit?: string; prUrl?: string; deviation?: string }
export interface RunEvent { t: string; kind: string; payload: { tool?: string; decision?: string; text?: string; turns?: number; costUsd?: number; raw?: unknown } }
export interface Ticket { key: string; title: string; priority: string; status: string; assignee: string; url: string; helpdeskRef: string; updatedAt: string; latestRun?: RunSummary }
export interface Quota { provider: string; observedAt: string; fiveHour?: { utilization: number; resetsAt: string }; sevenDay?: { utilization: number; resetsAt: string }; usedPercent?: number; resetsAt?: string }
export interface RegisterRow { key: string; kind: string; runId: string; date: string; provider: string; model: string; service: string; classification: string; confidence: string; severity: string; turns: number; costUsd: number; triageVerdict: string; notePath: string }
export interface Check { name: string; ok: boolean; detail: string }

// --- eval and the golden set ---
/** One key in the golden set. The bundle itself never crosses to the UI. */
export interface GoldenEntry { key: string; dir: string; bundleDir: string; assertions: number; hasExpectedNote: boolean }
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

// --- the read-only configuration summary ---
/**
 * What Settings shows of the workspace's notify and webhooks blocks. Every
 * credential is reported as the scheme of its reference — 'env', 'keychain',
 * 'file', 'cmd' — and never as the reference, the variable name, or the
 * secret.
 */
export interface ConfigSummary { notify: NotifySummary; webhooks: WebhooksSummary }
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
  | { kind: 'quota.updated'; quota: Quota }
  | { kind: 'job.finished'; jobId: string; workspaceId: string; outcomes: { key: string; status: RunState; runId: string }[] }
  | { kind: 'hook.received'; source: string; key?: string; outcome: HookOutcome }
  | { kind: 'log'; text: string };
/** The one-off overrides every start accepts; empty means the workspace's own. */
export interface Overrides { provider?: string; model?: string }
export interface TriageStart extends Overrides { dryRun?: boolean }
export interface RCAStart extends Overrides { prUrl?: string; resolution?: string }
export interface FixStart extends Overrides { dryRun?: boolean; noPr?: boolean; base?: string; acceptDeviation?: boolean }
export interface EvalStart extends Overrides { concurrency?: number }
export interface Transport {
  workspaces(): Promise<Workspace[]>; addWorkspace(root: string): Promise<Workspace>; removeWorkspace(id: string): Promise<void>;
  queue(ws: string, f?: { assignee?: string; status?: string; limit?: number }): Promise<Ticket[]>;
  runs(ws: string, key?: string): Promise<RunSummary[]>; run(ws: string, runId: string): Promise<RunDetail>;
  events(ws: string, runId: string, after: number): Promise<{ events: RunEvent[]; next: number }>;
  note(ws: string, runId: string, kind: NoteKind): Promise<string>; prompt(ws: string, runId: string): Promise<string>;
  startTriage(ws: string, keys: string[], o?: TriageStart): Promise<{ jobId: string }>;
  startRCA(ws: string, key: string, o?: RCAStart): Promise<{ jobId: string }>;
  startFix(ws: string, key: string, o?: FixStart): Promise<{ jobId: string }>;
  startEval(ws: string, keys?: string[], o?: EvalStart): Promise<{ jobId: string }>;
  evalReports(ws: string): Promise<EvalReport[]>;
  golden(ws: string): Promise<GoldenEntry[]>;
  addGolden(ws: string, o: { key?: string; runId?: string }): Promise<GoldenEntry>;
  configSummary(ws: string): Promise<ConfigSummary>;
  resume(ws: string, runId: string, answer?: string): Promise<{ jobId: string }>; cancel(jobId: string): Promise<void>;
  register(ws: string): Promise<RegisterRow[]>; doctor(ws: string): Promise<Check[]>; quota(): Promise<Quota[]>;
  subscribe(handler: (e: AppEvent) => void): () => void;
  /** Desktop build version, e.g. "1.2.3" or "dev". Only the Wails transport implements it. */
  version?(): Promise<string>;
}
