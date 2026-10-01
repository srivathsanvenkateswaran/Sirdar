import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { ModelList, RunDetail, RunEvent } from '../../api/types'
import { MODEL_LOCKED } from '../../components/run/Composer'
import { PrimaryActionProvider } from '../../components/shell/primaryAction'
import { resetRunJobs } from '../../lib/jobs'
import { resetPickedModels } from '../../lib/pickedModel'
import { resetSessionLayout, setSessionLayout } from '../../lib/sessionLayout'
import { createFakeTransport, type FakeTransport } from '../../store/fakeTransport'
import Session from '../Session'
import { FIX_DETAIL, TRIAGE_DETAIL, fixEvents, triageEvents } from './fixtures'

/*
 * The session composer's Model chip: locked while the run works, a picker
 * once it stops, "next:" when the pick is not what ran — and the pick held
 * per run, so a layout switch does not drop it and another run does not
 * inherit it.
 */

const DISCOVERED: ModelList = {
  provider: 'claude',
  canProbe: true,
  probeDue: false,
  probedAt: new Date().toISOString(),
  models: [
    { id: 'claude-opus-4-5-20251101', label: 'Opus 4.5', source: 'probe', seenAt: new Date().toISOString(), alias: 'opus' },
    { id: 'claude-sonnet-4-5-20250929', label: 'Sonnet 4.5', source: 'probe', seenAt: new Date().toISOString(), alias: 'sonnet' },
  ],
}

function fake(details: RunDetail[]): FakeTransport {
  const byId = new Map(details.map((d) => [d.runId, d]))
  const transport = createFakeTransport({ models: { claude: DISCOVERED } })
  transport.run = vi.fn(async (_ws: string, runId: string) => byId.get(runId) as RunDetail)
  transport.events = vi.fn(async (_ws: string, runId: string) => {
    const events: RunEvent[] = runId === FIX_DETAIL.runId ? fixEvents() : triageEvents()
    return { events, next: events.length }
  })
  transport.note = vi.fn(async () => '')
  transport.prompt = vi.fn(async () => '')
  const inner = transport.steer
  transport.steer = vi.fn(inner)
  return transport
}

function scene(transport: FakeTransport, runId: string): JSX.Element {
  return (
    <PrimaryActionProvider>
      <Session
        transport={transport}
        workspaceId="ws1"
        runId={runId}
        notesDir="/notes"
        onBack={() => {}}
        onOpenReview={() => {}}
        onStartFix={() => {}}
      />
    </PrimaryActionProvider>
  )
}

const chip = () => screen.getByRole('button', { name: /^Model/ })

async function pick(label: RegExp): Promise<void> {
  fireEvent.click(chip())
  fireEvent.click(await screen.findByRole('option', { name: label }))
}

