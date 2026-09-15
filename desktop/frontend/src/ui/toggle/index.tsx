import './Toggle.css'

export interface ToggleProps {
  checked: boolean
  onChange: (checked: boolean) => void
  /** The accessible name; the setting row beside it carries the visible one. */
  label: string
  disabled?: boolean
  /** Ties the switch to its visible label by id, when there is one. */
  labelledBy?: string
}

/**
 * A switch: on or off, and nothing in between.
 *
 * 44 by 24, the knob travelling 20px along the logical axis so it goes the
 * other way in an Arabic pane without a mirrored transform. The track is the
 * ink when on and the strong rule when off, which is the same pair the
 * primary button uses, because a switch that is on is a commitment.
 */
export default function Toggle({
  checked,
  onChange,
  label,
  disabled = false,
  labelledBy,
}: ToggleProps): JSX.Element {
  return (
    <button
      type="button"
      role="switch"
      className="sd-toggle"
      aria-checked={checked}
      aria-label={labelledBy ? undefined : label}
      aria-labelledby={labelledBy}
      disabled={disabled}
      onClick={() => onChange(!checked)}
    >
      <span className="sd-toggle__knob" aria-hidden="true" />
    </button>
  )
}
