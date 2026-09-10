// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import '@testing-library/jest-dom/vitest'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { Check, Transport, Workspace } from '../api/types'
import Settings from './Settings'

afterEach(cleanup)

function fakeTransport(overrides: Partial<Transport> = {}): Transport {
  const notImplemented = () => Promise.reject(new Error('not used by Settings'))
  return {
    workspaces: notImplemented,
    addWorkspace: notImplemented,
    removeWorkspace: notImplemented,
    queue: notImplemented,
    runs: notImplemented,
    run: notImplemented,
    events: notImplemented,
    note: notImplemented,
    prompt: notImplemented,
    startTriage: notImplemented,
    startRCA: notImplemented,
    resume: notImplemented,
    cancel: notImplemented,
    register: notImplemented,
    doctor: notImplemented,
    quota: notImplemented,
    subscribe: () => () => {},
    ...overrides,
  }
}

const workspace: Workspace = {
  id: 'ws1',
  name: 'sirdar',
  root: '/repos/sirdar',
  provider: 'claude',
  model: 'sonnet',
  notesDir: '.sirdar/notes',
}

describe('Settings', () => {
  it('calls addWorkspace and the callback on submit', async () => {
    const addWorkspace = vi.fn().mockResolvedValue(workspace)
    const onWorkspacesChanged = vi.fn()
    const transport = fakeTransport({ addWorkspace })

    render(<Settings transport={transport} workspaces={[]} onWorkspacesChanged={onWorkspacesChanged} />)

    fireEvent.change(screen.getByLabelText('Workspace path'), {
      target: { value: '/repos/sirdar' },
    })
    fireEvent.click(screen.getByRole('button', { name: 'Add workspace' }))

    await waitFor(() => expect(addWorkspace).toHaveBeenCalledWith('/repos/sirdar'))
    await waitFor(() => expect(onWorkspacesChanged).toHaveBeenCalled())
  })

  it('shows the error message when addWorkspace fails', async () => {
    const addWorkspace = vi.fn().mockRejectedValue(new Error('bad path'))
    const transport = fakeTransport({ addWorkspace })

    render(<Settings transport={transport} workspaces={[]} onWorkspacesChanged={vi.fn()} />)

    fireEvent.change(screen.getByLabelText('Workspace path'), { target: { value: '/nope' } })
    fireEvent.click(screen.getByRole('button', { name: 'Add workspace' }))

    expect(await screen.findByText('bad path')).toBeInTheDocument()
  })

  it('renders doctor rows as OK/!! with detail', async () => {
    const checks: Check[] = [
      { name: 'config', ok: true, detail: 'valid' },
      { name: 'git', ok: false, detail: 'not a repo' },
    ]
    const doctor = vi.fn().mockResolvedValue(checks)
    const transport = fakeTransport({ doctor })

    render(<Settings transport={transport} workspaces={[workspace]} onWorkspacesChanged={vi.fn()} />)

    fireEvent.click(screen.getByRole('button', { name: 'Run doctor' }))

    expect(await screen.findByText('OK')).toBeInTheDocument()
    expect(screen.getByText('!!')).toBeInTheDocument()
    expect(screen.getByText('not a repo')).toBeInTheDocument()
    expect(doctor).toHaveBeenCalledWith('ws1')
  })
})
