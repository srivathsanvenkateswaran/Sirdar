import { useState } from 'react'
import { detectTable, type TextTable } from '../../lib/events'

/** How many lines a block shows before it asks. */
export const LINE_CAP = 40

/**
 * A tool's input or output, in full but not all at once: the first forty
 * lines, then a "Show all" that says how many there are. A result shaped like
 * a table — pipes or tabs, the same cells on every row — is drawn as one, with
 * the same cap on its rows, because a query result read as a wall of pipes
 * is the thing the ellipsised ledger used to hide.
 *
 * Mono at the ledger size, `white-space: pre-wrap` so a long line wraps
 * inside the pane rather than widening it.
 */
export default function CappedBlock({
  text,
  label,
  table = false,
}: {
  text: string
  /** Names the block for a screen reader; the visible heading is the caller's. */
  label: string
  /** Try to read the text as a table first. */
  table?: boolean
}) {
  const [all, setAll] = useState(false)
  const shaped: TextTable | undefined = table ? detectTable(text) : undefined
  const lines = text.split('\n')
  const total = shaped ? shaped.rows.length : lines.length
  const capped = total > LINE_CAP && !all
  const more = (
    <button type="button" className="call-more" onClick={() => setAll((v) => !v)}>
      {all ? 'Show less' : `Show all (${total} ${shaped ? 'rows' : 'lines'})`}
    </button>
  )

  if (shaped) {
    const rows = capped ? shaped.rows.slice(0, LINE_CAP) : shaped.rows
    return (
      <div className="call-block" aria-label={label}>
        <div className="call-table-wrap">
          <table className="call-table">
            <thead>
              <tr>
                {shaped.head.map((h, i) => (
                  <th key={i} scope="col">
                    {h}
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {rows.map((row, r) => (
                <tr key={r}>
                  {row.map((cell, c) => (
                    <td key={c}>{cell}</td>
                  ))}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
        {total > LINE_CAP ? more : null}
      </div>
    )
  }

  const shown = capped ? lines.slice(0, LINE_CAP).join('\n') : text
  return (
    <div className="call-block" aria-label={label}>
      <pre className="call-pre">{shown}</pre>
      {total > LINE_CAP ? more : null}
    </div>
  )
}
