import { useEffect, useId, useRef, type KeyboardEvent, type ReactNode } from 'react'
import { focusable } from '../dialog'
import { MODAL_ENTER_CLASS, SCRIM_ENTER_CLASS } from '../motion'
import './ModalSheet.css'

export interface ModalNavItem {
  id: string
  label: string
  /** An inline stroke SVG on the 24 grid, drawn at 18px before the label. */
  icon?: ReactNode
}

export interface ModalNavGroup {
  /** The group's heading. A group with no label prints no heading. */
  label?: string
  items: ModalNavItem[]
}

export interface ModalSheetProps {
  open: boolean
  /** The page heading, set in the display serif — the app's only one. */
  title: string
  groups: ModalNavGroup[]
  /** The page the reader is on. */
  current: string
  onSelect: (id: string) => void
  onClose: () => void
  children: ReactNode
  /** The modal's own actions. The one that commits is the primary button. */
  footer?: ReactNode
  /** Under the secondary nav: the version, the last sync. 11px, readable. */
  navFooter?: ReactNode
  /** Names the secondary nav; two navs on a screen need two names. */
  navLabel?: string
}

/** Every item in the secondary nav, flattened, in the order it is drawn. */
function order(groups: ModalNavGroup[]): string[] {
  return groups.flatMap((group) => group.items.map((item) => item.id))
}

/**
 * The large dialog: a settings sheet with its own secondary nav.
 *
 * Settings is a place you leave. Keeping the board painted behind the scrim is
 * what says you are coming back, and it is the reason this is a modal rather
 * than a sixth screen — which is also why the scrim is a tint rather than a
 * blur. Nothing else in the app gets one of these.
 *
 * There is no shadow. The scrim is the elevation, and a soft shadow under a
 * sheet that already sits on a dimmed window is a second answer to a question
 * that has one.
 */
export default function ModalSheet({
  open,
  title,
  groups,
  current,
  onSelect,
  onClose,
  children,
  footer,
  navFooter,
  navLabel = 'Settings sections',
}: ModalSheetProps): JSX.Element | null {
  const panel = useRef<HTMLDivElement | null>(null)
  const opener = useRef<HTMLElement | null>(null)
  const titleId = useId()

  useEffect(() => {
    if (!open) return
    opener.current = document.activeElement as HTMLElement | null
    const first = panel.current ? focusable(panel.current)[0] : null
    ;(first ?? panel.current)?.focus()
    return () => {
      // Back to the nav row that opened it, which is what the app-shell
      // language asks for by name.
      opener.current?.focus?.()
    }
  }, [open])

  useEffect(() => {
    if (!open) return
    function onKeyDown(event: globalThis.KeyboardEvent): void {
      if (event.key === 'Escape') {
        event.preventDefault()
        onClose()
        return
      }
      if (event.key !== 'Tab' || !panel.current) return
      const stops = focusable(panel.current)
      if (stops.length === 0) return
      const first = stops[0]
      const last = stops[stops.length - 1]
      const active = document.activeElement
      if (event.shiftKey && (active === first || !panel.current.contains(active))) {
        event.preventDefault()
        last.focus()
      } else if (!event.shiftKey && active === last) {
        event.preventDefault()
        first.focus()
      }
    }
    window.addEventListener('keydown', onKeyDown)
    return () => window.removeEventListener('keydown', onKeyDown)
  }, [open, onClose])

  /** Arrow keys move within the secondary nav, as a nav list rather than a tab list. */
  function onNavKeyDown(event: KeyboardEvent<HTMLElement>): void {
    if (event.key !== 'ArrowDown' && event.key !== 'ArrowUp') return
    const ids = order(groups)
    const at = ids.indexOf(current)
    if (at < 0) return
    event.preventDefault()
    const next = ids[(at + (event.key === 'ArrowDown' ? 1 : -1) + ids.length) % ids.length]
    onSelect(next)
    const rows = event.currentTarget.querySelectorAll<HTMLButtonElement>('.sd-modal__nav-row')
    rows[ids.indexOf(next)]?.focus()
  }

  if (!open) return null

  return (
    <div
      className={`sd-modal-scrim ${SCRIM_ENTER_CLASS}`}
      onMouseDown={(event) => {
        if (event.target === event.currentTarget) onClose()
      }}
    >
      <div
        ref={panel}
        className={`sd-modal ${MODAL_ENTER_CLASS}`}
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        tabIndex={-1}
      >
        <nav className="sd-modal__nav" aria-label={navLabel} onKeyDown={onNavKeyDown}>
          <div className="sd-modal__nav-list">
            {groups.map((group, index) => (
              <div className="sd-modal__nav-group" key={group.label ?? `group-${index}`}>
                {group.label && <p className="sd-modal__nav-label">{group.label}</p>}
                {group.items.map((item) => (
                  <button
                    key={item.id}
                    type="button"
                    className="sd-modal__nav-row"
                    aria-current={item.id === current ? 'page' : undefined}
                    onClick={() => onSelect(item.id)}
                  >
                    {item.icon && (
                      <span className="sd-modal__nav-icon" aria-hidden="true">
                        {item.icon}
                      </span>
                    )}
                    <span>{item.label}</span>
                  </button>
                ))}
              </div>
            ))}
          </div>
          {navFooter && <div className="sd-modal__nav-foot">{navFooter}</div>}
        </nav>

        <div className="sd-modal__panel">
          <h2 className="sd-modal__title" id={titleId}>
            {title}
          </h2>
          <div className="sd-modal__body">{children}</div>
          {footer && <div className="sd-modal__footer">{footer}</div>}
        </div>
      </div>
    </div>
  )
}
