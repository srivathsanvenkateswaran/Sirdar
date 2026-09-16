/**
 * Evidence markers: E1…En on the answer's evidence items and on the tool
 * calls that produced them, C1…Cn on a fix's hunks and on the edits that
 * wrote them.
 *
 * The answer's `rootCause.evidence` names its source, the query it ran
 * ("Read ledger.go", `rg -n "Return|restock" --type go`, "ticket.json
 * Thread[0]") and the finding, with `file:line` references inside it. The
 * path holds the calls the agent made. A marker is the pairing: an evidence
 * item matched to the call whose query it quotes, else to the reads of the
 * files its finding cites. Clicking a marker on one side finds its twin on
 * the other, which is the audit the B direction is built around, and the
 * same marker sits in the Tools table and the change's hunk headers.
 *
 * Everything here is pure. A query that matches no call is a marker with no
 * step, drawn but going nowhere, which is the honest answer.
 */

export interface FileRef {
  /** The file as written, its directories dropped: `ledger.go`. */
  file: string
  from?: number
  to?: number
  /** The reference as written: `ledger.go:27-34`. */
  text: string
}

/**
 * `file.ext:12` and `file.ext:12-34`, with or without a directory before the
 * file. The extension has to start with a letter so a clock (`09:14:00`) or
 * an address (`127.0.0.1:47349`) is never read as a file, and a URL's host
 * never carries `:digits` after a dotted name.
 */
const REF = /(?<![\w/.:-])((?:[\w.-]+\/)*[\w-]+\.[A-Za-z]\w*):(\d+)(?:\s?[-–]\s?(\d+))?(?!\d)/g

export function parseRefs(text: string): FileRef[] {
  const out: FileRef[] = []
  for (const m of text.matchAll(REF)) {
    const path = m[1]
    const file = path.slice(path.lastIndexOf('/') + 1)
    const from = Number(m[2])
    const to = m[3] !== undefined ? Number(m[3]) : undefined
    out.push({ file, from, to: to !== undefined && to >= from ? to : undefined, text: m[0] })
  }
  return out
}

/** The base name of a path, or the path itself when it has no separators. */
export function fileName(path: string): string {
  const clean = path.replace(/[\\/]+$/, '')
  return clean.slice(Math.max(clean.lastIndexOf('/'), clean.lastIndexOf('\\')) + 1) || clean
}

/** Words that name a file: `ticket.json`, `ledger_test.go`, `bundle/thread.md`. */
export function fileTokens(text: string): string[] {
  const out: string[] = []
  for (const m of text.matchAll(/(?<![\w/.-])((?:[\w.-]+\/)*[\w-]+\.[A-Za-z]\w*)(?![\w/])/g)) {
    out.push(fileName(m[1]))
  }
  return [...new Set(out)]
}

/** A tool call as the derivation needs it: where it sits, what it did. */
export interface StepLike {
  index: number
  tool: string
  /** The file a read or an edit named. */
  path?: string
  /** The shell command a Bash call ran. */
  command?: string
}

export interface EvidenceSource {
  source: string
  query: string
  finding: string
}

export interface Marker {
  /** `E1`, `C2`. */
  id: string
  kind: 'E' | 'C'
  /** The `index` of every step this marker sits on; empty when none matched. */
  steps: number[]
  /** The `file:line` references the item cites. */
  refs: FileRef[]
  /** Every file the item names, in its refs or its query. */
  files: string[]
  source?: string
  query?: string
  finding?: string
  /** For a change marker: the file and the 0-based hunk it names. */
  path?: string
  hunk?: number
}

