// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import '@testing-library/jest-dom/vitest'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { Transport } from '../../api/types'
import { PrimaryActionProvider, usePrimaryAction } from '../../components/shell/primaryAction'
import {
  createFakeTransport,
  workspace as sampleWorkspace,
  type FakeTransport,
} from '../../store/fakeTransport'
import Settings from '../Settings'
import { headingFor, nameProblem, template } from './PlaybooksPage'

const WORKSPACE = sampleWorkspace({ id: 'ws1', name: 'omni', root: '/repos/omni' })

afterEach(cleanup)

/** What the sidebar's footer would draw: the screen's published primary action. */
function PrimaryProbe(): JSX.Element {
  const action = usePrimaryAction()
  return (
    <output data-testid="primary">
      {action ? `${action.label}${action.disabled ? ' (disabled)' : ''}` : 'New session'}
    </output>
  )
}

/** Opens Settings on the Playbooks page over the transport given. */
function open(transport: Transport = createFakeTransport({ workspaces: [WORKSPACE] })) {
  const onClose = vi.fn()
  const view = render(
    <PrimaryActionProvider>
      <Settings
        open
        page="playbooks"
        transport={transport}
        workspaces={[WORKSPACE]}
        currentWorkspaceId="ws1"
        onClose={onClose}
        onWorkspacesChanged={vi.fn()}
      />
      <PrimaryProbe />
    </PrimaryActionProvider>,
  )
  return { ...view, onClose }
}

function fake(seed: Parameters<typeof createFakeTransport>[0] = {}): FakeTransport {
  return createFakeTransport({ workspaces: [WORKSPACE], ...seed })
}

/** The row whose title is `title`. */
function row(title: string): HTMLElement {
  return screen.getByText(title).closest('.playbook-row') as HTMLElement
}

describe('the playbooks list', () => {
  it('draws a row per playbook, in prompt order, with its order prefix, title and lede', async () => {
    open(fake())

    const titles = await screen.findAllByText(/Helpdesk|Logs/)
    expect(titles.map((t) => t.textContent)).toEqual(['Helpdesk', 'Logs'])
    const helpdesk = row('Helpdesk')
    expect(within(helpdesk).getByText('10')).toBeInTheDocument()
    expect(
      within(helpdesk).getByText(/The helpdesk thread is the customer/),
    ).toBeInTheDocument()
    // The file's path is not on the row: the title is the link.
    expect(helpdesk.textContent).not.toContain('.sirdar/playbooks')
  })

  it('says a playbook is read into every run and that a change applies to the next one', async () => {
    open(fake())
    expect(
      await screen.findByText(/A change here applies to the next run, not to one already going/),
    ).toBeInTheDocument()
  })

  it('publishes New playbook as the window\'s one filled button, and the footer only closes', async () => {
    open(fake())
    await screen.findByText('Helpdesk')

    expect(screen.getByTestId('primary')).toHaveTextContent('New playbook')
    expect(screen.queryByRole('button', { name: 'Save' })).toBeNull()
    expect(screen.getByRole('button', { name: 'Close' })).toBeInTheDocument()
  })

  it('reports a read that failed rather than drawing an empty list', async () => {
    const t = fake()
    t.failPlaybooks(new Error('unsupported: this workspace keeps its playbooks outside .sirdar'))
    open(t)

    expect(await screen.findByRole('alert')).toHaveTextContent(/outside \.sirdar/)
  })

  it('asks for a workspace when none is chosen', () => {
    render(
      <PrimaryActionProvider>
        <Settings
          open
          page="playbooks"
          transport={fake()}
          workspaces={[WORKSPACE]}
          onClose={vi.fn()}
          onWorkspacesChanged={vi.fn()}
        />
      </PrimaryActionProvider>,
    )
    expect(screen.getByText(/Choose a workspace from the switcher/)).toBeInTheDocument()
  })
})

describe('the empty state', () => {
  it('explains what a playbook is in two sentences and scaffolds the starting set', async () => {
    const t = fake({ playbooks: {} })
    open(t)

    const create = await screen.findByRole('button', { name: 'Create the starting set' })
    expect(screen.getByText(/A playbook is a markdown file/)).toBeInTheDocument()
    expect(screen.getByText(/Every run reads all of them, in filename order/)).toBeInTheDocument()
    await waitFor(() =>
      expect(screen.getByTestId('primary')).toHaveTextContent('Create the starting set'),
    )

    fireEvent.click(create)

    expect(await screen.findByText('Helpdesk')).toBeInTheDocument()
    expect(t.calls.scaffoldPlaybooks).toEqual(['ws1'])
  })
})

