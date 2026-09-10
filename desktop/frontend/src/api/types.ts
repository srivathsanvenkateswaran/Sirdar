export type RunState = 'preparing'|'running'|'completed'|'failed'|'blocked'|'over_budget';
export type NoteKind = 'triage'|'rca'|'resolution';
export interface Workspace { id: string; name: string; root: string; provider: 'claude'|'codex'; model: string; notesDir: string; billing: string }
export interface Usage { turns: number; inputTokens: number; outputTokens: number; costUsd: number }
export interface RunSummary { runId: string; key: string; kind: 'triage'|'rca'; status: RunState; provider: string; model: string; startedAt: string; updatedAt: string; reason: string; usage: Usage; notes: string[] }
export interface RunDetail extends RunSummary { promptPath: string; bundleDir: string; warnings: string[]; handle: string; budget: { maxTurns: number; maxMinutes: number; maxUsd: number } }
export interface RunEvent { t: string; kind: string; payload: { tool?: string; decision?: string; text?: string; turns?: number; costUsd?: number; raw?: unknown } }
export interface Ticket { key: string; title: string; priority: string; status: string; assignee: string; url: string; helpdeskRef: string; updatedAt: string; latestRun?: RunSummary }
export interface Quota { provider: string; observedAt: string; fiveHour?: { utilization: number; resetsAt: string }; sevenDay?: { utilization: number; resetsAt: string }; usedPercent?: number; resetsAt?: string }
export interface RegisterRow { key: string; kind: string; runId: string; date: string; provider: string; model: string; service: string; classification: string; confidence: string; severity: string; turns: number; costUsd: number; triageVerdict: string; notePath: string }
export interface Check { name: string; ok: boolean; detail: string }
export type AppEvent =
  | { kind: 'run.updated'; workspaceId: string; run: RunSummary }
  | { kind: 'run.event'; workspaceId: string; runId: string; index: number; event: RunEvent }
  | { kind: 'quota.updated'; quota: Quota }
  | { kind: 'job.finished'; jobId: string; workspaceId: string; outcomes: { key: string; status: RunState; runId: string }[] }
  | { kind: 'log'; text: string };
export interface Transport {
  workspaces(): Promise<Workspace[]>; addWorkspace(root: string): Promise<Workspace>; removeWorkspace(id: string): Promise<void>;
  queue(ws: string, f?: { assignee?: string; status?: string; limit?: number }): Promise<Ticket[]>;
  runs(ws: string, key?: string): Promise<RunSummary[]>; run(ws: string, runId: string): Promise<RunDetail>;
  events(ws: string, runId: string, after: number): Promise<{ events: RunEvent[]; next: number }>;
  note(ws: string, runId: string, kind: NoteKind): Promise<string>; prompt(ws: string, runId: string): Promise<string>;
  startTriage(ws: string, keys: string[], o?: { provider?: string; model?: string; dryRun?: boolean }): Promise<{ jobId: string }>;
  startRCA(ws: string, key: string, o?: { prUrl?: string; resolution?: string }): Promise<{ jobId: string }>;
  resume(ws: string, runId: string, answer?: string): Promise<{ jobId: string }>; cancel(jobId: string): Promise<void>;
  register(ws: string): Promise<RegisterRow[]>; doctor(ws: string): Promise<Check[]>; quota(): Promise<Quota[]>;
  subscribe(handler: (e: AppEvent) => void): () => void;
}
