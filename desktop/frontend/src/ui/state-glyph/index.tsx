import { STATE_WORDS, type SdStatus } from '../status-badge'
import './StateGlyph.css'

/**
 * The states a glyph exists for: the same set the status badge names, so
 * the two never disagree about what a state is called.
 */
export type GlyphState = SdStatus

/** The word each state is shown by; the status badge's map, re-exported so a card footer and a pill read alike. */
export { STATE_WORDS }

/** Which of the five drawings a state gets. */
function glyphOf(state: GlyphState): 'queued' | 'running' | 'blocked' | 'check' | 'failed' {
  switch (state) {
    case 'queued':
      return 'queued'
    case 'preparing':
    case 'running':
      return 'running'
    case 'blocked':
      return 'blocked'
    case 'completed':
    case 'done':
      return 'check'
    default:
      return 'failed'
  }
}

export interface StateGlyphProps {
  state: GlyphState
  /** A translation of the word, and nothing else; another English word is a state missing from `STATE_WORDS`. */
  word?: string
  /**
   * Already formatted, e.g. `04:12`. Shown in the ledger face beside the word.
   * The caller decides when a clock is wanted: the board shows one on a live
   * or blocked run and on nothing else.
   */
  clock?: string
}

/**
 * A run's state as a 16px outline glyph and its word, in the state's hue.
 *
 * Five drawings for eight states: a dashed ring for queued, a play mark for
 * running, a question mark for blocked, a check for completed and done, a
 * cross for failed and over budget. The word is always beside it, so the
 * glyph is a second copy of the fact and never the only one.
 */
export default function StateGlyph({ state, word, clock }: StateGlyphProps): JSX.Element {
  const glyph = glyphOf(state)
  return (
    <span className="sd-state" data-state={state} data-glyph={glyph}>
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
        {glyph === 'queued' ? (
          <circle cx="12" cy="12" r="9" strokeDasharray="3.6 3" />
        ) : (
          <circle cx="12" cy="12" r="9" />
        )}
        {glyph === 'running' && <path d="M10 8.5v7l5.5-3.5z" />}
        {glyph === 'blocked' && (
          <>
            <path d="M9.5 9.5a2.5 2.5 0 1 1 3.6 2.2c-.8.4-1.1.9-1.1 1.6" />
            <path d="M12 16.5h.01" />
          </>
        )}
        {glyph === 'check' && <path d="m8.5 12.2 2.4 2.4 4.8-4.8" />}
        {glyph === 'failed' && <path d="m9 9 6 6M15 9l-6 6" />}
      </svg>
      <span className="sd-state__word">{word ?? STATE_WORDS[state] ?? state}</span>
      {clock && (
        <span className="sd-state__clock" dir="ltr">
          {clock}
        </span>
      )}
    </span>
  )
}
