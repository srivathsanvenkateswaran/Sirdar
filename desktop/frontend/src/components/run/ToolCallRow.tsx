import { useEffect, useState } from 'react'
import {
  callDuration,
  inputJSON,
  inputSummary,
  offsetLabel,
  outputFailed,
  outputText,
  toolLabel,
  type ToolCall,
} from '../../lib/events'
import CappedBlock from './CappedBlock'
import { Row } from './EventRow'

/**
 * One tool call: what the agent asked for, what the policy said, what came
 * back. Collapsed it is one line — the tool, a one-line summary of its
 * input, the decision word when a permission was recorded, how long it
 * took — and the whole line is the button that opens it. Open, it shows the
 * input as pretty-printed JSON and the output in full, each capped at forty
 * lines behind a "Show all", and a denied call leads with the policy's
 * reason.
 *
 * `defaultOpen` is the stream's say: the last call of a run and any denied
 * one start open. It is followed until the reader clicks, and from then on
 * the reader's choice holds — a row opened because it was last stays open
 * when the next call lands, and one closed by hand stays closed.
 */
export default function ToolCallRow({
  call,
  startedAt,
  defaultOpen = false,
  live = false,
}: {
  call: ToolCall
  startedAt: string | undefined
  defaultOpen?: boolean
  /** The run is still working, so a call without a result is running rather than lost. */
  live?: boolean
}) {
  const [open, setOpen] = useState(defaultOpen)
  const [touched, setTouched] = useState(false)
  useEffect(() => {
    if (!touched) setOpen(defaultOpen)
  }, [defaultOpen, touched])

  const started = call.started.event
  const at = offsetLabel(started.t, startedAt)
  const tool = toolLabel(started.payload?.tool ?? '') || 'tool'
  const summary = inputSummary(started)
  const input = inputJSON(started)
  const decision = call.permission?.event.payload?.decision ?? ''
  const denied = decision === 'deny'
  const reason = call.permission?.event.payload?.text ?? ''
  const output = outputText(call.finished?.event)
  const failed = !denied && outputFailed(call.finished?.event)
  const took = callDuration(call)
  const state = call.finished ? took : live ? 'running' : ''

  const toggle = () => {
    setTouched(true)
    setOpen((v) => !v)
  }

  return (
    <Row at={at} glyph={denied ? '✕' : '›'} variant={denied ? 'deny' : 'tool'}>
      <button
        type="button"
        className="call"
        aria-expanded={open}
        onClick={toggle}
        data-testid="tool-call"
      >
        <span className="call-caret" aria-hidden="true">
          {open ? '▾' : '▸'}
        </span>
        <span className="ev-tool">{tool}</span>
        <span className="ev-summary" title={summary}>
          {summary}
        </span>
        {decision ? (
          <span className="call-word" data-decision={decision}>
            {decision === 'deny' ? 'denied' : 'allowed'}
          </span>
        ) : null}
        {failed ? (
          <span className="call-word" data-decision="failed">
            failed
          </span>
        ) : null}
        {state ? <span className="call-time">{state}</span> : null}
      </button>
      {open ? (
        <div className="call-body">
          {denied ? <div className="ev-reason">{reason || 'The policy refused this call.'}</div> : null}
          {input ? (
            <section className="call-section">
              <div className="call-label">Input</div>
              <CappedBlock text={input} label={`${tool} input`} />
            </section>
          ) : null}
          <section className="call-section">
            <div className="call-label">Output</div>
            {output ? (
              <CappedBlock text={output} label={`${tool} output`} table />
            ) : (
              <p className="call-none">
                {call.finished
                  ? 'The tool returned nothing.'
                  : live
                    ? 'Waiting for the result.'
                    : 'No result was recorded.'}
              </p>
            )}
          </section>
        </div>
      ) : null}
    </Row>
  )
}
