import {
  useCallback,
  useEffect,
  useId,
  useMemo,
  useRef,
  useState,
  type KeyboardEvent,
  type ReactNode,
} from 'react'
import { useAnchor } from '../../lib/anchor'
import {
  catalogChoices,
  probeStatus,
  GROUP_HEADINGS,
  GROUP_ORDER,
  type CatalogChoice,
  type ModelCatalog,
  type RowGroup,
} from '../../lib/modelCatalog'
import { CLI_DEFAULT, describeModel, hintFor, modelLabel, PICKABLE_PROVIDERS } from '../../lib/models'
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
  /** The workspace's configured provider, marked "workspace default" in the rail. */
  defaultProvider?: string
  /** The workspace's configured model, shown while no override names one. */
  defaultModel?: string
  /**
   * What the newest run on the provider reported: named under the CLI
   * default row and in the chip's title when nothing names a model, so a
   * reader learns what "CLI default" turned out to be last time.
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
  /**
   * The discovered list (`lib/modelCatalog`). Given, opening the popover
   * loads it and the list shows what this login and this workspace have
   * actually used; absent, the static table is the list, marked as not
   * verified on this login.
   */
  catalog?: ModelCatalog
  /**
   * The choice is for the next turn of a run that ran on another model: the
   * chip reads "next: Opus 4.5", so it is not mistaken for the past.
   */
  next?: boolean
}

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

/** One row of the list: a model on a provider, under its group. */
export interface ModelRow extends CatalogChoice {
  provider: string
}

/** The shortcut the first nine rows get, ⌘1 … ⌘9. */
export const SHORTCUT_ROWS = 9

const FAVOURITES_KEY = 'sirdar.modelFavourites.'

/** localStorage is absent in some tests and can throw in a locked-down webview. */
export function readFavourites(provider: string): string[] {
  try {
    const raw = globalThis.localStorage?.getItem(FAVOURITES_KEY + provider)
    const parsed: unknown = raw ? JSON.parse(raw) : []
    return Array.isArray(parsed) ? parsed.filter((x): x is string => typeof x === 'string') : []
  } catch {
    return []
  }
}

function writeFavourites(provider: string, ids: string[]): void {
  try {
    globalThis.localStorage?.setItem(FAVOURITES_KEY + provider, JSON.stringify(ids))
  } catch {
    // A favourite that cannot be remembered still holds for this session.
  }
}

/**
 * Whether a row answers a search: by its label, its id, the provider's
 * config name or the vendor's name, case folded.
 */
export function matchesQuery(row: Pick<ModelRow, 'id' | 'label' | 'provider'>, query: string): boolean {
  const q = query.trim().toLowerCase()
  if (!q) return true
  return [row.label, row.id, row.provider, providerName(row.provider)].some((s) =>
    s.toLowerCase().includes(q),
  )
}

/**
 * The rows the list shows, in order: the favourites first, then the rest.
 * With no query, the chosen provider's own list; with one, every pickable
 * provider's curated rows that answer it, so a reader who types "gpt" finds
 * Codex's model without walking the rail first.
 */
export function listRows(
  provider: string,
  query: string,
  favourites: (provider: string) => string[],
  choicesFor: (provider: string) => CatalogChoice[] = (p) => catalogChoices(p, undefined),
): { favourites: ModelRow[]; rest: ModelRow[]; groups: { group: RowGroup; rows: ModelRow[] }[] } {
  const searching = query.trim() !== ''
  const providers = searching ? PICKABLE_PROVIDERS : [provider]
  const all: ModelRow[] = []
  for (const p of providers) {
    for (const m of choicesFor(p)) all.push({ ...m, provider: p })
  }
  const shown = all.filter((row) => matchesQuery(row, query))
  const starred = new Map(providers.map((p) => [p, new Set(favourites(p))] as const))
  const rest = shown.filter((row) => !starred.get(row.provider)?.has(row.id))
  // A search is one list of matches; otherwise each source is its own group.
  const groups = searching
    ? [{ group: 'default' as RowGroup, rows: rest }]
    : GROUP_ORDER.map((group) => ({ group, rows: rest.filter((r) => r.group === group) })).filter(
        (g) => g.rows.length > 0,
      )
  return {
    favourites: shown.filter((row) => starred.get(row.provider)?.has(row.id)),
    rest: groups.flatMap((g) => g.rows),
    groups,
  }
}

