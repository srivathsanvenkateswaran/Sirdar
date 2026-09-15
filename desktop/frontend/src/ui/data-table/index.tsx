import { Fragment, type ReactNode } from 'react'
import './DataTable.css'

export type SortDirection = 'asc' | 'desc'

export interface DataColumn<Row> {
  /**
   * Also written onto every header and body cell as `data-col`, so a screen's
   * own stylesheet can narrow or drop one column at a width without the table
   * knowing anything about it.
   */
  id: string
  header: ReactNode
  /** The cell. Returning a string is the common case. */
  cell: (row: Row) => ReactNode
  /** Numbers, keys, ids and clocks take the ledger face and tabular figures. */
  numeric?: boolean
  sortable?: boolean
  /** A fixed width, when a column would otherwise shift as rows load. */
  width?: string
}

export interface DataTableProps<Row> {
  /** Says what the table lists. Read out before the first row. */
  caption: string
  columns: DataColumn<Row>[]
  rows: Row[]
  rowKey: (row: Row) => string
  sort?: { columnId: string; direction: SortDirection }
  onSort?: (columnId: string) => void
  /** Prose for a table with nothing in it. */
  empty: ReactNode
  /**
   * A block of prose about one row — why a check did not hold, what a run
   * stopped on — drawn full width underneath it. Returning nothing draws
   * nothing, which is the common case.
   */
  detail?: (row: Row) => ReactNode
}

/** The `aria-sort` value each direction is announced as. */
const ANNOUNCED: Record<SortDirection, 'ascending' | 'descending'> = {
  asc: 'ascending',
  desc: 'descending',
}

/**
 * A table of facts: the register, the eval sheet, a list of deliveries.
 *
 * Rows separate with a hairline and nothing else, and every number in it is
 * monospace with tabular figures, so a column of costs lines up on the decimal
 * and a column of keys can be scanned character by character.
 *
 * The header is sticky and the whole table sits in its own horizontal scroll
 * container: a wide register scrolls inside its panel, and the page body never
 * scrolls sideways underneath the board.
 */
export default function DataTable<Row>({
  caption,
  columns,
  rows,
  rowKey,
  sort,
  onSort,
  empty,
  detail,
}: DataTableProps<Row>): JSX.Element {
  if (rows.length === 0) {
    return (
      <div className="sd-table__empty">
        <p className="sd-table__empty-text">{empty}</p>
      </div>
    )
  }

  return (
    <div className="sd-table__scroll" tabIndex={0} role="group" aria-label={caption}>
      <table className="sd-table">
        <caption className="sd-table__caption">{caption}</caption>
        <thead>
          <tr>
            {columns.map((column) => {
              const active = sort?.columnId === column.id
              return (
                <th
                  key={column.id}
                  scope="col"
                  data-col={column.id}
                  style={column.width ? { width: column.width } : undefined}
                  data-numeric={column.numeric ? 'true' : undefined}
                  aria-sort={
                    active ? ANNOUNCED[sort.direction] : column.sortable ? 'none' : undefined
                  }
                >
                  {column.sortable && onSort ? (
                    <button
                      type="button"
                      className="sd-table__sort"
                      onClick={() => onSort(column.id)}
                    >
                      {column.header}
                      <span className="sd-table__arrow" aria-hidden="true">
                        {active ? (sort.direction === 'asc' ? '↑' : '↓') : ''}
                      </span>
                    </button>
                  ) : (
                    column.header
                  )}
                </th>
              )
            })}
          </tr>
        </thead>
        <tbody>
          {rows.map((row) => {
            const why = detail?.(row)
            return (
              <Fragment key={rowKey(row)}>
                <tr>
                  {columns.map((column) => (
                    <td
                      key={column.id}
                      data-col={column.id}
                      data-numeric={column.numeric ? 'true' : undefined}
                    >
                      {column.numeric ? (
                        <span dir="ltr">{column.cell(row)}</span>
                      ) : (
                        column.cell(row)
                      )}
                    </td>
                  ))}
                </tr>
                {why ? (
                  <tr className="sd-table__detail">
                    <td colSpan={columns.length}>{why}</td>
                  </tr>
                ) : null}
              </Fragment>
            )
          })}
        </tbody>
      </table>
    </div>
  )
}
