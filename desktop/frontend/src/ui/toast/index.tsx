import { useEffect } from 'react'
import './Toast.css'

export type ToastTone = 'info' | 'error'

export interface ToastItem {
  id: number
  tone: ToastTone
  text: string
}

export interface ToastsProps {
  toasts: ToastItem[]
  onDismiss: (id: number) => void
  /** How long a toast stays before it dismisses itself. */
  dismissAfterMs?: number
}

const DEFAULT_DISMISS_MS = 7000

function ToastRow({
  toast,
  onDismiss,
  dismissAfterMs,
}: {
  toast: ToastItem
  onDismiss: (id: number) => void
  dismissAfterMs: number
}): JSX.Element {
  useEffect(() => {
    const timer = setTimeout(() => onDismiss(toast.id), dismissAfterMs)
    return () => clearTimeout(timer)
  }, [toast.id, onDismiss, dismissAfterMs])

  return (
    <li className="sd-toast" data-tone={toast.tone}>
      <span className="sd-toast__text" dir="auto">
        {toast.text}
      </span>
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
          dismissAfterMs={dismissAfterMs}
        />
      ))}
    </ul>
  )
}
