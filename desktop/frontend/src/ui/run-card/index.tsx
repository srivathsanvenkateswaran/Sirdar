import KindChip from '../kind-chip'
import ProviderMark from '../provider-mark'
import StateGlyph, { type GlyphState } from '../state-glyph'
import './RunCard.css'

export interface RunCardProps {
  /** The tracker's key, in the ledger face at the foot. */
  runKey: string
  /** What the run is: triage, rca, fix. */
  kind: string
  /** The CLI's state, or `done` for a key whose RCA is written. */
  status: GlyphState
  /** The ticket's title. Falls back to the key when the tracker has none. */
  title?: string
  /** The provider running it; drawn as the card's avatar. */
  provider: string
  /**
   * Already formatted, e.g. `4:12`. Drawn only while the run is live or
   * blocked — the two states where a clock is a fact about now — whatever the
   * caller hands over otherwise. This component does no arithmetic on a clock.
   */
  clock?: string
  /** The clock's tooltip: what it is counting. */
  clockTitle?: string
  onOpen: () => void
}

const CLOCKED: GlyphState[] = ['preparing', 'running', 'blocked']

/**
 * A run, as the board shows it: the Jira-shaped card from the 2026-09-15
 * screens round.
 *
 * The title first, up to two lines, with no key above it: a board is read by
 * title and the key is looked up second, so the key sits at the foot in the
 * ledger face beside the provider's mark. Under the title a kind chip; at the
 * foot the state's glyph and word, with a clock only while the run is live
 * or waiting on a person. No reason and no cost: those are the session's, and
 * a card that quoted them was a second session screen at 240 wide.
 *
 * The title carries `dir="auto"` because it can be the customer's own Arabic
 * inside an English board.
 */
export default function RunCard({
  runKey,
  kind,
  status,
  title,
  provider,
  clock,
  clockTitle,
  onOpen,
}: RunCardProps): JSX.Element {
  const live = status === 'preparing' || status === 'running'
  const heading = title || runKey
  const showClock = Boolean(clock) && CLOCKED.includes(status)

  return (
    <button
      type="button"
      className="sd-run-card"
      data-status={status}
      data-live={live ? 'true' : undefined}
      aria-label={`${runKey}: ${heading}`}
      onClick={onOpen}
    >
      <span className="sd-run-card__title" dir="auto">
        {heading}
      </span>

      <span className="sd-run-card__kind">
        <KindChip kind={kind} />
      </span>

      <span className="sd-run-card__foot">
        <span title={showClock ? clockTitle : undefined}>
          <StateGlyph state={status} clock={showClock ? clock : undefined} />
        </span>
        <span className="sd-run-card__who">
          <span className="sd-run-card__key" dir="ltr">
            {runKey}
          </span>
          <ProviderMark provider={provider} size="sm" />
        </span>
      </span>
    </button>
  )
}
