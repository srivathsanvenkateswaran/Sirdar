import { fireEvent, render, screen, within } from '@testing-library/react'
import { useState } from 'react'
import { describe, expect, it, vi } from 'vitest'
import ModelPicker, { effectivePair, type ModelChoicePair } from './index'

/** The picker is controlled, so the cases that change it hold the pair. */
function Harness({
  start = { provider: '', model: '' },
  defaultProvider = 'claude',
  defaultModel = '',
  lastUsed = '',
  onChange,
}: {
  start?: ModelChoicePair
  defaultProvider?: string
  defaultModel?: string
  lastUsed?: string
  onChange?: (pair: ModelChoicePair) => void
}): JSX.Element {
  const [pair, setPair] = useState(start)
  return (
    <>
      <ModelPicker
        provider={pair.provider}
        model={pair.model}
        defaultProvider={defaultProvider}
        defaultModel={defaultModel}
        lastUsed={lastUsed}
        onChange={(next) => {
          setPair(next)
          onChange?.(next)
        }}
      />
      <p data-testid="pair">
        {pair.provider || '(default)'} / {pair.model || '(cli)'}
      </p>
    </>
  )
}

const chip = () => screen.getByRole('button', { name: /^Model/ })
const popover = () => screen.getByRole('dialog', { name: 'Provider and model' })
const providers = () => within(popover()).getByRole('listbox', { name: 'Provider' })
const models = () => within(popover()).getByRole('listbox', { name: 'Model' })

describe('effectivePair', () => {
  it('lets an override win and falls back to the workspace otherwise', () => {
    expect(effectivePair('', '', 'claude', 'sonnet')).toEqual({ provider: 'claude', model: 'sonnet' })
    expect(effectivePair('', 'claude-opus-5', 'claude', 'sonnet')).toEqual({
      provider: 'claude',
      model: 'claude-opus-5',
    })
    // A provider override with no model is that provider's own default, not
    // the workspace's model, which belongs to the workspace's provider.
    expect(effectivePair('codex', '', 'claude', 'sonnet')).toEqual({ provider: 'codex', model: '' })
  })
})

