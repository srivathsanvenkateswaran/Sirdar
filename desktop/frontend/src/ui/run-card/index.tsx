import KindChip from '../kind-chip'
import ProviderMark from '../provider-mark'
import StateGlyph, { STATE_WORDS, type GlyphState } from '../state-glyph'
import './RunCard.css'

export interface RunCardProps {
  /** The tracker's key, in the ledger face at the foot. */
  runKey: string
  /** What the run is: triage, rca, fix. */
  kind: string
  /** The CLI's state, or `done` for a key whose RCA is written. */
  status: GlyphState
  /**
   * The ticket's title. Without one the key is the title, drawn once: a card
   * that said the key twice was a card with nothing else to say.
   */
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
  /**
   * The accessible name, when the default of `<key>: <title>, <state>` does
   * not say what the click does.
   */
  label?: string
  /** Opens the session. The card is a button while this is given. */
  onOpen?: () => void
  /**
   * Where the card goes instead of opening a session: the ticket's own page
   * in the tracker, for a queued key with no run yet. The card is a link,
   * opened in the browser, and nothing about the click costs anything. Given
   * both, `onOpen` wins.
   */
  href?: string
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
 *
 * The card is a button when it opens a session, a link when it goes to the
 * tracker, and a plain box when it does neither; the anatomy inside is the
 * same in all three.
 */
export default function RunCard({
  runKey,
  kind,
  status,
  title,
  provider,
  clock,
  clockTitle,
  label,
  onOpen,
  href,
}: RunCardProps): JSX.Element {
  const live = status === 'preparing' || status === 'running'
  const heading = title || runKey
  const showClock = Boolean(clock) && CLOCKED.includes(status)
  const word = STATE_WORDS[status] ?? status
  const name = label ?? (title ? `${runKey}: ${title}, ${word}` : `${runKey}, ${word}`)

  const body = (
    <>
      <span className="sd-run-card__title" dir="auto">
        {heading}
      </span>

      <span className="sd-run-card__kind">
        <KindChip kind={kind} />
      </span>

      <span className="sd-run-card__foot">
        <span className="sd-run-card__state" title={showClock ? clockTitle : undefined}>
          <StateGlyph state={status} clock={showClock ? clock : undefined} />
        </span>
        <span className="sd-run-card__who">
          {title && (
            <span className="sd-run-card__key" dir="ltr">
              {runKey}
            </span>
          )}
          <ProviderMark provider={provider} size="sm" />
        </span>
      </span>
    </>
  )

  const shared = {
    className: 'sd-run-card',
    'data-status': status,
    'data-live': live ? 'true' : undefined,
    'aria-label': name,
  }

  if (onOpen) {
    return (
      <button type="button" {...shared} onClick={onOpen}>
        {body}
      </button>
    )
  }
  if (href) {
    return (
      <a {...shared} href={href} target="_blank" rel="noreferrer noopener">
        {body}
      </a>
    )
  }
  return <div {...shared}>{body}</div>
}
