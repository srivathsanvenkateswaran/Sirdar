import { parseTime } from '../../lib/format'

/*
 * How a tool's output is read before it is drawn. The conversation layout
 * shows a call collapsed as one line with its output's size, and expanded
 * as the output in the shape it has: an `rg` result as a file · line · match
 * table, a file read as numbered lines, a test run as a block with its
 * verdict lines coloured, and anything else as text. Everything here is
 * pure; a text that fits no shape is `text`.
 */

export interface Size {
  bytes: number
  lines: number
}

/** How big an output is, for the collapsed line: `17 lines`, `1.1 kB`. */
export function sizeOf(text: string): Size {
  if (!text) return { bytes: 0, lines: 0 }
  const bytes = new TextEncoder().encode(text).length
  const trimmed = text.replace(/\n+$/, '')
  return { bytes, lines: trimmed === '' ? 0 : trimmed.split('\n').length }
}

/** `624 B`, `1.1 kB`, `13.6 kB`, `2.0 MB`. */
export function formatBytes(bytes: number): string {
  if (bytes < 1000) return `${bytes} B`
  if (bytes < 1_000_000) return `${(bytes / 1000).toFixed(1)} kB`
  return `${(bytes / 1_000_000).toFixed(1)} MB`
}

/** `82 ms`, `2.6 s`, `1:04`. */
export function formatMs(ms: number): string {
  if (!Number.isFinite(ms) || ms < 0) return ''
  if (ms < 1000) return `${Math.round(ms)} ms`
  if (ms < 60_000) return `${(ms / 1000).toFixed(1)} s`
  const total = Math.round(ms / 1000)
  return `${Math.floor(total / 60)}:${String(total % 60).padStart(2, '0')}`
}

/** Seconds since the run started, as the mock's clock: `02:06`, `1:02:06`. */
export function clock(t: string | undefined, startedAt: string | undefined): string {
  const at = parseTime(t)
  const start = parseTime(startedAt)
  if (Number.isNaN(at) || Number.isNaN(start)) return ''
  const total = Math.max(0, Math.floor((at - start) / 1000))
  const h = Math.floor(total / 3600)
  const m = Math.floor((total % 3600) / 60)
  const s = total % 60
  const ms = `${String(m).padStart(2, '0')}:${String(s).padStart(2, '0')}`
  return h > 0 ? `${h}:${ms}` : ms
}

/** One `file:line: match` row of an rg or grep result. */
export interface MatchRow {
  file: string
  line: number
  text: string
}

export interface NumberedLine {
  n: number
  text: string
}

export type Shaped =
  | { kind: 'matches'; rows: MatchRow[]; files: number }
  | { kind: 'lines'; rows: NumberedLine[] }
  | { kind: 'test'; lines: { text: string; tone: 'fail' | 'ok' | '' }[] }
  | { kind: 'text'; text: string }

const MATCH = /^([^:\n\t]+):(\d+):(.*)$/
const NUMBERED = /^\s*(\d+)\t(.*)$/

/** True for a command that runs tests, so its output is read for verdicts. */
function isTestRun(command: string): boolean {
  return /(?:^|[\s;&|(])(?:go test|npm (?:run )?test|pnpm test|yarn test|bun test|(?:npx )?vitest|(?:npx )?jest|(?:python3? -m )?pytest|cargo test|make test)(?=$|[\s;&|)])/.test(
    command,
  )
}

/**
 * Reads an output into the shape it has. `tool` and `command` say what
 * produced it, since an `rg` result and a file read look alike to a regex
 * and differently to a reader.
 */
export function shapeOutput(tool: string, command: string, text: string): Shaped {
  const body = text.replace(/\n+$/, '')
  if (body === '') return { kind: 'text', text }
  const lines = body.split('\n')

  if (/^(?:read|read_file|readfile|view_file)$/i.test(tool)) {
    const rows = lines.map((l) => NUMBERED.exec(l))
    if (rows.length > 0 && rows.filter(Boolean).length >= lines.length * 0.9) {
      return {
        kind: 'lines',
        rows: rows.map((m, i) => (m ? { n: Number(m[1]), text: m[2] } : { n: i + 1, text: lines[i] })),
      }
    }
  }

  if (/(?:^|[\s;&|(])(?:rg|grep)\b/.test(command)) {
    const rows = lines.map((l) => MATCH.exec(l))
    const hits = rows.filter(Boolean).length
    if (hits > 0 && hits >= lines.length * 0.8) {
      const out: MatchRow[] = []
      for (const m of rows) if (m) out.push({ file: m[1], line: Number(m[2]), text: m[3] })
      return { kind: 'matches', rows: out, files: new Set(out.map((r) => r.file)).size }
    }
  }

  if (isTestRun(command)) {
    return {
      kind: 'test',
      lines: lines.map((l) => ({
        text: l,
        tone: /^(?:--- FAIL|FAIL\b|Exit code [1-9]|\s+.*:\d+: )/.test(l)
          ? 'fail'
          : /^(?:--- PASS|PASS\b|ok\s)/.test(l)
            ? 'ok'
            : '',
      })),
    }
  }

  return { kind: 'text', text: body }
}

/** `Exit code 1` at the head of a shell result, or undefined. */
export function exitCodeOf(text: string): number | undefined {
  const m = /^Exit code (\d+)\b/.exec(text.trimStart())
  return m ? Number(m[1]) : undefined
}
