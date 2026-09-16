import { useEffect, useRef, useState, type ReactNode } from 'react'
import { ChevronIcon, toolIcon } from './icons'
import type { StepCall } from './model'
import { formatBytes, formatMs, shapeOutput, type Shaped } from './shape'

/*
 * One tool call as a card in the conversation: a 36px line, the button that
 * opens it, and under it the input as a key–value list and the output in
 * its own shape, capped at 262px with its own scrollbar. A denied call
 * carries the policy's reason on a line of its own in the blocked hue,
 * because the denial is the most useful line in a read-only run.
 *
 * Stamps say what the policy did and what the tool came back with — words,
 * never the colour alone.
 */

/** How long the copy control says "Copied" before it goes back to offering. */
const COPIED_MS = 1500

export type StampTone = 'deny' | 'allow' | 'wait' | 'fail' | 'ok' | 'live' | ''

export function Stamp({ tone, children }: { tone: StampTone; children: ReactNode }): JSX.Element {
  return (
    <span className="sc-stamp" data-t={tone || undefined}>
      {children}
    </span>
  )
}

/** The stamps a collapsed card carries at its end, from what the run recorded. */
export function stamps(step: StepCall, live: boolean, blocked: boolean): { tone: StampTone; text: string }[] {
  const out: { tone: StampTone; text: string }[] = []
  if (step.decision === 'denied') out.push({ tone: 'deny', text: 'denied by policy' })
  else if (step.decision === 'accepted') out.push({ tone: 'allow', text: `accepted · ${step.reason || 'accepted'}` })
  else if (step.decision === 'approved') out.push({ tone: 'allow', text: 'approved' })
  if (step.pending) {
    if (blocked) out.push({ tone: 'wait', text: 'waiting for your approval' })
    else if (live) out.push({ tone: 'live', text: 'running' })
    else out.push({ tone: '', text: 'no result' })
  } else if (step.failed) {
    out.push({ tone: 'fail', text: step.exitCode !== undefined ? `exit ${step.exitCode}` : 'failed' })
  } else if (step.exitCode === 0 || (step.decision === 'approved' && !step.failed && /^(?:bash|shell)$/i.test(step.tool))) {
    out.push({ tone: 'ok', text: 'exit 0' })
  }
  return out
}

/** `17 lines`, `+14 −0`, `1 line`. */
function sizeWord(step: StepCall): string {
  if (step.edit) {
    const parts = []
    if (step.edit.added > 0) parts.push(`+${step.edit.added}`)
    if (step.edit.removed > 0) parts.push(`−${step.edit.removed}`)
    return parts.join(' ')
  }
  if (step.pending || step.decision === 'denied') return ''
  return `${step.size.lines} ${step.size.lines === 1 ? 'line' : 'lines'}`
}

