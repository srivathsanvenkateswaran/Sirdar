/**
 * What a tool call did, in the words a step reads as, and what it returned,
 * in the shape it has.
 *
 * The B mock reads the path as sentences — "Read ledger.go · 65 lines",
 * "Ran rg -n "Return|restock" · 10 matches", "Ran go test ./... · FAIL · 1
 * test" — and renders an expanded output by its shape rather than as one
 * wall of text: `rg`, `ls` and `git --stat` become tables, a test run a mono
 * block with the failing line in the failed hue, a file read keeps its line
 * numbers. Everything here is pure and defensive: an output that fits no
 * shape is a plain block, and a count that cannot be read is left out.
 */

/** The family a shell command belongs to, read off its first words. */
export type CommandKind = 'rg' | 'grep' | 'ls' | 'git-stat' | 'git-log' | 'test' | 'build' | 'other'

const WRITE_TOOLS = new Set([
  'Edit',
  'Write',
  'MultiEdit',
  'NotebookEdit',
  'write_file',
  'edit',
  'replace',
  'edit_file',
  'apply_patch',
  'fileChange',
])
const READ_TOOLS = new Set(['Read', 'read_file', 'view', 'cat', 'fileRead', 'read'])
const SHELL_TOOLS = /^(bash|shell|commandexecution|command_execution|execute|run_command)$/i
const OUTPUT_TOOLS = new Set(['StructuredOutput', 'structured_output'])

export function isWriteTool(tool: string): boolean {
  return WRITE_TOOLS.has(tool)
}
export function isReadTool(tool: string): boolean {
  return READ_TOOLS.has(tool)
}
export function isShellTool(tool: string): boolean {
  return SHELL_TOOLS.test(tool)
}
export function isOutputTool(tool: string): boolean {
  return OUTPUT_TOOLS.has(tool)
}

/**
 * The command's segments: `a && b | c` is three, each trimmed, with a
 * leading `cd` dropped. Quotes are honoured, so the pipe inside
 * `rg "Return|restock"` splits nothing.
 */
export function segments(command: string): string[] {
  const out: string[] = []
  let current = ''
  let quote = ''
  for (let i = 0; i < command.length; i += 1) {
    const c = command[i]
    if (quote) {
      current += c
      if (c === quote) quote = ''
      continue
    }
    if (c === '"' || c === "'") {
      quote = c
      current += c
      continue
    }
    if (c === ';' || c === '|' || (c === '&' && command[i + 1] === '&')) {
      out.push(current)
      current = ''
      if (c === '&' || (c === '|' && command[i + 1] === '|')) i += 1
      continue
    }
    current += c
  }
  out.push(current)
  return out
    .map((s) => s.trim())
    .filter(Boolean)
    .filter((s) => !/^cd\s/.test(s))
}

export function commandKind(command: string): CommandKind {
  const parts = segments(command)
  const first = parts[0] ?? command.trim()
  if (/^rg\b/.test(first)) return 'rg'
  if (/^(?:e|f)?grep\b/.test(first)) return 'grep'
  if (/^ls\b/.test(first)) return 'ls'
  if (/^git\b.*\blog\b.*--stat\b/.test(first)) return 'git-stat'
  if (/^git\b.*\blog\b/.test(first)) return 'git-log'
  if (parts.some((p) => /^(?:go test|npm (?:run )?test|pnpm test|yarn test|bun test|(?:npx )?vitest|(?:npx )?jest|(?:python3? -m )?pytest|cargo test|make test|mvn test|gradle test|dotnet test|rspec)\b/.test(p)))
    return 'test'
  if (parts.some((p) => /^(?:go (?:build|vet)|npm run build|(?:npx )?tsc|cargo (?:build|check)|make(?: build)?)\b/.test(p)))
    return 'build'
  return 'other'
}

