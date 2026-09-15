import { useId, type FormEvent, type InputHTMLAttributes, type ReactNode } from 'react'
import './SearchBar.css'

export type SearchBarVariant = 'bar' | 'well'

export interface SearchBarProps
  extends Omit<
    InputHTMLAttributes<HTMLInputElement>,
    'className' | 'onChange' | 'onSubmit' | 'value' | 'size'
  > {
  value: string
  onChange: (value: string) => void
  /** Names the field for a screen reader; the placeholder is not a name. */
  label: string
  /** `bar` is the 64-tall search-style field; `well` is the 40-tall sunk one. */
  variant?: SearchBarVariant
  /** At the inline end: a hint, a shortcut, a count. */
  aside?: ReactNode
  /** Enter submits, when the field starts something. */
  onSubmit?: (value: string) => void
}

/** lucide `search` at stroke 1.5. */
function SearchIcon(): JSX.Element {
  return (
    <svg
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.5"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
      focusable="false"
    >
      <circle cx="11" cy="11" r="7" />
      <path d="m20 20-3.5-3.5" />
    </svg>
  )
}

/**
 * A search-style field.
 *
 * Two sizes of the same thing. The bar is where a session starts: 64 tall
 * with a 16px corner, on the sheet, holding a ticket key or a URL. The well
 * is the board's filter: 40 tall, sunk, and the width of a column. Both are
 * one input with a magnifier before it and room for a hint after it.
 */
export default function SearchBar({
  value,
  onChange,
  label,
  variant = 'bar',
  aside,
  onSubmit,
  placeholder,
  ...rest
}: SearchBarProps): JSX.Element {
  const id = useId()

  function submit(event: FormEvent<HTMLFormElement>): void {
    event.preventDefault()
    onSubmit?.(value)
  }

  return (
    <form className="sd-search" data-variant={variant} role="search" onSubmit={submit}>
      <label className="sd-search__label" htmlFor={id}>
        {label}
      </label>
      <span className="sd-search__icon">
        <SearchIcon />
      </span>
      <input
        {...rest}
        id={id}
        type="search"
        className="sd-search__input"
        value={value}
        placeholder={placeholder}
        onChange={(event) => onChange(event.target.value)}
      />
      {aside && <span className="sd-search__aside">{aside}</span>}
    </form>
  )
}