function Output({ shaped, raw }: { shaped: Shaped; raw: string }): JSX.Element {
  if (shaped.kind === 'matches') {
    return (
      <table className="sc-ot">
        <thead>
          <tr>
            <th scope="col">file</th>
            <th scope="col" className="sc-ot__n">line</th>
            <th scope="col">match</th>
          </tr>
        </thead>
        <tbody>
          {shaped.rows.map((row, i) => (
            <tr key={i}>
              <td className="sc-ot__f">{row.file}</td>
              <td className="sc-ot__n">{row.line}</td>
              <td className="sc-ot__t" title={row.text.trim()}>
                {row.text.trim()}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    )
  }
  if (shaped.kind === 'lines') {
    return (
      <table className="sc-ot sc-ot--lines">
        <tbody>
          {shaped.rows.map((row) => (
            <tr key={row.n}>
              <td className="sc-ot__n">{row.n}</td>
              <td className="sc-ot__t">{row.text}</td>
            </tr>
          ))}
        </tbody>
      </table>
    )
  }
  if (shaped.kind === 'test') {
    return (
      <pre className="sc-code">
        {shaped.lines.map((line, i) => (
          <span key={i} className={line.tone ? `sc-code__${line.tone}` : undefined}>
            {line.text}
            {'\n'}
          </span>
        ))}
      </pre>
    )
  }
  return <pre className="sc-code">{raw}</pre>
}

/** `17 rows · 1.1 kB · 3 files`, `65 lines · 2.0 kB`. */
function outputLabel(step: StepCall, shaped: Shaped): string {
  const parts: string[] = []
  if (shaped.kind === 'matches') parts.push(`${shaped.rows.length} ${shaped.rows.length === 1 ? 'row' : 'rows'}`)
  else parts.push(`${step.size.lines} ${step.size.lines === 1 ? 'line' : 'lines'}`)
  parts.push(formatBytes(step.size.bytes))
  if (shaped.kind === 'matches') parts.push(`${shaped.files} ${shaped.files === 1 ? 'file' : 'files'}`)
  return parts.join(' · ')
}

/** The Input section's rows: what the tool was asked, key by key, verbatim. */
function inputRows(step: StepCall): { key: string; value: string; prose?: boolean }[] {
  const input = step.input
  if (!input) return step.summary ? [{ key: 'input', value: step.summary }] : []
  const rows: { key: string; value: string; prose?: boolean }[] = []
  for (const [key, value] of Object.entries(input)) {
    if (value === undefined || value === null || value === '') continue
    if (key === 'description') {
      rows.push({ key, value: String(value), prose: true })
      continue
    }
    const text = typeof value === 'string' ? value : JSON.stringify(value)
    rows.push({ key, value: text.length > 400 ? `${text.slice(0, 399)}…` : text })
  }
  return rows
}

export interface ToolStepProps {
  step: StepCall
  open: boolean
  onToggle: () => void
  /** Picked from the Tools table or an evidence reference: tinted until the next pick. */
  highlighted?: boolean
  /** The run is working, so a call without a result is running rather than lost. */
  live?: boolean
  /** The run is waiting on the operator, so a call without a result is the one waiting. */
  blocked?: boolean
  /** Opens the Tools tab on this call. */
  onOpenInTools?: (step: StepCall) => void
}

export default function ToolStep({
  step,
  open,
  onToggle,
  highlighted = false,
  live = false,
  blocked = false,
  onOpenInTools,
}: ToolStepProps): JSX.Element {
  const [asText, setAsText] = useState(false)
  const [copied, setCopied] = useState(false)
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null)
  useEffect(
    () => () => {
      if (timer.current) clearTimeout(timer.current)
    },
    [],
  )

  const command = step.summary
  const shaped = shapeOutput(step.tool, command, step.output)
  const shown: Shaped = asText ? { kind: 'text', text: step.output } : shaped
  const marks = stamps(step, live, blocked)
  const size = sizeWord(step)
  const took = step.tookMs !== undefined && !step.pending ? formatMs(step.tookMs) : ''
  const denied = step.decision === 'denied'

  async function copy(): Promise<void> {
    try {
      await navigator.clipboard?.writeText(step.output)
      setCopied(true)
      if (timer.current) clearTimeout(timer.current)
      timer.current = setTimeout(() => setCopied(false), COPIED_MS)
    } catch {
      // Nothing to say: the button simply does not flip.
    }
  }

  return (
    <div
      className="sc-tc-wrap"
      data-call={step.index}
      data-on={highlighted ? 'true' : undefined}
      data-denied={denied ? 'true' : undefined}
      data-testid="tool-step"
    >
      <button
        type="button"
        className="sc-tc"
        aria-expanded={open}
        onClick={onToggle}
        title={step.description || step.summary}
      >
        {toolIcon(step.tool)}
        <span className="sc-tc__tn">{step.tool}</span>
        {step.description ? <span className="sc-tc__td">{step.description}</span> : null}
        <span className="sc-tc__ts" dir="ltr">
          {step.summary}
        </span>
        <span className="sc-tc__tr">
          {marks.map((m, i) => (
            <Stamp key={i} tone={m.tone}>
              {m.text}
            </Stamp>
          ))}
          {size ? <span>{size}</span> : null}
          {took ? <span>{took}</span> : null}
          {step.pending || denied ? <span>{step.at}</span> : null}
          <ChevronIcon />
        </span>
      </button>
      {denied && step.reason ? <div className="sc-tc-why">{step.reason}</div> : null}
      {open ? (
        <div className="sc-tc-x">
          <section className="sc-tc-x__sec">
            <div className="sc-tc-x__lab">
              Input <span className="sc-mono">{step.tool}</span>
            </div>
            <dl className="sc-kv">
              {inputRows(step).map((row) => (
                <div key={row.key} className="sc-kv__row">
                  <dt>{row.key}</dt>
                  <dd className={row.prose ? 'sc-kv__say' : undefined} dir={row.prose ? 'auto' : 'ltr'}>
                    {row.value}
                  </dd>
                </div>
              ))}
              {step.description ? null : null}
            </dl>
          </section>
          <section className="sc-tc-x__sec">
            <div className="sc-tc-x__lab">
              Output
              {step.output ? <span className="sc-mono">{outputLabel(step, shaped)}</span> : null}
              {step.output ? (
                <span className="sc-tc-x__acts">
                  {shaped.kind !== 'text' ? (
                    <button type="button" className="sc-link" aria-pressed={asText} onClick={() => setAsText((v) => !v)}>
                      {asText ? 'As table' : 'As text'}
                    </button>
                  ) : null}
                  <button type="button" className="sc-link" onClick={() => void copy()}>
                    {copied ? 'Copied' : 'Copy'}
                  </button>
                  {onOpenInTools ? (
                    <button type="button" className="sc-link" onClick={() => onOpenInTools(step)}>
                      Open in Tools
                    </button>
                  ) : null}
                </span>
              ) : null}
            </div>
            {step.output ? (
              <div className="sc-oscroll" tabIndex={0} aria-label={`${step.tool} output`}>
                <Output shaped={shown} raw={step.output} />
              </div>
            ) : (
              <p className="sc-tc-x__none">
                {step.pending
                  ? blocked
                    ? 'Waiting for your approval.'
                    : live
                      ? 'Waiting for the result.'
                      : 'No result was recorded.'
                  : 'The tool returned nothing.'}
              </p>
            )}
          </section>
        </div>
      ) : null}
    </div>
  )
}

/**
 * Consecutive calls as one stack of cards. A stack long enough gets a
 * summary row — `8 calls · 00:03 – 00:10 · all within policy` — so eight
 * reads in a row read as one thing that happened.
 */
export function ToolStack({
  calls,
  head,
  openIndex,
  onToggle,
  highlighted,
  live,
  blocked,
  onOpenInTools,
}: {
  calls: StepCall[]
  head?: string
  /** The call that is open, by its event index; one per layout. */
  openIndex: number
  onToggle: (index: number) => void
  highlighted?: number
  live?: boolean
  blocked?: boolean
  onOpenInTools?: (step: StepCall) => void
}): JSX.Element {
  return (
    <div className="sc-tcs" role="group" aria-label={head ?? `${calls.length} ${calls.length === 1 ? 'call' : 'calls'}`}>
      {head ? <div className="sc-tcs__head">{head}</div> : null}
      {calls.map((step) => (
        <ToolStep
          key={step.index}
          step={step}
          open={openIndex === step.index}
          onToggle={() => onToggle(step.index)}
          highlighted={highlighted === step.index}
          live={live}
          blocked={blocked}
          onOpenInTools={onOpenInTools}
        />
      ))}
    </div>
  )
}
