// @vitest-environment jsdom
import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import '@testing-library/jest-dom/vitest'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { Check, Transport } from '../api/types'
import { PrimaryActionProvider, usePrimaryAction } from '../components/shell/primaryAction'
import { composerPrefs, resetComposerPrefs } from '../lib/composerPrefs'
import { resetShowLibrary, showLibrary } from '../lib/library'
import { prefersRTL, resetPreferRTL, setPreferRTL } from '../lib/rtl'
import { resetSessionLayout, sessionLayout } from '../lib/sessionLayout'
import { resetSessionsShow, sessionsShow } from '../lib/sessionsShow'
import { resetTheme, theme } from '../lib/theme'
import {
  configSummary,
  createFakeTransport,
  mcpInventory,
  mcpTools,
  workspace as sampleWorkspace,
  type FakeTransport,
} from '../store/fakeTransport'
import Settings, { CONFIG_DOCS_URL, SETTINGS_GROUPS } from './Settings'
import { RTL_LABEL } from './settings/AppPages'

const WORKSPACE = sampleWorkspace({ id: 'ws1', name: 'omni', root: '/repos/omni' })

/** The whole doctor report the sample workspace gives, provider rows first. */
const DOCTOR: Check[] = [
  { name: 'config', ok: true, level: 'ok', detail: '/repos/omni/.sirdar/config.yaml' },
  { name: 'claude --version', ok: true, level: 'ok', detail: '1.0.0' },
  { name: 'claude auth status', ok: true, level: 'ok', detail: 'logged in as sri' },
  { name: 'claude environment', ok: true, level: 'ok', detail: '' },
  { name: 'mcp', ok: true, level: 'warn', detail: 'no workspace .mcp.json' },
  { name: 'notes.dir', ok: false, level: 'fail', detail: 'not writable' },
]

afterEach(() => {
  cleanup()
  vi.useRealTimers()
  localStorage.clear()
  resetPreferRTL()
  resetShowLibrary()
  resetTheme()
  resetSessionsShow()
  resetSessionLayout()
  resetComposerPrefs()
})

/** A transport over the fake, with whatever a case wants overridden. */
function transportWith(over: Partial<Transport> = {}, seed: Parameters<typeof createFakeTransport>[0] = {}): FakeTransport {
  const fake = createFakeTransport({ workspaces: [WORKSPACE], configSummary: configSummary(), ...seed })
  return Object.assign(fake, over)
}

/** What the sidebar's footer would draw: the screen's published primary action. */
function PrimaryProbe(): JSX.Element {
  const action = usePrimaryAction()
  return (
    <output data-testid="primary">
      {action
        ? `${action.label}${action.disabled ? ' (disabled)' : ''} on the ${action.placement ?? 'footer'}`
        : 'New session'}
    </output>
  )
}

function open(
  props: Partial<React.ComponentProps<typeof Settings>> = {},
  transport: Transport = transportWith(),
) {
  const onClose = vi.fn()
  const onSelectPage = vi.fn()
  const onWorkspacesChanged = vi.fn()
  const view = render(
    <PrimaryActionProvider>
      <Settings
        open
        transport={transport}
        workspaces={[WORKSPACE]}
        currentWorkspaceId="ws1"
        onClose={onClose}
        onSelectPage={onSelectPage}
        onWorkspacesChanged={onWorkspacesChanged}
        {...props}
      />
      <PrimaryProbe />
    </PrimaryActionProvider>,
  )
  return { ...view, onClose, onSelectPage, onWorkspacesChanged }
}

/** Opens a page by its nav row. */
function go(page: string): void {
  fireEvent.click(within(screen.getByRole('navigation')).getByRole('button', { name: page }))
}