describe('the session Model chip', () => {
  beforeEach(() => {
    resetRunJobs()
    resetPickedModels()
    localStorage.clear()
    setSessionLayout('conversation')
  })
  afterEach(() => {
    localStorage.clear()
    resetSessionLayout()
  })

  it.each(['running', 'preparing'] as const)('is locked while the run is %s', async (status) => {
    const transport = fake([{ ...TRIAGE_DETAIL, status }])
    render(scene(transport, TRIAGE_DETAIL.runId))
    await screen.findByRole('heading', { name: 'SBX-1' })
    const fact = chip()
    expect(fact).toBeDisabled()
    expect(fact).toHaveAttribute('data-readonly', 'true')
    expect(fact).toHaveAttribute('title', MODEL_LOCKED)
    expect(fact).toHaveAccessibleName(new RegExp(`${MODEL_LOCKED.replace('.', '\\.')}$`))
    expect(fact.querySelector('.sd-model-chip__chevron')).toBeNull()
    expect(fact.querySelector('.sd-model-chip__lock')).not.toBeNull()
  })

  it.each(['blocked', 'completed', 'failed'] as const)('is a picker once the run is %s', async (status) => {
    const transport = fake([{ ...TRIAGE_DETAIL, status, reason: status === 'blocked' ? 'agent asked: which?' : '' }])
    render(scene(transport, TRIAGE_DETAIL.runId))
    await screen.findByRole('heading', { name: 'SBX-1' })
    expect(chip()).toBeEnabled()
    expect(chip()).toHaveAttribute('aria-haspopup', 'dialog')
    expect(chip()).toHaveTextContent('Opus 5')
  })

  it('lists the discovered models, says next: for a different pick, and steers on it', async () => {
    const transport = fake([{ ...TRIAGE_DETAIL, status: 'completed' }])
    render(scene(transport, TRIAGE_DETAIL.runId))
    await screen.findByRole('heading', { name: 'SBX-1' })
    fireEvent.click(chip())
    await waitFor(() => expect(transport.calls.models).toEqual([{ ws: 'ws1', provider: 'claude' }]))
    // The list is the login's, not the static table.
    expect(await screen.findByRole('option', { name: /^Opus 4.5/ })).toBeInTheDocument()
    expect(screen.queryByRole('option', { name: /^Fable 5.1/ })).toBeNull()
    fireEvent.click(screen.getByRole('option', { name: /^Sonnet 4.5/ }))

    expect(chip()).toHaveTextContent('next: Sonnet 4.5')
    expect(chip()).toHaveAccessibleName('Model for the next turn claude · Sonnet 4.5')

    // Picking what the run ran on is not a "next".
    await pick(/^CLI default/)
    expect(chip()).toHaveTextContent('Opus 5')
    expect(chip().textContent).not.toMatch(/next:/)

    await pick(/^Sonnet 4.5/)
    const box = await screen.findByRole('textbox', { name: 'Steer' })
    fireEvent.change(box, { target: { value: 'Try again' } })
    fireEvent.click(within(screen.getByRole('form')).getByRole('button', { name: 'Steer' }))
    await waitFor(() =>
      expect(transport.steer).toHaveBeenCalledWith('ws1', TRIAGE_DETAIL.runId, 'Try again', 'claude-sonnet-4-5-20250929'),
    )
  })

  /*
   * The stuck picker. The pick was the Conversation layout's own state, so a
   * switch to Document and back unmounted it: the chip went back to the
   * run's model while the reader believed the next turn was on the one they
   * had picked.
   */
  it('keeps the pick across a layout switch', async () => {
    const transport = fake([{ ...TRIAGE_DETAIL, status: 'completed' }])
    render(scene(transport, TRIAGE_DETAIL.runId))
    await screen.findByRole('heading', { name: 'SBX-1' })
    await pick(/^Sonnet 4.5/)
    expect(chip()).toHaveTextContent('next: Sonnet 4.5')

    act(() => setSessionLayout('document'))
    await waitFor(() => expect(screen.queryByRole('button', { name: /^Model/ })).toBeNull())
    act(() => setSessionLayout('conversation'))
    expect(await screen.findByRole('button', { name: /^Model/ })).toHaveTextContent('next: Sonnet 4.5')
  })

  it('does not carry a pick to another run, and keeps it for the run it was made on', async () => {
    const transport = fake([
      { ...TRIAGE_DETAIL, status: 'completed' },
      { ...FIX_DETAIL, status: 'completed' },
    ])
    const view = render(scene(transport, TRIAGE_DETAIL.runId))
    await screen.findByRole('heading', { name: 'SBX-1' })
    await pick(/^Sonnet 4.5/)

    view.rerender(scene(transport, FIX_DETAIL.runId))
    await waitFor(() => expect(transport.run).toHaveBeenLastCalledWith('ws1', FIX_DETAIL.runId))
    await waitFor(() => expect(chip().textContent).not.toMatch(/next:/))

    view.rerender(scene(transport, TRIAGE_DETAIL.runId))
    await waitFor(() => expect(chip()).toHaveTextContent('next: Sonnet 4.5'))
  })

  it('spends the pick once the run moves', async () => {
    const transport = fake([{ ...TRIAGE_DETAIL, status: 'completed' }])
    render(scene(transport, TRIAGE_DETAIL.runId))
    await screen.findByRole('heading', { name: 'SBX-1' })
    await pick(/^Sonnet 4.5/)
    act(() =>
      transport.emit({
        kind: 'run.updated',
        workspaceId: 'ws1',
        run: { ...TRIAGE_DETAIL, status: 'running' },
      }),
    )
    await waitFor(() => expect(chip()).toBeDisabled())
    expect(chip().textContent).not.toMatch(/next:/)
  })
})
