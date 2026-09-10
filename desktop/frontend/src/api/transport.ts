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
 * Wails transport. D8 replaces every body with a call to the bound Bridge and
 * wires subscribe() to `window.runtime.EventsOn`; the method list is complete so
 * the UI can be written against it now.
 */
export function createWailsTransport(): Transport {
  const todo = (): never => {
    throw new Error('wails bridge not wired (D8)')
  }
  return {
    workspaces: todo,
    addWorkspace: todo,
    removeWorkspace: todo,
    queue: todo,
    runs: todo,
    run: todo,
    events: todo,
    note: todo,
    prompt: todo,
    startTriage: todo,
    startRCA: todo,
    resume: todo,
    cancel: todo,
    register: todo,
    doctor: todo,
    quota: todo,
    subscribe: todo,
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
