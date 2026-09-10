import { useState } from 'react'
import type { RunEvent } from '../../api/types'
import { inputJSON, inputSummary, toolLabel, truncate } from '../../lib/events'
import { Row } from './EventRow'

/**
 * A tool call or its result. The one-line summary is what the engineer scans
 * for — the Bash command, the file read, the MCP server and tool — and the full
 * arguments stay behind a disclosure so the column keeps its shape.
 */
export default function ToolCallRow({ event, at }: { event: RunEvent; at: string }) {
  const [open, setOpen] = useState(false)
  const started = event.kind === 'tool_started'
  const tool = toolLabel(event.payload?.tool ?? '')
  const json = inputJSON(event)
  const summary = started
    ? inputSummary(event)
    : truncate(event.payload?.text ?? '', 100) || 'done'

  return (
    <Row at={at} glyph={started ? '▸' : '◂'} variant="tool">
      <div className="ev-line">
        {started ? <span className="ev-tool">{tool || 'tool'}</span> : null}
        <span className="ev-summary" title={summary}>
          {summary}
        </span>
        {json ? (
          <button
            type="button"
            className="ev-toggle"
            aria-expanded={open}
            onClick={() => setOpen((v) => !v)}
          >
            {open ? 'hide input' : 'input'}
          </button>
        ) : null}
      </div>
      {open && json ? <pre className="ev-json">{json}</pre> : null}
    </Row>
  )
}
