import './StatusBadge.css'

/**
 * The run states a screen can name: the CLI's state machine, plus `done`,
 * which the board and the eval page use for a key whose work is finished
 * beyond the run itself (an RCA written, a suite scored).
 */
export type SdStatus =
  | 'queued'
  | 'preparing'
  | 'running'
  | 'blocked'
  | 'completed'
  | 'done'
  | 'failed'
  | 'over_budget'

/**
 * The one word each state is shown by, everywhere: the badge here, the state
 * glyph in a card footer, a row's tooltip, a screen reader's live region.
 * Lowercase, because it sits inside sentences and card footers as often as
 * on its own, and the mocks spell it that way on every screen.
 *
 * Status is never colour alone, so this map is part of the component rather
 * than a label beside it: the badge cannot be rendered without its word. The
 * state glyph (`src/ui/state-glyph`) reads the same map, so the two can never
 * disagree about what a state is called.
 */
export const STATE_WORDS: Record<SdStatus, string> = {
  queued: 'queued',
  preparing: 'preparing',
  running: 'running',
  blocked: 'blocked',
  completed: 'completed',
  done: 'done',
  failed: 'failed',
  over_budget: 'over budget',
}

/**
 * The same map under the name the Library page still imports it by. New code
 * reads `STATE_WORDS`; this alias goes when that page switches.
 */
export const STATUS_WORDS = STATE_WORDS

/** True for a state the map knows, so a string off the wire can be shown by its word. */
export function isSdStatus(status: string): status is SdStatus {
  return Object.prototype.hasOwnProperty.call(STATE_WORDS, status)
}

/** The word for a status, or the status itself for one the map does not know. */
export function stateWord(status: string): string {
  return isSdStatus(status) ? STATE_WORDS[status] : status
}

export interface StatusBadgeProps {
  status: SdStatus
  /**
   * What the state means right here, after the word: the session topbar
   * says `blocked · waiting on you` because on that screen the reader is the
   * one being waited on. The word itself stays.
   */
  detail?: string
  /**
   * A translation of the word, and nothing else. A state that wants another
   * English word is a state missing from `STATE_WORDS`, not an override.
   */
  children?: string
}

/**
 * A run's state, in the hue its board column uses.
 *
 * Inside a lane the badge takes the lane's hue from the column rail, so the
 * rail at the top of a column and every badge under it are one signal and the
 * rail doubles as the legend. Outside a lane it takes the hue its own state
 * names.
 */
export default function StatusBadge({ status, detail, children }: StatusBadgeProps): JSX.Element {
  const word = children ?? STATE_WORDS[status] ?? status
  return (
    <span className="sd-badge" data-status={status}>
      {detail ? `${word} · ${detail}` : word}
    </span>
  )
}

/**
 * The tracker's own priority string, shown verbatim.
 *
 * Trackers disagree about whether the top priority is `P1`, `High` or
 * `Blocker`, and inventing a mapping would hide what the ticket actually says.
 * Only the two that every tracker treats as urgent get a hue.
 */
export function PriorityBadge({ priority }: { priority: string }): JSX.Element | null {
  const text = priority?.trim()
  if (!text) return null
  return (
    <span className="sd-badge sd-badge--priority" data-priority={text.toLowerCase()}>
      {text}
    </span>
  )
}
