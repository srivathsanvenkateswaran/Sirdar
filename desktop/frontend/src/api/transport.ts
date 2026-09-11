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
  Workspace,
} from './types'

/** Base path of the JSON API served by `internal/httpapi`. */
const API = '/api'

/** Shape of the error body every endpoint returns on failure. */
interface ApiError {
  error?: { code?: string; message?: string }
}

/** The event names the SSE stream uses, in the order AppEvent declares them. */
const EVENT_KINDS: AppEvent['kind'][] = [
  'run.updated',
  'run.event',
  'quota.updated',
  'job.finished',
  'log',
]

function query(params: Record<string, string | number | undefined>): string {
  const sp = new URLSearchParams()
  for (const [k, v] of Object.entries(params)) {
    if (v === undefined || v === '') continue
    sp.set(k, String(v))
  }
  const s = sp.toString()
  return s ? `?${s}` : ''
}

async function fail(res: Response): Promise<never> {
  let message = `${res.status} ${res.statusText}`
  try {
    const body = (await res.json()) as ApiError
    if (body?.error?.message) {
      message = body.error.code ? `${body.error.code}: ${body.error.message}` : body.error.message
    }
  } catch {
    // Body was not JSON; keep the status line.
  }
  throw new Error(message)
}

async function request(path: string, init?: RequestInit): Promise<Response> {
  const res = await fetch(`${API}${path}`, init)
  if (!res.ok) return fail(res)
  return res
}

async function getJSON<T>(path: string): Promise<T> {
  const res = await request(path)
  return (await res.json()) as T
}

async function getText(path: string): Promise<string> {
  const res = await request(path)
  return await res.text()
}

async function postJSON<T>(path: string, body?: unknown): Promise<T> {
  const res = await request(path, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: body === undefined ? undefined : JSON.stringify(body),
  })
  if (res.status === 204) return undefined as T
  const text = await res.text()
  return (text ? JSON.parse(text) : undefined) as T
}

/** HTTP + SSE transport, used by `sirdar serve` and by `npm run dev`. */
export function createHTTPTransport(): Transport {
  return {
    workspaces: () => getJSON<Workspace[]>('/workspaces'),
    addWorkspace: (root) => postJSON<Workspace>('/workspaces', { root }),
    removeWorkspace: async (id) => {
      await request(`/workspaces/${encodeURIComponent(id)}`, { method: 'DELETE' })
    },
    queue: (ws, f) =>
      getJSON<Ticket[]>(
        `/workspaces/${encodeURIComponent(ws)}/queue${query({
          assignee: f?.assignee,
          status: f?.status,
          limit: f?.limit,
        })}`,
      ),
    runs: (ws, key) =>
      getJSON<RunSummary[]>(`/workspaces/${encodeURIComponent(ws)}/runs${query({ key })}`),
    run: (ws, runId) =>
      getJSON<RunDetail>(
        `/workspaces/${encodeURIComponent(ws)}/runs/${encodeURIComponent(runId)}`,
      ),
    events: (ws, runId, after) =>
      getJSON<{ events: RunEvent[]; next: number }>(
        `/workspaces/${encodeURIComponent(ws)}/runs/${encodeURIComponent(runId)}/events${query({
          after,
        })}`,
      ),
    note: (ws, runId, kind) =>
      getText(
        `/workspaces/${encodeURIComponent(ws)}/runs/${encodeURIComponent(runId)}/note${query({
          kind,
        })}`,
      ),
    prompt: (ws, runId) =>
      getText(`/workspaces/${encodeURIComponent(ws)}/runs/${encodeURIComponent(runId)}/prompt`),
    startTriage: (ws, keys, o) =>
      postJSON<{ jobId: string }>(`/workspaces/${encodeURIComponent(ws)}/triage`, {
        keys,
        ...(o ?? {}),
      }),
    startRCA: (ws, key, o) =>
      postJSON<{ jobId: string }>(`/workspaces/${encodeURIComponent(ws)}/rca`, {
        key,
        ...(o ?? {}),
      }),
    resume: (ws, runId, answer) =>
      postJSON<{ jobId: string }>(
        `/workspaces/${encodeURIComponent(ws)}/runs/${encodeURIComponent(runId)}/resume`,
        { answer },
      ),
    cancel: async (jobId) => {
      await postJSON<void>(`/jobs/${encodeURIComponent(jobId)}/cancel`)
    },
    register: (ws) => getJSON<RegisterRow[]>(`/workspaces/${encodeURIComponent(ws)}/register`),
    doctor: (ws) => getJSON<Check[]>(`/workspaces/${encodeURIComponent(ws)}/doctor`),
    quota: () => getJSON<Quota[]>('/quota'),
    subscribe: (handler) => {
      const source = new EventSource(`${API}/events`)
      const listeners: [string, (e: MessageEvent) => void][] = []
      for (const kind of EVENT_KINDS) {
        const listener = (e: MessageEvent) => {
          try {
            handler({ ...(JSON.parse(e.data) as object), kind } as AppEvent)
          } catch {
            // A malformed frame must not tear down the stream.
          }
        }
        source.addEventListener(kind, listener as EventListener)
        listeners.push([kind, listener])
      }
      return () => {
        for (const [kind, listener] of listeners) {
          source.removeEventListener(kind, listener as EventListener)
        }
        source.close()
      }
    },
  }
}