describe('the settings nav', () => {
  it('lists the thirteen pages in two groups, General first', () => {
    open()
    const nav = screen.getByRole('navigation', { name: 'Settings sections' })
    expect(within(nav).getByText('Settings')).toBeInTheDocument()
    expect(within(nav).getByText('This app')).toBeInTheDocument()
    expect(
      SETTINGS_GROUPS.flatMap((g) => g.items.map((i) => i.id)),
    ).toEqual([
      'general', 'providers', 'budgets', 'mcp', 'tools', 'permissions', 'notes',
      'playbooks', 'notifications', 'webhooks', 'reading', 'library', 'about',
    ])
    expect(screen.getByRole('dialog', { name: 'General' })).toBeInTheDocument()
    expect(within(nav).getByRole('button', { name: 'General' })).toHaveAttribute('aria-current', 'page')
  })

  it('opens the page the address names and tells the address about a chosen one', () => {
    const { onSelectPage } = open({ page: 'budgets' })
    expect(screen.getByRole('dialog', { name: 'Budgets' })).toBeInTheDocument()

    go('Permissions')
    expect(onSelectPage).toHaveBeenCalledWith('permissions')
    expect(screen.getByRole('dialog', { name: 'Permissions' })).toBeInTheDocument()
  })

  it('falls back to General for a page it does not have, and maps the old workspaces address there', () => {
    const { unmount } = open({ page: 'nope' })
    expect(screen.getByRole('dialog', { name: 'General' })).toBeInTheDocument()
    unmount()
    open({ page: 'workspaces' })
    expect(screen.getByRole('dialog', { name: 'General' })).toBeInTheDocument()
  })

  it('names the version in the nav foot when the bridge reports one, and just Sirdar otherwise', async () => {
    const { unmount } = open({}, transportWith({ version: async () => '0.9.2' }))
    expect(await screen.findByText('Sirdar v0.9.2')).toBeInTheDocument()
    unmount()
    open({}, transportWith())
    expect(screen.getByText('Sirdar')).toBeInTheDocument()
    expect(screen.queryByText(/Sirdar v/)).toBeNull()
  })
})

describe('Save', () => {
  it.each(
    SETTINGS_GROUPS.flatMap((g) => g.items.map((i) => i.label)).filter(
      // The two pages that write have their own commit button in the page,
      // so the footer's disabled Save is not drawn there at all.
      (l) => l !== 'Try a tool' && l !== 'Playbooks',
    ),
  )(
    'is disabled on %s, with a footer note saying why',
    (label) => {
      open()
      go(label)
      const save = screen.getByRole('button', { name: 'Save' })
      expect(save).toBeDisabled()
      // Published as the window's one filled button, drawn here, so the
      // sidebar's New session steps down rather than making two.
      expect(screen.getByTestId('primary')).toHaveTextContent('Save (disabled) on the screen')
      expect(screen.getByRole('button', { name: 'Cancel' })).toBeInTheDocument()
      expect(
        screen.getByText(/Settings are read from \.sirdar\/config\.yaml|These apply as they are switched/),
      ).toBeInTheDocument()
    },
  )

  it('is absent on Try a tool, where Call is the page\'s own commit', async () => {
    open()
    go('Try a tool')
    expect(screen.queryByRole('button', { name: 'Save' })).toBeNull()
    expect(screen.getByRole('button', { name: 'Close' })).toBeInTheDocument()
    await screen.findByRole('button', { name: 'Call' })
  })

  it('Cancel and Close both close the modal', () => {
    const { onClose } = open()
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }))
    expect(onClose).toHaveBeenCalledTimes(1)
    go('Try a tool')
    fireEvent.click(screen.getByRole('button', { name: 'Close' }))
    expect(onClose).toHaveBeenCalledTimes(2)
  })
})