describe('the editor', () => {
  it('reads the body, saves the change and goes back to the list', async () => {
    const t = fake()
    open(t)
    fireEvent.click(await screen.findByRole('button', { name: 'Edit Helpdesk' }))

    const area = (await screen.findByLabelText('10-helpdesk.md')) as HTMLTextAreaElement
    expect(area.value).toContain('# Helpdesk')
    // Nothing typed yet, so there is nothing to save.
    expect(screen.getByTestId('primary')).toHaveTextContent('Save (disabled)')

    fireEvent.change(area, { target: { value: '# Helpdesk\n\nRead the thread first.\n' } })
    expect(screen.getByTestId('primary')).toHaveTextContent('Save')
    expect(screen.getByTestId('primary')).not.toHaveTextContent('disabled')

    fireEvent.click(screen.getByRole('button', { name: 'Save' }))

    await waitFor(() => expect(t.calls.savePlaybook).toHaveLength(1))
    expect(t.calls.savePlaybook[0]).toEqual({
      ws: 'ws1',
      name: '10-helpdesk.md',
      body: '# Helpdesk\n\nRead the thread first.\n',
    })
    // Back on the list, with the row reading the new lede.
    expect(await screen.findByText('Read the thread first.')).toBeInTheDocument()
  })

  it('leaves without asking when nothing was typed', async () => {
    open(fake())
    fireEvent.click(await screen.findByRole('button', { name: 'Edit Helpdesk' }))
    await screen.findByLabelText('10-helpdesk.md')

    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }))

    expect(await screen.findByRole('button', { name: 'Edit Helpdesk' })).toBeInTheDocument()
    expect(screen.queryByRole('dialog', { name: /Discard/ })).toBeNull()
  })

  it('guards unsaved changes: Cancel asks, Keep editing stays, Discard leaves', async () => {
    const t = fake()
    open(t)
    fireEvent.click(await screen.findByRole('button', { name: 'Edit Helpdesk' }))
    const area = await screen.findByLabelText('10-helpdesk.md')
    fireEvent.change(area, { target: { value: '# Helpdesk\n\nHalf a thought' } })

    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }))
    expect(
      await screen.findByRole('dialog', { name: 'Discard the changes to 10-helpdesk.md?' }),
    ).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: 'Keep editing' }))
    expect((screen.getByLabelText('10-helpdesk.md') as HTMLTextAreaElement).value).toContain(
      'Half a thought',
    )

    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }))
    fireEvent.click(await screen.findByRole('button', { name: 'Discard' }))

    expect(await screen.findByRole('button', { name: 'Edit Helpdesk' })).toBeInTheDocument()
    expect(t.calls.savePlaybook).toEqual([])
  })

  it('says why a save was refused and keeps the draft', async () => {
    const t = fake()
    open(t)
    fireEvent.click(await screen.findByRole('button', { name: 'Edit Helpdesk' }))
    const area = await screen.findByLabelText('10-helpdesk.md')
    fireEvent.change(area, { target: { value: '# Helpdesk\n\nNew.\n' } })
    t.failPlaybooks(new Error('unsupported: this workspace keeps its playbooks outside .sirdar'))

    fireEvent.click(screen.getByRole('button', { name: 'Save' }))

    expect(await screen.findByRole('alert')).toHaveTextContent(/outside \.sirdar/)
    expect((screen.getByLabelText('10-helpdesk.md') as HTMLTextAreaElement).value).toContain('New.')
  })
})

