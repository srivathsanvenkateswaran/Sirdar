import { useEffect, useId, useRef, type ReactNode } from 'react'
import './Dialog.css'

export interface DialogProps {
  open: boolean
  title: string
  /** Called on Escape, on a scrim click, and by the dialog's own close button. */
  onClose: () => void
  children: ReactNode
  /** The actions row. The one that commits is the primary button. */
  actions?: ReactNode
}

/**
 * Everything inside a modal that can take focus, in DOM order.
 *
 * Exported because the Modal sheet is this component with a secondary nav down
 * its leading edge, and a second copy of this selector is a second chance to
 * forget `:not(:disabled)`.
 */
export function focusable(root: HTMLElement): HTMLElement[] {
  return [
    ...root.querySelectorAll<HTMLElement>(
      'a[href], button:not(:disabled), input:not(:disabled), select:not(:disabled), textarea:not(:disabled), [tabindex]:not([tabindex="-1"])',
    ),
  ]
}

/**
 * A modal question: start a run, add a workspace, confirm a fix.
 *
 * Focus moves into the dialog when it opens, stays inside it while it is
 * there, and returns to whatever opened it when it closes. That last part is
 * the one most often skipped and the one a keyboard reader notices first:
 * without it, dismissing a dialog drops focus on the document body and the
 * next Tab starts again from the address bar.
 *
 * The scrim is 32% ink rather than a blur. A blur costs a repaint of the whole
 * board behind it on every frame, and the board can have a live run on it.
 */
export default function Dialog({
  open,
  title,
  onClose,
  children,
  actions,
}: DialogProps): JSX.Element | null {
  const panel = useRef<HTMLDivElement | null>(null)
  const opener = useRef<HTMLElement | null>(null)
  const titleId = useId()

  useEffect(() => {
    if (!open) return
    opener.current = document.activeElement as HTMLElement | null
    const first = panel.current ? focusable(panel.current)[0] : null
    ;(first ?? panel.current)?.focus()
    return () => {
      // Back to the button that asked the question.
      opener.current?.focus?.()
    }
  }, [open])

  useEffect(() => {
    if (!open) return
    function onKeyDown(event: KeyboardEvent): void {
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

  if (!open) return null

  return (
    <div
      className="sd-scrim"
      onMouseDown={(event) => {
        if (event.target === event.currentTarget) onClose()
      }}
    >
      <div
        ref={panel}
        className="sd-dialog"
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        tabIndex={-1}
      >
        <h2 className="sd-dialog__title" id={titleId}>
          {title}
        </h2>
        <div className="sd-dialog__body">{children}</div>
        {actions && <div className="sd-dialog__actions">{actions}</div>}
      </div>
    </div>
  )
}
