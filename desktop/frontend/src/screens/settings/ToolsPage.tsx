import { useEffect, useId, useRef, useState } from 'react'
import type { MCPCallResult, MCPInventory, MCPToolList, Transport } from '../../api/types'
import Button from '../../ui/button'
import { SettingCard } from '../../ui/setting-row'
import { message, VerdictChip, type Loaded } from './shared'

/** One hand-run call, kept for the session. */
export interface RecentCall {
  at: Date
  server: string
  tool: string
  result: MCPCallResult
}

/** What the tool tester holds between renders and across the modal closing. */
export interface ToolTester {
  server: string
  setServer: (server: string) => void
  tool: string
  setTool: (tool: string) => void
  args: string
  setArgs: (args: string) => void
  tools: Loaded<MCPToolList>
  /** The problem with the call as typed: bad JSON, no tool. Empty when it can go. */
  problem: string
  calling: boolean
  result: MCPCallResult | null
  /** A call that did not happen: the transport rejected. */
  failure: string
  recent: RecentCall[]
  call: () => void
  canCall: boolean
}

/**
 * The tool a fresh listing selects: the first one a run could call. A server
 * whose first tool is a write would otherwise open on a denied verdict, and
 * the point of the page is to see something come back.
 */
export function firstAllowed(list: MCPToolList): string {
  return (list.tools.find((t) => t.verdict === 'allowed') ?? list.tools[0])?.name ?? ''
}

/**
 * The tool tester's state, lifted out of the page so the modal can publish
 * Call as the screen's one primary action while the page is up, and so
 * Recent calls survive the modal closing: Settings stays mounted, the page
 * does not.
 */
export function useToolTester(
  transport: Transport,
  workspaceId: string | undefined,
  inventory: Loaded<MCPInventory>,
): ToolTester {
  const [server, setServer] = useState('')
  const [tool, setTool] = useState('')
  const [args, setArgs] = useState('{}')
  const [tools, setTools] = useState<Loaded<MCPToolList>>({ status: 'idle' })
  const [calling, setCalling] = useState(false)
  const [result, setResult] = useState<MCPCallResult | null>(null)
  const [failure, setFailure] = useState('')
  const [recent, setRecent] = useState<RecentCall[]>([])
  const listed = useRef<Record<string, MCPToolList>>({})

  // The first server in the listing is selected once there is one.
  const servers = inventory.status === 'done' ? inventory.data.servers : []
  useEffect(() => {
    if (!server && servers.length > 0) setServer(servers[0].name)
    if (server && servers.length > 0 && !servers.some((s) => s.name === server)) {
      setServer(servers[0].name)
    }
  }, [server, servers])

  useEffect(() => {
    if (!workspaceId || !server) {
      setTools({ status: 'idle' })
      return
    }
    const cached = listed.current[server]
    if (cached) {
      setTools({ status: 'done', data: cached })
      setTool((t) => (cached.tools.some((x) => x.name === t) ? t : firstAllowed(cached)))
      return
    }
    let cancelled = false
    setTools({ status: 'loading' })
    transport
      .mcpTools(workspaceId, server)
      .then((data) => {
        if (cancelled) return
        listed.current[server] = data
        setTools({ status: 'done', data })
        setTool(firstAllowed(data))
      })
      .catch((err: unknown) => {
        if (!cancelled) setTools({ status: 'error', message: message(err) })
      })
    return () => {
      cancelled = true
    }
  }, [transport, workspaceId, server])

  let problem = ''
  let parsed: unknown = {}
  if (!tool) problem = 'Choose a tool'
  else {
    try {
      parsed = args.trim() ? JSON.parse(args) : {}
      if (parsed === null || typeof parsed !== 'object' || Array.isArray(parsed)) {
        problem = 'Arguments must be a JSON object'
      }
    } catch {
      problem = 'Arguments are not valid JSON'
    }
  }
  const canCall = Boolean(workspaceId && server && tool && !problem && !calling)

  // Not memoised: the primary-action slot reads the latest one out of a ref,
  // and `parsed` is fresh on every render anyway.
  function call(): void {
    if (!canCall || !workspaceId) return
    setCalling(true)
    setFailure('')
    setResult(null)
    const at = new Date()
    transport
      .mcpCall(workspaceId, server, tool, parsed)
      .then((got) => {
        setResult(got)
        setRecent((r) => [{ at, server, tool, result: got }, ...r].slice(0, 20))
      })
      .catch((err: unknown) => setFailure(message(err)))
      .finally(() => setCalling(false))
  }

  return {
    server,
    setServer,
    tool,
    setTool,
    args,
    setArgs,
    tools,
    problem,
    calling,
    result,
    failure,
    recent,
    call,
    canCall,
  }
}

function took(ms: number): string {
  return ms < 1000 ? `${ms}ms` : `${(ms / 1000).toFixed(1)}s`
}

function clock(at: Date): string {
  return at.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
}

function Chevron(): JSX.Element {
  return (
    <svg
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.5"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
      focusable="false"
    >
      <path d="m6 9 6 6 6-6" />
    </svg>
  )
}

