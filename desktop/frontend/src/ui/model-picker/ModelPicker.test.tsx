import { fireEvent, render, screen, within } from '@testing-library/react'
import { useState } from 'react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import ModelPicker, {
  effectivePair,
  listRows,
  matchesQuery,
  readFavourites,
  type ModelChoicePair,
} from './index'

afterEach(() => {
  localStorage.clear()
})

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
const search = () => within(popover()).getByRole('searchbox', { name: 'Search models' })
const rail = () => within(popover()).getByRole('tablist', { name: 'Provider' })
const list = () => within(popover()).getByRole('listbox', { name: 'Model' })
const labels = () =>
  within(list())
    .getAllByRole('option')
    .map((r) => r.querySelector('.sd-model-picker__label')?.textContent)

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

describe('listRows', () => {
  it('lists the chosen provider with CLI default first and floats the favourites', () => {
    const none = () => []
    expect(listRows('claude', '', none).rest.map((r) => r.label)).toEqual([
      'CLI default',
      'Fable 5.1',
      'Opus 5',
      'Sonnet 5',
      'Haiku 4.5',
    ])
    const starred = (p: string) => (p === 'claude' ? ['claude-sonnet-5'] : [])
    const rows = listRows('claude', '', starred)
    expect(rows.favourites.map((r) => r.label)).toEqual(['Sonnet 5'])
    expect(rows.rest.map((r) => r.label)).toEqual(['CLI default', 'Fable 5.1', 'Opus 5', 'Haiku 4.5'])
  })

  it('searches every provider by model, id, config name or vendor name', () => {
    const none = () => []
    expect(listRows('claude', 'gpt', none).rest.map((r) => `${r.provider} ${r.label}`)).toEqual([
      'codex gpt-5.6-luna',
    ])
    expect(listRows('claude', 'codex', none).rest.map((r) => r.label)).toEqual([
      'CLI default',
      'gpt-5.6-luna',
    ])
    expect(listRows('claude', 'OpenAI', none).rest.map((r) => r.provider)).toEqual(['openai'])
    expect(matchesQuery({ id: 'claude-opus-5', label: 'Opus 5', provider: 'claude' }, ' opus ')).toBe(true)
    expect(matchesQuery({ id: 'claude-opus-5', label: 'Opus 5', provider: 'claude' }, 'luna')).toBe(false)
  })
})

