import { useId, useRef, type KeyboardEvent } from 'react'
import './SegmentedControl.css'

export interface SegmentedOption {
  id: string
  label: string
}

export interface SegmentedControlProps {
  options: SegmentedOption[]
  value: string
  onChange: (id: string) => void
  /** Names the group for a screen reader. */
  label: string
  disabled?: boolean
}

/** The reading direction the control is laid out in, read from the DOM. */
function directionOf(node: HTMLElement | null): 'ltr' | 'rtl' {
  const carrier = node?.closest('[dir]')
  const dir = carrier?.getAttribute('dir')
  if (dir === 'rtl' || dir === 'ltr') return dir
  return typeof document !== 'undefined' && document.dir === 'rtl' ? 'rtl' : 'ltr'
}

/**
 * Two to four options, one of which is true.
 *
 * It is a radio group wearing one track: a single sunk well at pill radius
 * with a surface thumb that slides to the chosen option. The thumb is the only
 * thing that moves, over `--sd-dur-2`, and under reduced motion it jumps —
 * which is the whole reduced-motion answer, because nothing here depends on
 * the slide finishing.
 *
 * Keyboard follows the radio-group pattern rather than the tab-list one: one
 * tab stop for the group, and the arrows move the choice. The arrows are read
 * in the reader's own direction, so in an Arabic pane the right arrow moves to
 * the previous option, which is the one to its right.
 */
export default function SegmentedControl({
  options,
  value,
  onChange,
  label,
  disabled = false,
}: SegmentedControlProps): JSX.Element {
  const group = useRef<HTMLDivElement | null>(null)
  const name = useId()
  const index = Math.max(
    options.findIndex((option) => option.id === value),
    0,
  )

  function move(to: number): void {
    const next = options[(to + options.length) % options.length]
    if (!next || disabled) return
    onChange(next.id)
    const buttons = group.current?.querySelectorAll<HTMLButtonElement>('.sd-segmented__option')
    buttons?.[(to + options.length) % options.length]?.focus()
  }

  function onKeyDown(event: KeyboardEvent<HTMLDivElement>): void {
    const rtl = directionOf(group.current) === 'rtl'
    switch (event.key) {
      case 'ArrowRight':
        event.preventDefault()
        move(rtl ? index - 1 : index + 1)
        break
      case 'ArrowLeft':
        event.preventDefault()
        move(rtl ? index + 1 : index - 1)
        break
      case 'ArrowDown':
        event.preventDefault()
        move(index + 1)
        break
      case 'ArrowUp':
        event.preventDefault()
        move(index - 1)
        break
      case 'Home':
        event.preventDefault()
        move(0)
        break
      case 'End':
        event.preventDefault()
        move(options.length - 1)
        break
      default:
    }
  }

  return (
    <div
      ref={group}
      role="radiogroup"
      aria-label={label}
      aria-disabled={disabled ? true : undefined}
      className="sd-segmented"
      data-disabled={disabled ? 'true' : undefined}
      style={{ '--sd-segmented-count': options.length, '--sd-segmented-index': index } as never}
      onKeyDown={onKeyDown}
    >
      <span className="sd-segmented__thumb" aria-hidden="true" />
      {options.map((option, i) => (
        <button
          key={option.id}
          type="button"
          role="radio"
          id={`${name}-${option.id}`}
          className="sd-segmented__option"
          aria-checked={option.id === value}
          // Roving tabindex: the group is one stop, the arrows do the rest.
          tabIndex={i === index ? 0 : -1}
          disabled={disabled}
          onClick={() => onChange(option.id)}
        >
          {option.label}
        </button>
      ))}
    </div>
  )
}