/**
 * Try a tool: one MCP tool by hand, the verdict Sirdar's rules give it,
 * and what came back. The same three calls `sirdar mcp tools` and
 * `sirdar mcp call` make, given a face.
 */
export default function ToolsPage({
  currentWorkspaceId,
  inventory,
  tester,
}: {
  currentWorkspaceId?: string
  inventory: Loaded<MCPInventory>
  tester: ToolTester
}): JSX.Element {
  const serverId = useId()
  const toolId = useId()
  const argsId = useId()

  if (!currentWorkspaceId) {
    return <p className="empty-state">Choose a workspace from the switcher to call one of its tools.</p>
  }

  const servers = inventory.status === 'done' ? inventory.data.servers : []
  const chosen =
    tester.tools.status === 'done'
      ? tester.tools.data.tools.find((t) => t.name === tester.tool)
      : undefined
  const { result } = tester

  return (
    <>
      <SettingCard heading="Call a tool">
        {inventory.status === 'error' ? (
          <p className="form-error">{inventory.message}</p>
        ) : inventory.status !== 'done' ? (
          <p className="empty-state">Listing servers…</p>
        ) : servers.length === 0 ? (
          <p className="empty-state">No MCP servers to call. Add them to .mcp.json first.</p>
        ) : (
          <div className="settings-tester">
            <div className="settings-tester__selects">
              <span className="settings-select">
                <select
                  id={serverId}
                  className="settings-select__input"
                  aria-label="Server"
                  value={tester.server}
                  onChange={(e) => tester.setServer(e.target.value)}
                >
                  {servers.map((s) => (
                    <option key={`${s.scope}/${s.name}`} value={s.name}>
                      {s.name}
                    </option>
                  ))}
                </select>
                <Chevron />
              </span>
              <span className="settings-select settings-select--wide">
                <select
                  id={toolId}
                  className="settings-select__input"
                  aria-label="Tool"
                  value={tester.tool}
                  onChange={(e) => tester.setTool(e.target.value)}
                  disabled={tester.tools.status !== 'done'}
                >
                  {tester.tools.status === 'done' ? (
                    tester.tools.data.tools.map((t) => (
                      <option key={t.name} value={t.name}>
                        {t.name} · {t.verdict}
                      </option>
                    ))
                  ) : (
                    <option value="">
                      {tester.tools.status === 'error' ? 'Tools could not be listed' : 'Listing tools…'}
                    </option>
                  )}
                </select>
                <Chevron />
              </span>
            </div>
            {tester.tools.status === 'error' && <p className="form-error">{tester.tools.message}</p>}
            <label className="visually-hidden" htmlFor={argsId}>
              Arguments, as JSON
            </label>
            <textarea
              id={argsId}
              className="settings-textarea"
              rows={3}
              spellCheck={false}
              value={tester.args}
              onChange={(e) => tester.setArgs(e.target.value)}
            />
            <div className="settings-verdict-line">
              {chosen ? (
                <>
                  <VerdictChip verdict={chosen.verdict} />
                  <span>
                    {chosen.rule} · {chosen.reason}
                  </span>
                </>
              ) : (
                <span>{tester.problem || 'Choose a tool'}</span>
              )}
              {chosen && tester.problem && <span className="form-error">{tester.problem}</span>}
              {/* The page's one filled button: the sidebar's New session is demoted while it is up. */}
              <Button
                variant="primary"
                disabled={!tester.canCall}
                busy={tester.calling}
                onClick={tester.call}
                title={
                  chosen?.verdict === 'denied'
                    ? 'Sirdar refuses this call the way it would refuse a run; the refusal is the answer'
                    : undefined
                }
              >
                Call
              </Button>
            </div>
            {tester.failure && (
              <p className="form-error" role="alert">
                {tester.failure}
              </p>
            )}
            {result && (
              <div className="settings-well" aria-live="polite">
                <div className="settings-well__head">
                  <VerdictChip verdict={result.verdict} />
                  <span>
                    {result.verdict === 'denied'
                      ? result.reason
                      : result.isError
                        ? 'The tool reported an error'
                        : result.reason}
                  </span>
                  <span className="settings-well__took">
                    took {took(result.tookMs)}
                    {result.truncated ? ' · truncated at 64 KiB' : ''}
                  </span>
                </div>
                {result.error && <p className="form-error">{result.error}</p>}
                {result.result && <pre className="settings-well__body">{result.result}</pre>}
              </div>
            )}
          </div>
        )}
      </SettingCard>

      <SettingCard heading="Recent calls">
        {tester.recent.length === 0 ? (
          <p className="empty-state">Nothing called yet this session.</p>
        ) : (
          <div className="settings-calls" role="list">
            {tester.recent.map((c, i) => (
              <div className="settings-call" role="listitem" key={`${c.at.getTime()}-${i}`}>
                <span className="settings-mono">
                  {c.server} · {c.tool}
                </span>
                <span>
                  <VerdictChip verdict={c.result.verdict} />
                </span>
                <span className="settings-mono">{took(c.result.tookMs)}</span>
                <span className="settings-mono settings-call__time">{clock(c.at)}</span>
              </div>
            ))}
          </div>
        )}
      </SettingCard>
    </>
  )
}
