import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { ModelList, Workspace } from '../api/types'
import { PrimaryActionProvider } from '../components/shell/primaryAction'
import { createFakeTransport, workspace, type FakeTransport } from '../store/fakeTransport'
import NewSession from './NewSession'

/*
 * New session's Model chip: its list is the discovered one, loaded when the
 * chip opens and probed only when the list is due; and a pick belongs to the
 * workspace it was made in.
 */

function scene(transport: FakeTransport, ws: Workspace, onStart = vi.fn(async () => 'job-1')): JSX.Element {
  return (
    <PrimaryActionProvider>
      <NewSession transport={transport} workspaceId={ws.id} workspace={ws} runs={[]} onStart={onStart} onOpenRun={() => {}} />
    </PrimaryActionProvider>
  )
}

const chip = () => screen.getByRole('button', { name: /^Model/ })

afterEach(() => localStorage.clear())

describe('New session Model chip', () => {
  it('starts nothing on mount, and on opening reads the list and probes it once when due', async () => {
    const due: ModelList = { provider: 'claude', canProbe: true, probeDue: true, models: [] }
    const probed: ModelList = {
      provider: 'claude',
      canProbe: true,
      probeDue: false,
      probedAt: new Date().toISOString(),
      models: [{ id: 'claude-opus-4-5-20251101', label: 'Opus 4.5', source: 'probe', seenAt: new Date().toISOString(), alias: 'opus' }],
    }
    const transport = createFakeTransport({ tickets: [], models: { claude: due }, refreshed: { claude: probed } })
    render(scene(transport, workspace({ id: 'ws1', model: '' })))
    expect(transport.calls.models).toEqual([])
    expect(transport.calls.refreshModels).toEqual([])

    fireEvent.click(chip())
    await waitFor(() => expect(transport.calls.refreshModels).toEqual([{ ws: 'ws1', provider: 'claude' }]))
    expect(await screen.findByRole('option', { name: /^Opus 4.5/ })).toBeInTheDocument()
    const popover = screen.getByRole('dialog', { name: 'Provider and model' })
    expect(popover.querySelector('.sd-model-picker__status')).toHaveTextContent('Probed just now')
  })

  it('drops a pick when the workspace changes', async () => {
    const transport = createFakeTransport({ tickets: [] })
    const a = workspace({ id: 'ws1', model: '' })
    const b = workspace({ id: 'ws2', model: '' })
    const view = render(scene(transport, a))
    fireEvent.click(chip())
    fireEvent.click(await screen.findByRole('option', { name: /^Opus 5/ }))
    expect(chip()).toHaveAccessibleName('Model claude · Opus 5')

    view.rerender(scene(transport, b))
    await waitFor(() => expect(chip()).toHaveAccessibleName('Model claude · CLI default'))
  })
})
