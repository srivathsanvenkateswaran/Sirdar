import { useEffect, useState } from 'react'
import './Toast.css'

export type ToastTone = 'info' | 'error'

export interface ToastItem {
  id: number
  tone: ToastTone
  text: string
  /** A link to where the outcome can be read. It is never the only way there: the place it opens is reachable without it. */
  link?: { label: string }
}

export interface ToastsProps {
  toasts: ToastItem[]
  onDismiss: (id: number) => void
  /** Follows a toast's link; a toast with a link and no handler draws none. */
  onLink?: (id: number) => void
  /** How long a toast stays before it dismisses itself. */
  dismissAfterMs?: number
}

const DEFAULT_DISMISS_MS = 7000

function ToastRow({
  toast,
  onDismiss,
  onLink,
  dismissAfterMs,
}: {
  toast: ToastItem
  onDismiss: (id: number) => void
  onLink?: (id: number) => void
  dismissAfterMs: number
}): JSX.Element {
  // A toast the reader is pointing at or has tabbed into stays put: its link
  // is a real control, and a timer that unmounts it mid-use drops focus to the
  // page (WCAG 2.2.1). Its full time starts again once the reader lets go.
  const [hovered, setHovered] = useState(false)
  const [focused, setFocused] = useState(false)
  const held = hovered || focused

  useEffect(() => {
    if (held) return
    const timer = setTimeout(() => onDismiss(toast.id), dismissAfterMs)
    return () => clearTimeout(timer)
  }, [toast.id, onDismiss, dismissAfterMs, held])

  return (
    <li
      className="sd-toast"
      data-tone={toast.tone}
      onMouseEnter={() => setHovered(true)}
      onMouseLeave={() => setHovered(false)}
      onFocus={() => setFocused(true)}
      onBlur={(e) => {
        // Moving from the link to Dismiss is still inside the toast.
        if (!e.currentTarget.contains(e.relatedTarget as Node | null)) setFocused(false)
      }}
    >
      <span className="sd-toast__text" dir="auto">
        {toast.text}
      </span>
      {toast.link && onLink ? (
        <button type="button" className="sd-toast__link" onClick={() => onLink(toast.id)}>
          {toast.link.label}
        </button>
      ) : null}
      <button
        type="button"
        className="sd-toast__dismiss"
        aria-label="Dismiss"
        onClick={() => onDismiss(toast.id)}
      >
        ×
      </button>
    </li>
  )
}

/**
 * What happened, bottom trailing corner, out of the board's way.
 *
 * A toast reports the outcome of something the reader asked for, so it is a
 * polite live region rather than an alert: it is read after whatever the
 * screen reader is in the middle of, and it never takes focus. The stack is
 * anchored with logical insets, so in an Arabic window it appears in the
 * bottom-left corner, which is where that window's trailing corner is.
 */
export default function Toasts({
  toasts,
  onDismiss,
  onLink,
  dismissAfterMs = DEFAULT_DISMISS_MS,
}: ToastsProps): JSX.Element | null {
  if (toasts.length === 0) return null
  return (
    <ul className="sd-toasts" role="status" aria-live="polite">
      {toasts.map((toast) => (
        <ToastRow
          key={toast.id}
          toast={toast}
          onDismiss={onDismiss}
          onLink={onLink}
          dismissAfterMs={dismissAfterMs}
        />
      ))}
    </ul>
  )
}
