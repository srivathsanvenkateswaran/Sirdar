import type { ButtonHTMLAttributes, ReactNode } from 'react'
import './Button.css'

import '../motion'

export type ButtonVariant = 'primary' | 'secondary' | 'ghost'

export interface ButtonProps extends Omit<ButtonHTMLAttributes<HTMLButtonElement>, 'className'> {
  variant?: ButtonVariant
  /** Shown in place of the label while the action this button started runs. */
  busy?: boolean
  /** A shortcut hint, drawn as a kbd inside the button. */
  shortcut?: string
  children: ReactNode
}

/**
 * The one control that does something.
 *
 * Three variants, and the difference between them is a promise rather than a
 * decoration: only `primary` carries the hard offset shadow, and only a
 * primary button is allowed to change anything — start a run, apply a fix,
 * edit a workspace. Everything read-only is flat. A reader can tell what a
 * click can change without reading the label, which is the point of section 3
 * of the design language.
 *
 * The shadow is a printed offset, not a float: no blur, and on press the
 * button travels a pixel into the page and the shadow shrinks to match. Under
 * `[dir="rtl"]` the offset flips to the other side so the light still comes
 * from the leading edge; that flip is the one documented exception to the
 * library's logical-properties rule and it lives in `styles/tokens.css`.
 */
export default function Button({
  variant = 'secondary',
  busy = false,
  shortcut,
  children,
  type = 'button',
  disabled,
  ...rest
}: ButtonProps): JSX.Element {
  return (
    <button
      {...rest}
      type={type}
      className="sd-button"
      data-variant={variant}
      data-busy={busy ? 'true' : undefined}
      // A busy button is still focusable and still announces itself; it just
      // refuses a second click. `disabled` would move focus off it mid-action.
      aria-disabled={busy ? true : undefined}
      disabled={disabled}
      onClick={busy ? undefined : rest.onClick}
    >
      <span className="sd-button__label">{children}</span>
      {shortcut && (
        <kbd className="sd-button__kbd" aria-hidden="true">
          {shortcut}
        </kbd>
      )}
    </button>
  )
}
