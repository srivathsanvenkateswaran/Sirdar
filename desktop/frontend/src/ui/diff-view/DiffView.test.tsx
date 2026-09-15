import { fireEvent, render, screen, within } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { diff, SAMPLE_PATCH } from '../../store/fakeTransport'
import DiffView, { hunkKey } from './index'

describe('DiffView', () => {
  it('draws every file, hunk and line of the patch with its number and mark', () => {
    render(<DiffView patch={SAMPLE_PATCH} />)
    expect(screen.getByRole('article', { name: 'internal/export/statement.go' })).toBeInTheDocument()
    expect(
      screen.getByRole('article', { name: 'internal/export/statement_test.go' }),
    ).toBeInTheDocument()
    expect(screen.getAllByRole('region')).toHaveLength(3)

    const first = screen.getByRole('region', { name: 'internal/export/statement.go hunk 1' })
    const rows = within(first).getAllByRole('row')
    expect(rows[0]).toHaveAttribute('data-type', 'del')
    expect(within(rows[0]).getAllByRole('cell')[0]).toHaveTextContent('41')
    expect(within(rows[0]).getAllByRole('cell')[1].textContent).toBe('−\tconn := e.pool.Get()')
    expect(rows[2]).toHaveAttribute('data-type', 'add')
    expect(within(rows[2]).getAllByRole('cell')[0]).toHaveTextContent('41')
    expect(within(rows[2]).getAllByRole('cell')[1].textContent).toMatch(/^\+/)
    expect(rows[7]).toHaveAttribute('data-type', 'context')
  })

  it('shows the counts and status on each file header, from the patch alone', () => {
    render(<DiffView patch={SAMPLE_PATCH} />)
    const file = screen.getByRole('article', { name: 'internal/export/statement_test.go' })
    expect(within(file).getByText('+9')).toBeInTheDocument()
    expect(within(file).queryByText('−0')).not.toBeInTheDocument()
    // The sample patch has no `/dev/null` side, so on its own it reads as modified.
    expect(within(file).getByText('modified')).toBeInTheDocument()
    const first = screen.getByRole('article', { name: 'internal/export/statement.go' })
    expect(within(first).getByText('+8')).toBeInTheDocument()
    expect(within(first).getByText('−2')).toBeInTheDocument()
  })

  it('takes status and counts from the service\'s file list when it is given one', () => {
    render(<DiffView patch={SAMPLE_PATCH} files={diff().files} />)
    const file = screen.getByRole('article', { name: 'internal/export/statement_test.go' })
    expect(within(file).getByText('new')).toBeInTheDocument()
    const first = screen.getByRole('article', { name: 'internal/export/statement.go' })
    expect(within(first).getByText('+9')).toBeInTheDocument()
    expect(within(first).getByText('modified')).toBeInTheDocument()
  })

  it('draws no buttons on a read-only change', () => {
    render(<DiffView patch={SAMPLE_PATCH} />)
    expect(screen.queryByRole('button')).not.toBeInTheDocument()
  })

  it('offers Keep and Drop per hunk and reports which one was pressed', () => {
    const onKeep = vi.fn()
    const onDrop = vi.fn()
    render(<DiffView patch={SAMPLE_PATCH} editable onKeep={onKeep} onDrop={onDrop} />)
    expect(screen.getAllByRole('button', { name: 'Keep' })).toHaveLength(3)
    const second = screen.getByRole('region', { name: 'internal/export/statement.go hunk 2' })
    fireEvent.click(within(second).getByRole('button', { name: 'Drop' }))
    expect(onDrop).toHaveBeenCalledWith('internal/export/statement.go', 1)
    const test = screen.getByRole('region', { name: 'internal/export/statement_test.go hunk 1' })
    fireEvent.click(within(test).getByRole('button', { name: 'Keep' }))
    expect(onKeep).toHaveBeenCalledWith('internal/export/statement_test.go', 0)
  })

  it('shows a kept hunk as Kept, pressed', () => {
    render(
      <DiffView
        patch={SAMPLE_PATCH}
        editable
        decisions={{ [hunkKey('internal/export/statement.go', 0)]: 'kept' }}
      />,
    )
    const kept = screen.getByRole('button', { name: 'Kept' })
    expect(kept).toHaveAttribute('aria-pressed', 'true')
    expect(screen.getAllByRole('button', { name: 'Keep' })).toHaveLength(2)
  })

  it('waits on the hunk whose drop is in flight and leaves the others live', () => {
    render(
      <DiffView
        patch={SAMPLE_PATCH}
        editable
        dropping={hunkKey('internal/export/statement.go', 0)}
      />,
    )
    const first = screen.getByRole('region', { name: 'internal/export/statement.go hunk 1' })
    expect(within(first).getByRole('button', { name: 'Dropping…' })).toHaveAttribute(
      'aria-disabled',
      'true',
    )
    expect(within(first).getByRole('button', { name: 'Keep' })).toBeDisabled()
    expect(screen.getAllByRole('button', { name: 'Drop' })).toHaveLength(2)
  })

  it('says so instead of drawing a split view', () => {
    render(<DiffView patch={SAMPLE_PATCH} mode="split" />)
    expect(screen.getByText(/Split view is next/)).toBeInTheDocument()
    expect(screen.queryByRole('article')).not.toBeInTheDocument()
  })

  it('says the change is empty rather than drawing nothing', () => {
    render(<DiffView patch="" />)
    expect(screen.getByText('The change is empty.')).toBeInTheDocument()
  })

  it('marks the active file and notes a truncated patch', () => {
    render(<DiffView patch={SAMPLE_PATCH} activePath="internal/export/statement_test.go" truncated />)
    expect(
      screen.getByRole('article', { name: 'internal/export/statement_test.go' }),
    ).toHaveAttribute('data-active', 'true')
    expect(screen.getByRole('status')).toHaveTextContent('cut at its size limit')
  })

  it('is always left-to-right', () => {
    const { container } = render(
      <div dir="rtl">
        <DiffView patch={SAMPLE_PATCH} />
      </div>,
    )
    expect(container.querySelector('.sd-diff')).toHaveAttribute('dir', 'ltr')
  })
})
