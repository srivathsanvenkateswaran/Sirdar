import { memo, useMemo, type MouseEvent } from 'react'
import type { Marker as MarkerModel } from '../../lib/evidence'
import { bytes, shapeCount, shapeOutput, type OutputShape } from '../../lib/toolOutput'
import Button from '../../ui/button'
import Marker from '../../ui/marker'
import { took, type SessionStep } from './model'
import Stamp, { stepStamp } from './Stamp'

export interface ToolStepProps {
  step: SessionStep
  /** The markers sitting on this step, E and C alike. */
  markers?: MarkerModel[]
  /** The marker whose twin is in view; the chip lights and the card outlines. */
  hotMarker?: string
  open: boolean
  onToggle: (index: number) => void
  onMarker?: (id: string, event: MouseEvent<HTMLElement>) => void
  /** Opens the same call in the Tools table. */
  onOpenInTools?: (index: number) => void
  /** `file:line` references the answer cites, to light the rows that hold them. */
  hotRefs?: string[]
  /** The run is live, so a call without a result is running rather than lost. */
  live?: boolean
}

/** The expanded output, by its shape. */
export function OutputView({ shape, hotRefs = [] }: { shape: OutputShape; hotRefs?: string[] }): JSX.Element | null {
  const hot = new Set(hotRefs.map((r) => r.toLowerCase()))
  switch (shape.kind) {
    case 'empty':
      return null
    case 'table':
      return (
        <div className="sn-otbl">
          <div className="sn-scroll">
            <table>
              <thead>
                <tr>
                  {shape.columns.map((c) => (
                    <th key={c} scope="col">
                      {c}
                    </th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {shape.rows.map((row, i) => (
                  <tr key={i} data-hit={row.ref && hot.has(row.ref.toLowerCase()) ? 'true' : undefined} data-head={row.head ? 'true' : undefined}>
                    {row.cells.map((cell, c) => (
                      <td key={c} className={c === 0 ? 'f' : 't'} title={c > 0 && cell.length > 40 ? cell : undefined}>
                        {cell}
                      </td>
                    ))}
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      )
    case 'lines':
      return (
        <div className="sn-lines" role="table" aria-label="File contents">
          {shape.rows.map((row) => {
            const hit = hotRefs.some((r) => /:(\d+)$/.test(r) && Number(/:(\d+)$/.exec(r)?.[1]) === row.n) ? 'true' : undefined
            return [
              <span key={`n${row.n}`} className="sn-lines__n" data-hit={hit}>
                {row.n}
              </span>,
              <span key={`c${row.n}`} className="sn-lines__c" data-hit={hit}>
                {row.text}
              </span>,
            ]
          })}
        </div>
      )
    case 'pre':
      return (
        <pre className="sn-pre">
          {shape.lines.map((l, i) => (
            <span key={i} className="sn-pre__ln" data-tone={l.tone}>
              {l.text}
              {'\n'}
            </span>
          ))}
        </pre>
      )
  }
}

/**
 * One tool call as a step on the path. Collapsed it is one line — "Read
 * ledger.go · 65 lines", "Ran rg … · 17 matches", "denied" with the policy's
 * reason under it — and the line is the button that opens it. Open, the step
 * sits on a card with the input verbatim and the output by shape: a table
 * for rg, ls and git --stat, numbered lines for a read, a mono block with
 * the failing line in the failed hue for a test, each capped at 296px with
 * its own scrollbar, and a footer with the rows, the size, the time and the
 * model's one-line reading of it, plus "Open in Tools".
 */
function ToolStep({
  step,
  markers = [],
  hotMarker,
  open,
  onToggle,
  onMarker,
  onOpenInTools,
  hotRefs,
  live = false,
}: ToolStepProps): JSX.Element {
  const stamp = stepStamp(step)
  const shape = useMemo<OutputShape | undefined>(
    () => (open && step.output !== undefined ? shapeOutput(step.tool, step.command ?? '', step.output) : undefined),
    [open, step.tool, step.command, step.output],
  )
  const hot = hotMarker !== undefined && markers.some((m) => m.id === hotMarker)
  const count = shape ? shapeCount(shape) : undefined
  const waiting = step.state === 'waiting'

  const line = (
    <button
      type="button"
      className="sn-step"
      data-state={step.state}
      data-kind={step.kind}
      data-index={step.index}
      aria-expanded={open}
      onClick={() => onToggle(step.index)}
      data-testid="tool-step"
    >
      <span className="sn-step__at">{step.at}</span>
      <span className="sn-step__what">
        <span className="sn-step__verb">{step.verb}</span> <span className="sn-step__obj">{step.object}</span>
        {step.result ? <span className="sn-step__res"> · {step.result}</span> : null}
        {!step.result && step.state === 'running' && live ? <span className="sn-step__res"> · running</span> : null}
      </span>
      <span className="sn-step__side">
        {stamp ? <Stamp tone={stamp.tone}>{stamp.word}</Stamp> : null}
        {step.state === 'failed' && step.kind !== 'shell' ? <Stamp tone="fail">failed</Stamp> : null}
        {markers.map((m) => (
          <Marker key={m.id} id={m.id} hot={m.id === hotMarker} onClick={onMarker} title={m.query || m.path} />
        ))}
      </span>
      {step.state === 'denied' && step.reason ? <span className="sn-step__reason">{step.reason}</span> : null}
    </button>
  )

  if (!open) return line

  return (
    <div className="sn-xstep" data-hot={hot ? 'true' : undefined} data-step={step.index}>
      {line}
      <div className="sn-io">
        <div className="sn-io__lab">{waiting ? 'Asks to run' : 'Input'}</div>
        <pre className="sn-io__cmd">{step.input}</pre>
        {waiting ? (
          <>
            <div className="sn-io__lab">Policy</div>
            <p className="sn-io__said">
              {step.reason ? `${step.reason}. ` : ''}
              The harness paused the run and asked.
              {step.rule ? (
                <>
                  {' '}
                  The agent suggests allowing <code>{step.rule}</code> for this session.
                </>
              ) : null}
            </p>
          </>
        ) : null}
        {step.state === 'denied' ? (
          <>
            <div className="sn-io__lab">Policy</div>
            <p className="sn-io__said">{step.reason || 'The policy refused this call.'}</p>
          </>
        ) : null}
        {!waiting && step.state !== 'denied' ? (
          <>
            <div className="sn-io__lab">
              Output
              {count && count.n > 0 ? ` · ${count.n} ${count.noun}${count.n === 1 ? '' : 's'}` : ''}
              {step.outputBytes > 0 ? ` · ${bytes(step.outputBytes)}` : ''}
              {step.durationMs !== undefined ? ` · ${took(step.durationMs)}` : ''}
            </div>
            {shape && shape.kind !== 'empty' ? (
              <OutputView shape={shape} hotRefs={hotRefs} />
            ) : (
              <p className="sn-io__none">
                {step.output !== undefined
                  ? 'The tool returned nothing.'
                  : live || step.state === 'running'
                    ? 'Waiting for the result.'
                    : 'No result was recorded.'}
              </p>
            )}
          </>
        ) : null}
        {(step.description || onOpenInTools) && !waiting ? (
          <div className="sn-io__foot">
            {step.description ? <span>{step.description}</span> : null}
            {onOpenInTools ? (
              <Button variant="ghost" size="sm" onClick={() => onOpenInTools(step.index)}>
                Open in Tools
              </Button>
            ) : null}
          </div>
        ) : null}
      </div>
    </div>
  )
}

export default memo(ToolStep)
