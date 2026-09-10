// @vitest-environment jsdom
import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import '@testing-library/jest-dom/vitest'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { Check, Transport, Workspace } from '../api/types'
import Settings, { CONFIG_DOCS_URL } from './Settings'

afterEach(() => {
  cleanup()
  vi.useRealTimers()
})

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
  billing: 'subscription',
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

  // A relative docs path resolves against the asset server, which answers
  // with the app's own index.html, so the link has to be the absolute one.
  it('links the configuration reference at GitHub and shows the billing mode', () => {
    render(<Settings transport={fakeTransport()} workspaces={[workspace]} onWorkspacesChanged={vi.fn()} />)

    const link = screen.getByRole('link', { name: 'docs/config.md' })
    expect(link).toHaveAttribute('href', CONFIG_DOCS_URL)
    expect(screen.getByText('billing: subscription')).toBeInTheDocument()
  })

  // window.confirm blocks the whole webview, run stream included, so the
  // button arms itself instead.
  it('removes a workspace only on the second press', async () => {
    const removeWorkspace = vi.fn().mockResolvedValue(undefined)
    const onWorkspacesChanged = vi.fn()
    const transport = fakeTransport({ removeWorkspace })

    render(
      <Settings
        transport={transport}
        workspaces={[workspace]}
        onWorkspacesChanged={onWorkspacesChanged}
      />,
    )

    fireEvent.click(screen.getByRole('button', { name: 'Remove' }))
    expect(removeWorkspace).not.toHaveBeenCalled()

    const armed = await screen.findByRole('button', { name: 'Confirm remove' })
    fireEvent.click(armed)
    await waitFor(() => expect(removeWorkspace).toHaveBeenCalledWith('ws1'))
    await waitFor(() => expect(onWorkspacesChanged).toHaveBeenCalled())
    expect(screen.getByRole('button', { name: 'Remove' })).toBeInTheDocument()
  })

  it('disarms the remove button after five seconds', async () => {
    const removeWorkspace = vi.fn().mockResolvedValue(undefined)
    const { unmount } = render(
      <Settings
        transport={fakeTransport({ removeWorkspace })}
        workspaces={[workspace]}
        onWorkspacesChanged={vi.fn()}
      />,
    )

    vi.useFakeTimers()
    fireEvent.click(screen.getByRole('button', { name: 'Remove' }))
    await vi.waitFor(() =>
      expect(screen.getByRole('button', { name: 'Confirm remove' })).toBeInTheDocument(),
    )

    act(() => vi.advanceTimersByTime(5000))
    expect(screen.getByRole('button', { name: 'Remove' })).toBeInTheDocument()
    expect(removeWorkspace).not.toHaveBeenCalled()

    // And the timer does not outlive the screen.
    fireEvent.click(screen.getByRole('button', { name: 'Remove' }))
    expect(vi.getTimerCount()).toBeGreaterThan(0)
    unmount()
    expect(vi.getTimerCount()).toBe(0)
  })
})
