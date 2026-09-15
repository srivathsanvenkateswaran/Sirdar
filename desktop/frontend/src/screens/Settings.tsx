import { useCallback, useEffect, useState } from 'react'
import type { ConfigSummary, MCPInventory, Transport, Workspace } from '../api/types'
import { useProvidePrimaryAction } from '../components/shell/primaryAction'
import { reasonOf } from '../lib/format'
import Button from '../ui/button'
import ModalSheet, { type ModalNavGroup } from '../ui/modal-sheet'
import { AboutPage, LibraryPage, ReadingPage } from './settings/AppPages'
import {
  BudgetsPage,
  NotesPage,
  NotificationsPage,
  PermissionsPage,
  WebhooksPage,
} from './settings/ConfigPages'
import GeneralPage from './settings/GeneralPage'
import {
  AboutIcon,
  BudgetsIcon,
  CloudCheckIcon,
  GeneralIcon,
  LibraryIcon,
  NotesIcon,
  NotificationsIcon,
  PermissionsIcon,
  ProvidersIcon,
  ReadingIcon,
  ServersIcon,
  ToolIcon,
  WebhooksIcon,
} from './settings/icons'
import MCPPage, { type InventoryState } from './settings/MCPPage'
import ProvidersPage from './settings/ProvidersPage'
import { type DoctorState, type Loaded } from './settings/shared'
import ToolsPage, { useToolTester } from './settings/ToolsPage'
import './settings/settings.css'

export { CONFIG_DOCS_URL } from './settings/AppPages'

/**
 * The pages, in two groups: what belongs to the workspace, read from its
 * `.sirdar/config.yaml` and from doctor, and what belongs to this copy of
 * the app. The ids are the `#/settings/<page>` addresses.
 */
export const SETTINGS_GROUPS: ModalNavGroup[] = [
  {
    label: 'Settings',
    items: [
      { id: 'general', label: 'General', icon: <GeneralIcon /> },
      { id: 'providers', label: 'Providers', icon: <ProvidersIcon /> },
      { id: 'budgets', label: 'Budgets', icon: <BudgetsIcon /> },
      { id: 'mcp', label: 'MCP servers', icon: <ServersIcon /> },
      { id: 'tools', label: 'Try a tool', icon: <ToolIcon /> },
      { id: 'permissions', label: 'Permissions', icon: <PermissionsIcon /> },
      { id: 'notes', label: 'Notes', icon: <NotesIcon /> },
      { id: 'notifications', label: 'Notifications', icon: <NotificationsIcon /> },
      { id: 'webhooks', label: 'Webhooks', icon: <WebhooksIcon /> },
    ],
  },
  {
    label: 'This app',
    items: [
      { id: 'reading', label: 'Reading', icon: <ReadingIcon /> },
      { id: 'library', label: 'Library', icon: <LibraryIcon /> },
      { id: 'about', label: 'About', icon: <AboutIcon /> },
    ],
  },
]

const TITLES: Record<string, string> = Object.fromEntries(
  SETTINGS_GROUPS.flatMap((g) => g.items.map((item) => [item.id, item.label])),
)

/** Addresses the modal used to have, so an old link still opens a page. */
const ALIASES: Record<string, string> = { workspaces: 'general' }

/** The pages whose values are this browser's, not the workspace's. */
const APP_PAGES = new Set(['reading', 'library', 'about'])

/** A payload with the workspace it was read for. */
interface Tagged<T> {
  ws: string
  state: T
}

const IDLE = { status: 'idle' } as const
const untagged = { ws: '', state: IDLE }

