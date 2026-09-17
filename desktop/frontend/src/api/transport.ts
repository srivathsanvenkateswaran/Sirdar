import { coalesce } from './coalesce'
import type {
  AppEvent,
  Check,
  ComposedIntent,
  ConfigSummary,
  EvalReport,
  GoldenEntry,
  HelpdeskLink,
  MCPCallResult,
  MCPInventory,
  MCPToolList,
  PlaybookSummary,
  RetroReport,
  Quota,
  RegisterRow,
  RunDetail,
  RunDiff,
  RunEvent,
  RunSummary,
  SearchHit,
  SteerStarted,
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
  'run.resync',
  'run.removed',
  'quota.updated',
  'job.finished',
  'hook.received',
  'log',
]

/**
 * One stream for the whole page, however many screens want it.
 *
 * Every subscriber used to open its own — the store, the run feed, Eval,
 * New session and Register each called `subscribe` — and a browser allows
 * six connections per host. Opening a session took two of them, and a
 * server-side stream is held until a write to it fails, so a few reloads
 * left enough dead ones behind to fill the pool: the sixth consecutive load
 * of `sirdar serve` stalled for twelve seconds with API calls and fonts
 * alike waiting for a socket.
 *
 * `shared` opens the underlying stream on the first subscriber and closes it
 * when the last one leaves, fanning what arrives out to whoever is listening.
 * One handler that throws does not keep the rest from being called.
 */
function shared(
  open: (emit: (e: AppEvent) => void) => () => void,
): (handler: (e: AppEvent) => void) => () => void {
  const handlers = new Set<(e: AppEvent) => void>()
  let close: (() => void) | null = null

  const emit = (e: AppEvent) => {
    for (const handler of [...handlers]) {
      try {
        handler(e)
      } catch {
        // One screen's handler must not take the stream down with it.
      }
    }
  }

  return (handler) => {
    handlers.add(handler)
    close ??= open(emit)
    let left = false
    return () => {
      if (left) return
      left = true
      // The last one out closes the stream, and closing hands over what is
      // still queued — so it is closed while this handler is still
      // listening, or that last burst would reach nobody.
      if (handlers.size === 1 && close) {
        const stop = close
        close = null
        stop()
      }
      handlers.delete(handler)
    }
  }
}

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

/**
 * A hand-run MCP call is the one route whose refusal is an answer rather than
 * an error: a denied tool comes back 403 with the same `MCPCallResult` body an
 * allowed one gets, verdict and reason filled in. The tool tester shows that
 * verdict; it is the point of the screen. Anything else that is not 2xx is
 * the usual error envelope.
 */
async function postMCPCall(path: string, body: unknown): Promise<MCPCallResult> {
  const res = await fetch(`${API}${path}`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  })
  if (res.ok) return (await res.json()) as MCPCallResult
  if (res.status === 403) {
    const text = await res.text()
    try {
      const parsed = JSON.parse(text) as MCPCallResult | ApiError
      if ('verdict' in parsed && parsed.verdict === 'denied') return parsed
    } catch {
      // Not the call result; fall through to the error envelope.
    }
    return fail(new Response(text, { status: res.status, statusText: res.statusText }))
  }
  return fail(res)
}

