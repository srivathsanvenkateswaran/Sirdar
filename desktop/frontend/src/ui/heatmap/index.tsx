import './Heatmap.css'

export interface HeatmapDay {
  /** `YYYY-MM-DD`, in the reader's own timezone. */
  date: string
  count: number
}

export interface HeatmapProps {
  days: HeatmapDay[]
  /** How many weeks of columns to draw. The grid scrolls past the container. */
  weeks?: number
  /** The last day in the grid. Defaults to the latest day it was given. */
  endDate?: string
  /** Names the grid, and says what a cell counts. */
  label?: string
  /** Called with the day's date when a cell is chosen. */
  onSelect?: (date: string) => void
}

/** The five buckets, in order. Index is the `--sd-heat-N` step. */
export const BUCKETS = [
  { from: 0, to: 0, label: '0' },
  { from: 1, to: 1, label: '1' },
  { from: 2, to: 4, label: '2-4' },
  { from: 5, to: 9, label: '5-9' },
  { from: 10, to: Infinity, label: '10+' },
] as const

/** Which of the five steps a count falls in. */
export function bucketOf(count: number): number {
  if (!Number.isFinite(count) || count <= 0) return 0
  if (count === 1) return 1
  if (count <= 4) return 2
  if (count <= 9) return 3
  return 4
}

const MONTHS = [
  'January',
  'February',
  'March',
  'April',
  'May',
  'June',
  'July',
  'August',
  'September',
  'October',
  'November',
  'December',
]

const WEEKDAYS = ['Sunday', 'Monday', 'Tuesday', 'Wednesday', 'Thursday', 'Friday', 'Saturday']

/** A date-only key, built without a Date so a timezone cannot shift a day. */
function key(y: number, m: number, d: number): string {
  return `${y}-${String(m + 1).padStart(2, '0')}-${String(d).padStart(2, '0')}`
}

/** "14 September" from a `YYYY-MM-DD` key. Numbers stay as written. */
export function dayName(date: string): string {
  const [y, m, d] = date.split('-').map(Number)
  if (!y || !m || !d) return date
  return `${d} ${MONTHS[m - 1] ?? m}`
}

/** The accessible name of one cell: "14 September, 6 runs". */
export function cellName(date: string, count: number): string {
  return `${dayName(date)}, ${count} ${count === 1 ? 'run' : 'runs'}`
}

/**
 * The grid's days, oldest first, ending on `end` and running back far enough
 * to fill `weeks` columns from the start of that week.
 */
function grid(days: HeatmapDay[], weeks: number, end?: string): { date: string; count: number }[] {
  const counts = new Map(days.map((d) => [d.date, d.count]))
  const last = end ?? days.map((d) => d.date).sort().at(-1) ?? ''
  const [y, m, d] = last ? last.split('-').map(Number) : []
  const anchor = y ? new Date(Date.UTC(y, m - 1, d)) : new Date()
  // The grid ends on the last day of the week the anchor falls in, so the
  // trailing column is a whole week and the weekday rows stay square.
  anchor.setUTCDate(anchor.getUTCDate() + (6 - anchor.getUTCDay()))
  const total = weeks * 7
  const out: { date: string; count: number }[] = []
  for (let i = total - 1; i >= 0; i -= 1) {
    const day = new Date(anchor)
    day.setUTCDate(anchor.getUTCDate() - i)
    const id = key(day.getUTCFullYear(), day.getUTCMonth(), day.getUTCDate())
    out.push({ date: id, count: counts.get(id) ?? 0 })
  }
  return out
}

/**
 * Runs per day, above the Register table.
 *
 * The reference this is translated from calls its version a streak, counts
 * consecutive days and puts a flame on it. Sirdar takes the calendar and
 * leaves the habit app: a support engineer's good week is a week with few
 * runs, and counting consecutive days of work as an achievement would be a lie
 * on this screen.
 *
 * Colour is the summary and never the only copy of the fact. Every cell is a
 * button whose accessible name reads "14 September, 6 runs", the same string
 * is its tooltip, and the legend prints the bucket boundaries as numbers
 * rather than only More and Less.
 */
export default function Heatmap({
  days,
  weeks = 12,
  endDate,
  label = 'Runs per day',
  onSelect,
}: HeatmapProps): JSX.Element {
  const cells = grid(days, weeks, endDate)
  const columns = Math.ceil(cells.length / 7)

  return (
    <section className="sd-heatmap" aria-label={label}>
      <div className="sd-heatmap__scroll" tabIndex={0} role="group" aria-label={label}>
        <div
          className="sd-heatmap__grid"
          style={{ gridTemplateColumns: `repeat(${columns}, 12px)` }}
        >
          {cells.map((cell) => {
            const name = cellName(cell.date, cell.count)
            const weekday = WEEKDAYS[new Date(`${cell.date}T00:00:00Z`).getUTCDay()] ?? ''
            return (
              <button
                key={cell.date}
                type="button"
                className="sd-heatmap__cell"
                data-heat={bucketOf(cell.count)}
                title={name}
                aria-label={weekday ? `${weekday} ${name}` : name}
                onClick={() => onSelect?.(cell.date)}
              />
            )
          })}
        </div>
      </div>
      <p className="sd-heatmap__legend">
        <span className="sd-heatmap__legend-word">Runs a day</span>
        {BUCKETS.map((bucket, step) => (
          <span className="sd-heatmap__legend-step" key={bucket.label}>
            <span className="sd-heatmap__cell" data-heat={step} aria-hidden="true" />
            <span className="sd-heatmap__legend-count">{bucket.label}</span>
          </span>
        ))}
      </p>
    </section>
  )
}
