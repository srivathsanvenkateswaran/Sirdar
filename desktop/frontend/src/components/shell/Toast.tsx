import { useEffect } from 'react'
import type { Toast } from '../../store/appStore'

const DISMISS_AFTER_MS = 7000

function ToastRow(props: { toast: Toast; onDismiss: (id: number) => void }): JSX.Element {
  const { toast, onDismiss } = props

  useEffect(() => {
    const id = setTimeout(() => onDismiss(toast.id), DISMISS_AFTER_MS)
    return () => clearTimeout(id)
  }, [toast.id, onDismiss])

  return (
    <li className="toast" data-tone={toast.tone}>
      <span className="toast-text">{toast.text}</span>
      <button
        type="button"
        className="toast-dismiss"
        aria-label="Dismiss"
        onClick={() => onDismiss(toast.id)}
      >
        ×
      </button>
    </li>
  )
}

/** Job outcomes and failures, bottom right, out of the board's way. */
export default function Toasts(props: {
  toasts: Toast[]
  onDismiss: (id: number) => void
}): JSX.Element | null {
  const { toasts, onDismiss } = props
  if (toasts.length === 0) return null
  return (
    <ul className="toasts" role="status" aria-live="polite">
      {toasts.map((toast) => (
        <ToastRow key={toast.id} toast={toast} onDismiss={onDismiss} />
      ))}
    </ul>
  )
}