/**
 * A path as a step names it: the file, with `bundle/` kept when the file is
 * in the run's bundle and `worktree/` when it is in a fix run's worktree,
 * because those two prefixes say where the agent was reading.
 */
export function shortPath(path: string): string {
  const clean = path.replace(/[\\/]+$/, '')
  const parts = clean.split(/[\\/]/)
  const name = parts[parts.length - 1] || clean
  const parent = parts[parts.length - 2] ?? ''
  if (parent === 'bundle') return `bundle/${name}`
  if (/\/\.sirdar\/worktrees\/[^/]+\//.test(clean)) return `worktree/${name}`
  return name
}

/** `/Users/me/repos/app` → `…/app`; a relative path stays as it is. */
function elidePath(token: string): string {
  if (!/^\/[^\s]+\/[^\s/]+$/.test(token)) return token
  const name = token.slice(token.lastIndexOf('/') + 1)
  return `…/${name}`
}

/**
 * The command as the collapsed step spells it: absolute paths cut to their
 * last segment, `--format=` values elided, `--type` and `--date` flags and
 * `./...` dropped, and the whole thing held to about forty characters.
 */
export function shortCommand(command: string, max = 44): string {
  const parts = segments(command).map((segment) => {
    // A token may carry a quoted part inside it: `--format='%h %ad'` is one.
    const tokens = segment.match(/(?:[^\s"']+|"[^"]*"|'[^']*')+/g) ?? []
    const out: string[] = []
    for (let i = 0; i < tokens.length; i += 1) {
      const t = tokens[i]
      if (t === '--type' || t === '--date') {
        i += 1
        continue
      }
      if (/^--(?:type|date)=/.test(t)) continue
      if (t === './...') continue
      if (/^--format=/.test(t)) {
        out.push('--format=…')
        continue
      }
      out.push(elidePath(t))
    }
    return out.join(' ')
  })
  const joined = parts.join(' && ')
  return joined.length > max ? `${joined.slice(0, max - 1)}…` : joined
}

/** `12.6 kB`, `624 B`, `1.1 MB`. */
export function bytes(n: number): string {
  if (!Number.isFinite(n) || n < 0) n = 0
  if (n < 1000) return `${Math.round(n)} B`
  if (n < 1_000_000) {
    const k = n / 1000
    return `${k < 10 ? k.toFixed(1) : Math.round(k)} kB`
  }
  const m = n / 1_000_000
  return `${m < 10 ? m.toFixed(1) : Math.round(m)} MB`
}

/** UTF-8 size of a string, without a TextEncoder allocation per call. */
export function byteLength(text: string): number {
  let n = 0
  for (let i = 0; i < text.length; i += 1) {
    const c = text.charCodeAt(i)
    if (c < 0x80) n += 1
    else if (c < 0x800) n += 2
    else if (c >= 0xd800 && c <= 0xdbff) {
      n += 4
      i += 1
    } else n += 3
  }
  return n
}

function lines(text: string): string[] {
  const all = text.replace(/\r\n/g, '\n').split('\n')
  while (all.length > 0 && all[all.length - 1] === '') all.pop()
  return all
}

function plural(n: number, one: string, many = `${one}s`): string {
  return `${n} ${n === 1 ? one : many}`
}

const RG_LINE = /^([^:\s][^:]*?):(\d+):(.*)$/

/** A test run's verdict, off what the runner printed. */
export function testVerdict(text: string): { failed: boolean; tests?: number; seconds?: string } {
  const failed = /^(?:FAIL\b|--- FAIL)/m.test(text) || /\[exit status [1-9]\d*\]\s*$/.test(text)
  const fails = text.match(/^\s*--- FAIL:/gm)
  const passes = text.match(/^\s*--- PASS:/gm)
  const ok = /^ok\s+\S+\s+([\d.]+s)/m.exec(text)
  return {
    failed,
    tests: failed ? fails?.length : passes?.length,
    seconds: ok?.[1],
  }
}

/**
 * The result a collapsed step shows after its dot: a count for what the
 * command listed, a verdict for a test run, a size for anything else.
 * '' when the call has not returned.
 */
export function resultSummary(tool: string, command: string, output: string | undefined): string {
  if (output === undefined) return ''
  const text = output
  const rows = lines(text)
  if (isReadTool(tool)) return plural(rows.length, 'line')
  if (!isShellTool(tool)) {
    if (text.trim() === '') return ''
    return plural(rows.length, 'line')
  }
  switch (commandKind(command)) {
    case 'rg':
    case 'grep': {
      const hits = rows.filter((l) => RG_LINE.test(l)).length
      if (hits === 0 && /no matches|exit status 1/i.test(text)) return 'no matches'
      return hits > 0 ? plural(hits, 'match', 'matches') : plural(rows.length, 'line')
    }
    case 'ls': {
      const entries = rows.filter((l) => !/^total\s+\d+/.test(l) && l.trim() !== '')
      return plural(entries.length, 'entry', 'entries')
    }
    case 'git-stat': {
      const commits = rows.filter((l) => /^[0-9a-f]{7,40}\b/.test(l)).length
      const files = rows.filter((l) => /\|\s+\d+/.test(l)).length
      const parts = [plural(commits, 'commit'), files > 0 ? plural(files, 'file') : ''].filter(Boolean)
      return parts.join(' · ')
    }
    case 'git-log':
      return plural(rows.filter((l) => /^[0-9a-f]{7,40}\b/.test(l)).length || rows.length, 'commit')
    case 'test': {
      const v = testVerdict(text)
      if (v.failed) return v.tests ? `FAIL · ${plural(v.tests, 'test')}` : 'FAIL'
      return v.seconds ? `ok · ${v.seconds}` : 'ok'
    }
    case 'build':
      return /\b(?:error|FAIL|cannot|undefined:)\b/i.test(text) ? 'error' : text.trim() === '' ? 'ok' : plural(rows.length, 'line')
    default:
      if (text.trim() === '') return 'no output'
      return plural(rows.length, 'line')
  }
}

/** `+14`, `−11 +1`: what an edit changed, as a line count. */
export function editDelta(oldText: string | undefined, newText: string | undefined): string {
  const before = oldText ? lines(oldText) : []
  const after = newText ? lines(newText) : []
  const pool = new Map<string, number>()
  for (const l of before) pool.set(l, (pool.get(l) ?? 0) + 1)
  let added = 0
  for (const l of after) {
    const n = pool.get(l) ?? 0
    if (n > 0) pool.set(l, n - 1)
    else added += 1
  }
  let removed = 0
  for (const n of pool.values()) removed += n
  const parts: string[] = []
  if (removed > 0) parts.push(`−${removed}`)
  if (added > 0) parts.push(`+${added}`)
  return parts.join(' ') || '±0'
}

// ------------------------------------------------------------------ shapes

export type OutputShape =
  | {
      kind: 'table'
      /** What the two or three columns hold, for the header. */
      columns: string[]
      rows: { cells: string[]; ref?: string; head?: boolean }[]
    }
  | { kind: 'lines'; rows: { n: number; text: string }[] }
  | { kind: 'pre'; lines: { text: string; tone?: 'bad' | 'good' }[] }
  | { kind: 'empty' }

/** Claude's Read prints `N\tline`; other providers print `N | line` or `N: line`. */
const NUMBERED = /^\s*(\d+)(?:\t|→| \| |: )(.*)$/

/**
 * The output rendered by shape. The command decides the shape when the tool
 * was a shell; a file read is numbered lines; anything else is a block.
 */
export function shapeOutput(tool: string, command: string, text: string): OutputShape {
  const rows = lines(text)
  if (rows.length === 0 || text.trim() === '') return { kind: 'empty' }

  if (isReadTool(tool)) {
    const numbered = rows.map((l) => NUMBERED.exec(l))
    if (numbered.filter(Boolean).length >= rows.length * 0.9) {
      return {
        kind: 'lines',
        rows: rows.map((l, i) => {
          const m = numbered[i]
          return m ? { n: Number(m[1]), text: m[2] } : { n: i + 1, text: l }
        }),
      }
    }
    return { kind: 'lines', rows: rows.map((l, i) => ({ n: i + 1, text: l })) }
  }

  if (!isShellTool(tool)) return pre(rows)

  switch (commandKind(command)) {
    case 'rg':
    case 'grep': {
      const hits = rows.map((l) => RG_LINE.exec(l))
      if (hits.filter(Boolean).length >= Math.max(1, rows.length * 0.8)) {
        return {
          kind: 'table',
          columns: ['file:line', 'match'],
          rows: rows.flatMap((l, i) => {
            const m = hits[i]
            if (!m) return l.trim() === '' ? [] : [{ cells: [l, ''] }]
            const file = m[1].slice(m[1].lastIndexOf('/') + 1)
            return [{ cells: [`${file}:${m[2]}`, m[3].trim()], ref: `${file}:${m[2]}` }]
          }),
        }
      }
      return pre(rows)
    }
    case 'ls': {
      const long = rows.filter((l) => /^[-dlcbps][rwxsStT-]{9}/.test(l))
      if (long.length >= Math.max(1, (rows.length - 1) * 0.8)) {
        return {
          kind: 'table',
          columns: ['name', 'size', 'modified'],
          rows: long.map((l) => {
            const cols = l.split(/\s+/)
            // perms links owner group size mon day time|year name…
            const name = cols.slice(8).join(' ')
            return { cells: [name, cols[4] ?? '', cols.slice(5, 8).join(' ')] }
          }),
        }
      }
      return {
        kind: 'table',
        columns: ['name'],
        rows: rows.filter((l) => l.trim() !== '').map((l) => ({ cells: [l.trim()] })),
      }
    }
    case 'git-stat': {
      const out: { cells: string[]; head?: boolean }[] = []
      for (const l of rows) {
        if (/^[0-9a-f]{7,40}\b/.test(l)) {
          const m = /^([0-9a-f]{7,40})\s+(.*)$/.exec(l)
          out.push({ cells: [m?.[1] ?? l, m?.[2] ?? '', ''], head: true })
          continue
        }
        const stat = /^\s*(\S.*?)\s+\|\s+(\d+|Bin)\s*(.*)$/.exec(l)
        if (stat) {
          out.push({ cells: [stat[1], stat[2], stat[3].trim()] })
          continue
        }
        const total = /^\s*(\d+ files? changed.*)$/.exec(l)
        if (total) out.push({ cells: [total[1], '', ''], head: true })
      }
      if (out.length > 0) return { kind: 'table', columns: ['file', 'lines', 'change'], rows: out }
      return pre(rows)
    }
    case 'test':
    case 'build':
      return {
        kind: 'pre',
        lines: rows.map((l) => ({
          text: l,
          tone: /^(?:FAIL\b|--- FAIL|\s+\S+\.go:\d+:|.*\berror\b|Exit code [1-9])/.test(l)
            ? 'bad'
            : /^(?:ok\b|--- PASS|PASS\b)/.test(l)
              ? 'good'
              : undefined,
        })),
      }
    default:
      return pre(rows)
  }
}

function pre(rows: string[]): OutputShape {
  return { kind: 'pre', lines: rows.map((text) => ({ text })) }
}

/** How many rows or lines a shape holds, for the footer. */
export function shapeCount(shape: OutputShape): { n: number; noun: string } {
  switch (shape.kind) {
    case 'table':
      return { n: shape.rows.length, noun: 'row' }
    case 'lines':
      return { n: shape.rows.length, noun: 'line' }
    case 'pre':
      return { n: shape.lines.length, noun: 'line' }
    default:
      return { n: 0, noun: 'line' }
  }
}
