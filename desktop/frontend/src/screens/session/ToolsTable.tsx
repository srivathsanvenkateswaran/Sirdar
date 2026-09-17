import { useMemo, type MouseEvent } from 'react'
import type { Marker as MarkerModel } from '../../lib/evidence'
import ToolsPane, { type ToolRow } from '../../components/session/ToolsPane'
import type { Decision, StepCall } from './model'

/*
 * The Conversation layout's Tools tab: the shared pane, fed from this
 * layout's own call model.
 *
 * The eight-column table this replaced — `#`, at, tool, input, decision,
 * cites, took, output — was drawn in a pane 30% of a window wide, under a
 * totals line reading "41 calls · 38 by policy · 0 denied · 3 asked you".
 * Three columns and one headline is what a support engineer actually reads
 * off it; everything else is one click away in the row's drawer.
 */

/** A call's decision as the shared pane names it. */
function decisionOf(decision: Decision, pending: boolean): ToolRow['decision'] {
  switch (decision) {
    case 'denied':
      return 'denied'
    case 'approved':
    case 'accepted':
      return pending ? 'waiting' : 'approved'
    default:
      return 'policy'
  }
}

export function rowsOf(calls: StepCall[]): ToolRow[] {
  return calls.map((c) => ({
    index: c.index,
    tool: c.tool,
    summary: c.description || c.summary,
    decision: decisionOf(c.decision, c.pending),
    tookMs: c.tookMs,
    pending: c.pending,
    at: c.at,
    input: c.summary,
    output: c.output,
    outputBytes: c.size.bytes,
  }))
}

export interface ToolsTableProps {
  calls: StepCall[]
  /** The call tinted as the transcript's twin, by its event index. */
  highlighted?: number
  onLocate?: (index: number) => void
  /** The evidence markers (E1…En) derived from the answer. */
  markers?: MarkerModel[]
  hotMarker?: string
  onMarker?: (id: string, event: MouseEvent<HTMLElement>) => void
}

export default function ToolsTable({ calls, highlighted, onLocate, markers, hotMarker, onMarker }: ToolsTableProps): JSX.Element {
  const rows = useMemo(() => rowsOf(calls), [calls])
  return <ToolsPane rows={rows} markers={markers} hotMarker={hotMarker} onMarker={onMarker} onLocate={onLocate} highlighted={highlighted} />
}
