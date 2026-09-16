import {
  useCallback,
  useEffect,
  useId,
  useLayoutEffect,
  useRef,
  useState,
  type KeyboardEvent as ReactKeyboardEvent,
  type RefObject,
} from 'react'
import { useAnchor, type Anchorable } from '../../lib/anchor'
import './ContextMenu.css'

/** One thing the menu can do. */
export interface MenuAction {
  kind?: 'item'
  id: string
  label: string
  /** At the inline end, in the third ink: a shortcut, a value, a count. */
  detail?: string
  /** Kept in the list and focusable, so a reader learns it exists, but never picked. */
  disabled?: boolean
  /** `danger` paints the label in the failed hue; the label's own word still says what it does. */
  tone?: 'danger'
  onSelect: () => void
}

/** A label that opens a second menu beside it. */
export interface MenuSubmenu {
  kind: 'submenu'
  id: string
  label: string
  items: MenuEntry[]
}

export interface MenuSeparator {
  kind: 'separator'
  id: string
}

export type MenuEntry = MenuAction | MenuSubmenu | MenuSeparator

export interface ContextMenuProps {
  open: boolean
  /**
   * What the menu hangs from: the dots button at a row's end, or the point
   * a right-click landed at (`pointAnchor` in lib/anchor). Read when the
   * menu is measured, so the ref may be filled after the first render.
   */
  anchor: RefObject<Anchorable | null>
  items: MenuEntry[]
  /** Names the menu for a screen reader: "Session menu for OMNI-2815". */
  label: string
  /** Called on Escape, on a press outside, and after any item is picked. */
  onClose: () => void
  /**
   * Drawn in the flow rather than pinned to the viewport, with no focus
   * taken and no outside-press closing it: the gallery's specimen.
   */
  inline?: boolean
}

/** lucide `chevron-right`, on a submenu's row; mirrored in an Arabic pane by the stylesheet. */
function ChevronIcon(): JSX.Element {
  return (
    <svg
      className="sd-menu__chevron"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="2"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
      focusable="false"
    >
      <path d="m9 6 6 6-6 6" />
    </svg>
  )
}

/** The focusable rows of one panel, in order, disabled ones included. */
function rowsOf(panel: HTMLElement | null): HTMLElement[] {
  if (!panel) return []
  return [...panel.children].filter(
    (el): el is HTMLElement => el instanceof HTMLElement && el.getAttribute('role') === 'menuitem',
  )
}

function focusRow(rows: HTMLElement[], index: number): void {
  if (rows.length === 0) return
  rows[((index % rows.length) + rows.length) % rows.length]?.focus()
}

/**
 * One level of the menu: the rows, their keyboard, and the one submenu
 * that may be open beside it. The root and each submenu are this, so every
 * level answers the same keys.
 */