function Icon({ children, className }: { children: ReactNode; className?: string }): JSX.Element {
  return (
    <svg
      className={className}
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.8"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
      focusable="false"
    >
      {children}
    </svg>
  )
}

function ChevronIcon(): JSX.Element {
  return (
    <Icon className="sd-model-chip__chevron">
      <path d="m6 9 6 6 6-6" />
    </Icon>
  )
}

/** lucide `lock`: the chip is fixed while the run works. */
function LockIcon(): JSX.Element {
  return (
    <Icon className="sd-model-chip__lock">
      <rect x="5" y="11" width="14" height="10" rx="2" />
      <path d="M8 11V7a4 4 0 0 1 8 0v4" />
    </Icon>
  )
}

/** lucide `rotate-cw`. */
function RefreshIcon(): JSX.Element {
  return (
    <Icon className="sd-model-picker__refresh-icon">
      <path d="M21 12a9 9 0 1 1-2.6-6.4L21 8" />
      <path d="M21 3v5h-5" />
    </Icon>
  )
}

function SearchIcon(): JSX.Element {
  return (
    <Icon className="sd-model-picker__search-icon">
      <circle cx="11" cy="11" r="7" />
      <path d="m20 20-3.5-3.5" />
    </Icon>
  )
}

function CheckIcon(): JSX.Element {
  return (
    <Icon className="sd-model-picker__check">
      <path d="m5 12 5 5 9-10" />
    </Icon>
  )
}

/** lucide `star`; filled when the row is a favourite. */
function StarIcon({ filled }: { filled: boolean }): JSX.Element {
  return (
    <svg
      viewBox="0 0 24 24"
      fill={filled ? 'currentColor' : 'none'}
      stroke="currentColor"
      strokeWidth="1.8"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
      focusable="false"
    >
      <path d="M12 3.5l2.7 5.6 6.1.9-4.4 4.3 1 6.1L12 17.5l-5.4 2.9 1-6.1L3.2 10l6.1-.9z" />
    </svg>
  )
}