describe('new playbook', () => {
  it('asks for a name, refuses one that is not shaped, and opens the editor on the template', async () => {
    const t = fake()
    open(t)
    fireEvent.click(await screen.findByRole('button', { name: 'New playbook' }))

    const field = screen.getByLabelText('Filename')
    fireEvent.change(field, { target: { value: 'Database' } })
    expect(screen.getByRole('button', { name: 'Create' })).toBeDisabled()
    expect(screen.getByText(/Two digits, a hyphen, a lowercase slug and \.md/)).toBeInTheDocument()

    fireEvent.change(field, { target: { value: '10-helpdesk.md' } })
    expect(screen.getByRole('button', { name: 'Create' })).toBeDisabled()
    expect(screen.getByText(/already filed under that name/)).toBeInTheDocument()

    fireEvent.change(field, { target: { value: '40-database.md' } })
    fireEvent.click(screen.getByRole('button', { name: 'Create' }))

    await waitFor(() => expect(t.calls.addPlaybook).toHaveLength(1))
    expect(t.calls.addPlaybook[0]).toEqual({
      ws: 'ws1',
      name: '40-database.md',
      body: template('40-database.md'),
    })
    // The editor opens on it, so the first thing after Create is the file.
    const area = (await screen.findByLabelText('40-database.md')) as HTMLTextAreaElement
    expect(area.value).toContain('# Database')
  })
})

describe('the row menu', () => {
  it('deletes behind a confirm that says where the file goes', async () => {
    const t = fake()
    open(t)
    fireEvent.click(await screen.findByRole('button', { name: 'More for Helpdesk' }))
    fireEvent.click(await screen.findByRole('menuitem', { name: 'Delete' }))

    const dialog = await screen.findByRole('dialog', { name: 'Delete 10-helpdesk.md?' })
    expect(within(dialog).getByText(/moved into the playbooks directory/)).toBeInTheDocument()

    fireEvent.click(within(dialog).getByRole('button', { name: 'Delete' }))

    await waitFor(() => expect(t.calls.deletePlaybook).toHaveLength(1))
    expect(t.calls.deletePlaybook[0]).toEqual({ ws: 'ws1', name: '10-helpdesk.md' })
    await waitFor(() => expect(screen.queryByText('Helpdesk')).toBeNull())
    expect(screen.getByText('Logs')).toBeInTheDocument()
  })

  it('deletes nothing when the confirm is cancelled', async () => {
    const t = fake()
    open(t)
    fireEvent.click(await screen.findByRole('button', { name: 'More for Helpdesk' }))
    fireEvent.click(await screen.findByRole('menuitem', { name: 'Delete' }))
    const dialog = await screen.findByRole('dialog', { name: 'Delete 10-helpdesk.md?' })

    fireEvent.click(within(dialog).getByRole('button', { name: 'Cancel' }))

    expect(t.calls.deletePlaybook).toEqual([])
    expect(screen.getByText('Helpdesk')).toBeInTheDocument()
  })
})

describe('open in editor', () => {
  it('hands the file to the operator\'s own editor', async () => {
    const t = fake()
    open(t)
    fireEvent.click(await screen.findByRole('button', { name: 'Open Helpdesk in editor' }))

    await waitFor(() => expect(t.calls.openPlaybook).toHaveLength(1))
    expect(t.calls.openPlaybook[0]).toEqual({ ws: 'ws1', name: '10-helpdesk.md' })
  })

  it('says why the machine could not open it', async () => {
    const t = fake()
    open(t)
    await screen.findByText('Helpdesk')
    t.failPlaybooks(new Error('forbidden: opening a playbook starts an editor on the machine'))

    fireEvent.click(screen.getByRole('button', { name: 'Open Helpdesk in editor' }))

    expect(await screen.findByRole('alert')).toHaveTextContent(/starts an editor on the machine/)
  })
})

describe('the name rules', () => {
  it.each([
    ['', 'Give it a name'],
    ['logs.md', 'Two digits, a hyphen, a lowercase slug and .md — 20-logs.md'],
    ['10-Logs.md', 'Two digits, a hyphen, a lowercase slug and .md — 20-logs.md'],
    ['10-logs', 'Two digits, a hyphen, a lowercase slug and .md — 20-logs.md'],
    ['../secret.md', 'Two digits, a hyphen, a lowercase slug and .md — 20-logs.md'],
    ['20-logs.md', 'A playbook is already filed under that name'],
    ['40-database.md', ''],
  ])('reads %s as %s', (name, problem) => {
    expect(nameProblem(name, ['10-helpdesk.md', '20-logs.md'])).toBe(problem)
  })

  it('opens a new playbook on a heading taken from its own name', () => {
    expect(headingFor('40-database.md')).toBe('Database')
    expect(headingFor('60-tenancy-and-vocabulary.md')).toBe('Tenancy and vocabulary')
    expect(template('20-logs.md')).toMatch(/^# Logs\n\n/)
  })
})