function MenuPanel({
  id: ownId,
  items,
  label,
  anchor,
  beside,
  inline,
  autoFocus,
  onCloseAll,
  onBack,
}: {
  /** The submenu's id, for the `aria-controls` of the row that opened it. */
  id?: string
  items: MenuEntry[]
  label: string
  anchor: RefObject<Anchorable | null>
  beside: boolean
  inline: boolean
  autoFocus: boolean
  onCloseAll: () => void
  /** ArrowLeft, and Escape on a submenu: back to the row that opened it. */
  onBack?: () => void
}): JSX.Element {
  const panel = useRef<HTMLDivElement | null>(null)
  const [openSub, setOpenSub] = useState<string | null>(null)
  /** The row of the open submenu, which that submenu hangs beside. */
  const subAnchor = useRef<HTMLElement | null>(null)
  const id = useId()

  useAnchor(!inline, anchor, panel, beside ? { beside: true } : { align: 'start' })

  useEffect(() => {
    if (!autoFocus) return
    // The first row that can be picked; else the first row at all.
    const rows = rowsOf(panel.current)
    const first = rows.find((r) => r.getAttribute('aria-disabled') !== 'true') ?? rows[0]
    first?.focus()
  }, [autoFocus])

  const submenuOf = (entryId: string): MenuSubmenu | undefined =>
    items.find((e): e is MenuSubmenu => e.kind === 'submenu' && e.id === entryId)

  function openSubmenu(entry: MenuSubmenu, row: HTMLElement): void {
    subAnchor.current = row
    setOpenSub(entry.id)
  }

  function closeSubmenu(): void {
    setOpenSub(null)
  }

  /** Back from a submenu: it closes and its row takes focus again. */
  function backFromSub(): void {
    const row = subAnchor.current
    setOpenSub(null)
    row?.focus()
  }

  function pick(entry: MenuAction): void {
    if (entry.disabled) return
    entry.onSelect()
    onCloseAll()
  }

  function onKeyDown(event: ReactKeyboardEvent<HTMLDivElement>): void {
    // A key inside the open submenu is the submenu's; it bubbles here only
    // when the submenu did not take it.
    if (event.target instanceof HTMLElement && !rowsOf(panel.current).includes(event.target)) return
    const rows = rowsOf(panel.current)
    const at = rows.indexOf(event.target as HTMLElement)
    const current = at >= 0 ? rows[at] : null
    switch (event.key) {
      case 'ArrowDown':
        event.preventDefault()
        focusRow(rows, at + 1)
        return
      case 'ArrowUp':
        event.preventDefault()
        focusRow(rows, at < 0 ? -1 : at - 1)
        return
      case 'Home':
        event.preventDefault()
        focusRow(rows, 0)
        return
      case 'End':
        event.preventDefault()
        focusRow(rows, rows.length - 1)
        return
      case 'ArrowRight': {
        const sub = current && submenuOf(current.dataset.entry ?? '')
        if (sub) {
          event.preventDefault()
          openSubmenu(sub, current)
        }
        return
      }
      case 'ArrowLeft':
        if (onBack) {
          event.preventDefault()
          event.stopPropagation()
          onBack()
        }
        return
      case 'Escape':
        event.preventDefault()
        event.stopPropagation()
        if (onBack) onBack()
        else onCloseAll()
        return
      case 'Enter':
      case ' ': {
        if (!current) return
        event.preventDefault()
        const entryId = current.dataset.entry ?? ''
        const sub = submenuOf(entryId)
        if (sub) {
          openSubmenu(sub, current)
          return
        }
        const action = items.find((e): e is MenuAction => (e.kind ?? 'item') === 'item' && e.id === entryId)
        if (action) pick(action)
        return
      }
      case 'Tab':
        // A menu is not a place Tab goes through; it closes instead.
        event.preventDefault()
        onCloseAll()
        return
      default:
    }
    // Typeahead: a letter moves to the next row starting with it.
    if (event.key.length === 1 && !event.metaKey && !event.ctrlKey && !event.altKey) {
      const wanted = event.key.toLowerCase()
      for (let step = 1; step <= rows.length; step += 1) {
        const row = rows[(Math.max(at, -1) + step) % rows.length]
        if ((row.dataset.label ?? '').startsWith(wanted)) {
          event.preventDefault()
          row.focus()
          return
        }
      }
    }
  }

  const sub = openSub ? submenuOf(openSub) : undefined

  return (
    <div
      ref={panel}
      id={ownId}
      className="sd-menu"
      role="menu"
      aria-label={label}
      tabIndex={-1}
      data-inline={inline ? 'true' : undefined}
      onKeyDown={onKeyDown}
    >
      {items.map((entry) => {
        if (entry.kind === 'separator') {
          return <div key={entry.id} className="sd-menu__separator" role="separator" />
        }
        if (entry.kind === 'submenu') {
          const isOpen = openSub === entry.id
          return (
            <button
              key={entry.id}
              type="button"
              className="sd-menu__item"
              role="menuitem"
              tabIndex={-1}
              aria-haspopup="menu"
              aria-expanded={isOpen}
              aria-controls={isOpen ? `${id}-${entry.id}` : undefined}
              data-entry={entry.id}
              data-label={entry.label.toLowerCase()}
              onClick={(event) =>
                isOpen ? closeSubmenu() : openSubmenu(entry, event.currentTarget)
              }
              onPointerEnter={(event) => {
                if (!isOpen) openSubmenu(entry, event.currentTarget)
              }}
            >
              <span className="sd-menu__label">{entry.label}</span>
              <ChevronIcon />
            </button>
          )
        }
        return (
          <button
            key={entry.id}
            type="button"
            className="sd-menu__item"
            role="menuitem"
            tabIndex={-1}
            aria-disabled={entry.disabled ? 'true' : undefined}
            data-entry={entry.id}
            data-label={entry.label.toLowerCase()}
            data-tone={entry.tone}
            onClick={() => pick(entry)}
            onPointerEnter={() => {
              // The pointer on another row closes a submenu a sibling opened.
              if (openSub) closeSubmenu()
            }}
          >
            <span className="sd-menu__label">{entry.label}</span>
            {entry.detail ? (
              <span className="sd-menu__detail" dir="ltr">
                {entry.detail}
              </span>
            ) : null}
          </button>
        )
      })}
      {sub ? (
        <div className="sd-menu__sub">
          <MenuPanel
            id={`${id}-${sub.id}`}
            items={sub.items}
            label={sub.label}
            anchor={subAnchor}
            beside
            inline={inline}
            autoFocus={!inline}
            onCloseAll={onCloseAll}
            onBack={backFromSub}
          />
        </div>
      ) : null}
    </div>
  )
}

