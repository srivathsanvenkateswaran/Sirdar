import { useId } from 'react'
import type { ConfigSummary, MCPInventory, MCPServer, Transport } from '../../api/types'
import Badge from '../../ui/badge'
import SettingRow, { SettingCard } from '../../ui/setting-row'
import Toggle from '../../ui/toggle'
import { basename, Chips, count, OpenConfig, type Loaded } from './shared'

/** What the MCP inventory is up to, and whether Test has reached the servers. */
export interface InventoryState {
  inventory: Loaded<MCPInventory>
  /** True while `mcpServers(connect)` is in flight. */
  connecting: boolean
  /** True once a connected listing has come back, so rows show their counts. */
  tested: boolean
}

/** The status line of one server row. */
function statusOf(server: MCPServer, tested: boolean): { text: string; level?: 'fail' } {
  if (server.error) return { text: `failed · ${server.error}`, level: 'fail' }
  if (server.connected) {
    const took = server.tookMs ? ` in ${server.tookMs < 1000 ? `${server.tookMs}ms` : `${(server.tookMs / 1000).toFixed(1)}s`}` : ''
    return { text: `${count(server.tools ?? 0, 'tool')} · connected${took}` }
  }
  const where = `${server.scope} · ${basename(server.source)}`
  return { text: tested ? `${where} · not reached` : where }
}

/**
 * MCP servers: which servers a run here would be offered, and which of
 * their tools it may call. Test connects to every server the listing
 * names — `sirdar mcp list --connect` has no per-server form — and the rows
 * fill in with a tool count or the error the connection gave.
 */
export default function MCPPage({
  transport,
  currentWorkspaceId,
  summary,
  state,
  onTest,
}: {
  transport: Transport
  currentWorkspaceId?: string
  summary: Loaded<ConfigSummary>
  state: InventoryState
  onTest: () => void
}): JSX.Element {
  const toggleLabel = useId()
  const { inventory, connecting, tested } = state
  const configPath = summary.status === 'done' ? summary.data.general.configPath : undefined

  if (!currentWorkspaceId) {
    return <p className="empty-state">Choose a workspace from the switcher to list its MCP servers.</p>
  }

  const workspaceOnly =
    inventory.status === 'done'
      ? inventory.data.workspaceOnly
      : summary.status === 'done'
        ? summary.data.mcp.workspaceOnly
        : true
  const patterns =
    inventory.status === 'done'
      ? inventory.data.permissions
      : summary.status === 'done'
        ? summary.data.permissions.mcp
        : []

  return (
    <>
      <SettingCard heading="Servers in .mcp.json">
        {inventory.status === 'error' ? (
          <p className="form-error">{inventory.message}</p>
        ) : inventory.status !== 'done' ? (
          <p className="empty-state">Listing servers…</p>
        ) : inventory.data.servers.length === 0 ? (
          <p className="empty-state">
            No MCP servers. A run here has no MCP tools until the servers the playbooks need are
            added to .mcp.json.
          </p>
        ) : (
          <div className="settings-servers" role="list">
            {inventory.data.servers.map((server) => {
              const status = statusOf(server, tested)
              return (
                <div className="settings-server" role="listitem" key={`${server.scope}/${server.name}`}>
                  <span className="settings-server__name">
                    <span>{server.name}</span>
                    <Badge title="Transport">{server.transport}</Badge>
                  </span>
                  <span className="settings-server__status">
                    <span className="settings-status" data-level={status.level ?? 'none'}>
                      {connecting ? 'connecting…' : status.text}
                    </span>
                    {server.note && <span className="settings-server__note">{server.note}</span>}
                  </span>
                  <button
                    type="button"
                    className="sd-setting-button"
                    aria-label={`${server.error ? 'Retry' : 'Test'} ${server.name}`}
                    disabled={connecting}
                    onClick={onTest}
                  >
                    {server.error ? 'Retry' : 'Test'}
                  </button>
                </div>
              )
            })}
          </div>
        )}
        {inventory.status === 'done' && inventory.data.warnings.length > 0 && (
          <ul className="settings-warnings">
            {inventory.data.warnings.map((w) => (
              <li key={w}>{w}</li>
            ))}
          </ul>
        )}
      </SettingCard>

      <SettingCard heading="What a run may call">
        <SettingRow
          label={<span id={toggleLabel}>Workspace servers only</span>}
          value={
            workspaceOnly
              ? 'The run sees .mcp.json and nothing from your global config'
              : 'Every user-level MCP server is visible to the run'
          }
          help="Set by mcp.workspaceOnly in config.yaml; shown here, not switched here."
          control={
            <Toggle
              label="Workspace servers only"
              labelledBy={toggleLabel}
              checked={workspaceOnly}
              onChange={() => {}}
              disabled
            />
          }
        />
        <SettingRow
          label="Allowed tool patterns"
          value={
            <Chips
              items={patterns}
              empty="None: read-shaped tools are allowed and write-shaped ones are denied by name"
            />
          }
          help="Everything else is denied by name: write words (create, update, delete, send, run) and generic passthroughs."
          control={
            <OpenConfig
              transport={transport}
              workspaceId={currentWorkspaceId}
              path={configPath}
              setting="Allowed tool patterns"
            />
          }
        />
      </SettingCard>
    </>
  )
}
