import { describe, expect, it } from 'vitest'
import { SAMPLE_PATCH } from '../../store/fakeTransport'
import { hunkKey, parsePatch } from './patch'

describe('parsePatch', () => {
  it('reads the fake transport\'s two files and three hunks, numbered the way dropHunk counts', () => {
    const files = parsePatch(SAMPLE_PATCH)
    expect(files.map((f) => f.path)).toEqual([
      'internal/export/statement.go',
      'internal/export/statement_test.go',
    ])
    expect(files[0].hunks.map((h) => h.index)).toEqual([0, 1])
    expect(files[1].hunks.map((h) => h.index)).toEqual([0])
    expect(files[0].hunks[0].section).toBe('func (e *Exporter) page(ctx context.Context, n int) error {')
    expect(files[0].additions).toBe(8)
    expect(files[0].deletions).toBe(2)
    expect(files[1].additions).toBe(9)
    expect(files[1].deletions).toBe(0)
  })

  it('numbers lines from each side of the hunk header', () => {
    const [first] = parsePatch(SAMPLE_PATCH)
    const lines = first.hunks[0].lines
    expect(lines[0]).toMatchObject({ type: 'del', oldNo: 41, text: '\tconn := e.pool.Get()' })
    expect(lines[1]).toMatchObject({ type: 'del', oldNo: 42 })
    expect(lines[2]).toMatchObject({ type: 'add', newNo: 41 })
    expect(lines[6]).toMatchObject({ type: 'add', newNo: 45, text: '\tdefer conn.Release()' })
    expect(lines[7]).toMatchObject({ type: 'context', oldNo: 43, newNo: 46 })
    expect(lines[7].text).toBe('\trows, err := conn.Query(ctx, statementPage, n)')
  })

  it('reads a file\'s status off its paths and mode lines', () => {
    const files = parsePatch(
      [
        'diff --git a/gone.go b/gone.go',
        'deleted file mode 100644',
        '--- a/gone.go',
        '+++ /dev/null',
        '@@ -1 +0,0 @@',
        '-package gone',
        'diff --git a/old.go b/new.go',
        'similarity index 90%',
        'rename from old.go',
        'rename to new.go',
        '--- a/old.go',
        '+++ b/new.go',
        '@@ -1 +1 @@',
        '-package old',
        '+package renamed',
        'diff --git a/fresh.go b/fresh.go',
        'new file mode 100644',
        '--- /dev/null',
        '+++ b/fresh.go',
        '@@ -0,0 +1,2 @@',
        '+package fresh',
        '+',
        '\\ No newline at end of file',
        '',
      ].join('\n'),
    )
    expect(files.map((f) => [f.path, f.status])).toEqual([
      ['gone.go', 'deleted'],
      ['new.go', 'renamed'],
      ['fresh.go', 'added'],
    ])
    expect(files[1].oldPath).toBe('old.go')
    // A hunk header with no count means one line.
    expect(files[1].hunks[0]).toMatchObject({ oldLines: 1, newLines: 1 })
    // The marker line is kept, as meta, rather than dropped.
    expect(files[2].hunks[0].lines.at(-1)).toEqual({
      type: 'meta',
      text: '\\ No newline at end of file',
    })
  })

  it('reads a patch with no diff --git header, as a hand-made one has', () => {
    const files = parsePatch('--- a/x.go\n+++ b/x.go\n@@ -1,2 +1,2 @@\n a\n-b\n+c\n')
    expect(files).toHaveLength(1)
    expect(files[0].path).toBe('x.go')
    expect(files[0].hunks[0].lines.map((l) => l.type)).toEqual(['context', 'del', 'add'])
  })

  it('answers nothing for an empty patch', () => {
    expect(parsePatch('')).toEqual([])
  })

  it('keys a hunk by path and index', () => {
    expect(hunkKey('a/b.go', 2)).not.toBe(hunkKey('a/b.go', 1))
    expect(hunkKey('a/b.go', 1)).not.toBe(hunkKey('a/b.go1', 1))
  })
})
