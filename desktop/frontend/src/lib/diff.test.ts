import { describe, expect, it } from 'vitest'
import { SAMPLE_PATCH } from '../store/fakeTransport'
import { lineNumber, parsePatch } from './diff'

describe('parsePatch', () => {
  it('reads the sample patch into two files and three hunks', () => {
    const files = parsePatch(SAMPLE_PATCH)
    expect(files.map((f) => f.path)).toEqual([
      'internal/export/statement.go',
      'internal/export/statement_test.go',
    ])
    expect(files[0].hunks).toHaveLength(2)
    expect(files[1].hunks).toHaveLength(1)
    expect(files[0].hunks.map((h) => h.index)).toEqual([0, 1])
    expect(files[0].hunks[0].header).toBe(
      '@@ -41,7 +41,9 @@ func (e *Exporter) page(ctx context.Context, n int) error {',
    )
  })

  it('counts additions and deletions per hunk and per file', () => {
    const [go, test] = parsePatch(SAMPLE_PATCH)
    expect(go.hunks[0]).toMatchObject({ additions: 5, deletions: 2 })
    expect(go.hunks[1]).toMatchObject({ additions: 3, deletions: 0 })
    expect(go).toMatchObject({ additions: 8, deletions: 2 })
    expect(test).toMatchObject({ additions: 9, deletions: 0 })
  })

  it('numbers lines from the hunk header, old side for deletions and new side otherwise', () => {
    const [go] = parsePatch(SAMPLE_PATCH)
    const lines = go.hunks[0].lines
    expect(lines[0]).toEqual({ kind: 'del', text: '\tconn := e.pool.Get()', oldNo: 41 })
    expect(lines[1]).toEqual({ kind: 'del', text: '\tdefer conn.Close()', oldNo: 42 })
    expect(lines[2]).toEqual({ kind: 'add', text: '\tconn, err := e.pool.Acquire(ctx)', newNo: 41 })
    const context = lines[lines.length - 1]
    expect(context).toEqual({
      kind: 'context',
      text: '\trows, err := conn.Query(ctx, statementPage, n)',
      oldNo: 43,
      newNo: 46,
    })
    expect(lineNumber(lines[0])).toBe(41)
    expect(lineNumber(lines[2])).toBe(41)
    expect(lineNumber(context)).toBe(46)
  })

  it('names a deleted file by the path it had', () => {
    const files = parsePatch(
      ['diff --git a/old.go b/old.go', '--- a/old.go', '+++ /dev/null', '@@ -1,2 +0,0 @@', '-a', '-b'].join(
        '\n',
      ),
    )
    expect(files[0].path).toBe('old.go')
    expect(files[0].hunks[0].deletions).toBe(2)
  })

  it('keeps a no-newline marker out of the lines and answers an empty patch with no files', () => {
    const files = parsePatch(
      'diff --git a/x b/x\n--- a/x\n+++ b/x\n@@ -1 +1 @@\n-a\n\\ No newline at end of file\n+b\n',
    )
    expect(files[0].hunks[0].lines.map((l) => l.kind)).toEqual(['del', 'add'])
    expect(parsePatch('')).toEqual([])
  })
})