/**
 * A menu of actions on one thing: a session row's Pin, Snooze, Rename,
 * Copy, Delete. Opened by a right-click, a double-click or the dots button
 * at the row's end, and by the context-menu key on a focused row.
 *
 * Pinned to the viewport by `lib/anchor` — below the anchor when it fits,
 * above when it does not, held inside the window — on the dialog's surface.
 * The first row takes focus when it opens; the arrows move along the rows
 * and wrap, Home and End jump, Enter and Space pick, a letter jumps to the
 * next row starting with it, ArrowRight opens a submenu beside its row and
 * ArrowLeft comes back, Escape closes, and focus returns to whatever had it
 * before. A press anywhere outside closes it too.
 */
export default function ContextMenu({
  open,
  anchor,
  items,
  label,
  onClose,
  inline = false,
}: ContextMenuProps): JSX.Element | null {
  const opener = useRef<HTMLElement | null>(null)
  const root = useRef<HTMLDivElement | null>(null)

  const closeAll = useCallback(() => {
    onClose()
  }, [onClose])

  // Focus goes back to what had it, once the menu is gone. A layout effect,
  // because the panel's own effect moves focus onto its first row, and a
  // child's passive effect runs before the parent's: read after it, this
  // would remember the row rather than the opener.
  useLayoutEffect(() => {
    if (!open || inline) return
    opener.current = document.activeElement as HTMLElement | null
    return () => {
      const back = opener.current
      opener.current = null
      if (back && back.isConnected) back.focus?.()
    }
  }, [open, inline])

  // A press outside any level of the menu closes it. Capture phase, so a
  // press on something that stops propagation still counts as outside.
  useEffect(() => {
    if (!open || inline) return
    function onPress(event: PointerEvent): void {
      if (root.current?.contains(event.target as Node)) return
      onClose()
    }
    document.addEventListener('pointerdown', onPress, true)
    return () => document.removeEventListener('pointerdown', onPress, true)
  }, [open, inline, onClose])

  if (!open) return null

  return (
    <div ref={root} className="sd-menu__root" data-inline={inline ? 'true' : undefined}>
      <MenuPanel
        items={items}
        label={label}
        anchor={anchor}
        beside={false}
        inline={inline}
        autoFocus={!inline}
        onCloseAll={closeAll}
      />
    </div>
  )
}