describe('General', () => {
  it('reads the workspace, root, notes directory and language off the config summary', async () => {
    open()
    expect(await screen.findByText('/repos/omni/notes')).toBeInTheDocument()
    // The name and the root appear on the Workspace card and on the registry below it.
    expect(screen.getAllByText('omni').length).toBeGreaterThan(1)
    expect(screen.getAllByText('/repos/omni').length).toBeGreaterThan(1)
    expect(screen.getByText("Notes in en, customer replies in the ticket's own language")).toBeInTheDocument()
  })

  it('says who you are, where that came from, and the other spellings', async () => {
    open()
    expect(await screen.findByText('sri@acme.com')).toBeInTheDocument()
    expect(screen.getByText('from the me block in config.yaml')).toBeInTheDocument()
    expect(screen.getByText(/Also known as Sri Venkateswaran, sri/)).toBeInTheDocument()
    // The avatar is the initials of the address's local part.
    expect(document.querySelector('.settings-you .sd-avatar')?.textContent).toBe('S')
  })

  it('names the rule that answered when it is not the me block', async () => {
    const summary = configSummary({ me: { email: 'sri@acme.com', names: [], source: 'git' } })
    open({}, transportWith({}, { configSummary: summary }))
    expect(await screen.findByText('from git config')).toBeInTheDocument()
  })

  it('says what to add when the workspace can name nobody', async () => {
    const summary = configSummary({ me: { email: '', names: [], source: '' } })
    open({}, transportWith({}, { configSummary: summary }))
    expect(
      await screen.findByText('Nobody. Add a me block with your email to config.yaml.'),
    ).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /config.*: You/ })).toBeInTheDocument()
  })

  it('offers to copy the config path where the transport cannot open files', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined)
    Object.assign(navigator, { clipboard: { writeText } })
    open()
    const copy = await screen.findByRole('button', { name: 'Copy config path: Root' })
    fireEvent.click(copy)
    await waitFor(() => expect(writeText).toHaveBeenCalledWith('/repos/omni/.sirdar/config.yaml'))
    expect(await screen.findByText('Copied')).toBeInTheDocument()
  })

  it('opens the config through the bridge where the transport can', async () => {
    const openConfig = vi.fn().mockResolvedValue(undefined)
    open({}, transportWith({ openConfig }))
    fireEvent.click(await screen.findByRole('button', { name: 'Open config: Root' }))
    await waitFor(() => expect(openConfig).toHaveBeenCalledWith('ws1'))
  })

  it('switches the theme live and remembers it', () => {
    open()
    fireEvent.click(screen.getByRole('radio', { name: 'Dark' }))
    expect(theme()).toBe('dark')
    expect(document.documentElement.dataset.theme).toBe('dark')
    expect(screen.getByText('Dark', { selector: '.sd-setting-row__value' })).toBeInTheDocument()
  })

  it('switches the session layout, listing Conversation first as the default, and remembers it', () => {
    open()
    const group = screen.getByRole('radiogroup', { name: 'Session layout' })
    expect(within(group).getAllByRole('radio').map((o) => o.textContent)).toEqual(['Conversation', 'Document', 'Workbench'])
    expect(screen.getByRole('radio', { name: 'Conversation' })).toBeChecked()
    fireEvent.click(screen.getByRole('radio', { name: 'Workbench' }))
    expect(sessionLayout()).toBe('workbench')
    expect(localStorage.getItem('sirdar.sessionLayout')).toBe('workbench')
    expect(
      screen.getByText(/^Workbench: documents over a structured console/, { selector: '.sd-setting-row__value' }),
    ).toBeInTheDocument()
  })

  it('turns the composer\u2019s reading off for this workspace, and remembers it', () => {
    open()
    const control = screen.getByRole('switch', { name: 'Read what I type' })
    expect(control).toHaveAttribute('aria-checked', 'true')
    expect(composerPrefs('ws1').intentAssist).toBe(true)
    fireEvent.click(control)
    expect(composerPrefs('ws1').intentAssist).toBe(false)
    expect(screen.getByText('Off', { selector: '.sd-setting-row__value' })).toBeInTheDocument()
    // Another workspace is unaffected: the habit is per repository.
    expect(composerPrefs('ws2').intentAssist).toBe(true)
  })

  it('switches which ticket number the sessions show, and remembers it', () => {
    open()
    expect(screen.getByRole('radio', { name: 'Tracker number' })).toBeChecked()
    fireEvent.click(screen.getByRole('radio', { name: 'Helpdesk number' }))
    expect(sessionsShow()).toBe('helpdesk')
    expect(localStorage.getItem('sirdar.sessionsShow')).toBe('helpdesk')
    expect(
      screen.getByText('The helpdesk number', { selector: '.sd-setting-row__value' }),
    ).toBeInTheDocument()
  })

  it('runs doctor and lists the rows with the CLI\'s own marks', async () => {
    const doctor = vi.fn().mockResolvedValue(DOCTOR)
    open({}, transportWith({ doctor }))
    fireEvent.click(screen.getByRole('button', { name: 'Run doctor' }))
    expect(await screen.findByText('6 checks read')).toBeInTheDocument()
    expect(screen.getByText('!!')).toBeInTheDocument()
    expect(screen.getByText('XX')).toBeInTheDocument()
    expect(screen.getByText('not writable')).toBeInTheDocument()
    expect(doctor).toHaveBeenCalledWith('ws1')
  })

  it('shows why the summary could not be read instead of an empty page', async () => {
    open({}, transportWith({ configSummary: vi.fn().mockRejectedValue(new Error('no such workspace')) }))
    expect(await screen.findByText('no such workspace')).toBeInTheDocument()
  })

  it('asks for nothing and says so when no workspace is selected', () => {
    const configSummarySpy = vi.fn()
    open({ currentWorkspaceId: undefined, workspaces: [] }, transportWith({ configSummary: configSummarySpy }))
    expect(configSummarySpy).not.toHaveBeenCalled()
    expect(screen.getByText(/Choose a workspace from the switcher/)).toBeInTheDocument()
  })

  it('registers a workspace and reports the failure when it cannot', async () => {
    const addWorkspace = vi.fn().mockResolvedValue(WORKSPACE)
    const { onWorkspacesChanged } = open({}, transportWith({ addWorkspace }))
    fireEvent.change(screen.getByLabelText('Workspace path'), { target: { value: '/repos/sirdar' } })
    fireEvent.click(screen.getByRole('button', { name: 'Add workspace' }))
    await waitFor(() => expect(addWorkspace).toHaveBeenCalledWith('/repos/sirdar'))
    await waitFor(() => expect(onWorkspacesChanged).toHaveBeenCalled())

    addWorkspace.mockRejectedValueOnce(new Error('bad path'))
    fireEvent.change(screen.getByLabelText('Workspace path'), { target: { value: '/nope' } })
    fireEvent.click(screen.getByRole('button', { name: 'Add workspace' }))
    expect(await screen.findByText('bad path')).toBeInTheDocument()
  })

  // window.confirm blocks the whole webview, run stream included, so the
  // button arms itself instead and disarms after five seconds.
  it('removes a workspace only on the second press, and disarms on its own', async () => {
    const removeWorkspace = vi.fn().mockResolvedValue(undefined)
    const { onWorkspacesChanged, unmount } = open({}, transportWith({ removeWorkspace }))

    fireEvent.click(screen.getByRole('button', { name: 'Remove omni' }))
    expect(removeWorkspace).not.toHaveBeenCalled()
    fireEvent.click(await screen.findByRole('button', { name: 'Confirm remove omni' }))
    await waitFor(() => expect(removeWorkspace).toHaveBeenCalledWith('ws1'))
    await waitFor(() => expect(onWorkspacesChanged).toHaveBeenCalled())

    vi.useFakeTimers()
    fireEvent.click(screen.getByRole('button', { name: 'Remove omni' }))
    await vi.waitFor(() =>
      expect(screen.getByRole('button', { name: 'Confirm remove omni' })).toBeInTheDocument(),
    )
    act(() => vi.advanceTimersByTime(5000))
    expect(screen.getByRole('button', { name: 'Remove omni' })).toBeInTheDocument()
    expect(removeWorkspace).toHaveBeenCalledTimes(1)

    fireEvent.click(screen.getByRole('button', { name: 'Remove omni' }))
    unmount()
    expect(vi.getTimerCount()).toBe(0)
  })
})