describe('ModelPicker', () => {
  describe('the chip', () => {
    it('is the mark, the curated label and a chevron, and names the whole pair', () => {
      render(<Harness start={{ provider: '', model: 'claude-sonnet-5' }} />)
      expect(chip()).toHaveTextContent('Sonnet 5')
      expect(chip()).not.toHaveTextContent('claude ·')
      expect(chip()).toHaveAccessibleName('Model claude · Sonnet 5')
      expect(chip()).toHaveAttribute('title', 'Model: claude · Sonnet 5')
      expect(within(chip()).getByRole('img', { name: 'Claude' })).toBeInTheDocument()
      expect(chip().querySelector('.sd-model-chip__chevron')).not.toBeNull()
    })

    it('reads CLI default when nothing is chosen, with what the last run used in its name', () => {
      render(<Harness lastUsed="claude-sonnet-5" />)
      expect(chip()).toHaveTextContent('CLI default')
      expect(chip()).toHaveAccessibleName('Model claude · CLI default · last used claude-sonnet-5')
    })

    it('keeps the workspace model when only that names one, and passes an alias through', () => {
      render(<Harness defaultModel="sonnet" />)
      expect(chip()).toHaveTextContent('sonnet')
      expect(chip()).toHaveAccessibleName('Model claude · sonnet')
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
      expect(fact).toHaveTextContent('model unknown')
      expect(fact).toHaveAccessibleName(/^Model claude · model unknown\./)
    })
  })

  describe('the popover', () => {
    it('opens pinned to the viewport with the search focused, the rail, the rows and their keys', () => {
      render(<Harness lastUsed="claude-sonnet-5" />)
      expect(chip()).toHaveAttribute('aria-haspopup', 'dialog')
      expect(chip()).toHaveAttribute('aria-expanded', 'false')
      fireEvent.click(chip())
      expect(chip()).toHaveAttribute('aria-expanded', 'true')
      expect(popover().style.position).toBe('fixed')
      expect(search()).toHaveFocus()

      const tabs = within(rail()).getAllByRole('tab')
      expect(tabs.map((t) => t.getAttribute('aria-label'))).toEqual([
        'Claude, workspace default',
        'Codex',
        'OpenAI',
        'acp',
        'Qwen',
        'Cursor',
      ])
      expect(within(rail()).getByRole('tab', { selected: true })).toHaveAttribute('title', 'Claude · workspace default')
      // Icons only: the tab's text is the mark's accessible name, nothing more.
      expect(tabs[1].querySelector('.sd-mark')).not.toBeNull()

      expect(labels()).toEqual(['CLI default', 'Fable 5.1', 'Opus 5', 'Sonnet 5', 'Haiku 4.5', 'Other…'])
      const rows = within(list()).getAllByRole('option')
      // Each row says its provider under the label; CLI default says what it was last time.
      expect(rows[0].querySelector('.sd-model-picker__meta')).toHaveTextContent(
        /^claude· last used claude-sonnet-5$/,
      )
      expect(rows[1].querySelector('.sd-model-picker__meta')).toHaveTextContent(/^claude$/)
      expect(within(rows[1]).getByRole('img', { name: 'Claude' })).toBeInTheDocument()
      expect(rows.slice(0, 5).map((r) => r.querySelector('kbd')?.textContent)).toEqual([
        '⌘1',
        '⌘2',
        '⌘3',
        '⌘4',
        '⌘5',
      ])
      expect(rows[1]).toHaveAttribute('aria-keyshortcuts', 'Meta+2')
      expect(rows[5].querySelector('kbd')).toBeNull()
      expect(within(list()).getByRole('option', { selected: true })).toHaveTextContent('CLI default')
      expect(within(popover()).getByRole('textbox', { name: 'Other model' })).toBeInTheDocument()
    })

    it('picks a row with a click, closes, and returns focus to the chip', () => {
      const onChange = vi.fn()
      render(<Harness onChange={onChange} />)
      fireEvent.click(chip())
      fireEvent.click(within(list()).getByRole('option', { name: /Sonnet 5/ }))
      // The workspace's own provider is the absence of an override.
      expect(onChange).toHaveBeenLastCalledWith({ provider: '', model: 'claude-sonnet-5' })
      expect(screen.queryByRole('dialog')).toBeNull()
      expect(chip()).toHaveFocus()
      expect(chip()).toHaveTextContent('Sonnet 5')
    })

    it('switches provider from the rail and resets the model to that CLI default', () => {
      const onChange = vi.fn()
      render(<Harness start={{ provider: '', model: 'claude-opus-5' }} onChange={onChange} />)
      fireEvent.click(chip())
      fireEvent.click(within(rail()).getByRole('tab', { name: 'Codex' }))
      expect(onChange).toHaveBeenLastCalledWith({ provider: 'codex', model: '' })
      expect(labels()).toEqual(['CLI default', 'gpt-5.6-luna', 'Other…'])
      expect(chip()).toHaveTextContent('CLI default')
      expect(within(chip()).getByRole('img', { name: 'Codex' })).toBeInTheDocument()
    })

    it('walks the rail with the arrows, wrapping', () => {
      render(<Harness />)
      fireEvent.click(chip())
      fireEvent.keyDown(rail(), { key: 'ArrowDown' })
      expect(screen.getByTestId('pair')).toHaveTextContent('codex / (cli)')
      expect(within(rail()).getByRole('tab', { name: 'Codex' })).toHaveFocus()
      fireEvent.keyDown(rail(), { key: 'ArrowUp' })
      fireEvent.keyDown(rail(), { key: 'ArrowUp' })
      expect(screen.getByTestId('pair')).toHaveTextContent('cursor / (cli)')
      fireEvent.keyDown(rail(), { key: 'Home' })
      expect(screen.getByTestId('pair')).toHaveTextContent('(default) / (cli)')
    })

    it('filters across every provider as the search is typed, and ArrowDown steps into the list', () => {
      render(<Harness />)
      fireEvent.click(chip())
      fireEvent.change(search(), { target: { value: 'gpt' } })
      expect(labels()).toEqual(['gpt-5.6-luna', 'Other…'])
      expect(within(list()).getByText('Matches')).toBeInTheDocument()
      fireEvent.keyDown(search(), { key: 'ArrowDown' })
      expect(within(list()).getByRole('option', { name: /gpt-5.6-luna/ })).toHaveFocus()
      // Enter on a row from another provider takes that provider with it.
      fireEvent.keyDown(list(), { key: 'Enter' })
      expect(screen.getByTestId('pair')).toHaveTextContent('codex / gpt-5.6-luna')
      expect(screen.queryByRole('dialog')).toBeNull()
    })

    it('says so when nothing matches, and Enter in the search picks the first match', () => {
      render(<Harness />)
      fireEvent.click(chip())
      fireEvent.change(search(), { target: { value: 'zzz' } })
      expect(within(list()).getByText('No model matches “zzz”.')).toBeInTheDocument()
      fireEvent.change(search(), { target: { value: 'haiku' } })
      fireEvent.keyDown(search(), { key: 'Enter' })
      expect(screen.getByTestId('pair')).toHaveTextContent('(default) / claude-haiku-4-5-20251001')
    })

    it('walks the list with the arrows, Home and End, and ArrowUp from the top returns to the search', () => {
      render(<Harness />)
      fireEvent.click(chip())
      fireEvent.keyDown(search(), { key: 'ArrowDown' })
      expect(within(list()).getByRole('option', { name: /CLI default/ })).toHaveFocus()
      fireEvent.keyDown(list(), { key: 'ArrowDown' })
      fireEvent.keyDown(list(), { key: 'ArrowDown' })
      expect(within(list()).getByRole('option', { name: /Opus 5/ })).toHaveFocus()
      fireEvent.keyDown(list(), { key: 'End' })
      expect(within(list()).getByRole('option', { name: /Other/ })).toHaveFocus()
      // Enter on Other… steps into its box.
      fireEvent.keyDown(list(), { key: 'Enter' })
      expect(within(popover()).getByRole('textbox', { name: 'Other model' })).toHaveFocus()
      fireEvent.keyDown(list(), { key: 'Home' })
      expect(within(list()).getByRole('option', { name: /CLI default/ })).toHaveFocus()
      fireEvent.keyDown(list(), { key: 'ArrowUp' })
      expect(search()).toHaveFocus()
    })

    it('answers ⌘1…⌘9 while open, picking that row and closing', () => {
      const onChange = vi.fn()
      render(<Harness onChange={onChange} />)
      fireEvent.click(chip())
      fireEvent.keyDown(popover(), { key: '3', metaKey: true })
      expect(onChange).toHaveBeenLastCalledWith({ provider: '', model: 'claude-opus-5' })
      expect(screen.queryByRole('dialog')).toBeNull()
      expect(chip()).toHaveFocus()

      // Ctrl stands in for ⌘, and a digit past the rows does nothing.
      fireEvent.click(chip())
      fireEvent.keyDown(popover(), { key: '9', ctrlKey: true })
      expect(popover()).toBeInTheDocument()
      fireEvent.keyDown(popover(), { key: '2', ctrlKey: true })
      expect(onChange).toHaveBeenLastCalledWith({ provider: '', model: 'claude-fable-5-1' })
    })

    it('stars a favourite, floats it under Favourites, remembers it per provider, and unstars', () => {
      render(<Harness />)
      fireEvent.click(chip())
      const star = within(list()).getByRole('button', { name: 'Favourite Sonnet 5' })
      fireEvent.click(star)
      // The star is the row's, but starring is not picking.
      expect(popover()).toBeInTheDocument()
      expect(screen.getByTestId('pair')).toHaveTextContent('(default) / (cli)')
      expect(within(list()).getByText('Favourites')).toBeInTheDocument()
      expect(labels()).toEqual(['Sonnet 5', 'CLI default', 'Fable 5.1', 'Opus 5', 'Haiku 4.5', 'Other…'])
      // The favourite takes ⌘1 with it.
      expect(within(list()).getByRole('option', { name: /Sonnet 5/ }).querySelector('kbd')).toHaveTextContent('⌘1')
      expect(readFavourites('claude')).toEqual(['claude-sonnet-5'])
      expect(localStorage.getItem('sirdar.modelFavourites.claude')).toBe('["claude-sonnet-5"]')

      // Another provider's list has its own favourites.
      fireEvent.click(within(rail()).getByRole('tab', { name: 'Codex' }))
      expect(within(list()).queryByText('Favourites')).toBeNull()
      fireEvent.click(within(rail()).getByRole('tab', { name: /Claude/ }))
      expect(within(list()).getByText('Favourites')).toBeInTheDocument()

      const unstar = within(list()).getByRole('button', { name: 'Unfavourite Sonnet 5' })
      expect(unstar).toHaveAttribute('aria-pressed', 'true')
      fireEvent.click(unstar)
      expect(within(list()).queryByText('Favourites')).toBeNull()
      expect(readFavourites('claude')).toEqual([])
    })

    it('stars the focused row on f', () => {
      render(<Harness />)
      fireEvent.click(chip())
      fireEvent.keyDown(search(), { key: 'ArrowDown' })
      fireEvent.keyDown(list(), { key: 'ArrowDown' })
      fireEvent.keyDown(list(), { key: 'f' })
      expect(readFavourites('claude')).toEqual(['claude-fable-5-1'])
      expect(labels()[0]).toBe('Fable 5.1')
    })

    it('takes a free-text model, trimmed, and Enter in the box closes', () => {
      const onChange = vi.fn()
      render(<Harness onChange={onChange} />)
      fireEvent.click(chip())
      fireEvent.click(within(list()).getByRole('option', { name: /Other/ }))
      const box = within(popover()).getByRole('textbox', { name: 'Other model' })
      expect(box).toHaveFocus()
      fireEvent.change(box, { target: { value: ' opus ' } })
      expect(onChange).toHaveBeenLastCalledWith({ provider: '', model: 'opus' })
      expect(within(list()).getByRole('option', { selected: true })).toHaveTextContent('Other…')
      expect(chip()).toHaveTextContent('opus')
      fireEvent.keyDown(box, { key: 'Enter' })
      expect(screen.queryByRole('dialog')).toBeNull()
      expect(chip()).toHaveFocus()
    })

    it('shows the hint for a provider that takes free text only', () => {
      render(<Harness start={{ provider: 'qwen', model: '' }} />)
      fireEvent.click(chip())
      expect(labels()).toEqual(['CLI default', 'Other…'])
      expect(
        within(popover()).getByText('The name OPENAI_MODEL or QWEN_MODEL carries, e.g. qwen3-coder.'),
      ).toBeInTheDocument()
    })

    it('closes on Escape and on a click outside, keeping the choice', () => {
      render(<Harness />)
      fireEvent.click(chip())
      fireEvent.click(within(rail()).getByRole('tab', { name: 'Codex' }))
      fireEvent.keyDown(popover(), { key: 'Escape' })
      expect(screen.queryByRole('dialog')).toBeNull()
      expect(chip()).toHaveFocus()
      expect(chip()).toHaveAccessibleName('Model codex · CLI default')

      fireEvent.click(chip())
      fireEvent.change(search(), { target: { value: 'gpt' } })
      fireEvent.mouseDown(document.body)
      expect(screen.queryByRole('dialog')).toBeNull()
      // The search does not carry over to the next opening.
      fireEvent.click(chip())
      expect(search()).toHaveValue('')
    })

    it('opens from a Change button in the setting-row form', () => {
      render(
        <ModelPicker provider="" model="" defaultProvider="claude" trigger="change" onChange={() => {}} />,
      )
      expect(screen.queryByRole('button', { name: /^Model/ })).toBeNull()
      const change = screen.getByRole('button', { name: 'Change' })
      expect(change).toHaveAttribute('aria-haspopup', 'dialog')
      fireEvent.click(change)
      expect(popover()).toBeInTheDocument()
      fireEvent.keyDown(popover(), { key: 'Escape' })
      expect(change).toHaveFocus()
    })
  })
})