/** Lowercase, one space between words, no quotes: the shape two spellings of one command share. */
function normalise(s: string): string {
  return s
    .toLowerCase()
    .replace(/[`"'“”‘’]/g, '')
    .replace(/\s+/g, ' ')
    .trim()
}

const READ_WORDS = /^(?:read|reading|open|opened|cat|view|viewed)\b/i

const READ_TOOLS = new Set(['Read', 'read_file', 'view', 'cat', 'fileRead', 'read'])
const SHELL_TOOLS = /^(bash|shell|commandexecution|command_execution|execute|run_command)$/i

/** The steps an evidence item's query names. */
function stepsForQuery(query: string, finding: string, steps: StepLike[]): number[] {
  const q = normalise(query)
  const files = fileTokens(query)

  // "Read ledger.go", "Read ledger.go lines 22-37 against movement.go": the
  // first file named is the one that was read.
  if (READ_WORDS.test(query) && files.length > 0) {
    const want = files[0].toLowerCase()
    const reads = steps.filter(
      (s) => READ_TOOLS.has(s.tool) && s.path && fileName(s.path).toLowerCase() === want,
    )
    if (reads.length > 0) return reads.map((s) => s.index)
  }

  // A shell query quoted verbatim, or near enough: the command holds every
  // word of the query, or the query holds the whole command.
  if (q !== '') {
    const shells = steps.filter((s) => SHELL_TOOLS.test(s.tool) && s.command)
    const exact = shells.filter((s) => {
      const c = normalise(s.command as string)
      return c === q || c.includes(q) || q.includes(c)
    })
    if (exact.length > 0) return exact.map((s) => s.index)
    const words = q.split(' ').filter((w) => w.length > 1)
    if (words.length >= 2) {
      const loose = shells.filter((s) => {
        const c = normalise(s.command as string)
        return words.every((w) => c.includes(w))
      })
      if (loose.length > 0) return loose.map((s) => s.index)
    }
  }

  // Files the query names ("ticket.json Thread[0]"), then files the
  // finding cites: the reads of those files.
  for (const pool of [files, parseRefs(finding).map((r) => r.file)]) {
    const wanted = new Set(pool.map((f) => f.toLowerCase()))
    if (wanted.size === 0) continue
    const reads = steps.filter(
      (s) => READ_TOOLS.has(s.tool) && s.path && wanted.has(fileName(s.path).toLowerCase()),
    )
    if (reads.length > 0) return [...new Set(reads.map((s) => s.index))]
  }
  return []
}

/** E1…En for the answer's evidence items, each on the steps that produced it. */
export function deriveEvidenceMarkers(items: EvidenceSource[], steps: StepLike[]): Marker[] {
  return items.map((item, i) => {
    const refs = parseRefs(item.finding)
    const files = [...new Set([...refs.map((r) => r.file), ...fileTokens(item.query)])]
    return {
      id: `E${i + 1}`,
      kind: 'E',
      steps: stepsForQuery(item.query, item.finding, steps),
      refs,
      files,
      source: item.source,
      query: item.query,
      finding: item.finding,
    }
  })
}

/**
 * C1…Cn for a change's hunks, numbered in the order the agent edited the
 * files (a file it never edited in this log comes last), then by hunk. An
 * edit step carries every marker of the file it wrote.
 */
export function deriveChangeMarkers(
  files: { path: string; hunks: number }[],
  edits: { index: number; path: string }[],
): Marker[] {
  const firstEdit = new Map<string, number>()
  for (const e of edits) {
    const name = fileName(e.path)
    if (!firstEdit.has(name)) firstEdit.set(name, e.index)
  }
  const ordered = files
    .map((f, i) => ({ ...f, name: fileName(f.path), order: firstEdit.get(fileName(f.path)) ?? Number.MAX_SAFE_INTEGER, i }))
    .sort((a, b) => a.order - b.order || a.i - b.i)
  const out: Marker[] = []
  let n = 0
  for (const f of ordered) {
    const steps = edits.filter((e) => fileName(e.path) === f.name).map((e) => e.index)
    for (let hunk = 0; hunk < f.hunks; hunk += 1) {
      n += 1
      out.push({ id: `C${n}`, kind: 'C', steps, refs: [], files: [f.name], path: f.path, hunk })
    }
  }
  return out
}

/** True when two line ranges touch; a reference with no range covers the file. */
function overlaps(a: FileRef, b: FileRef): boolean {
  if (a.file.toLowerCase() !== b.file.toLowerCase()) return false
  if (a.from === undefined || b.from === undefined) return true
  const a2 = a.to ?? a.from
  const b2 = b.to ?? b.from
  return a.from <= b2 && b.from <= a2
}

/**
 * The markers a `file:line` written in prose belongs to: those whose
 * findings cite the same file at an overlapping line. At most `max`, so a
 * line every item cites does not grow a row of chips.
 */
export function markersForRef(ref: FileRef, markers: Marker[], max = 2): Marker[] {
  return markers.filter((m) => m.refs.some((r) => overlaps(r, ref))).slice(0, max)
}

/** The markers sitting on one step, in marker order. */
export function markersForStep(index: number, markers: Marker[]): Marker[] {
  return markers.filter((m) => m.steps.includes(index))
}

/** The markers whose finding cites a file, for a step that read it but matched no query. */
export function markersForFile(file: string, markers: Marker[]): Marker[] {
  const want = fileName(file).toLowerCase()
  return markers.filter((m) => m.files.some((f) => f.toLowerCase() === want))
}
