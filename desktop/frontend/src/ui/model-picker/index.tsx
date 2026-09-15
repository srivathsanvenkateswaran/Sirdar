import { useCallback, useEffect, useId, useRef, useState, type KeyboardEvent } from 'react'
import {
  CLI_DEFAULT,
  describeModel,
  hintFor,
  modelsFor,
  PICKABLE_PROVIDERS,
  type ModelChoice,
} from '../../lib/models'
import Button from '../button'
import ProviderMark, { providerName } from '../provider-mark'
import './ModelPicker.css'

export interface ModelChoicePair {
  /** The provider, or '' for the workspace's own. */
  provider: string
  /** The model id, or '' for the provider's CLI default. */
  model: string
}

export interface ModelPickerProps {
  /** The override so far; '' is the workspace's own provider. */
  provider: string
  /** The override so far; '' is the CLI default. */
  model: string
  /** The workspace's configured provider, marked "workspace default" in the list. */
  defaultProvider?: string
  /** The workspace's configured model, shown while no override names one. */
  defaultModel?: string
  /**
   * What the newest run on the provider reported, appended to the chip as
   * "last used …" when nothing names a model — so a reader learns what
   * "CLI default" turned out to be last time.
   */
  lastUsed?: string
  /** Called on every change; the choice is applied as it is made. */
  onChange?: (choice: ModelChoicePair) => void
  /**
   * The chip opens the popover; a Change button is the setting-row form,
   * with the chip's words drawn by the row instead.
   */
  trigger?: 'chip' | 'change'
  /** The chip is a fact, not a control, and this says why. */
  readOnly?: string
  /** What an empty model reads as where "CLI default" would be a claim. */
  unknownAs?: string
  disabled?: boolean
}

/** The "Other…" row, which is the free-text input. */
const OTHER = 'other'
type ModelRow = ModelChoice | typeof OTHER

/**
 * The effective pair: an override wins outright, and a provider override
 * with no model is that provider's CLI default rather than the workspace's
 * model, which belongs to the workspace's provider.
 */
export function effectivePair(
  provider: string,
  model: string,
  defaultProvider = '',
  defaultModel = '',
): ModelChoicePair {
  if (provider) return { provider, model }
  return { provider: defaultProvider, model: model || defaultModel }
}

function CheckIcon(): JSX.Element {
  return (
    <svg
      className="sd-model-picker__check"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="2"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
      focusable="false"
    >
      <path d="m5 12 5 5 9-10" />
    </svg>
  )
}

/**
 * Which provider and model a session will run on, and the popover that
 * changes it.
 *
 * The chip states the pair the way the mocks write it — the mark, then
 * "claude · Sonnet 5" in the ledger face — and opens a popover with the
 * providers down the left, the chosen provider's models on the right, a
 * free-text row for a name the list does not have, and Done. Every choice
 * applies as it is made, so the chip is always the truth; Done, Escape and
 * a click outside all close, and focus goes back to the chip.
 *
 * The two lists are single-select listboxes: one tab stop each, the arrows
 * move the selection with focus, Home and End jump. Enter on a model, or in
 * the free-text row, is Done. Enter on a provider moves to its models, since
 * the model is still to be chosen.
 */
