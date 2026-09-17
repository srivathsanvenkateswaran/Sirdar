import { render, screen, waitFor } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { configSummary, createFakeTransport } from '../../store/fakeTransport'
import ModelLimitBanner, { offeredModels } from './ModelLimitBanner'

/*
 * The line above the composer on a run whose login has spent one model's
 * allowance. A per-model limit is a choice, not a clock: every other model
 * on the account is answering, so the banner's job is to make the choice
 * one click.
 */

describe('offeredModels', () => {
  it("leaves out the model that was refused and the CLI's own default", () => {
    const got = offeredModels('claude', 'Fable')
    expect(got).not.toContain('')
    expect(got.some((id) => id.includes('fable'))).toBe(false)
    expect(got).toContain('claude-opus-5')
  })

  it('puts the configured fallbacks first, in their own order', () => {
    expect(offeredModels('claude', 'Fable', ['claude-sonnet-5', 'claude-opus-5'])).toEqual([
      'claude-sonnet-5',
      'claude-opus-5',
      'claude-haiku-4-5-20251001',
    ])
  })

  it('drops a fallback that names the model that has just run out', () => {
    expect(offeredModels('claude', 'Fable', ['claude-fable-5-1', 'claude-opus-5'])[0]).toBe(
      'claude-opus-5',
    )
  })

  it('offers nothing for a provider whose only listed model is the CLI default', () => {
    expect(offeredModels('acp', 'Fable')).toEqual([])
  })
})

describe('ModelLimitBanner', () => {
  function transportWith(fallbacks?: string[]) {
    const t = createFakeTransport()
    t.configSummary = vi.fn(async () =>
      configSummary({
        general: { ...configSummary().general, fallbackModels: fallbacks },
      }),
    )
    return t
  }

  it('names the model and the login, and offers the configured fallback first', async () => {
    const onContinue = vi.fn()
    render(
      <ModelLimitBanner
        transport={transportWith(['claude-sonnet-5'])}
        workspaceId="ws1"
        provider="claude"
        limited="Fable"
        onContinue={onContinue}
      />,
    )
    expect(screen.getByText(/Fable’s limit is reached on this login/)).toBeInTheDocument()
    expect(screen.getByText(/Continue with:/)).toBeInTheDocument()

    const first = await screen.findByRole('button', { name: 'Sonnet 5' })
    first.click()
    expect(onContinue).toHaveBeenCalledWith('claude-sonnet-5')
  })

  it("falls back to the provider's own list when the workspace configured none", async () => {
    render(
      <ModelLimitBanner
        transport={transportWith()}
        workspaceId="ws1"
        provider="claude"
        limited="Fable"
        onContinue={vi.fn()}
      />,
    )
    await waitFor(() => expect(screen.getByRole('button', { name: 'Opus 5' })).toBeInTheDocument())
    expect(screen.queryByRole('button', { name: /Fable/ })).toBeNull()
  })

  it('says so honestly when the provider lists no other model', () => {
    render(
      <ModelLimitBanner
        transport={transportWith()}
        workspaceId="ws1"
        provider="acp"
        limited="Fable"
        onContinue={vi.fn()}
      />,
    )
    expect(screen.getByText(/lists no other model/)).toBeInTheDocument()
  })

  it('waits while a resume is already in flight', async () => {
    render(
      <ModelLimitBanner
        transport={transportWith(['claude-opus-5'])}
        workspaceId="ws1"
        provider="claude"
        limited="Fable"
        busy
        onContinue={vi.fn()}
      />,
    )
    const button = await screen.findByRole('button', { name: 'Opus 5' })
    expect(button).toBeDisabled()
  })
})
