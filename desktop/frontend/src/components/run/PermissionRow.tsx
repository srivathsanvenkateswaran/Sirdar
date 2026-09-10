import { useState } from 'react'
import type { RunEvent } from '../../api/types'
import { inputJSON, inputSummary, toolLabel } from '../../lib/events'
import { Row } from './EventRow'

/**
 * What the policy did with a tool the agent asked to use. A denial is the most
 * useful line in a triage run — it says the agent tried to leave the read-only
 * envelope — so it takes the warning rail and shows the policy message. An
 * allow is routine and stays muted.
 */
export default function PermissionRow({ event, at }: { event: RunEvent; at: string }) {
  const [open, setOpen] = useState(false)
  const denied = event.payload?.decision === 'deny'
  const tool = toolLabel(event.payload?.tool ?? '')
  const message = event.payload?.text ?? ''
  const summary = inputSummary(event)
  const json = inputJSON(event)

  return (
    <Row at={at} glyph={denied ? '✕' : '✓'} variant={denied ? 'deny' : 'allow'}>
      <div className="ev-line">
        <span className="ev-tool">{tool || 'permission'}</span>
        <span className={denied ? 'ev-reason' : 'ev-summary'}>
          {denied ? message || 'denied' : `allowed${summary ? ` ${summary}` : ''}`}
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
      {denied && summary ? (
        <div className="ev-summary" title={summary}>
          {summary}
        </div>
      ) : null}
      {open && json ? <pre className="ev-json">{json}</pre> : null}
    </Row>
  )
}
