import { fireEvent, render, screen } from '@testing-library/react'
import type { ComponentProps } from 'react'
import { describe, expect, it, vi } from 'vitest'
import DataTable, { type DataColumn } from './index'

interface Row {
  key: string
  title: string
  cost: string
}

const ROWS: Row[] = [
  { key: 'OMNI-2510', title: 'Statement export times out', cost: '$0.42' },
  { key: 'OMNI-2511', title: 'العميل لا يستطيع تسجيل الدخول', cost: '$1.08' },
]

const COLUMNS: DataColumn<Row>[] = [
  { id: 'key', header: 'Key', cell: (r) => r.key, sortable: true },
  { id: 'title', header: 'Ticket', cell: (r) => <span dir="auto">{r.title}</span> },
  { id: 'cost', header: 'Cost', cell: (r) => r.cost, numeric: true, sortable: true },
]

function table(props: Partial<ComponentProps<typeof DataTable<Row>>> = {}) {
  return (
    <DataTable
      caption="Runs in this workspace"
      columns={COLUMNS}
      rows={ROWS}
      rowKey={(r) => r.key}
      empty="No run has been recorded in this workspace yet."
      {...props}
    />
  )
}

describe('DataTable', () => {
  it('says what it lists', () => {
    render(table())
    expect(screen.getByRole('table', { name: 'Runs in this workspace' })).toBeInTheDocument()
  })

  it('names the column on every cell, so a screen can hide one at a width', () => {
    render(table())
    expect(screen.getByRole('columnheader', { name: 'Ticket' })).toHaveAttribute(
      'data-col',
      'title',
    )
    const cells = screen.getAllByRole('cell').filter((c) => c.getAttribute('data-col') === 'cost')
    expect(cells.map((c) => c.textContent)).toEqual(['$0.42', '$1.08'])
  })

  it('renders every row once', () => {
    render(table())
    expect(screen.getAllByRole('row')).toHaveLength(3)
    expect(screen.getByText('OMNI-2510')).toBeInTheDocument()
  })

  it('offers prose, not an empty grid, when there is nothing to show', () => {
    render(table({ rows: [] }))
    expect(screen.queryByRole('table')).toBeNull()
    expect(
      screen.getByText('No run has been recorded in this workspace yet.'),
    ).toBeInTheDocument()
  })

  it('announces which column is sorted and which way', () => {
    render(table({ sort: { columnId: 'cost', direction: 'desc' }, onSort: () => {} }))
    expect(screen.getByRole('columnheader', { name: /Cost/ })).toHaveAttribute(
      'aria-sort',
      'descending',
    )
    expect(screen.getByRole('columnheader', { name: 'Ticket' })).not.toHaveAttribute('aria-sort')
  })

  it('reports a sort from a header button, by keyboard as well as mouse', () => {
    const onSort = vi.fn()
    render(table({ onSort }))
    const header = screen.getByRole('button', { name: /Key/ })
    header.focus()
    expect(header).toHaveFocus()
    fireEvent.click(header)
    expect(onSort).toHaveBeenCalledWith('key')
  })

  it('leaves an unsortable column without a button', () => {
    render(table({ onSort: () => {} }))
    expect(screen.queryByRole('button', { name: 'Ticket' })).toBeNull()
  })

  it('keeps numbers left to right and lets titles resolve their own direction', () => {
    render(
      <div dir="rtl">
        {table()}
      </div>,
    )
    expect(screen.getByText('$1.08')).toHaveAttribute('dir', 'ltr')
    expect(screen.getByText('العميل لا يستطيع تسجيل الدخول')).toHaveAttribute('dir', 'auto')
  })

  it('can be scrolled from the keyboard', () => {
    render(table())
    const scroller = screen.getByRole('group', { name: 'Runs in this workspace' })
    scroller.focus()
    expect(scroller).toHaveFocus()
  })
})
