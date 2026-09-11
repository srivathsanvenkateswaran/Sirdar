// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import '@testing-library/jest-dom/vitest'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { RegisterRow, Transport } from '../api/types'
import Register from './Register'

afterEach(() => {
  cleanup()
  vi.useRealTimers()
})

function fakeTransport(rows: RegisterRow[] | Error): Transport {
  const notImplemented = () => Promise.reject(new Error('not used by Register'))
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
    register: () => (rows instanceof Error ? Promise.reject(rows) : Promise.resolve(rows)),
    doctor: notImplemented,
    quota: notImplemented,
    subscribe: () => () => {},
  }
}

const rows: RegisterRow[] = [
  {
    key: 'OMNI-1',
    kind: 'triage',
    runId: 'run-1',
    date: '2026-09-01',
    provider: 'claude',
    model: 'sonnet',
    service: 'oxo-api',
    classification: 'null-pointer',
    confidence: 'high',
    severity: 'sev2',
    turns: 4,
    costUsd: 0.5,
    triageVerdict: 'confirmed',
    notePath: 'triage.md',
  },
  {
    key: 'OMNI-2',
    kind: 'triage',
    runId: 'run-2',
    date: '2026-09-02',
    provider: 'claude',
    model: 'sonnet',
    service: 'payments',
    classification: 'timeout',
    confidence: 'low',
    severity: 'sev3',
    turns: 3,
    costUsd: 0.25,
    triageVerdict: 'wrong',
    notePath: 'triage.md',
  },
]

describe('Register', () => {
  it('shows the empty state sentence when there are no rows', async () => {
    render(<Register transport={fakeTransport([])} workspaceId="ws1" />)
    expect(await screen.findByText(/No triage runs recorded yet/)).toBeInTheDocument()
  })

  it('renders one grouped row per key with the summary line', async () => {
    render(<Register transport={fakeTransport(rows)} workspaceId="ws1" />)
    expect(await screen.findByText('OMNI-1')).toBeInTheDocument()
    expect(screen.getByText('OMNI-2')).toBeInTheDocument()
    expect(screen.getByText(/Hypothesis held 1 of 2 reviewed \(50%\)/)).toBeInTheDocument()
  })

  it('filters by verdict', async () => {
    render(<Register transport={fakeTransport(rows)} workspaceId="ws1" />)
    await screen.findByText('OMNI-1')

    fireEvent.change(screen.getByLabelText('Verdict'), { target: { value: 'confirmed' } })

    await waitFor(() => expect(screen.queryByText('OMNI-2')).not.toBeInTheDocument())
    expect(screen.getByText('OMNI-1')).toBeInTheDocument()
  })

  // The copy button restores its label on a timer; leaving the workspace
  // before it fires must not leave the timer running.
  it('clears the copy timeout when it unmounts', async () => {
    const writeText = vi.fn(async () => {})
    Object.defineProperty(navigator, 'clipboard', {
      configurable: true,
      value: { writeText },
    })
    const { unmount } = render(<Register transport={fakeTransport(rows)} workspaceId="ws1" />)
    await screen.findByRole('button', { name: 'Copy as Markdown table' })

    vi.useFakeTimers()
    fireEvent.click(screen.getByRole('button', { name: 'Copy as Markdown table' }))
    await vi.waitFor(() => expect(writeText).toHaveBeenCalled())
    expect(vi.getTimerCount()).toBeGreaterThan(0)

    unmount()
    expect(vi.getTimerCount()).toBe(0)
  })
})