export default function ModelPicker({
  provider,
  model,
  defaultProvider = '',
  defaultModel = '',
  lastUsed = '',
  onChange,
  trigger = 'chip',
  readOnly,
  unknownAs,
  disabled = false,
}: ModelPickerProps): JSX.Element {
  const [open, setOpen] = useState(false)
  const root = useRef<HTMLSpanElement | null>(null)
  const providerList = useRef<HTMLDivElement | null>(null)
  const modelList = useRef<HTMLDivElement | null>(null)
  const input = useRef<HTMLInputElement | null>(null)
  const id = useId()

  const pair = effectivePair(provider, model, defaultProvider, defaultModel)
  const models = modelsFor(pair.provider)
  const curated = models.some((m) => m.id === pair.model)
  /** The row that is checked in the model list. */
  const checked = curated ? pair.model : OTHER
  const [text, setText] = useState(curated ? '' : pair.model)

  // A choice made elsewhere — the workspace changing under the chip — is
  // reflected in the free-text row rather than left as stale words.
  useEffect(() => {
    setText(curated ? '' : pair.model)
  }, [curated, pair.model])

  const value = describeModel(pair.provider, pair.model, lastUsed, unknownAs)

  const close = useCallback(() => {
    setOpen(false)
    root.current?.querySelector<HTMLElement>('[aria-haspopup]')?.focus()
  }, [])

  // The chosen provider takes focus when the popover opens, so the arrows
  // work at once; the trigger gets it back when it closes.
  useEffect(() => {
    if (!open) return
    const chosen = providerList.current?.querySelector<HTMLElement>('[aria-selected="true"]')
    ;(chosen ?? providerList.current)?.focus()
  }, [open])

  useEffect(() => {
    if (!open) return
    function onDown(event: globalThis.MouseEvent): void {
      if (root.current && !root.current.contains(event.target as Node)) setOpen(false)
    }
    document.addEventListener('mousedown', onDown)
    return () => document.removeEventListener('mousedown', onDown)
  }, [open])

  function emit(next: ModelChoicePair): void {
    // The workspace's own provider is the absence of an override.
    onChange?.({
      provider: next.provider === defaultProvider ? '' : next.provider,
      model: next.model,
    })
  }

  function pickProvider(next: string): void {
    if (next === pair.provider) return
    setText('')
    emit({ provider: next, model: '' })
  }

  function pickModel(row: ModelRow): void {
    if (row === OTHER) {
      input.current?.focus()
      return
    }
    setText('')
    emit({ provider: pair.provider, model: row.id })
  }

  function typeModel(raw: string): void {
    setText(raw)
    emit({ provider: pair.provider, model: raw.trim() })
  }

  /**
   * The listbox pattern: the arrows move focus and the selection together
   * from the row that has focus, Home and End jump, and a run past either
   * end wraps. `fallback` is the selected row, for a list nothing in has
   * focus yet.
   */
  function walk<T>(
    event: KeyboardEvent<HTMLDivElement>,
    items: T[],
    fallback: number,
    pick: (item: T) => void,
  ): void {
    const options = [...event.currentTarget.querySelectorAll<HTMLElement>('[role="option"]')]
    const focused = options.indexOf(document.activeElement as HTMLElement)
    const at = focused >= 0 ? focused : fallback
    let to = -1
    switch (event.key) {
      case 'ArrowDown':
        to = (at + 1) % items.length
        break
      case 'ArrowUp':
        to = (at - 1 + items.length) % items.length
        break
      case 'Home':
        to = 0
        break
      case 'End':
        to = items.length - 1
        break
      default:
        return
    }
    event.preventDefault()
    pick(items[to])
    options[to]?.focus()
  }

  function onProviderKey(event: KeyboardEvent<HTMLDivElement>): void {
    if (event.key === 'Enter') {
      event.preventDefault()
      const chosen = modelList.current?.querySelector<HTMLElement>('[aria-selected="true"]')
      ;(chosen ?? modelList.current)?.focus()
      return
    }
    const providers: string[] = [...PICKABLE_PROVIDERS]
    walk(event, providers, providers.indexOf(pair.provider), pickProvider)
  }

  function onModelKey(event: KeyboardEvent<HTMLDivElement>): void {
    const rows: ModelRow[] = [...models, OTHER]
    if (event.key === 'Enter') {
      event.preventDefault()
      // Enter on Other… steps into the box; on a model it is Done.
      const options = [...event.currentTarget.querySelectorAll<HTMLElement>('[role="option"]')]
      const focused = options.indexOf(document.activeElement as HTMLElement)
      const onOther = focused >= 0 ? rows[focused] === OTHER : checked === OTHER
      if (onOther) input.current?.focus()
      else close()
      return
    }
    const at = rows.findIndex((r) => (r === OTHER ? checked === OTHER : r.id === checked))
    // Arrowing onto Other… lands on the row; the box is a step further in.
    walk(event, rows, at, (row) => {
      if (row !== OTHER) pickModel(row)
    })
  }

  function onPopoverKey(event: KeyboardEvent<HTMLDivElement>): void {
    if (event.key === 'Escape') {
      event.preventDefault()
      event.stopPropagation()
      close()
    }
  }

  const chipBody = (
    <>
      <span className="sd-model-chip__label">Model</span>
      {pair.provider ? <ProviderMark provider={pair.provider} size="sm" /> : null}
      <span className="sd-model-chip__value" dir="ltr">
        {value}
      </span>
    </>
  )

  const popoverId = `${id}-popover`

  return (
    <span className="sd-model-picker" ref={root}>
      {readOnly ? (
        <button
          type="button"
          className="sd-model-chip"
          data-readonly="true"
          disabled
          title={readOnly}
          aria-label={`Model ${value}. ${readOnly}`}
        >
          {chipBody}
        </button>
      ) : trigger === 'change' ? (
        <Button
          variant="pale"
          disabled={disabled}
          aria-haspopup="dialog"
          aria-expanded={open}
          aria-controls={open ? popoverId : undefined}
          onClick={() => setOpen((was) => !was)}
        >
          Change
        </Button>
      ) : (
        <button
          type="button"
          className="sd-model-chip"
          disabled={disabled}
          aria-haspopup="dialog"
          aria-expanded={open}
          aria-controls={open ? popoverId : undefined}
          title="Choose the provider and model"
          onClick={() => setOpen((was) => !was)}
        >
          {chipBody}
        </button>
      )}

      {open ? (
        <div
          id={popoverId}
          className="sd-model-picker__popover"
          role="dialog"
          aria-label="Provider and model"
          onKeyDown={onPopoverKey}
        >
          <div className="sd-model-picker__columns">
            <div className="sd-model-picker__col">
              <span className="sd-model-picker__heading" id={`${id}-providers`}>
                Provider
              </span>
              <div
                ref={providerList}
                className="sd-model-picker__list"
                role="listbox"
                aria-labelledby={`${id}-providers`}
                tabIndex={-1}
                onKeyDown={onProviderKey}
              >
                {PICKABLE_PROVIDERS.map((p) => {
                  const selected = p === pair.provider
                  return (
                    <div
                      key={p}
                      role="option"
                      className="sd-model-picker__option"
                      aria-selected={selected}
                      tabIndex={selected ? 0 : -1}
                      onClick={() => pickProvider(p)}
                    >
                      <ProviderMark provider={p} size="sm" label={providerName(p)} />
                      <span className="sd-model-picker__name">{p}</span>
                      {p === defaultProvider ? (
                        <span className="sd-model-picker__tag">workspace default</span>
                      ) : null}
                      {selected ? <CheckIcon /> : null}
                    </div>
                  )
                })}
              </div>
            </div>

            <div className="sd-model-picker__col">
              <span className="sd-model-picker__heading" id={`${id}-models`}>
                Model
              </span>
              <div
                ref={modelList}
                className="sd-model-picker__list"
                role="listbox"
                aria-labelledby={`${id}-models`}
                tabIndex={-1}
                onKeyDown={onModelKey}
              >
                {models.map((m) => {
                  const selected = checked === m.id
                  return (
                    <div
                      key={m.id || CLI_DEFAULT.label}
                      role="option"
                      className="sd-model-picker__option"
                      aria-selected={selected}
                      tabIndex={selected ? 0 : -1}
                      onClick={() => {
                        pickModel(m)
                        close()
                      }}
                    >
                      <span className="sd-model-picker__name">
                        {m.label}
                        {m.note ? <span className="sd-model-picker__note">{m.note}</span> : null}
                      </span>
                      {selected ? <CheckIcon /> : null}
                    </div>
                  )
                })}
                <div
                  role="option"
                  className="sd-model-picker__option"
                  aria-selected={checked === OTHER}
                  tabIndex={checked === OTHER ? 0 : -1}
                  onClick={() => pickModel(OTHER)}
                >
                  <span className="sd-model-picker__name">Other…</span>
                  {checked === OTHER ? <CheckIcon /> : null}
                </div>
              </div>
              <div className="sd-model-picker__other">
                <label className="visually-hidden" htmlFor={`${id}-other`}>
                  Other model
                </label>
                <input
                  ref={input}
                  id={`${id}-other`}
                  className="sd-model-picker__input"
                  value={text}
                  placeholder="Type a model id"
                  autoComplete="off"
                  spellCheck={false}
                  dir="ltr"
                  onChange={(e) => typeModel(e.target.value)}
                  onKeyDown={(e) => {
                    if (e.key === 'Enter') {
                      e.preventDefault()
                      close()
                    }
                  }}
                />
                {hintFor(pair.provider) ? (
                  <p className="sd-model-picker__hint">{hintFor(pair.provider)}</p>
                ) : null}
              </div>
            </div>
          </div>
          <div className="sd-model-picker__foot">
            <Button variant="pale" size="sm" onClick={close}>
              Done
            </Button>
          </div>
        </div>
      ) : null}
    </span>
  )
}
