import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import type { ModelList } from '../../api/types'
import { createFakeTransport } from '../../store/fakeTransport'
import ModelsCard from './ModelsCard'

const claude: ModelList = {
  provider: 'claude',
  canProbe: true,
  probeDue: true,
  models: [
    { id: 'claude-opus-5[1m]', label: 'Opus 5 (1M)', source: 'run', seenAt: new Date(Date.now() - 2 * 86400_000).toISOString() },
    { id: 'claude-sonnet-4-5', label: 'سونيت', source: 'config' },
  ],
}
const probed: ModelList = {
  ...claude,
  probeDue: false,
  probedAt: new Date().toISOString(),
  probeErrors: ['haiku: no init line'],
  models: [
    { id: 'claude-opus-4-5-20251101', label: 'Opus 4.5', source: 'probe', seenAt: new Date().toISOString(), alias: 'opus' },
    ...claude.models,
  ],
}

describe('Settings › Providers › Models', () => {
  it('reads the list without probing, and probes only on Refresh', async () => {
    const transport = createFakeTransport({ models: { claude }, refreshed: { claude: probed } })
    render(<ModelsCard transport={transport} workspaceId="ws1" defaultProvider="claude" />)
    const list = await screen.findByRole('list', { name: 'Claude models' })
    expect(transport.calls.refreshModels).toEqual([])
    const rows = within(list).getAllByRole('listitem')
    expect(rows.map((r) => r.querySelector('.settings-models__label')?.textContent)).toEqual(['Opus 5 (1M)', 'سونيت'])
    expect(rows[0]).toHaveTextContent('seen in a run 2d ago')
    expect(rows[1]).toHaveTextContent('pinned in config')
    // A pin's label is the operator's own words, in any script.
    expect(rows[1].querySelector('.settings-models__label')).toHaveAttribute('dir', 'auto')
    expect(screen.getByRole('status')).toHaveTextContent('Not probed yet')

    fireEvent.click(screen.getByRole('button', { name: 'Refresh' }))
    await waitFor(() => expect(transport.calls.refreshModels).toEqual([{ ws: 'ws1', provider: 'claude' }]))
    expect(await screen.findByText('Opus 4.5')).toBeInTheDocument()
    expect(screen.getByRole('status')).toHaveTextContent('Probed just now')
    expect(screen.getByText(/Not resolved on the last probe: haiku: no init line/)).toBeInTheDocument()
  })

  it('switches provider, says what an empty list means, and reports a failure', async () => {
    const transport = createFakeTransport({ models: { claude } })
    render(<ModelsCard transport={transport} workspaceId="ws1" defaultProvider="claude" />)
    await screen.findByRole('list', { name: 'Claude models' })
    fireEvent.click(screen.getByRole('radio', { name: 'Codex' }))
    expect(await screen.findByText('No run has reported a model, and the config pins none.')).toBeInTheDocument()
    expect(screen.getByRole('status')).toHaveTextContent('From runs and config')

    transport.failModels(new Error('service unavailable'))
    fireEvent.click(screen.getByRole('radio', { name: 'Qwen' }))
    expect(await screen.findByText(/service unavailable/)).toBeInTheDocument()
  })

  it('asks for a workspace when none is chosen', () => {
    render(<ModelsCard transport={createFakeTransport()} defaultProvider="" />)
    expect(screen.getByText('Choose a workspace from the switcher to see its models.')).toBeInTheDocument()
  })
})