describe('ModelPicker', () => {
  describe('the chip', () => {
    it('reads the chosen pair by its label', () => {
      render(<Harness start={{ provider: '', model: 'claude-sonnet-5' }} />)
      expect(chip()).toHaveTextContent('Model')
      expect(within(chip()).getByRole('img', { name: 'Claude' })).toBeInTheDocument()
      expect(screen.getByText('claude · Sonnet 5')).toBeInTheDocument()
    })

    it('reads CLI default when nothing is chosen', () => {
      render(<Harness />)
      expect(screen.getByText('claude · CLI default')).toBeInTheDocument()
    })

    it('appends what the newest run used when nothing names a model', () => {
      render(<Harness lastUsed="claude-sonnet-5" />)
      expect(screen.getByText('claude · CLI default · last used claude-sonnet-5')).toBeInTheDocument()
    })

    it('keeps the workspace model when only that names one, and passes an alias through', () => {
      render(<Harness defaultModel="sonnet" />)
      expect(screen.getByText('claude · sonnet')).toBeInTheDocument()
    })

    it('is a fact rather than a control when read-only, and says why', () => {
      render(
        <ModelPicker
          provider="claude"
          model=""
          unknownAs="model unknown"
          readOnly="A steer resumes the same session, so the model cannot change"
        />,
      )
      const fact = screen.getByRole('button', { name: /^Model/ })
      expect(fact).toBeDisabled()
      expect(fact).toHaveAttribute(
        'title',
        'A steer resumes the same session, so the model cannot change',
      )
      expect(screen.getByText('claude · model unknown')).toBeInTheDocument()
    })
  })

  describe('the popover', () => {
    it('opens from the chip with the providers, the models and the workspace default marked', () => {
      render(<Harness />)
      expect(chip()).toHaveAttribute('aria-haspopup', 'dialog')
      expect(chip()).toHaveAttribute('aria-expanded', 'false')
      fireEvent.click(chip())
      expect(chip()).toHaveAttribute('aria-expanded', 'true')

      const rows = within(providers()).getAllByRole('option')
      // The name beside the mark; `acp` has no mark and its tile spells "AC".
      expect(rows.map((r) => r.querySelector('.sd-model-picker__name')?.textContent)).toEqual([
        'claude',
        'codex',
        'openai',
        'acp',
        'qwen',
        'cursor',
      ])
      expect(rows[0]).toHaveTextContent('workspace default')
      expect(rows[1]).not.toHaveTextContent('workspace default')
      expect(within(providers()).getByRole('option', { selected: true })).toHaveTextContent('claude')
      expect(within(providers()).getByRole('option', { selected: true })).toHaveFocus()

      expect(within(models()).getAllByRole('option').map((r) => r.textContent)).toEqual([
        'CLI defaultWhatever the provider’s CLI is set to',
        'Fable 5.1',
        'Opus 5',
        'Sonnet 5',
        'Haiku 4.5',
        'Other…',
      ])
      expect(within(models()).getByRole('option', { selected: true })).toHaveTextContent('CLI default')
      expect(within(popover()).getByRole('textbox', { name: 'Other model' })).toBeInTheDocument()
      expect(within(popover()).getByRole('button', { name: 'Done' })).toBeInTheDocument()
    })

    it('picks a model with a click, closes, and returns focus to the chip', () => {
      const onChange = vi.fn()
      render(<Harness onChange={onChange} />)
      fireEvent.click(chip())
      fireEvent.click(within(models()).getByRole('option', { name: 'Sonnet 5' }))
      // The workspace's own provider is the absence of an override.
      expect(onChange).toHaveBeenLastCalledWith({ provider: '', model: 'claude-sonnet-5' })
      expect(screen.queryByRole('dialog')).toBeNull()
      expect(chip()).toHaveFocus()
      expect(screen.getByText('claude · Sonnet 5')).toBeInTheDocument()
    })

    it('switches provider with a click and resets the model to that CLI default', () => {
      const onChange = vi.fn()
      render(<Harness start={{ provider: '', model: 'claude-opus-5' }} onChange={onChange} />)
      fireEvent.click(chip())
      fireEvent.click(within(providers()).getByRole('option', { name: /codex/ }))
      expect(onChange).toHaveBeenLastCalledWith({ provider: 'codex', model: '' })
      expect(within(models()).getAllByRole('option').map((r) => r.textContent)).toEqual([
        'CLI defaultWhatever the provider’s CLI is set to',
        'gpt-5.6-lunaReported by the app-server probe',
        'Other…',
      ])
      expect(screen.getByText('codex · CLI default')).toBeInTheDocument()
    })

    it('walks the providers with the arrows, wrapping, and Enter moves on to the models', () => {
      render(<Harness />)
      fireEvent.click(chip())
      fireEvent.keyDown(providers(), { key: 'ArrowDown' })
      expect(screen.getByTestId('pair')).toHaveTextContent('codex / (cli)')
      expect(within(providers()).getByRole('option', { name: /codex/ })).toHaveFocus()
      fireEvent.keyDown(providers(), { key: 'ArrowUp' })
      fireEvent.keyDown(providers(), { key: 'ArrowUp' })
      expect(screen.getByTestId('pair')).toHaveTextContent('cursor / (cli)')
      fireEvent.keyDown(providers(), { key: 'Home' })
      expect(screen.getByTestId('pair')).toHaveTextContent('(default) / (cli)')
      fireEvent.keyDown(providers(), { key: 'Enter' })
      expect(within(models()).getByRole('option', { selected: true })).toHaveFocus()
    })

    it('walks the models with the arrows and Enter is Done', () => {
      render(<Harness />)
      fireEvent.click(chip())
      fireEvent.keyDown(providers(), { key: 'Enter' })
      fireEvent.keyDown(models(), { key: 'ArrowDown' })
      fireEvent.keyDown(models(), { key: 'ArrowDown' })
      expect(screen.getByTestId('pair')).toHaveTextContent('(default) / claude-opus-5')
      expect(within(models()).getByRole('option', { name: 'Opus 5' })).toHaveFocus()
      fireEvent.keyDown(models(), { key: 'End' })
      // The last row is Other…: the arrows land on it and leave the choice
      // alone; Enter there steps into the free-text box.
      expect(within(models()).getByRole('option', { name: 'Other…' })).toHaveFocus()
      expect(screen.getByTestId('pair')).toHaveTextContent('(default) / claude-opus-5')
      fireEvent.keyDown(models(), { key: 'ArrowUp' })
      expect(screen.getByTestId('pair')).toHaveTextContent('(default) / claude-haiku-4-5-20251001')
      fireEvent.keyDown(models(), { key: 'End' })
      fireEvent.keyDown(models(), { key: 'Enter' })
      expect(within(popover()).getByRole('textbox', { name: 'Other model' })).toHaveFocus()
      fireEvent.keyDown(models(), { key: 'ArrowUp' })
      fireEvent.keyDown(models(), { key: 'Enter' })
      expect(screen.queryByRole('dialog')).toBeNull()
      expect(chip()).toHaveFocus()
    })

    it('takes a free-text model, trimmed, and Enter in the box is Done', () => {
      const onChange = vi.fn()
      render(<Harness onChange={onChange} />)
      fireEvent.click(chip())
      fireEvent.click(within(models()).getByRole('option', { name: 'Other…' }))
      const box = within(popover()).getByRole('textbox', { name: 'Other model' })
      expect(box).toHaveFocus()
      fireEvent.change(box, { target: { value: ' opus ' } })
      expect(onChange).toHaveBeenLastCalledWith({ provider: '', model: 'opus' })
      expect(within(models()).getByRole('option', { selected: true })).toHaveTextContent('Other…')
      expect(screen.getByText('claude · opus')).toBeInTheDocument()
      fireEvent.keyDown(box, { key: 'Enter' })
      expect(screen.queryByRole('dialog')).toBeNull()
      expect(chip()).toHaveFocus()
    })

    it('shows the hint for a provider that takes free text only', () => {
      render(<Harness start={{ provider: 'qwen', model: '' }} />)
      fireEvent.click(chip())
      expect(within(models()).getAllByRole('option').map((r) => r.textContent)).toEqual([
        'CLI defaultWhatever the provider’s CLI is set to',
        'Other…',
      ])
      expect(
        within(popover()).getByText('The name OPENAI_MODEL or QWEN_MODEL carries, e.g. qwen3-coder.'),
      ).toBeInTheDocument()
    })

    it('closes on Escape, on Done and on a click outside, keeping the choice', () => {
      render(<Harness />)
      fireEvent.click(chip())
      fireEvent.click(within(providers()).getByRole('option', { name: /codex/ }))
      fireEvent.keyDown(popover(), { key: 'Escape' })
      expect(screen.queryByRole('dialog')).toBeNull()
      expect(chip()).toHaveFocus()
      expect(screen.getByText('codex · CLI default')).toBeInTheDocument()

      fireEvent.click(chip())
      fireEvent.click(within(popover()).getByRole('button', { name: 'Done' }))
      expect(screen.queryByRole('dialog')).toBeNull()
      expect(chip()).toHaveFocus()

      fireEvent.click(chip())
      fireEvent.mouseDown(document.body)
      expect(screen.queryByRole('dialog')).toBeNull()
    })

    it('opens from a Change button in the setting-row form', () => {
      render(
        <ModelPicker provider="" model="" defaultProvider="claude" trigger="change" onChange={() => {}} />,
      )
      expect(screen.queryByText('claude · CLI default')).toBeNull()
      const change = screen.getByRole('button', { name: 'Change' })
      expect(change).toHaveAttribute('aria-haspopup', 'dialog')
      fireEvent.click(change)
      expect(popover()).toBeInTheDocument()
      fireEvent.keyDown(popover(), { key: 'Escape' })
      expect(change).toHaveFocus()
    })
  })
})
