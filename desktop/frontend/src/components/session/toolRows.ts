import type { ToolRow } from './ToolsPane'
import type { SessionStep } from './model'

/*
 * The Document and Workbench layouts' own step model, mapped onto the three
 * cells the shared Tools pane draws. The Conversation layout has its own
 * call model and its own mapper beside its tab; both end up here, which is
 * what keeps a call reading the same in all three.
 */

/** What decided a call, as the shared pane names it. */
export function decisionOf(step: SessionStep): ToolRow['decision'] {
  if (step.state === 'denied') return 'denied'
  if (step.state === 'waiting') return 'waiting'
  if (step.decision === 'allow') return 'approved'
  return 'policy'
}

/**
 * The call's one line: the model's own description of why it made the call,
 * falling back to what it called it on. The description is the useful half
 * — `rg -n "Return|restock" --type go` says what ran, "Search Go code for
 * partial-return handling" says what for.
 */
function summaryOf(step: SessionStep): string {
  return step.description || [step.verb, step.object].filter(Boolean).join(' ')
}

export function rowsOf(steps: SessionStep[]): ToolRow[] {
  return steps.map((s) => ({
    index: s.index,
    tool: s.tool,
    summary: summaryOf(s),
    decision: decisionOf(s),
    tookMs: s.durationMs,
    pending: s.state === 'running' || s.state === 'waiting',
    at: s.at,
    input: s.input,
    output: s.output,
    outputBytes: s.outputBytes,
  }))
}
