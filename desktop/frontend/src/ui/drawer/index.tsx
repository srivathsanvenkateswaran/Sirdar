import { useEffect, useId, useRef, type ReactNode } from 'react'
import './Drawer.css'

export interface DrawerProps {
  open: boolean
  /** The word in the head: Bundle, Tools. */
  title: string
  /** The small mono line after the word: `15 calls · 2 denied`, `ticket.json 2.9 kB`. */
  meta?: string
  onClose: () => void
  children: ReactNode
  /** The column's width, when it is not the session path's 432px. */
  width?: number
  /** Names the drawer for a screen reader when the title alone is not enough. */
  label?: string
}

function CloseIcon(): JSX.Element {
  return (
    <svg
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.6"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
      focusable="false"
    >
      <path d="M18 6 6 18M6 6l12 12" />
    </svg>
  )
}

/**
 * A panel that slides over one column and leaves the rest of the window
 * as it was.
 *
 * The B session opens Bundle and Tools this way: they are things a reader
 * consults while reading the note, not destinations, so they cover the
 * path column and nothing else. The drawer is positioned against the
 * nearest `position: relative` ancestor and pinned to its start edge, so
 * it goes to the right in an Arabic pane on its own. Escape closes it and
 * focus goes back where it came from. It is not modal: the document beside
 * it stays live.
 */
export default function Drawer({
  open,
  title,
  meta,
  onClose,
  children,
  width,
  label,
}: DrawerProps): JSX.Element | null {
  const id = useId()
  const closeRef = useRef<HTMLButtonElement | null>(null)
  const returnTo = useRef<HTMLElement | null>(null)

  useEffect(() => {
    if (!open) return
    returnTo.current = (document.activeElement as HTMLElement | null) ?? null
    closeRef.current?.focus()
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== 'Escape') return
      e.preventDefault()
      onClose()
    }
    document.addEventListener('keydown', onKey)
    return () => {
      document.removeEventListener('keydown', onKey)
      const back = returnTo.current
      returnTo.current = null
      if (back && document.contains(back)) back.focus()
    }
  }, [open, onClose])

  if (!open) return null

  return (
    <section
      className="sd-drawer"
      role="dialog"
      aria-modal="false"
      aria-labelledby={`${id}-title`}
      aria-label={label}
      style={width ? ({ '--sd-drawer-w': `${width}px` } as never) : undefined}
    >
      <header className="sd-drawer__head">
        <h2 className="sd-drawer__title" id={`${id}-title`}>
          {title}
        </h2>
        {meta ? <span className="sd-drawer__meta">{meta}</span> : null}
        <button
          ref={closeRef}
          type="button"
          className="sd-drawer__close"
          aria-label={`Close ${title}`}
          title={`Close ${title} (Esc)`}
          onClick={onClose}
        >
          <CloseIcon />
        </button>
      </header>
      <div className="sd-drawer__body">{children}</div>
    </section>
  )
}