describe('Workspaces', () => {
  it('keeps the row and says why when a removal is refused', async () => {
    const removeWorkspace = vi.fn().mockRejectedValue(new Error('a run is in progress'))
    const { onWorkspacesChanged } = open({}, transportWith({ removeWorkspace }))
    fireEvent.click(screen.getByRole('button', { name: 'Remove omni' }))
    fireEvent.click(screen.getByRole('button', { name: 'Confirm remove omni' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('Could not remove: a run is in progress')
    expect(screen.getByRole('button', { name: 'Remove omni' })).toBeInTheDocument()
    expect(onWorkspacesChanged).not.toHaveBeenCalled()
  })
})

describe('Providers', () => {
  it('shows the default provider with its mark and lists every provider with its facts', async () => {
    open({ page: 'providers' })
    expect(await screen.findByText('claude · sonnet · subscription')).toBeInTheDocument()
    const table = screen.getByRole('table', { name: 'Providers' })
    expect(within(table).getAllByRole('row')).toHaveLength(8)
    expect(within(table).getByText('Claude Code CLI · stream-json')).toBeInTheDocument()
    expect(within(table).getAllByText('refused')).toHaveLength(2)
    expect(within(table).getAllByText('every call mediated')).toHaveLength(4)
    expect(within(table).getAllByText('not checked')).toHaveLength(6)
    // agy is off under Google's Antigravity terms, with or without doctor.
    expect(within(table).getByText('disabled')).toHaveAttribute('title', 'disabled (Antigravity terms)')
    expect(within(table).getByText('disabled (Antigravity terms)')).toBeInTheDocument()
  })

  it('prints only the parts the config names when the model is empty', async () => {
    const summary = configSummary()
    summary.general.model = ''
    open({ page: 'providers' }, transportWith({}, { configSummary: summary }))
    expect(await screen.findByText('claude · subscription')).toBeInTheDocument()
    expect(screen.queryByText(/· ·/)).toBeNull()
  })

  it('reads the agy row doctor emits for a workspace that still names it', async () => {
    const doctor = vi.fn().mockResolvedValue([
      { name: 'agy', ok: false, level: 'fail', detail: 'disabled (Antigravity terms)' },
    ] satisfies Check[])
    open(
      { page: 'providers', workspaces: [{ ...WORKSPACE, provider: 'agy' }] },
      transportWith({ doctor }, { configSummary: configSummary({ general: { ...configSummary().general, provider: 'agy' } }) }),
    )
    fireEvent.click(await screen.findByRole('button', { name: 'Check agy' }))
    const table = screen.getByRole('table', { name: 'Providers' })
    const cell = await within(table).findByText('disabled')
    expect(cell).toHaveAttribute('data-level', 'fail')
    expect(cell).toHaveAttribute('title', 'disabled (Antigravity terms)')
    expect(within(table).getAllByText('not checked')).toHaveLength(6)
  })

  it('Check re-runs doctor and fills the sign-in column from its rows', async () => {
    const doctor = vi.fn().mockResolvedValue(DOCTOR)
    open({ page: 'providers' }, transportWith({ doctor }))
    fireEvent.click(await screen.findByRole('button', { name: 'Check claude' }))
    expect(doctor).toHaveBeenCalledWith('ws1')
    const table = screen.getByRole('table', { name: 'Providers' })
    expect(await within(table).findByText('signed in')).toHaveAttribute('title', 'logged in as sri')
    // Doctor only looked at the workspace's own provider; agy stays disabled.
    expect(within(table).getAllByText('not checked')).toHaveLength(5)
  })

  it('reads a failing login as failed, with the row that failed', async () => {
    const doctor = vi.fn().mockResolvedValue([
      { name: 'claude auth status', ok: false, level: 'fail', detail: 'not logged in' },
    ] satisfies Check[])
    open({ page: 'providers' }, transportWith({ doctor }))
    fireEvent.click(await screen.findByRole('button', { name: 'Check codex' }))
    const cell = await screen.findByText('failed')
    expect(cell).toHaveAttribute('data-level', 'fail')
    expect(cell).toHaveAttribute('title', 'claude auth status: not logged in')
  })
})

describe('Budgets, Permissions and Notes', () => {
  it('reads the caps off the summary', async () => {
    open({ page: 'budgets' })
    expect(await screen.findByText('20 turns')).toBeInTheDocument()
    expect(screen.getByText('20 minutes')).toBeInTheDocument()
    expect(screen.getByText('2.00 USD')).toBeInTheDocument()
    expect(screen.getByText('6 minutes of silence')).toBeInTheDocument()
  })

  it('shows the allow-lists as chips and says what an empty one means', async () => {
    open({ page: 'permissions' })
    // git status* is on both the bash and the fix-bash lists.
    expect(await screen.findAllByText('git status*')).toHaveLength(2)
    expect(screen.getByText('npm test*')).toBeInTheDocument()
    expect(screen.getByText(/every web fetch is denied/)).toBeInTheDocument()
    expect(screen.getByText(/reads stay inside the workspace/)).toBeInTheDocument()
  })

  it('shows the note filenames, the template source and the rtl markup', async () => {
    open({ page: 'notes' })
    expect(await screen.findByText('{{key}}-triage.md')).toBeInTheDocument()
    expect(screen.getByText('Embedded defaults')).toBeInTheDocument()
    expect(screen.getByText(/On: a right-to-left paragraph is wrapped/)).toBeInTheDocument()
  })
})

describe('Notifications and Webhooks', () => {
  it('reads the summary redacted: schemes, never references', async () => {
    open({ page: 'notifications' })
    expect(await screen.findByText('completed, failed')).toBeInTheDocument()
    expect(screen.getByText('env: reference')).toBeInTheDocument()
    expect(screen.getByText(/Secrets are never shown/)).toBeInTheDocument()

    go('Webhooks')
    expect(await screen.findByText('10m0s')).toBeInTheDocument()
    expect(screen.getByText('only tickets assigned to me')).toBeInTheDocument()
    expect(screen.getByText(/signed deliveries · keychain: reference/)).toBeInTheDocument()
  })

  it('says when a workspace posts nowhere and serves no hooks', async () => {
    open(
      { page: 'notifications' },
      transportWith({}, {
        configSummary: configSummary({
          notify: { enabled: false, on: [], includeTitle: false, destinations: [] },
          webhooks: { enabled: false, cooldown: '10m0s', match: {}, sources: [] },
        }),
      }),
    )
    expect(await screen.findByText('This workspace posts nothing')).toBeInTheDocument()
    go('Webhooks')
    expect(await screen.findByText(/there is no \/hooks endpoint/)).toBeInTheDocument()
  })
})

describe('MCP servers', () => {
  it('lists the servers without reaching them, then Test connects and fills in the counts', async () => {
    const transport = transportWith()
    open({ page: 'mcp' }, transport)
    expect(await screen.findByText('filesystem')).toBeInTheDocument()
    expect(screen.getByText('zoho')).toBeInTheDocument()
    expect(screen.getByText('stdio')).toBeInTheDocument()
    expect(screen.getByText('http')).toBeInTheDocument()
    expect(transport.calls.mcpServers).toEqual([{ ws: 'ws1', connect: false }])
    expect(screen.getByText('workspace · .mcp.json')).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: 'Test filesystem' }))
    expect(await screen.findByText('3 tools · connected in 412ms')).toBeInTheDocument()
    expect(transport.calls.mcpServers).toEqual([
      { ws: 'ws1', connect: false },
      { ws: 'ws1', connect: true },
    ])
    const failed = screen.getByText('failed · dial tcp: connection refused')
    expect(failed).toHaveAttribute('data-level', 'fail')
    expect(screen.getByRole('button', { name: 'Retry zoho' })).toBeInTheDocument()
  })

  it('shows the workspaceOnly switch read-only and the allowed patterns as chips', async () => {
    open({ page: 'mcp' })
    const toggle = await screen.findByRole('switch', { name: 'Workspace servers only' })
    expect(toggle).toBeDisabled()
    expect(toggle).toHaveAttribute('aria-checked', 'false')
    expect(screen.getByText('mcp__filesystem__read_*')).toBeInTheDocument()
    expect(screen.getByText(/Everything else is denied by name/)).toBeInTheDocument()
  })

  it('says when there are no servers, and when the listing failed', async () => {
    const { unmount } = open(
      { page: 'mcp' },
      transportWith({}, { mcp: mcpInventory({ servers: [], warnings: ['no .mcp.json'] }) }),
    )
    expect(await screen.findByText(/No MCP servers/)).toBeInTheDocument()
    expect(screen.getByText('no .mcp.json')).toBeInTheDocument()
    unmount()

    open({ page: 'mcp' }, transportWith({ mcpServers: vi.fn().mockRejectedValue(new Error('config is invalid')) }))
    expect(await screen.findByText('config is invalid')).toBeInTheDocument()
  })
})

