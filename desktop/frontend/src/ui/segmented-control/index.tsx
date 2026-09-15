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
  /**
   * Options that cannot be chosen right now, each with the reason in words.
   * The reason is the option's `title`; the screen says it in prose as well.
   */
  disabledOptions?: Record<string, string>
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
  disabledOptions = {},
}: SegmentedControlProps): JSX.Element {
  const group = useRef<HTMLDivElement | null>(null)
  const name = useId()
  const index = Math.max(
    options.findIndex((option) => option.id === value),
    0,
  )

  const isOff = (id: string): boolean => disabled || id in disabledOptions

  /**
   * Moves the choice to `to`, or on past it when that option is off: an
   * arrow key walks the enabled options and wraps, and a run of disabled
   * ones in the middle is stepped over rather than stopping the reader.
   */
  function move(to: number, step: 1 | -1): void {
    if (disabled) return
    for (let n = 0; n < options.length; n += 1) {
      const at = (((to + n * step) % options.length) + options.length) % options.length
      const next = options[at]
      if (!next || isOff(next.id)) continue
      if (at === index) return
      onChange(next.id)
      const buttons = group.current?.querySelectorAll<HTMLButtonElement>('.sd-segmented__option')
      buttons?.[at]?.focus()
      return
    }
  }

  function onKeyDown(event: KeyboardEvent<HTMLDivElement>): void {
    const rtl = directionOf(group.current) === 'rtl'
    switch (event.key) {
      case 'ArrowRight':
        event.preventDefault()
        rtl ? move(index - 1, -1) : move(index + 1, 1)
        break
      case 'ArrowLeft':
        event.preventDefault()
        rtl ? move(index + 1, 1) : move(index - 1, -1)
        break
      case 'ArrowDown':
        event.preventDefault()
        move(index + 1, 1)
        break
      case 'ArrowUp':
        event.preventDefault()
        move(index - 1, -1)
        break
      case 'Home':
        event.preventDefault()
        move(0, 1)
        break
      case 'End':
        event.preventDefault()
        move(options.length - 1, -1)
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
          disabled={isOff(option.id)}
          title={disabledOptions[option.id]}
          onClick={() => onChange(option.id)}
        >
          {option.label}
        </button>
      ))}
    </div>
  )
}
