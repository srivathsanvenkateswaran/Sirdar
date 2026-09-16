import { detectTable, type TextTable } from '../../../lib/events'
import { shapeOutput as shapeBase } from '../shape'

/**
 * A tool's output read into the shape it has, so the expanded call can draw
 * a table where there is one rather than a wall of text. The Conversation
 * layout's `shape.ts` does the reading — rg hits, numbered lines, a test
 * run's verdict lines — and this adds the two shapes the Workbench's pane
 * also draws: a JSON document with its keys coloured, and a pipe or tab
 * table. Nothing is cut: the row is the summary, the pane is the whole
 * thing.
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
 * Reads the output's shape. `tool` and `command` say what produced it — an
 * rg result and a file read look alike to a regex and differently to a
 * reader — and the shared shaper judges those first; JSON and a table are
 * tried on what it left as text.
 */
export function shapeOutput(tool: string, command: string, text: string): OutputShape {
  const base = shapeBase(tool, command, text)
  switch (base.kind) {
    case 'matches':
      return { kind: 'grep', rows: base.rows.map((r) => ({ file: r.file, line: String(r.line), text: r.text })) }
    case 'lines':
      return { kind: 'lines', rows: base.rows.map((r) => ({ n: String(r.n), text: r.text })) }
    case 'test':
      return { kind: 'test', lines: base.lines.map((l) => ({ text: l.text, failed: l.tone === 'fail' })) }
    case 'text':
      break
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
