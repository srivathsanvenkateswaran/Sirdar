import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import FixForm from './FixForm'

function mount(over: { pending?: boolean; error?: string; defaultProvider?: string } = {}) {
  const onStart = vi.fn()
  const onCancel = vi.fn()
  render(
    <FixForm
      runKey="OMNI-2510"
      pending={over.pending ?? false}
      error={over.error ?? ''}
      defaultProvider={over.defaultProvider}
      onStart={onStart}
      onCancel={onCancel}
    />,
  )
  return { onStart, onCancel }
}

describe('FixForm', () => {
  it('starts with every flag off and no override, which is what `sirdar fix` does bare', () => {
    const { onStart } = mount()
    fireEvent.click(screen.getByRole('button', { name: 'Start fix' }))
    expect(onStart).toHaveBeenCalledWith({
      dryRun: false,
      noPr: false,
      base: '',
      provider: '',
      model: '',
    })
  })

  it('carries the base, both flags and the one-off provider and model', () => {
    const { onStart } = mount()

    fireEvent.change(screen.getByLabelText('Base branch'), { target: { value: '  release  ' } })
    fireEvent.click(screen.getByLabelText(/Dry run/))
    fireEvent.click(screen.getByLabelText(/no pull request/))
    fireEvent.change(screen.getByLabelText('Provider'), { target: { value: 'codex' } })
    fireEvent.change(screen.getByLabelText('Model'), { target: { value: ' gpt-5-codex ' } })
    fireEvent.click(screen.getByRole('button', { name: 'Start fix' }))

    expect(onStart).toHaveBeenCalledWith({
      dryRun: true,
      noPr: true,
      base: 'release',
      provider: 'codex',
      model: 'gpt-5-codex',
    })
  })

  it('names the workspace provider on the default option, so choosing nothing is not a guess', () => {
    mount({ defaultProvider: 'claude' })
    expect(
      screen.getByRole('option', { name: 'Workspace default (claude)' }),
    ).toBeInTheDocument()
    for (const name of ['claude', 'codex', 'openai', 'acp', 'qwen']) {
      expect(screen.getByRole('option', { name })).toBeInTheDocument()
    }
  })

  it('says what a fix run will do before it is started', () => {
    mount()
    expect(screen.getByText(/Fix run for OMNI-2510/)).toBeInTheDocument()
    expect(screen.getByText(/stops before the push/)).toBeInTheDocument()
  })

  it('locks every field while the start is in flight and shows a refusal', () => {
    mount({ pending: true, error: 'the triage note is not approved' })
    expect(screen.getByRole('button', { name: 'Starting…' })).toBeDisabled()
    expect(screen.getByLabelText('Base branch')).toBeDisabled()
    expect(screen.getByLabelText('Provider')).toBeDisabled()
    expect(screen.getByText('the triage note is not approved')).toBeInTheDocument()
  })

  it('cancels without starting anything', () => {
    const { onStart, onCancel } = mount()
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }))
    expect(onCancel).toHaveBeenCalledTimes(1)
    expect(onStart).not.toHaveBeenCalled()
  })
})