export default function Settings(props: {
  open: boolean
  /** The page the address names, when it names one. */
  page?: string
  /** Called with the page the reader chose, so the address can follow. */
  onSelectPage?: (page: string) => void
  transport: Transport
  workspaces: Workspace[]
  /** The workspace whose config and servers are shown. */
  currentWorkspaceId?: string
  onClose: () => void
  onWorkspacesChanged: () => void
}): JSX.Element | null {
  const {
    open,
    transport,
    workspaces,
    currentWorkspaceId,
    onClose,
    onWorkspacesChanged,
    onSelectPage,
  } = props
  // The page comes from the address when a link named one (`#/settings/<page>`)
  // and from the reader's last choice otherwise; a new address wins over an
  // old choice. A page the modal does not have falls back to the first, so a
  // stale link opens the modal rather than an empty panel.
  const named = props.page ? (ALIASES[props.page] ?? props.page) : undefined
  const addressed = named && named in TITLES ? named : undefined
  const [localPage, setLocalPage] = useState(addressed ?? 'general')
  useEffect(() => {
    if (addressed) setLocalPage(addressed)
  }, [addressed])
  const page = localPage in TITLES ? localPage : 'general'
  function setPage(id: string): void {
    setLocalPage(id)
    onSelectPage?.(id)
  }

  /** Desktop build version, when the transport exposes one (Wails only). */
  const [version, setVersion] = useState<string | null>(null)
  /*
   * Everything the workspace pages show is tagged with the workspace it was
   * read for, and read as idle under any other: a doctor report or a server
   * list from one repository is a wrong answer under another's name, and
   * tagging is what makes that true without an effect that has to run first.
   */
  const ws = currentWorkspaceId ?? ''
  const [summaryFor, setSummary] = useState<Tagged<Loaded<ConfigSummary>>>(untagged)
  const [doctorFor, setDoctor] = useState<Tagged<DoctorState>>(untagged)
  const [inventoryFor, setInventory] = useState<Tagged<Loaded<MCPInventory>>>(untagged)
  const [testedFor, setTested] = useState('')
  const summary = summaryFor.ws === ws ? summaryFor.state : IDLE
  const doctor = doctorFor.ws === ws ? doctorFor.state : IDLE
  const inventory = inventoryFor.ws === ws ? inventoryFor.state : IDLE
  const tested = testedFor === ws
  const [connecting, setConnecting] = useState(false)
  const workspace = workspaces.find((w) => w.id === currentWorkspaceId)

  useEffect(() => {
    let cancelled = false
    transport
      .version?.()
      .then((v) => {
        if (!cancelled) setVersion(v)
      })
      .catch(() => {
        // The HTTP transport has no Version to fail; the Wails one rarely
        // does either. Either way the footer just omits the version.
      })
    return () => {
      cancelled = true
    }
  }, [transport])

  // The config summary is read once the modal is open on a workspace, and
  // re-read when the modal opens again: the file may have been edited in
  // between, which is the whole way settings change.
  useEffect(() => {
    if (!open || !ws) return
    let cancelled = false
    setSummary({ ws, state: { status: 'loading' } })
    transport
      .configSummary(ws)
      .then((data) => {
        if (!cancelled) setSummary({ ws, state: { status: 'done', data } })
      })
      .catch((err: unknown) => {
        if (!cancelled) setSummary({ ws, state: { status: 'error', message: reasonOf(err) } })
      })
    return () => {
      cancelled = true
    }
  }, [transport, ws, open])

  // The server listing is read the first time a page that needs it is shown.
  // Without `connect` it is a read of two config files, cheap enough to do
  // on arrival; reaching the servers waits for Test. The answer is kept only
  // while the listing is still the one that was asked for: a Test that
  // finished first, or a workspace change, wins over it.
  const needsInventory = open && (page === 'mcp' || page === 'tools')
  useEffect(() => {
    if (!needsInventory || !ws || inventory.status !== 'idle') return
    setInventory({ ws, state: { status: 'loading' } })
    const still = (prev: Tagged<Loaded<MCPInventory>>) =>
      prev.ws === ws && prev.state.status === 'loading'
    transport
      .mcpServers(ws, false)
      .then((data) => {
        setInventory((prev) => (still(prev) ? { ws, state: { status: 'done', data } } : prev))
      })
      .catch((err: unknown) => {
        setInventory((prev) =>
          still(prev) ? { ws, state: { status: 'error', message: reasonOf(err) } } : prev,
        )
      })
  }, [transport, ws, needsInventory, inventory.status])

  const runDoctor = useCallback(() => {
    if (!ws) return
    setDoctor({ ws, state: { status: 'loading' } })
    transport
      .doctor(ws)
      .then((checks) => setDoctor({ ws, state: { status: 'done', checks } }))
      .catch((err: unknown) => setDoctor({ ws, state: { status: 'error', message: reasonOf(err) } }))
  }, [transport, ws])

  const testServers = useCallback(() => {
    if (!ws || connecting) return
    setConnecting(true)
    transport
      .mcpServers(ws, true)
      .then((data) => {
        setInventory({ ws, state: { status: 'done', data } })
        setTested(ws)
      })
      .catch((err: unknown) => setInventory({ ws, state: { status: 'error', message: reasonOf(err) } }))
      .finally(() => setConnecting(false))
  }, [transport, ws, connecting])

  const tester = useToolTester(transport, currentWorkspaceId, inventory)

  // Call is the one filled button while Try a tool is up, so the sidebar's
  // New session steps down for it; the button itself is drawn on the page,
  // which is what `placement: 'screen'` tells the footer. Every other page
  // has Save, disabled.
  const tools = open && page === 'tools'
  useProvidePrimaryAction(
    tools
      ? {
          label: 'Call',
          onRun: tester.call,
          disabled: !tester.canCall,
          busy: tester.calling,
          title: tester.problem || undefined,
          placement: 'screen',
        }
      : null,
  )

  const inventoryState: InventoryState = { inventory, connecting, tested }
  const configProps = { transport, currentWorkspaceId, summary }

  const pages: Record<string, JSX.Element> = {
    general: (
      <GeneralPage
        transport={transport}
        workspaces={workspaces}
        currentWorkspaceId={currentWorkspaceId}
        summary={summary}
        doctor={doctor}
        onRunDoctor={runDoctor}
        onWorkspacesChanged={onWorkspacesChanged}
      />
    ),
    providers: (
      <ProvidersPage
        transport={transport}
        workspace={workspace}
        currentWorkspaceId={currentWorkspaceId}
        summary={summary}
        doctor={doctor}
        onRunDoctor={runDoctor}
      />
    ),
    budgets: <BudgetsPage {...configProps} />,
    mcp: (
      <MCPPage
        transport={transport}
        currentWorkspaceId={currentWorkspaceId}
        summary={summary}
        state={inventoryState}
        onTest={testServers}
      />
    ),
    tools: <ToolsPage currentWorkspaceId={currentWorkspaceId} inventory={inventory} tester={tester} />,
    permissions: <PermissionsPage {...configProps} />,
    notes: <NotesPage {...configProps} />,
    notifications: <NotificationsPage {...configProps} />,
    webhooks: <WebhooksPage {...configProps} />,
    reading: <ReadingPage />,
    library: <LibraryPage />,
    about: <AboutPage version={version} />,
  }

  /*
   * The footer. Save is disabled on every page: nothing here has an API to
   * write through yet, and the note says where the values come from. Try a
   * tool has its own commit button in the page, so its footer only closes.
   */
  const footer = tools ? (
    <Button variant="ghost" onClick={onClose}>
      Close
    </Button>
  ) : (
    <>
      <p className="settings-foot-note">
        {APP_PAGES.has(page)
          ? 'These apply as they are switched.'
          : 'Settings are read from .sirdar/config.yaml'}
      </p>
      <Button variant="ghost" onClick={onClose}>
        Cancel
      </Button>
      <Button variant="primary" disabled title="Nothing on this page is written by the app">
        Save
      </Button>
    </>
  )

  return (
    <ModalSheet
      open={open}
      title={TITLES[page] ?? 'Settings'}
      groups={SETTINGS_GROUPS}
      current={page}
      onSelect={setPage}
      onClose={onClose}
      navFooter={
        <>
          <span>{version ? `Sirdar v${version}` : 'Sirdar'}</span>
          {version && (
            <span title="Version reported by the desktop bridge">
              <CloudCheckIcon />
            </span>
          )}
        </>
      }
      footer={footer}
    >
      <div className="settings">{pages[page]}</div>
    </ModalSheet>
  )
}
