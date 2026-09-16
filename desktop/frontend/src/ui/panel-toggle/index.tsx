import './PanelToggle.css'

export interface PanelToggleProps {
  /** Whether the pane it controls is showing. */
  open: boolean
  onToggle: () => void
  /**
   * Which edge of the window the pane sits at, read in the writing
   * direction: `start` draws the panel-left glyph (the sidebar), `end` the
   * panel-right one (a session's artefacts pane). Mirrored under RTL.
   */
  side: 'start' | 'end'
  /** The button's name while the pane is open: "Hide panel", "Hide sidebar". */
  hideLabel: string
  /** Its name while the pane is hidden: "Show panel", "Show sidebar". */
  showLabel: string
  /** The shortcut as it is drawn, `⌘\` or `⌘B`; goes into the tooltip and `aria-keyshortcuts`. */
  shortcut?: string
  /** The pane's id, for `aria-controls`. */
  controls?: string
  /** A pane the window has folded away by itself, at a width where the choice is not the reader's. */
  disabled?: boolean
  /** Why it is disabled, as the tooltip in place of the label. */
  disabledReason?: string
}

/** `⌘B` as `aria-keyshortcuts` spells it: `Meta+B`. */
export function keyShortcut(drawn: string): string {
  return drawn
    .replace('⌘', 'Meta+')
    .replace('⇧', 'Shift+')
    .replace('⌥', 'Alt+')
    .replace('⌃', 'Control+')
}

/**
 * The 28px ghost button that folds a pane away and brings it back.
 *
 * It is a toggle, so it carries `aria-expanded` for the pane it controls
 * rather than `aria-pressed`: what a screen reader wants to know is whether
 * the pane is there. The glyph does not change with the state — the label
 * does, and the pane itself is the evidence — because a glyph that flips
 * reads as "the pane is on the other side" rather than "the pane is gone".
 */
export default function PanelToggle({
  open,
  onToggle,
  side,
  hideLabel,
  showLabel,
  shortcut,
  controls,
  disabled = false,
  disabledReason,
}: PanelToggleProps): JSX.Element {
  const name = open ? hideLabel : showLabel
  const title = disabled && disabledReason ? disabledReason : shortcut ? `${name} (${shortcut})` : name
  return (
    <button
      type="button"
      className="sd-panel-toggle"
      aria-expanded={open}
      aria-controls={controls}
      aria-label={name}
      aria-keyshortcuts={shortcut ? keyShortcut(shortcut) : undefined}
      title={title}
      disabled={disabled}
      data-side={side}
      onClick={onToggle}
    >
      <svg
        viewBox="0 0 24 24"
        fill="none"
        stroke="currentColor"
        strokeWidth="1.6"
        strokeLinecap="round"
        strokeLinejoin="round"
        aria-hidden="true"
        focusable="false"
      >
        <rect width="18" height="18" x="3" y="3" rx="2" />
        <path d={side === 'start' ? 'M9 3v18' : 'M15 3v18'} />
      </svg>
    </button>
  )
}