/** HTTP + SSE transport, used by `sirdar serve` and by `npm run dev`. */
export function createHTTPTransport(): Transport {
  const subscribe = shared((emit) => {
    const source = new EventSource(`${API}/events`)
    // A burst of frames reaches the handlers as one task; see api/coalesce.
    const out = coalesce(emit)
    const listeners: [string, (e: MessageEvent) => void][] = []
    for (const kind of EVENT_KINDS) {
      const listener = (e: MessageEvent) => {
        try {
          out.push({ ...(JSON.parse(e.data) as object), kind } as AppEvent)
        } catch {
          // A malformed frame must not tear down the stream.
        }
      }
      source.addEventListener(kind, listener as EventListener)
      listeners.push([kind, listener])
    }
    // The browser reconnects a dropped EventSource by itself, firing
    // `error` on the way down and `open` on the way back. Nothing sent in
    // between reaches the window, so both are reported: the store says the
    // stream is lost, and resyncs when it is back.
    const onOpen = () => out.push({ kind: 'live', state: 'open' })
    const onError = () => out.push({ kind: 'live', state: 'lost' })
    source.addEventListener('open', onOpen)
    source.addEventListener('error', onError)
    return () => {
      for (const [kind, listener] of listeners) {
        source.removeEventListener(kind, listener as EventListener)
      }
      source.removeEventListener('open', onOpen)
      source.removeEventListener('error', onError)
      source.close()
      out.stop()
    }
  })

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
    resolveHelpdesk: (ws, number) =>
      getJSON<HelpdeskLink>(
        `/workspaces/${encodeURIComponent(ws)}/helpdesk/${encodeURIComponent(number)}`,
      ),
    composeIntent: (ws, text) =>
      postJSON<ComposedIntent>(`/workspaces/${encodeURIComponent(ws)}/compose-intent`, { text }),
    runs: (ws, key) =>
      getJSON<RunSummary[]>(`/workspaces/${encodeURIComponent(ws)}/runs${query({ key })}`),
    run: (ws, runId) =>
      getJSON<RunDetail>(
        `/workspaces/${encodeURIComponent(ws)}/runs/${encodeURIComponent(runId)}`,
      ),
    deleteRun: async (ws, runId) => {
      await request(`/workspaces/${encodeURIComponent(ws)}/runs/${encodeURIComponent(runId)}`, {
        method: 'DELETE',
      })
    },
    search: (ws, q) =>
      getJSON<SearchHit[]>(`/workspaces/${encodeURIComponent(ws)}/search${query({ q })}`),
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
    startFix: (ws, key, o) =>
      postJSON<{ jobId: string }>(`/workspaces/${encodeURIComponent(ws)}/fix`, {
        key,
        ...(o ?? {}),
      }),
    startEval: (ws, keys, o) =>
      postJSON<{ jobId: string }>(`/workspaces/${encodeURIComponent(ws)}/eval`, {
        ...(keys && keys.length > 0 ? { keys } : {}),
        ...(o ?? {}),
      }),
    evalReports: (ws) => getJSON<EvalReport[]>(`/workspaces/${encodeURIComponent(ws)}/eval`),
    latestRetro: (ws) =>
      getJSON<RetroReport | null>(`/workspaces/${encodeURIComponent(ws)}/eval/retro/latest`),
    golden: (ws) => getJSON<GoldenEntry[]>(`/workspaces/${encodeURIComponent(ws)}/golden`),
    addGolden: (ws, o) =>
      postJSON<GoldenEntry>(`/workspaces/${encodeURIComponent(ws)}/golden`, o),
    configSummary: (ws) =>
      getJSON<ConfigSummary>(`/workspaces/${encodeURIComponent(ws)}/config/summary`),
    playbooks: (ws) =>
      getJSON<PlaybookSummary[]>(`/workspaces/${encodeURIComponent(ws)}/playbooks`),
    playbook: (ws, name) =>
      getText(
        `/workspaces/${encodeURIComponent(ws)}/playbooks/${encodeURIComponent(name)}`,
      ),
    savePlaybook: async (ws, name, body) => {
      // The markdown crosses as a JSON field rather than as the request
      // body, so the server's cross-site guard — which requires
      // application/json on every mutating route — covers this one too.
      const res = await request(
        `/workspaces/${encodeURIComponent(ws)}/playbooks/${encodeURIComponent(name)}`,
        {
          method: 'PUT',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ body }),
        },
      )
      return (await res.json()) as PlaybookSummary
    },
    addPlaybook: (ws, name, body) =>
      postJSON<PlaybookSummary>(`/workspaces/${encodeURIComponent(ws)}/playbooks`, { name, body }),
    deletePlaybook: async (ws, name) => {
      await request(
        `/workspaces/${encodeURIComponent(ws)}/playbooks/${encodeURIComponent(name)}`,
        { method: 'DELETE' },
      )
    },
    scaffoldPlaybooks: (ws) =>
      postJSON<PlaybookSummary[]>(`/workspaces/${encodeURIComponent(ws)}/playbooks/scaffold`),
    openPlaybook: async (ws, name) => {
      await postJSON<void>(
        `/workspaces/${encodeURIComponent(ws)}/playbooks/${encodeURIComponent(name)}/open`,
      )
    },
    resume: (ws, runId, answer, model) =>
      postJSON<{ jobId: string }>(
        `/workspaces/${encodeURIComponent(ws)}/runs/${encodeURIComponent(runId)}/resume`,
        { answer, ...(model ? { model } : {}) },
      ),
    steer: (ws, runId, text, model) =>
      postJSON<SteerStarted>(
        `/workspaces/${encodeURIComponent(ws)}/runs/${encodeURIComponent(runId)}/steer`,
        { text, ...(model ? { model } : {}) },
      ),
    runDiff: (ws, runId) =>
      getJSON<RunDiff>(
        `/workspaces/${encodeURIComponent(ws)}/runs/${encodeURIComponent(runId)}/diff`,
      ),
    dropHunk: (ws, runId, req) =>
      postJSON<RunDiff>(
        `/workspaces/${encodeURIComponent(ws)}/runs/${encodeURIComponent(runId)}/diff/drop`,
        { path: req.path, hunk: req.hunk, etag: req.etag },
      ),
    mcpServers: (ws, connect) =>
      getJSON<MCPInventory>(
        `/workspaces/${encodeURIComponent(ws)}/mcp${query({ connect: connect ? 1 : undefined })}`,
      ),
    mcpTools: (ws, server) =>
      getJSON<MCPToolList>(
        `/workspaces/${encodeURIComponent(ws)}/mcp/${encodeURIComponent(server)}/tools`,
      ),
    mcpCall: (ws, server, tool, args) =>
      postMCPCall(
        `/workspaces/${encodeURIComponent(ws)}/mcp/${encodeURIComponent(server)}/call`,
        { tool, args: args ?? {} },
      ),
    cancel: async (jobId) => {
      await postJSON<void>(`/jobs/${encodeURIComponent(jobId)}/cancel`)
    },
    register: (ws) => getJSON<RegisterRow[]>(`/workspaces/${encodeURIComponent(ws)}/register`),
    doctor: (ws) => getJSON<Check[]>(`/workspaces/${encodeURIComponent(ws)}/doctor`),
    quota: () => getJSON<Quota[]>('/quota'),
    subscribe,
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
  ResolveHelpdesk(ws: string, number: string): Promise<HelpdeskLink>
  ComposeIntent(ws: string, text: string): Promise<ComposedIntent>
  Runs(ws: string, key: string): Promise<RunSummary[] | null>
  Run(ws: string, runId: string): Promise<RunDetail>
  DeleteRun(ws: string, runId: string): Promise<void>
  Search(ws: string, q: string): Promise<SearchHit[] | null>
  Events(ws: string, runId: string, after: number): Promise<{ events: RunEvent[] | null; next: number }>
  Note(ws: string, runId: string, kind: string): Promise<string>
  Prompt(ws: string, runId: string): Promise<string>
  Register(ws: string): Promise<RegisterRow[] | null>
  Doctor(ws: string): Promise<Check[] | null>
  Quota(): Promise<Quota[] | null>
  StartTriage(
    ws: string,
    keys: string[],
    o: { provider: string; model: string; dryRun: boolean; instruction: string },
  ): Promise<string>
  StartRCA(
    ws: string,
    key: string,
    o: { prUrl: string; resolution: string; provider: string; model: string; instruction: string },
  ): Promise<string>
  StartFix(
    ws: string,
    key: string,
    o: {
      dryRun: boolean
      noPr: boolean
      local: boolean
      base: string
      acceptDeviation: boolean
      provider: string
      model: string
      instruction: string
    },
  ): Promise<string>
  StartEval(
    ws: string,
    keys: string[],
    o: {
      provider: string
      model: string
      concurrency: number
      retro: boolean
      withRca: boolean
      rubric: boolean
    },
  ): Promise<string>
  EvalReports(ws: string): Promise<EvalReport[] | null>
  LatestRetro(ws: string): Promise<RetroReport | null>
  Golden(ws: string): Promise<GoldenEntry[] | null>
  AddGolden(ws: string, key: string, runId: string): Promise<GoldenEntry>
  ConfigSummary(ws: string): Promise<ConfigSummary>
  Playbooks(ws: string): Promise<PlaybookSummary[] | null>
  Playbook(ws: string, name: string): Promise<string>
  SavePlaybook(ws: string, name: string, body: string): Promise<PlaybookSummary>
  AddPlaybook(ws: string, name: string, body: string): Promise<PlaybookSummary>
  DeletePlaybook(ws: string, name: string): Promise<void>
  ScaffoldPlaybooks(ws: string): Promise<PlaybookSummary[] | null>
  OpenPlaybook(ws: string, name: string): Promise<void>
  Resume(ws: string, runId: string, answer: string, model: string): Promise<string>
  Steer(ws: string, runId: string, text: string, model: string): Promise<string>
  RunDiff(ws: string, runId: string): Promise<RunDiff>
  DropHunk(ws: string, runId: string, path: string, hunk: number, etag: string): Promise<RunDiff>
  MCPServers(ws: string, connect: boolean): Promise<MCPInventory>
  MCPTools(ws: string, server: string): Promise<MCPToolList>
  /** A denied tool answers normally with `verdict: 'denied'`; only a call that could not be made rejects. */
  MCPCall(ws: string, server: string, tool: string, args: Record<string, unknown>): Promise<MCPCallResult>
  Cancel(jobId: string): Promise<void>
  Version(): Promise<string>
  OpenConfig(ws: string): Promise<void>
  OpenNote(ws: string, runId: string, path: string): Promise<void>
  OpenRunDir(ws: string, runId: string): Promise<void>
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
  // The runtime bridge opens no socket, so the pool is not the reason here;
  // one registration per kind rather than one per screen is, and both
  // shells then behave the same way.
  const subscribe = shared((emit) => {
    const rt = (window as any).runtime as WailsRuntime | undefined
    if (!rt) return () => {}
    const out = coalesce(emit)
    const off = EVENT_KINDS.map((kind) =>
      rt.EventsOn(kind, (data: unknown) => {
        out.push({ ...((data as object) ?? {}), kind } as AppEvent)
      }),
    )
    return () => {
      for (const cancel of off) cancel()
      out.stop()
    }
  })

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
    resolveHelpdesk: (ws, number) => bridge().ResolveHelpdesk(ws, number),
    composeIntent: (ws, text) => bridge().ComposeIntent(ws, text),
    runs: async (ws, key) => list(await bridge().Runs(ws, key ?? '')),
    run: (ws, runId) => bridge().Run(ws, runId),
    deleteRun: async (ws, runId) => {
      await bridge().DeleteRun(ws, runId)
    },
    search: async (ws, q) => list(await bridge().Search(ws, q)),
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
        instruction: o?.instruction ?? '',
      }),
    }),
    startRCA: async (ws, key, o) => ({
      jobId: await bridge().StartRCA(ws, key, {
        prUrl: o?.prUrl ?? '',
        resolution: o?.resolution ?? '',
        provider: o?.provider ?? '',
        model: o?.model ?? '',
        instruction: o?.instruction ?? '',
      }),
    }),
    startFix: async (ws, key, o) => ({
      jobId: await bridge().StartFix(ws, key, {
        dryRun: o?.dryRun ?? false,
        noPr: o?.noPr ?? false,
        local: o?.local ?? false,
        base: o?.base ?? '',
        acceptDeviation: o?.acceptDeviation ?? false,
        provider: o?.provider ?? '',
        model: o?.model ?? '',
        instruction: o?.instruction ?? '',
      }),
    }),
    startEval: async (ws, keys, o) => ({
      jobId: await bridge().StartEval(ws, keys ?? [], {
        provider: o?.provider ?? '',
        model: o?.model ?? '',
        concurrency: o?.concurrency ?? 0,
        retro: o?.retro ?? false,
        withRca: o?.withRca ?? false,
        rubric: o?.rubric ?? false,
      }),
    }),
    evalReports: async (ws) => list(await bridge().EvalReports(ws)),
    latestRetro: async (ws) => (await bridge().LatestRetro(ws)) ?? null,
    golden: async (ws) => list(await bridge().Golden(ws)),
    addGolden: (ws, o) => bridge().AddGolden(ws, o.key ?? '', o.runId ?? ''),
    configSummary: (ws) => bridge().ConfigSummary(ws),
    playbooks: async (ws) => list(await bridge().Playbooks(ws)),
    playbook: (ws, name) => bridge().Playbook(ws, name),
    savePlaybook: (ws, name, body) => bridge().SavePlaybook(ws, name, body),
    addPlaybook: (ws, name, body) => bridge().AddPlaybook(ws, name, body),
    deletePlaybook: async (ws, name) => {
      await bridge().DeletePlaybook(ws, name)
    },
    scaffoldPlaybooks: async (ws) => list(await bridge().ScaffoldPlaybooks(ws)),
    openPlaybook: async (ws, name) => {
      await bridge().OpenPlaybook(ws, name)
    },
    resume: async (ws, runId, answer, model) => ({
      jobId: await bridge().Resume(ws, runId, answer ?? '', model ?? ''),
    }),
    steer: async (ws, runId, text, model) => ({
      jobId: await bridge().Steer(ws, runId, text, model ?? ''),
      runId,
    }),
    runDiff: async (ws, runId) => {
      const d = await bridge().RunDiff(ws, runId)
      return { ...d, files: list(d.files) }
    },
    dropHunk: async (ws, runId, req) => {
      const d = await bridge().DropHunk(ws, runId, req.path, req.hunk, req.etag)
      return { ...d, files: list(d.files) }
    },
    mcpServers: async (ws, connect) => {
      const inv = await bridge().MCPServers(ws, connect ?? false)
      return {
        ...inv,
        servers: list(inv.servers),
        warnings: list(inv.warnings),
        permissions: list(inv.permissions),
      }
    },
    mcpTools: async (ws, server) => {
      const l = await bridge().MCPTools(ws, server)
      return { ...l, tools: list(l.tools), permissions: list(l.permissions) }
    },
    mcpCall: (ws, server, tool, args) =>
      bridge().MCPCall(ws, server, tool, (args ?? {}) as Record<string, unknown>),
    cancel: async (jobId) => {
      await bridge().Cancel(jobId)
    },
    register: async (ws) => list(await bridge().Register(ws)),
    doctor: async (ws) => list(await bridge().Doctor(ws)),
    quota: async () => list(await bridge().Quota()),
    version: () => bridge().Version(),
    openConfig: async (ws) => {
      await bridge().OpenConfig(ws)
    },
    openNote: async (ws, runId, path) => {
      await bridge().OpenNote(ws, runId, path)
    },
    openRunDir: async (ws, runId) => {
      await bridge().OpenRunDir(ws, runId)
    },
    subscribe,
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
