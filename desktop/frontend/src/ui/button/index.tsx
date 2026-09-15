import type { ButtonHTMLAttributes, ReactNode } from 'react'
import './Button.css'

import '../motion'

export type ButtonVariant = 'primary' | 'secondary' | 'ghost' | 'pale'
export type ButtonSize = 'sm' | 'md' | 'lg'

export interface ButtonProps extends Omit<ButtonHTMLAttributes<HTMLButtonElement>, 'className'> {
  variant?: ButtonVariant
  /** 32, 40 (the control height) or 48 tall. */
  size?: ButtonSize
  /** A 16px icon before the label. */
  icon?: ReactNode
  /** Shown in place of the label while the action this button started runs. */
  busy?: boolean
  /** A shortcut hint, drawn as a kbd inside the button. */
  shortcut?: string
  /** The label, or the accessible name of an icon-only button. */
  children: ReactNode
  /** Draw only the icon, square; `children` becomes the accessible name. */
  iconOnly?: boolean
}

/**
 * The one control that does something.
 *
 * Four variants, and the difference between them is a promise rather than a
 * decoration: only `primary` is filled with the ink, and only a primary
 * button is allowed to change anything — start a run, apply a fix, edit a
 * workspace. `secondary` is the bordered surface button, `pale` the quiet
 * filled one on a setting row, `ghost` has no box at all. A reader can tell
 * what a click can change without reading the label, which is the point of
 * section 3 of the design language.
 *
 * Nothing casts a shadow. The 2026-09-15 screens round retired the printed
 * offset the primary used to carry; the ink fill, inverted per theme, is
 * what marks the one control on a screen that acts.
 */
export default function Button({
  variant = 'secondary',
  size = 'md',
  icon,
  busy = false,
  shortcut,
  children,
  iconOnly = false,
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
      data-size={size === 'md' ? undefined : size}
      data-icon-only={iconOnly ? 'true' : undefined}
      data-busy={busy ? 'true' : undefined}
      // A busy button is still focusable and still announces itself; it just
      // refuses a second click. `disabled` would move focus off it mid-action.
      aria-disabled={busy ? true : undefined}
      aria-label={iconOnly && typeof children === 'string' ? children : rest['aria-label']}
      disabled={disabled}
      onClick={busy ? undefined : rest.onClick}
    >
      {icon}
      {!iconOnly && <span className="sd-button__label">{children}</span>}
      {!iconOnly && shortcut && (
        <kbd className="sd-button__kbd" aria-hidden="true">
          {shortcut}
        </kbd>
      )}
    </button>
  )
}