describe('Try a tool', () => {
  it('lists the first server\'s tools with their verdicts and calls an allowed one', async () => {
    const transport = transportWith()
    open({ page: 'tools' }, transport)
    const toolSelect = (await screen.findByLabelText('Tool')) as HTMLSelectElement
    await waitFor(() => expect(toolSelect.options.length).toBe(3))
    expect(transport.calls.mcpTools).toEqual([{ ws: 'ws1', server: 'filesystem' }])
    expect(screen.getByText('allowed')).toBeInTheDocument()
    expect(screen.getByText('permissions.mcp · matches mcp__filesystem__read_*')).toBeInTheDocument()

    fireEvent.change(screen.getByLabelText('Arguments, as JSON'), {
      target: { value: '{"path": "README.md"}' },
    })
    fireEvent.click(screen.getByRole('button', { name: 'Call' }))
    await waitFor(() =>
      expect(transport.calls.mcpCall).toEqual([
        { ws: 'ws1', server: 'filesystem', tool: 'read_file', args: { path: 'README.md' } },
      ]),
    )
    expect(await screen.findByText('took 57ms')).toBeInTheDocument()
    expect(screen.getByText(/"path": "README.md"/, { selector: 'pre' })).toBeInTheDocument()
    // Kept for the session.
    expect(screen.getByText('filesystem · read_file')).toBeInTheDocument()
    expect(screen.getByText('57ms')).toBeInTheDocument()
  })

  it('opens on the first tool a run could call, not the first in the list', async () => {
    const tools = mcpTools('filesystem')
    tools.tools.reverse() // write_file, denied, now heads the list
    open({ page: 'tools' }, transportWith({}, { mcpTools: { filesystem: tools } }))
    const toolSelect = (await screen.findByLabelText('Tool')) as HTMLSelectElement
    await waitFor(() => expect(toolSelect.options.length).toBe(3))
    expect(toolSelect.options[0].value).toBe('write_file')
    expect(toolSelect.value).toBe('list_directory')
    expect(screen.getByText('allowed')).toBeInTheDocument()
  })

  it('shows a denied verdict as the answer, without an error', async () => {
    const transport = transportWith()
    open({ page: 'tools' }, transport)
    const toolSelect = (await screen.findByLabelText('Tool')) as HTMLSelectElement
    await waitFor(() => expect(toolSelect.options.length).toBe(3))
    fireEvent.change(toolSelect, { target: { value: 'write_file' } })
    expect(screen.getByText('denied')).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: 'Call' }))
    await waitFor(() => expect(transport.calls.mcpCall).toHaveLength(1))
    // The reason is the verdict line's before the call and the well's after it.
    expect(await screen.findAllByText(/the tool name reads as a write/)).toHaveLength(2)
    expect(screen.getAllByText('denied').length).toBeGreaterThan(1)
    expect(screen.queryByRole('alert')).toBeNull()
  })

  it('refuses to call with arguments that are not a JSON object', async () => {
    const transport = transportWith()
    open({ page: 'tools' }, transport)
    const toolSelect = (await screen.findByLabelText('Tool')) as HTMLSelectElement
    await waitFor(() => expect(toolSelect.options.length).toBe(3))
    fireEvent.change(screen.getByLabelText('Arguments, as JSON'), { target: { value: '{nope' } })
    expect(screen.getByText('Arguments are not valid JSON')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Call' })).toBeDisabled()
    fireEvent.click(screen.getByRole('button', { name: 'Call' }))
    expect(transport.calls.mcpCall).toEqual([])
  })

  it('reports a call the transport could not make', async () => {
    const transport = transportWith({ mcpCall: vi.fn().mockRejectedValue(new Error('server went away')) })
    open({ page: 'tools' }, transport)
    const toolSelect = (await screen.findByLabelText('Tool')) as HTMLSelectElement
    await waitFor(() => expect(toolSelect.options.length).toBe(3))
    fireEvent.click(screen.getByRole('button', { name: 'Call' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('server went away')
  })

  it('publishes Call as the primary action, drawn on the screen, so New session steps down', async () => {
    const { rerender } = open({ page: 'tools' })
    await waitFor(() => expect(screen.getByTestId('primary')).toHaveTextContent('Call on the screen'))
    go('About')
    await waitFor(() => expect(screen.getByTestId('primary')).toHaveTextContent('Save (disabled) on the screen'))
    // Closed, the modal publishes nothing and the sidebar's button is filled again.
    rerender(
      <PrimaryActionProvider>
        <Settings
          open={false}
          transport={transportWith()}
          workspaces={[WORKSPACE]}
          currentWorkspaceId="ws1"
          onClose={() => {}}
          onWorkspacesChanged={() => {}}
        />
        <PrimaryProbe />
      </PrimaryActionProvider>,
    )
    await waitFor(() => expect(screen.getByTestId('primary')).toHaveTextContent('New session'))
  })
})

describe('This app', () => {
  it('flips the reading direction and remembers it', () => {
    open({ page: 'reading' })
    const toggle = screen.getByRole('switch', { name: RTL_LABEL })
    expect(toggle).toHaveAttribute('aria-checked', 'false')
    fireEvent.click(toggle)
    expect(prefersRTL()).toBe(true)
    fireEvent.click(toggle)
    expect(prefersRTL()).toBe(false)
  })

  it('comes back checked for an engineer who set it last time', () => {
    setPreferRTL(true)
    open({ page: 'reading' })
    expect(screen.getByRole('switch', { name: RTL_LABEL })).toHaveAttribute('aria-checked', 'true')
  })

  it('flips the design library switch', () => {
    open({ page: 'library' })
    const toggle = screen.getByRole('switch', { name: 'Show the design library' })
    const before = showLibrary()
    fireEvent.click(toggle)
    expect(showLibrary()).toBe(!before)
  })

  // A relative docs path resolves against the asset server, which answers
  // with the app's own index.html, so the link has to be the absolute one.
  it('links the configuration reference at GitHub, on the About page', async () => {
    open({ page: 'about' }, transportWith({ version: async () => '0.9.2' }))
    expect(screen.getByRole('link', { name: 'docs/config.md' })).toHaveAttribute('href', CONFIG_DOCS_URL)
    expect(await screen.findByText('v0.9.2')).toBeInTheDocument()
  })
})
