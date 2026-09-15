import { useSyncExternalStore, type ReactNode } from 'react'
import { noteDir, subscribePreferRTL } from '../../lib/rtl'
import './NotePane.css'

export interface NotePaneProps {
  /** The note's own heading, set in the display serif. */
  title?: ReactNode
  /** Where the note came from: a path, a key, a timestamp. Ledger face. */
  source?: string
  children: ReactNode
  /**
   * Overrides the reader's stored preference. The gallery passes it to show
   * both layouts side by side; the app does not pass it at all.
   */
  dir?: 'auto' | 'rtl' | 'ltr'
}

/**
 * The note, as prose.
 *
 * Sirdar's notes are bilingual by default: an English body, the customer's own
 * Arabic complaint, and an Arabic reply draft. `dir="auto"` resolves each
 * block from its own first strong character, which handles the mixture
 * paragraph by paragraph; an engineer who reads mostly Arabic can lay the
 * whole pane out right to left instead, and that preference is what
 * `lib/rtl.ts` holds.
 *
 * This is the only place the display serif appears inside the app. A note is
 * the one screen a person reads rather than scans, and the serif is what says
 * so — it is also the one component here that carries the marker sweep, the
 * highlighter-pen wipe under a hovered link.
 */
export default function NotePane({ title, source, children, dir }: NotePaneProps): JSX.Element {
  const preferred = useSyncExternalStore(subscribePreferRTL, noteDir, () => 'auto' as const)
  return (
    <article className="sd-note" dir={dir ?? preferred}>
      {(title || source) && (
        <header className="sd-note__head">
          {title && <h1 className="sd-note__title">{title}</h1>}
          {source && (
            <p className="sd-note__source" dir="ltr">
              {source}
            </p>
          )}
        </header>
      )}
      <div className="sd-note__body">{children}</div>
    </article>
  )
}