/**
 * Which provider and model a session will run on, and the popover that
 * changes it.
 *
 * The chip is the mark, the model's curated label and a chevron; its
 * accessible name and title carry the whole pair, "Model: claude · Sonnet
 * 5". It opens a 480-wide popover pinned to the viewport beside it
 * (`lib/anchor`): a search field across the top, a 48px rail of provider
 * marks down the left, and the list — the favourites under their own
 * heading, then CLI default and the curated ids. The search field is also
 * the free-text entry: text that answers no row is offered back as one row,
 * "Use “…” as the model id", which Enter picks. The first nine rows carry
 * ⌘1…⌘9 and answer to them while the popover is open; a star on each row
 * keeps a favourite in localStorage, per provider. Every choice applies as
 * it is made, so the chip is always the truth; picking a row closes and
 * hands focus back to the chip.
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
  catalog,
  next = false,
}: ModelPickerProps): JSX.Element {
  const [open, setOpen] = useState(false)
  const [query, setQuery] = useState('')
  /** Bumped when a star is toggled, so the list re-reads the favourites. */
  const [starred, setStarred] = useState(0)
  // The wrapper is the anchor: the popover is fixed, so it adds nothing to
  // the wrapper's box, which is the trigger's own.
  const root = useRef<HTMLSpanElement | null>(null)
  const popover = useRef<HTMLDivElement | null>(null)
  const search = useRef<HTMLInputElement | null>(null)
  const list = useRef<HTMLDivElement | null>(null)
  const id = useId()

  const pair = effectivePair(provider, model, defaultProvider, defaultModel)
  const entry = catalog?.entry(pair.provider)
  const discovered = entry?.list?.models
  const value = describeModel(pair.provider, pair.model, lastUsed, unknownAs, discovered)
  const named = !pair.provider
    ? 'not set'
    : pair.model
      ? modelLabel(pair.provider, pair.model, discovered)
      : (unknownAs ?? CLI_DEFAULT.label)
  const label = next ? `next: ${named}` : named
  const spoken = next ? `Model for the next turn ${value}` : `Model ${value}`

  useAnchor(open, root, popover)

  const close = useCallback(() => {
    setOpen(false)
    setQuery('')
    root.current?.querySelector<HTMLElement>('[aria-haspopup]')?.focus()
  }, [])

  // The search field takes focus when the popover opens, so typing filters
  // at once; the chip gets it back when it closes.
  useEffect(() => {
    if (open) search.current?.focus()
  }, [open])

  // A chip that stops being a control while its popover is open — the run
  // started working, the screen went busy — takes the popover with it. Left
  // open, the list went on offering rows whose pick nothing would take.
  const locked = Boolean(readOnly) || disabled
  useEffect(() => {
    if (!locked) return
    setOpen(false)
    setQuery('')
  }, [locked])

  // Opening loads the provider's discovered list, and so does moving to
  // another provider on the rail while open. `load` asks for a probe only
  // when the list says one is due.
  const load = catalog?.load
  useEffect(() => {
    if (open && pair.provider) load?.(pair.provider)
  }, [open, pair.provider, load])

  useEffect(() => {
    if (!open) return
    function onDown(event: globalThis.MouseEvent): void {
      const target = event.target as Node
      if (root.current?.contains(target) || popover.current?.contains(target)) return
      setOpen(false)
      setQuery('')
    }
    document.addEventListener('mousedown', onDown)
    return () => document.removeEventListener('mousedown', onDown)
  }, [open])

  const { favourites, rest, groups } = useMemo(
    () =>
      listRows(pair.provider, query, readFavourites, (p) =>
        catalogChoices(p, catalog ? catalog.entry(p) : undefined),
      ),
    // `starred` and `open` carry no value of their own: each re-reads the
    // favourites out of storage, after a star or on opening.
    [pair.provider, query, starred, open, catalog],
  )
  const rows = [...favourites, ...rest]
  // The search field is the free-text entry: text that answers no row is
  // offered back as a model id, on one row of its own.
  const typed = query.trim()
  const offerTyped = typed !== '' && rows.length === 0

  function emit(next: ModelChoicePair): void {
    // The workspace's own provider is the absence of an override.
    onChange?.({
      provider: next.provider === defaultProvider ? '' : next.provider,
      model: next.model,
    })
  }

  function pickProvider(next: string): void {
    if (next === pair.provider) return
    emit({ provider: next, model: '' })
  }

  function pickRow(row: ModelRow): void {
    emit({ provider: row.provider, model: row.id })
    close()
  }

  /** Takes what was typed as the model id, on the provider the rail has. */
  function useTyped(): void {
    if (!typed) return
    emit({ provider: pair.provider, model: typed })
    close()
  }

  function toggleFavourite(row: ModelRow): void {
    const have = readFavourites(row.provider)
    const next = have.includes(row.id) ? have.filter((x) => x !== row.id) : [...have, row.id]
    writeFavourites(row.provider, next)
    setStarred((n) => n + 1)
  }

  function isSelected(row: ModelRow): boolean {
    return row.provider === pair.provider && row.id === pair.model
  }

  function options(): HTMLElement[] {
    return [...(list.current?.querySelectorAll<HTMLElement>('[role="option"]') ?? [])]
  }

  function focusOption(index: number): void {
    const all = options()
    if (all.length === 0) return
    all[((index % all.length) + all.length) % all.length]?.focus()
  }

  /** The listbox pattern: the arrows move focus, Home and End jump, Enter picks, f stars. */
  function onListKey(event: KeyboardEvent<HTMLDivElement>): void {
    const all = options()
    const at = all.indexOf(document.activeElement as HTMLElement)
    switch (event.key) {
      case 'ArrowDown':
        event.preventDefault()
        focusOption(at + 1)
        return
      case 'ArrowUp':
        event.preventDefault()
        if (at <= 0) search.current?.focus()
        else focusOption(at - 1)
        return
      case 'Home':
        event.preventDefault()
        focusOption(0)
        return
      case 'End':
        event.preventDefault()
        focusOption(all.length - 1)
        return
      case 'Enter':
      case ' ': {
        if (at < 0) return
        event.preventDefault()
        if (at < rows.length) pickRow(rows[at])
        else if (offerTyped) useTyped()
        else search.current?.focus()
        return
      }
      case 'f':
      case 'F':
        if (at >= 0 && at < rows.length && !event.metaKey && !event.ctrlKey) {
          event.preventDefault()
          toggleFavourite(rows[at])
        }
        return
      default:
    }
  }

  function onRailKey(event: KeyboardEvent<HTMLDivElement>): void {
    const providers: string[] = [...PICKABLE_PROVIDERS]
    const at = providers.indexOf(pair.provider)
    let to = -1
    switch (event.key) {
      case 'ArrowDown':
        to = (at + 1) % providers.length
        break
      case 'ArrowUp':
        to = (at - 1 + providers.length) % providers.length
        break
      case 'Home':
        to = 0
        break
      case 'End':
        to = providers.length - 1
        break
      default:
        return
    }
    event.preventDefault()
    pickProvider(providers[to])
    const tabs = event.currentTarget.querySelectorAll<HTMLElement>('[role="tab"]')
    tabs[to]?.focus()
  }

  function onSearchKey(event: KeyboardEvent<HTMLInputElement>): void {
    if (event.key === 'ArrowDown') {
      event.preventDefault()
      focusOption(0)
    } else if (event.key === 'Enter') {
      event.preventDefault()
      if (rows.length > 0) pickRow(rows[0])
      else useTyped()
    }
  }

  function onPopoverKey(event: KeyboardEvent<HTMLDivElement>): void {
    if (event.key === 'Escape') {
      event.preventDefault()
      event.stopPropagation()
      close()
      return
    }
    // ⌘1 … ⌘9 (Ctrl on a keyboard without a command key) pick a row outright.
    if ((event.metaKey || event.ctrlKey) && /^[1-9]$/.test(event.key)) {
      const row = rows[Number(event.key) - 1]
      if (row) {
        event.preventDefault()
        pickRow(row)
      }
    }
  }

  const popoverId = `${id}-popover`

  const chipBody = (
    <>
      {pair.provider ? <ProviderMark provider={pair.provider} size="sm" /> : null}
      <span className="sd-model-chip__value" dir="ltr">
        {label}
      </span>
    </>
  )

  function renderRow(row: ModelRow, index: number): JSX.Element {
    const selected = isSelected(row)
    const starred = favourites.includes(row)
    const shortcut = index < SHORTCUT_ROWS ? `⌘${index + 1}` : ''
    const note =
      row.id === '' && row.provider === pair.provider && lastUsed ? `last used ${lastUsed}` : (row.note ?? '')
    return (
      <div
        key={`${row.provider}/${row.id || CLI_DEFAULT.label}`}
        role="option"
        className="sd-model-picker__row"
        aria-selected={selected}
        aria-keyshortcuts={shortcut ? `Meta+${index + 1}` : undefined}
        data-group={row.group}
        tabIndex={-1}
        onClick={() => pickRow(row)}
      >
        <button
          type="button"
          className="sd-model-picker__star"
          aria-label={`${starred ? 'Unfavourite' : 'Favourite'} ${row.label}`}
          aria-pressed={starred}
          tabIndex={-1}
          onClick={(event) => {
            event.stopPropagation()
            toggleFavourite(row)
          }}
        >
          <StarIcon filled={starred} />
        </button>
        <span className="sd-model-picker__text">
          <span className="sd-model-picker__label" dir="auto">{row.label}</span>
          <span className="sd-model-picker__meta" dir="ltr">
            <ProviderMark provider={row.provider} size="sm" />
            <span>{row.provider}</span>
            {note ? <span className="sd-model-picker__note">· {note}</span> : null}
          </span>
        </span>
        {selected ? <CheckIcon /> : null}
        {shortcut ? (
          <kbd className="sd-model-picker__kbd" aria-hidden="true">
            {shortcut}
          </kbd>
        ) : null}
      </div>
    )
  }

  /** The footer's one line: where the list stands, and when the CLI was last asked. */
  function footStatus(): string {
    if (!entry || (entry.state === 'loading' && !entry.list)) return 'Checking which models are known…'
    if (entry.state === 'ready' && entry.probing) return 'Asking the CLI which models this login has…'
    if (entry.state === 'loading') return 'Checking…'
    if (entry.state === 'error') return `Could not read the list: ${entry.message}`
    return probeStatus(entry.list)
  }

  const busy = entry?.state === 'loading' || (entry?.state === 'ready' && entry.probing === true)
  const probeErrors = entry?.list?.probeErrors ?? []
  // Shortcut numbers run on across the groups, in display order.
  let shown = favourites.length

  return (
    <span className="sd-model-picker" ref={root}>
      {readOnly ? (
        <button
          type="button"
          className="sd-model-chip"
          data-readonly="true"
          disabled
          title={readOnly}
          aria-label={`${spoken}. ${readOnly}`}
        >
          {chipBody}
          <LockIcon />
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
          data-next={next ? 'true' : undefined}
          disabled={disabled}
          aria-haspopup="dialog"
          aria-expanded={open}
          aria-controls={open ? popoverId : undefined}
          aria-label={spoken}
          title={next ? `For the next turn: ${value}` : `Model: ${value}`}
          onClick={() => setOpen((was) => !was)}
        >
          {chipBody}
          <ChevronIcon />
        </button>
      )}

      {open ? (
        <div
          id={popoverId}
          ref={popover}
          className="sd-model-picker__popover"
          role="dialog"
          aria-label="Provider and model"
          onKeyDown={onPopoverKey}
        >
          <div className="sd-model-picker__search">
            <SearchIcon />
            <label className="visually-hidden" htmlFor={`${id}-search`}>
              Search models
            </label>
            <input
              ref={search}
              id={`${id}-search`}
              className="sd-model-picker__search-input"
              type="search"
              value={query}
              placeholder="Search models, or type a model id"
              autoComplete="off"
              spellCheck={false}
              onChange={(e) => setQuery(e.target.value)}
              onKeyDown={onSearchKey}
            />
          </div>

          <div className="sd-model-picker__body">
            <div
              className="sd-model-picker__rail"
              role="tablist"
              aria-label="Provider"
              aria-orientation="vertical"
              onKeyDown={onRailKey}
            >
              {PICKABLE_PROVIDERS.map((p) => {
                const selected = p === pair.provider
                const name = providerName(p)
                const isDefault = p === defaultProvider
                return (
                  <button
                    key={p}
                    type="button"
                    role="tab"
                    className="sd-model-picker__provider"
                    aria-selected={selected}
                    aria-label={isDefault ? `${name}, workspace default` : name}
                    title={isDefault ? `${name} · workspace default` : name}
                    tabIndex={selected ? 0 : -1}
                    onClick={() => pickProvider(p)}
                  >
                    <ProviderMark provider={p} size="sm" label={name} />
                  </button>
                )
              })}
            </div>

            <div
              ref={list}
              className="sd-model-picker__list"
              role="listbox"
              aria-label="Model"
              aria-busy={busy || undefined}
              onKeyDown={onListKey}
            >
              {favourites.length > 0 ? (
                <div role="group" aria-labelledby={`${id}-favourites`}>
                  <span className="sd-model-picker__heading" id={`${id}-favourites`}>
                    Favourites
                  </span>
                  {favourites.map((row, i) => renderRow(row, i))}
                </div>
              ) : null}
              {groups.map((g) => {
                const start = shown
                shown += g.rows.length
                const headingId = `${id}-group-${g.group}`
                return (
                  <div key={g.group} role="group" aria-labelledby={headingId}>
                    <span className="sd-model-picker__heading" id={headingId}>
                      {typed ? 'Matches' : GROUP_HEADINGS[g.group]}
                    </span>
                    {g.rows.map((row, i) => renderRow(row, start + i))}
                  </div>
                )
              })}
              <div role="group" aria-label="Other">
                {offerTyped ? (
                  <div
                    role="option"
                    className="sd-model-picker__row sd-model-picker__row--use"
                    aria-selected={false}
                    tabIndex={-1}
                    onClick={useTyped}
                  >
                    <span className="sd-model-picker__text">
                      <span className="sd-model-picker__label">
                        Use “<span dir="ltr">{typed}</span>” as the model id
                      </span>
                      {hintFor(pair.provider) ? (
                        <span className="sd-model-picker__hint">{hintFor(pair.provider)}</span>
                      ) : null}
                    </span>
                  </div>
                ) : null}
                {!typed ? (
                  // The free-text row: any id the list lacks is typed into
                  // the search, which offers it back as "Use …".
                  <div
                    role="option"
                    className="sd-model-picker__row sd-model-picker__row--other"
                    aria-selected={false}
                    tabIndex={-1}
                    onClick={() => search.current?.focus()}
                  >
                    <span className="sd-model-picker__text">
                      <span className="sd-model-picker__label">Other…</span>
                      <span className="sd-model-picker__hint">
                        {hintFor(pair.provider) || 'Type a model id in the search'}
                      </span>
                    </span>
                  </div>
                ) : null}
              </div>
            </div>
          </div>

          {catalog ? (
            <div className="sd-model-picker__foot">
              <span
                className="sd-model-picker__status"
                role="status"
                title={probeErrors.length > 0 ? probeErrors.join('\n') : undefined}
              >
                {footStatus()}
              </span>
              <button
                type="button"
                className="sd-model-picker__refresh"
                disabled={busy}
                onClick={() => catalog.refresh(pair.provider)}
              >
                <RefreshIcon />
                Refresh
              </button>
            </div>
          ) : null}
        </div>
      ) : null}
    </span>
  )
}