/**
 * The bound Go methods, as `desktop/bridge.go` declares them. Wails also
 * generates typed bindings under `frontend/wailsjs/go/main/Bridge` at
 * `wails build` / `wails dev` time, but that directory is not in the
 * repository, so the bindings are reached through `window.go` instead and
 * `npm run build` works without the wails CLI.
 */
interface BridgeBindings {
  Workspaces(): Promise<Workspace[] | null>
  AddWorkspace(root: string): Promise<Workspace>
  RemoveWorkspace(id: string): Promise<void>
  Queue(ws: string, f: { assignee: string; status: string; limit: number }): Promise<Ticket[] | null>
  Runs(ws: string, key: string): Promise<RunSummary[] | null>
  Run(ws: string, runId: string): Promise<RunDetail>
  Events(ws: string, runId: string, after: number): Promise<{ events: RunEvent[] | null; next: number }>
  Note(ws: string, runId: string, kind: string): Promise<string>
  Prompt(ws: string, runId: string): Promise<string>
  Register(ws: string): Promise<RegisterRow[] | null>
  Doctor(ws: string): Promise<Check[] | null>
  Quota(): Promise<Quota[] | null>
  StartTriage(
    ws: string,
    keys: string[],
    o: { provider: string; model: string; dryRun: boolean },
  ): Promise<string>
  StartRCA(ws: string, key: string, o: { prUrl: string; resolution: string }): Promise<string>
  Resume(ws: string, runId: string, answer: string): Promise<string>
  Cancel(jobId: string): Promise<void>
  Version(): Promise<string>
}

/** The subset of the Wails runtime the transport uses. */
interface WailsRuntime {
  EventsOn(kind: string, callback: (...data: any[]) => void): () => void
}

function bridge(): BridgeBindings {
  const bound = (window as any).go?.main?.Bridge as BridgeBindings | undefined
  if (!bound) throw new Error('wails bridge not available')
  return bound
}

/** A nil Go slice arrives as null; the UI always wants a list. */
function list<T>(rows: T[] | null): T[] {
  return rows ?? []
}

/** Wails transport: bound Go methods plus runtime events, used in the app shell. */
export function createWailsTransport(): Transport {
  return {
    workspaces: async () => list(await bridge().Workspaces()),
    addWorkspace: (root) => bridge().AddWorkspace(root),
    removeWorkspace: async (id) => {
      await bridge().RemoveWorkspace(id)
    },
    queue: async (ws, f) =>
      list(
        await bridge().Queue(ws, {
          assignee: f?.assignee ?? '',
          status: f?.status ?? '',
          limit: f?.limit ?? 0,
        }),
      ),
    runs: async (ws, key) => list(await bridge().Runs(ws, key ?? '')),
    run: (ws, runId) => bridge().Run(ws, runId),
    events: async (ws, runId, after) => {
      const page = await bridge().Events(ws, runId, after)
      return { events: list(page.events), next: page.next }
    },
    note: (ws, runId, kind) => bridge().Note(ws, runId, kind),
    prompt: (ws, runId) => bridge().Prompt(ws, runId),
    startTriage: async (ws, keys, o) => ({
      jobId: await bridge().StartTriage(ws, keys, {
        provider: o?.provider ?? '',
        model: o?.model ?? '',
        dryRun: o?.dryRun ?? false,
      }),
    }),
    startRCA: async (ws, key, o) => ({
      jobId: await bridge().StartRCA(ws, key, {
        prUrl: o?.prUrl ?? '',
        resolution: o?.resolution ?? '',
      }),
    }),
    resume: async (ws, runId, answer) => ({
      jobId: await bridge().Resume(ws, runId, answer ?? ''),
    }),
    cancel: async (jobId) => {
      await bridge().Cancel(jobId)
    },
    register: async (ws) => list(await bridge().Register(ws)),
    doctor: async (ws) => list(await bridge().Doctor(ws)),
    quota: async () => list(await bridge().Quota()),
    version: () => bridge().Version(),
    subscribe: (handler) => {
      const rt = (window as any).runtime as WailsRuntime | undefined
      if (!rt) return () => {}
      const off = EVENT_KINDS.map((kind) =>
        rt.EventsOn(kind, (data: unknown) => {
          handler({ ...((data as object) ?? {}), kind } as AppEvent)
        }),
      )
      return () => {
        for (const cancel of off) cancel()
      }
    },
  }
}

/** True when the page is running inside the Wails shell with bindings present. */
export function hasWailsBridge(): boolean {
  if (typeof window === 'undefined') return false
  return Boolean((window as any).go?.main?.Bridge)
}

/** Picks the Wails bridge when it is present, the HTTP+SSE client otherwise. */
export function createTransport(): Transport {
  return hasWailsBridge() ? createWailsTransport() : createHTTPTransport()
}
