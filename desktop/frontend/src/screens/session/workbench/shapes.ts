import { detectTable, type TextTable } from '../../../lib/events'

/**
 * A tool's output read into the shape it has, so the expanded call can draw
 * a table where there is one rather than a wall of text. Nothing is cut: the
 * row is the summary, the pane is the whole thing.
 */
export type OutputShape =
  /** `file:line:match` — what rg and grep -n print. */
  | { kind: 'grep'; rows: { file: string; line: string; text: string }[] }
  /** Numbered lines — Claude's Read tool, `cat -n`. */
  | { kind: 'lines'; rows: { n: string; text: string }[] }
  /** Pipes or tabs, the same cells on every row. */
  | { kind: 'table'; table: TextTable }
  /** A JSON document, drawn with its keys and strings coloured. */
  | { kind: 'json'; text: string }
  /** A test runner's output: mono, with the failing lines flagged. */
  | { kind: 'test'; lines: { text: string; failed: boolean }[] }
  | { kind: 'text'; text: string }

const GREP_LINE = /^([^:\s][^:]*?):(\d+):(.*)$/
const NUMBERED_LINE = /^\s*(\d+)(?:\t|→| {2,})(.*)$/
const FAILED_LINE = /^(?:---\s*FAIL|FAIL\b|Exit code [1-9]|panic:|.*\berror\b.*:\s|\s+\S+\.go:\d+:)/i

function nonEmpty(text: string): string[] {
  return text
    .replace(/\r/g, '')
    .split('\n')
    .filter((l) => l.trim() !== '')
}

/** The label the pane's header prints: `17 rows · parsed as file:line:match`. */
export function shapeLabel(shape: OutputShape): string {
  switch (shape.kind) {
    case 'grep':
      return `${shape.rows.length} rows · parsed as file:line:match`
    case 'lines':
      return `${shape.rows.length} numbered lines`
    case 'table':
      return `${shape.table.rows.length} rows · ${shape.table.head.length} columns`
    case 'json':
      return 'JSON'
    case 'test': {
      const failed = shape.lines.filter((l) => l.failed).length
      return failed > 0 ? `${shape.lines.length} lines · ${failed} failing` : `${shape.lines.length} lines`
    }
    case 'text':
      return `${nonEmpty(shape.text).length} lines`
  }
}

/**
 * Reads the output's shape. The command decides first — a test run is a
 * test run whatever it printed — then the text: grep hits, numbered lines,
 * a table, JSON, and plain text as the last resort.
 */
export function shapeOutput(text: string, opts: { test?: boolean } = {}): OutputShape {
  const lines = nonEmpty(text)
  if (opts.test) {
    return { kind: 'test', lines: lines.map((l) => ({ text: l, failed: FAILED_LINE.test(l) })) }
  }
  if (lines.length === 0) return { kind: 'text', text }

  const grep = lines.map((l) => GREP_LINE.exec(l))
  if (lines.length >= 2 && grep.filter(Boolean).length >= lines.length * 0.8) {
    return {
      kind: 'grep',
      rows: lines.map((l, i) => {
        const m = grep[i]
        return m ? { file: m[1], line: m[2], text: m[3] } : { file: '', line: '', text: l }
      }),
    }
  }

  const numbered = lines.map((l) => NUMBERED_LINE.exec(l))
  if (lines.length >= 2 && numbered.filter(Boolean).length >= lines.length * 0.9) {
    return {
      kind: 'lines',
      rows: lines.map((l, i) => {
        const m = numbered[i]
        return m ? { n: m[1], text: m[2] } : { n: '', text: l }
      }),
    }
  }

  const trimmed = text.trim()
  if ((trimmed.startsWith('{') && trimmed.endsWith('}')) || (trimmed.startsWith('[') && trimmed.endsWith(']'))) {
    try {
      const parsed: unknown = JSON.parse(trimmed)
      return { kind: 'json', text: JSON.stringify(parsed, null, 2) }
    } catch {
      // Not JSON after all.
    }
  }

  const table = detectTable(text)
  if (table) return { kind: 'table', table }

  return { kind: 'text', text }
}

/** One coloured piece of a JSON document. */
export type JsonToken = { kind: 'key' | 'string' | 'punct' | 'value'; text: string }

const JSON_TOKEN = /("(?:\\.|[^"\\])*")(\s*:)?|(\btrue\b|\bfalse\b|\bnull\b|-?\d+(?:\.\d+)?(?:[eE][+-]?\d+)?)|([{}[\],])/g

/**
 * Splits pretty-printed JSON into keys, strings, values and punctuation so
 * the pane can colour them with the tokens' hues. Whitespace between tokens
 * is kept as punctuation so the text round-trips.
 */
export function tokenizeJSON(text: string): JsonToken[] {
  const out: JsonToken[] = []
  let last = 0
  for (const m of text.matchAll(JSON_TOKEN)) {
    const at = m.index ?? 0
    if (at > last) out.push({ kind: 'punct', text: text.slice(last, at) })
    if (m[1] !== undefined) {
      if (m[2] !== undefined) {
        out.push({ kind: 'key', text: m[1] })
        out.push({ kind: 'punct', text: m[2] })
      } else {
        out.push({ kind: 'string', text: m[1] })
      }
    } else if (m[3] !== undefined) {
      out.push({ kind: 'value', text: m[3] })
    } else if (m[4] !== undefined) {
      out.push({ kind: 'punct', text: m[4] })
    }
    last = at + m[0].length
  }
  if (last < text.length) out.push({ kind: 'punct', text: text.slice(last) })
  return out
}
