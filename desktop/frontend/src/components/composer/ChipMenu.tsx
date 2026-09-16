import { useEffect, useId, useRef, useState, type KeyboardEvent, type ReactNode } from 'react'
import { useAnchor } from '../../lib/anchor'

export interface ChipMenuItem {
  id: string
  label: string
  /** One line under the label saying what choosing it means. */
  note?: string
  /** Set when the item cannot be chosen right now; the words are the reason. */
  disabled?: string
}

export interface ChipMenuProps {
  /** The chip's word: Mode, Access. */
  label: string
  /** The chosen item, by id. */
  value: string
  items: ChipMenuItem[]
  /**
   * Called with the item chosen. Absent, the popover only explains: every
   * item is described and the current one is marked, but nothing can be
   * picked — an Access chip says what a mode does to the tree without
   * offering to change it.
   */
  onSelect?: (id: string) => void
  /**
   * The chip is a fact, not a control, and this says why: the session
   * composer's Mode is the run's kind, which does not change.
   */
  readOnly?: string
  disabled?: boolean
  /** A 14px glyph before the value. */
  icon?: ReactNode
}

function ChevronIcon(): JSX.Element {
  return (
    <svg
      className="composer-chip__chevron"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.8"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
      focusable="false"
    >
      <path d="m6 9 6 6 6-6" />
    </svg>
  )
}

function CheckIcon(): JSX.Element {
  return (
    <svg
      className="composer-menu__check"
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
 * A chip in the composer bar — a word, the chosen value and a chevron — and
 * the small menu it opens, pinned to the viewport beside it by `lib/anchor`.
 *
 * With `onSelect` the popover is a `menu` of `menuitemradio`s: the arrows
 * move, Enter or Space picks and closes, Escape closes, and focus goes back
 * to the chip on every path. Without it the popover is a `dialog` that
 * describes the items and marks the current one, which is what the Access
 * chip needs: the posture follows the mode and is not a choice of its own.
 */
export default function ChipMenu({
  label,
  value,
  items,
  onSelect,
  readOnly,
  disabled = false,
  icon,
}: ChipMenuProps): JSX.Element {
  const [open, setOpen] = useState(false)
  const root = useRef<HTMLSpanElement | null>(null)
  const chip = useRef<HTMLButtonElement | null>(null)
  const popover = useRef<HTMLDivElement | null>(null)
  const id = useId()
  const current = items.find((item) => item.id === value)
  const explains = !onSelect

  useAnchor(open, root, popover)

  function close(): void {
    setOpen(false)
    chip.current?.focus()
  }

  // The chosen item takes focus when the menu opens, so the arrows work at
  // once; an explaining popover takes focus itself so Escape reaches it.
  useEffect(() => {
    if (!open) return
    const chosen = popover.current?.querySelector<HTMLElement>('[aria-checked="true"]')
    ;(chosen ?? popover.current)?.focus()
  }, [open])

  useEffect(() => {
    if (!open) return
    function onDown(event: globalThis.MouseEvent): void {
      if (root.current && !root.current.contains(event.target as Node)) setOpen(false)
    }
    document.addEventListener('mousedown', onDown)
    return () => document.removeEventListener('mousedown', onDown)
  }, [open])

  function pick(item: ChipMenuItem): void {
    if (item.disabled || !onSelect) return
    onSelect(item.id)
    close()
  }

  function onKey(event: KeyboardEvent<HTMLDivElement>): void {
    if (event.key === 'Escape') {
      event.preventDefault()
      event.stopPropagation()
      close()
      return
    }
    if (explains) return
    const rows = [...event.currentTarget.querySelectorAll<HTMLElement>('[role="menuitemradio"]')]
    const at = rows.indexOf(document.activeElement as HTMLElement)
    switch (event.key) {
      case 'ArrowDown':
        event.preventDefault()
        rows[(at + 1) % rows.length]?.focus()
        return
      case 'ArrowUp':
        event.preventDefault()
        rows[(at - 1 + rows.length) % rows.length]?.focus()
        return
      case 'Home':
        event.preventDefault()
        rows[0]?.focus()
        return
      case 'End':
        event.preventDefault()
        rows[rows.length - 1]?.focus()
        return
      case 'Enter':
      case ' ':
        if (at >= 0) {
          event.preventDefault()
          pick(items[at])
        }
        return
      default:
    }
  }

  const popoverId = `${id}-menu`
  const name = `${label}: ${current?.label ?? value}`

  return (
    <span className="composer-chip-menu" ref={root}>
      <button
        ref={chip}
        type="button"
        className="composer-chip"
        data-readonly={readOnly ? 'true' : undefined}
        disabled={disabled || Boolean(readOnly)}
        title={readOnly ?? (explains ? `What ${current?.label ?? value} means` : `Choose the ${label.toLowerCase()}`)}
        aria-label={readOnly ? `${name}. ${readOnly}` : name}
        aria-haspopup={readOnly ? undefined : explains ? 'dialog' : 'menu'}
        aria-expanded={readOnly ? undefined : open}
        aria-controls={open ? popoverId : undefined}
        onClick={() => setOpen((was) => !was)}
      >
        <span className="composer-chip__label">{label}</span>
        {icon}
        <span className="composer-chip__value">{current?.label ?? value}</span>
        {readOnly ? null : <ChevronIcon />}
      </button>

      {open ? (
        <div
          id={popoverId}
          ref={popover}
          className="composer-menu"
          role={explains ? 'dialog' : 'menu'}
          aria-label={label}
          tabIndex={-1}
          onKeyDown={onKey}
        >
          {items.map((item) => {
            const checked = item.id === value
            const body = (
              <>
                <span className="composer-menu__text">
                  <span className="composer-menu__label">{item.label}</span>
                  {item.note ? <span className="composer-menu__note">{item.note}</span> : null}
                  {item.disabled ? (
                    <span className="composer-menu__note" data-tone="off">
                      {item.disabled}
                    </span>
                  ) : null}
                </span>
                {checked ? <CheckIcon /> : null}
              </>
            )
            if (explains) {
              return (
                <div
                  key={item.id}
                  className="composer-menu__item"
                  data-current={checked ? 'true' : undefined}
                  aria-current={checked ? 'true' : undefined}
                >
                  {body}
                </div>
              )
            }
            return (
              <div
                key={item.id}
                role="menuitemradio"
                className="composer-menu__item"
                aria-checked={checked}
                aria-disabled={item.disabled ? true : undefined}
                title={item.disabled}
                tabIndex={-1}
                onClick={() => pick(item)}
              >
                {body}
              </div>
            )
          })}
        </div>
      ) : null}
    </span>
  )
}
