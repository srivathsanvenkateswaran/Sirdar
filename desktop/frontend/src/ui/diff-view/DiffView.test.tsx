import { fireEvent, render, screen, within } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { parsePatch } from '../../lib/diff'
import { diff, SAMPLE_PATCH } from '../../store/fakeTransport'
import DiffView, { hunkKey } from './index'

const FILES = parsePatch(SAMPLE_PATCH)
const META = diff().files

/** One rendered diff line, by its exact text — tabs included, which the default matcher folds. */
function line(scope: HTMLElement, text: string): HTMLElement {
  const found = [...scope.querySelectorAll<HTMLElement>('.sd-diff__c')].find(
    (el) => el.textContent === text,
  )
  if (!found) throw new Error(`no diff line reads ${JSON.stringify(text)}`)
  return found
}

describe('DiffView', () => {
  it('lists every file with its counts and status word, and every hunk with its lines', () => {
    render(<DiffView files={FILES} meta={META} />)
    const rows = screen.getAllByRole('listitem')
    expect(rows).toHaveLength(2)
    expect(rows[0]).toHaveTextContent('internal/export/statement.go')
    expect(rows[0]).toHaveTextContent('+9')
    expect(rows[0]).toHaveTextContent('−2')
    expect(rows[1]).toHaveTextContent('new')
    expect(rows[0]).toHaveAttribute('aria-current', 'true')

    const hunk = screen.getByRole('region', {
      name: '@@ -41,7 +41,9 @@ func (e *Exporter) page(ctx context.Context, n int) error {',
    })
    expect(line(hunk, '-\tconn := e.pool.Get()').closest('.sd-diff__line')).toHaveAttribute(
      'data-t',
      'del',
    )
    expect(line(hunk, '+\tdefer conn.Release()').closest('.sd-diff__line')).toHaveAttribute(
      'data-t',
      'add',
    )
    expect(line(hunk, ' \trows, err := conn.Query(ctx, statementPage, n)').closest('.sd-diff__line')).not.toHaveAttribute('data-t')
  })

  it('offers Keep and Drop on each hunk and says which hunk was pressed', () => {
    const onKeep = vi.fn()
    const onDrop = vi.fn()
    render(<DiffView files={FILES} onKeep={onKeep} onDrop={onDrop} />)

    const second = screen.getByRole('region', { name: /@@ -88,3 \+90,6 @@/ })
    fireEvent.click(within(second).getByRole('button', { name: 'Keep' }))
    expect(onKeep).toHaveBeenCalledWith('internal/export/statement.go', 1)

    fireEvent.click(within(second).getByRole('button', { name: 'Drop' }))
    expect(onDrop).toHaveBeenCalledWith('internal/export/statement.go', 1)
  })

  it('reads Kept once a hunk is kept, and the file reads reviewed once all of its hunks are', () => {
    const kept = new Set([
      hunkKey('internal/export/statement.go', 0),
      hunkKey('internal/export/statement.go', 1),
    ])
    render(<DiffView files={FILES} meta={META} kept={kept} onKeep={() => {}} />)
    const first = screen.getByRole('region', { name: /@@ -41,7/ })
    expect(within(first).getByRole('button', { name: 'Kept' })).toHaveAttribute('aria-pressed', 'true')
    expect(screen.getAllByRole('listitem')[0]).toHaveTextContent('reviewed')
    expect(screen.getAllByRole('listitem')[1]).toHaveTextContent('new')
  })

  it('shows a refusal under the hunk it was for, and a busy Drop while one is in flight', () => {
    const key = hunkKey('internal/export/statement.go', 0)
    render(
      <DiffView
        files={FILES}
        onDrop={() => {}}
        dropping={hunkKey('internal/export/statement_test.go', 0)}
        refusals={{ [key]: 'conflict: the diff has changed since it was read; read it again' }}
      />,
    )
    const first = screen.getByRole('region', { name: /@@ -41,7/ })
    expect(within(first).getByRole('alert')).toHaveTextContent('the diff has changed since it was read')
    const test = screen.getByRole('region', { name: /@@ -12,0/ })
    expect(within(test).getByRole('button', { name: 'Dropping…' })).toHaveAttribute('aria-disabled', 'true')
  })

  it('disables Drop with the reason on a change that can no longer be edited', () => {
    render(
      <DiffView
        files={FILES}
        onDrop={() => {}}
        editable={false}
        readOnlyReason="The branch has been pushed; drop nothing here."
      />,
    )
    const drop = screen.getAllByRole('button', { name: 'Drop' })[0]
    expect(drop).toBeDisabled()
    expect(drop).toHaveAttribute('title', 'The branch has been pushed; drop nothing here.')
  })

  it('says when there is no change, and when the patch was cut', () => {
    const { unmount } = render(<DiffView files={[]} />)
    expect(screen.getByText('No change to show.')).toBeInTheDocument()
    unmount()
    render(<DiffView files={FILES} truncated />)
    expect(screen.getByText(/cut at the size cap/)).toBeInTheDocument()
  })

  it('stays left to right inside an Arabic pane', () => {
    render(
      <div dir="rtl">
        <DiffView files={FILES} label="التغييرات" />
      </div>,
    )
    expect(screen.getByRole('region', { name: 'التغييرات' })).toHaveAttribute('dir', 'ltr')
  })
})
