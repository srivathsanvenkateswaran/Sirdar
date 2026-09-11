/**
 * Formatting helpers shared by the board cards and the shell.
 *
 * Everything here is pure and takes an explicit `now` where time is involved so
 * tests do not have to freeze the clock.
 */

const SECOND = 1000
const MINUTE = 60 * SECOND
const HOUR = 60 * MINUTE
const DAY = 24 * HOUR

/** Parses an RFC 3339 timestamp; returns NaN for empty or unparseable input. */
export function parseTime(value: string | undefined): number {
  if (!value) return Number.NaN
  const ms = Date.parse(value)
  return Number.isNaN(ms) ? Number.NaN : ms
}

/**
 * Short relative time: `just now`, `4m ago`, `3h ago`, `2d ago`. Future stamps
 * read as `in 4m`. Returns an empty string when the input is not a timestamp.
 */
export function relativeTime(value: string | undefined, now: number = Date.now()): string {
  const ms = parseTime(value)
  if (Number.isNaN(ms)) return ''
  const delta = now - ms
  const ago = delta >= 0
  const abs = Math.abs(delta)
  if (abs < 45 * SECOND) return 'just now'
  const unit =
    abs < HOUR
      ? `${Math.round(abs / MINUTE)}m`
      : abs < DAY
        ? `${Math.round(abs / HOUR)}h`
        : `${Math.round(abs / DAY)}d`
  return ago ? `${unit} ago` : `in ${unit}`
}

/**
 * Clock-style duration for a live run: `0:42`, `7:19`, `1:04:30`. Minutes and
 * seconds are always two digits so the column of numbers stays aligned.
 */
export function duration(ms: number): string {
  if (!Number.isFinite(ms) || ms < 0) ms = 0
  const total = Math.floor(ms / SECOND)
  const seconds = total % 60
  const minutes = Math.floor(total / 60) % 60
  const hours = Math.floor(total / 3600)
  const ss = String(seconds).padStart(2, '0')
  if (hours > 0) return `${hours}:${String(minutes).padStart(2, '0')}:${ss}`
  return `${minutes}:${ss}`
}

/** Elapsed clock since `startedAt`; empty when the stamp is missing. */
export function elapsedSince(startedAt: string | undefined, now: number = Date.now()): string {
  const ms = parseTime(startedAt)
  if (Number.isNaN(ms)) return ''
  return duration(now - ms)
}

/**
 * USD with enough precision to be useful at agent scale: sub-cent amounts keep
 * three decimals, everything else two. Zero reads as `$0.00`, never as a dash,
 * because a run that truly cost nothing is worth seeing.
 */
export function usd(value: number | undefined): string {
  const n = typeof value === 'number' && Number.isFinite(value) ? value : 0
  if (n > 0 && n < 0.01) return `$${n.toFixed(3)}`
  return `$${n.toFixed(2)}`
}

/**
 * Cost for a run that may still be going. Claude Code reports cost only in
 * its result line, so a live session's spend is unknown rather than zero, and
 * `$0.00` on a run burning a dollar a minute reads as a free one. Once the
 * run has ended the number is the provider's own and is shown as it is.
 */
export function costOrUnknown(value: number | undefined, live: boolean): string {
  const known = typeof value === 'number' && Number.isFinite(value) && value > 0
  if (live && !known) return 'n/a'
  return usd(value)
}

/** Compact token counts: `840`, `12.4k`, `1.2M`. */
export function tokens(value: number | undefined): string {
  const n = typeof value === 'number' && Number.isFinite(value) ? Math.max(0, value) : 0
  if (n < 1000) return String(Math.round(n))
  if (n < 1_000_000) {
    const k = n / 1000
    return `${k < 10 ? k.toFixed(1) : Math.round(k)}k`
  }
  const m = n / 1_000_000
  return `${m < 10 ? m.toFixed(1) : Math.round(m)}M`
}

/** `1.2k in / 340 out` for a card footer. */
export function tokenFlow(inputTokens: number | undefined, outputTokens: number | undefined): string {
  return `${tokens(inputTokens)} in / ${tokens(outputTokens)} out`
}

/** Percentage rendered from a 0..1 utilisation or an already-scaled 0..100. */
export function percent(value: number | undefined): string {
  if (typeof value !== 'number' || !Number.isFinite(value)) return ''
  const scaled = value <= 1 ? value * 100 : value
  return `${Math.round(scaled)}%`
}

/**
 * Splits a free-text key list on commas, spaces and newlines, trims, and drops
 * duplicates while keeping the order the engineer typed.
 */
export function parseKeys(input: string): string[] {
  const seen = new Set<string>()
  const out: string[] = []
  for (const raw of input.split(/[\s,]+/)) {
    const key = raw.trim()
    if (!key || seen.has(key)) continue
    seen.add(key)
    out.push(key)
  }
  return out
}
