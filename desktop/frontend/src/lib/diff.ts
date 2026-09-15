/**
 * Reads a unified patch — the `patch` field of a `RunDiff` — into files,
 * hunks and numbered lines, which is the shape the Changes pane and the
 * review screen lay out.
 *
 * Everything here is pure and defensive. A line the parser does not
 * recognise inside a hunk is kept as context rather than dropped, so a
 * patch the service produced is never shown with a hole in it; a patch cut
 * at the 2 MiB cap (`truncated`) simply ends where it ends.
 */

export type DiffLineKind = 'context' | 'add' | 'del'

export interface DiffLine {
  kind: DiffLineKind
  /** The line without its leading marker. */
  text: string
  /** Line number in the old file; absent on an added line. */
  oldNo?: number
  /** Line number in the new file; absent on a deleted line. */
  newNo?: number
}

export interface DiffHunk {
  /** 0-based within its file: what `dropHunk` is given. */
  index: number
  /** The `@@ -a,b +c,d @@ …` line, verbatim. */
  header: string
  lines: DiffLine[]
  additions: number
  deletions: number
}

export interface FilePatch {
  /** The path the file now has; a deleted file keeps the path it had. */
  path: string
  oldPath: string
  hunks: DiffHunk[]
  additions: number
  deletions: number
}

const HUNK = /^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@/

/** `a/internal/x.go` → `internal/x.go`; `/dev/null` stays as it is. */
function stripPrefix(path: string): string {
  if (path === '/dev/null') return path
  return path.replace(/^[ab]\//, '')
}

/** The two paths on a `diff --git a/X b/Y` line, quoted or not. */
function gitPaths(line: string): { oldPath: string; path: string } | undefined {
  const rest = line.slice('diff --git '.length)
  const quoted = /^"(.+?)" "(.+?)"$/.exec(rest)
  if (quoted) return { oldPath: stripPrefix(quoted[1]), path: stripPrefix(quoted[2]) }
  // The two halves split at ` b/`; a path with a space in it is still
  // found because `a/` and `b/` both lead their halves.
  const at = rest.indexOf(' b/')
  if (at === -1) return undefined
  return { oldPath: stripPrefix(rest.slice(0, at)), path: stripPrefix(rest.slice(at + 1)) }
}

/** Splits a unified patch into its files. An empty patch is an empty list. */
export function parsePatch(patch: string): FilePatch[] {
  const files: FilePatch[] = []
  let file: FilePatch | undefined
  let hunk: DiffHunk | undefined
  let oldNo = 0
  let newNo = 0

  // The final newline ends the last line; it does not start an empty one.
  for (const raw of patch.replace(/\r?\n$/, '').split('\n')) {
    const line = raw.endsWith('\r') ? raw.slice(0, -1) : raw

    if (line.startsWith('diff --git ')) {
      hunk = undefined
      const paths = gitPaths(line)
      file = {
        path: paths?.path ?? line,
        oldPath: paths?.oldPath ?? paths?.path ?? line,
        hunks: [],
        additions: 0,
        deletions: 0,
      }
      files.push(file)
      continue
    }

    if (!file) {
      // A patch with no `diff --git` header at all: one nameless file.
      if (line.startsWith('--- ') || line.startsWith('+++ ') || HUNK.test(line)) {
        file = { path: '', oldPath: '', hunks: [], additions: 0, deletions: 0 }
        files.push(file)
      } else {
        continue
      }
    }

    if (!hunk && line.startsWith('--- ')) {
      const p = stripPrefix(line.slice(4).trim())
      if (p !== '/dev/null' && !file.oldPath) file.oldPath = p
      continue
    }
    if (!hunk && line.startsWith('+++ ')) {
      const p = stripPrefix(line.slice(4).trim())
      if (p !== '/dev/null') file.path = p
      else if (!file.path) file.path = file.oldPath
      continue
    }

    const head = HUNK.exec(line)
    if (head) {
      oldNo = Number(head[1])
      newNo = Number(head[3])
      hunk = { index: file.hunks.length, header: line, lines: [], additions: 0, deletions: 0 }
      file.hunks.push(hunk)
      continue
    }

    if (!hunk) continue
    if (line.startsWith('\\')) continue // "\ No newline at end of file"

    const marker = line[0]
    const text = line.slice(1)
    if (marker === '+') {
      hunk.lines.push({ kind: 'add', text, newNo })
      newNo += 1
      hunk.additions += 1
      file.additions += 1
    } else if (marker === '-') {
      hunk.lines.push({ kind: 'del', text, oldNo })
      oldNo += 1
      hunk.deletions += 1
      file.deletions += 1
    } else if (marker === ' ' || line === '') {
      hunk.lines.push({ kind: 'context', text, oldNo, newNo })
      oldNo += 1
      newNo += 1
    } else {
      // Not a hunk line the format knows. Keep it rather than lose it.
      hunk.lines.push({ kind: 'context', text: line, oldNo, newNo })
      oldNo += 1
      newNo += 1
    }
  }

  return files
}

/** The number a diff line shows in its gutter: the new line, or the old one for a deletion. */
export function lineNumber(line: DiffLine): number | undefined {
  return line.kind === 'del' ? line.oldNo : line.newNo
}
