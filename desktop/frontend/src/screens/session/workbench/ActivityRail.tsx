import type { JSX } from 'react'
import { isRailCell, type RailItem } from './model'

const RAIL_WORDS: Record<string, string> = {
  tool: 'tool calls',
  deny: 'a call was denied',
  final: 'answered',
  write: 'wrote a file',
  ask: 'asked a question',
  think: 'thought',
  sys: 'housekeeping',
}

/**
 * The 56px activity rail: the run's turns as a clickable minimap, one cell
 * per turn with a square coloured by what happened in it — the accent for
 * an answer, the blocked hue for a denial or a question, a heat step for a
 * write — and a dashed rule where the operator steered or reviewed. The
 * colour is never alone: the cell's title says the same in words.
 *
 * Clicking a cell scrolls the console to that turn's first row; the current
 * cells are the ones whose rows the console has in view.
 */
export default function ActivityRail({
  items,
  current,
  onPick,
}: {
  items: RailItem[]
  /** The keys of the cells in view. */
  current: ReadonlySet<string>
  onPick: (key: string) => void
}): JSX.Element {
  return (
    <nav className="wb-rail" aria-label="Turns">
      <div className="wb-rail__label">turns</div>
      {items.map((item) =>
        isRailCell(item) ? (
          <button
            key={item.key}
            type="button"
            className="wb-rt"
            data-k={item.kind}
            data-cur={current.has(item.key) ? 'true' : undefined}
            title={`turn ${item.n} · ${RAIL_WORDS[item.kind] ?? item.kind}`}
            aria-label={`Turn ${item.n}, ${RAIL_WORDS[item.kind] ?? item.kind}`}
            aria-current={current.has(item.key) ? 'true' : undefined}
            onClick={() => onPick(item.key)}
          >
            <span>{item.n}</span>
            <i aria-hidden="true" />
          </button>
        ) : (
          <div key={item.key} className="wb-rt wb-rt--sep" role="separator" aria-label={item.sep}>
            {item.sep}
          </div>
        ),
      )}
    </nav>
  )
}
