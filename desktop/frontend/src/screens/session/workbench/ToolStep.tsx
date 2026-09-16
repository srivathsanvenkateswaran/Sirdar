import { useMemo, useState, type JSX } from 'react'
import { inputJSON, outputText } from '../../../lib/events'
import { bytesLabel, byteLength, callIdOf, lineCount, type ConsoleRow } from './model'
import { shapeLabel, shapeOutput, tokenizeJSON, type OutputShape } from './shapes'

/**
 * One call, expanded in place under its console row: a two-pane box with
 * the input as coloured JSON and the call's metadata (started, decision and
 * rule, duration, output size) on the left, and on the right the output in
 * the shape it has — a file/line/match table for rg, numbered lines for a
 * Read, a table where the text is one, mono with the failing lines flagged
 * for a test run, plain text otherwise — scrolling on its own, with wrap,
 * copy and raw in its corner. Nothing is cut.
 *
 * A local adapter with the shared `ToolStep`'s name (its `expanded` body);
 * when `src/components/session/ToolStep` lands, this file goes.
 */
export interface ToolStepProps {
  row: ConsoleRow
  /** The rail cell the call sits in, for the metadata: `turn 1 after resume`. */
  turnLabel?: string
  live?: boolean
  onOpenInTools?: () => void
}

function JSONBlock({ text }: { text: string }): JSX.Element {
  const tokens = useMemo(() => tokenizeJSON(text), [text])
  return (
    <pre className="wb-json">
      {tokens.map((t, i) =>
        t.kind === 'punct' ? (
          <span key={i}>{t.text}</span>
        ) : (
          <span key={i} className={`wb-json__${t.kind}`}>
            {t.text}
          </span>
        ),
      )}
    </pre>
  )
}

function Shaped({ shape, wrap }: { shape: OutputShape; wrap: boolean }): JSX.Element {
  switch (shape.kind) {
    case 'grep':
      return (
        <div className="wb-otbl" data-wrap={wrap ? 'true' : undefined} role="table" aria-label="Matches">
          <span className="wb-otbl__hh" role="columnheader">file</span>
          <span className="wb-otbl__hh" role="columnheader">line</span>
          <span className="wb-otbl__hh" role="columnheader">match</span>
          {shape.rows.map((r, i) => (
            <div key={i} className="wb-otbl__row" role="row">
              <span className="wb-otbl__f" role="cell">{r.file}</span>
              <span className="wb-otbl__ln" role="cell">{r.line}</span>
              <span className="wb-otbl__c" role="cell">{r.text}</span>
            </div>
          ))}
        </div>
      )
    case 'lines':
      return (
        <div className="wb-otbl wb-otbl--lines" data-wrap={wrap ? 'true' : undefined} role="table" aria-label="Lines">
          {shape.rows.map((r, i) => (
            <div key={i} className="wb-otbl__row" role="row">
              <span className="wb-otbl__ln" role="cell">{r.n}</span>
              <span className="wb-otbl__c" role="cell">{r.text}</span>
            </div>
          ))}
        </div>
      )
    case 'table':
      return (
        <table className="wb-table" data-wrap={wrap ? 'true' : undefined}>
          <thead>
            <tr>
              {shape.table.head.map((h, i) => (
                <th key={i} scope="col">
                  {h}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {shape.table.rows.map((r, i) => (
              <tr key={i}>
                {r.map((c, j) => (
                  <td key={j}>{c}</td>
                ))}
              </tr>
            ))}
          </tbody>
        </table>
      )
    case 'json':
      return <JSONBlock text={shape.text} />
    case 'test':
      return (
        <pre className="wb-mono-block" data-wrap={wrap ? 'true' : undefined}>
          {shape.lines.map((l, i) => (
            <span key={i} className="wb-mono-line" data-failed={l.failed ? 'true' : undefined}>
              {l.text}
              {'\n'}
            </span>
          ))}
        </pre>
      )
    case 'text':
      return (
        <pre className="wb-mono-block" data-wrap={wrap ? 'true' : undefined}>
          {shape.text}
        </pre>
      )
  }
}

export default function ToolStep({ row, turnLabel, live = false, onOpenInTools }: ToolStepProps): JSX.Element {
  const [wrap, setWrap] = useState(false)
  const [raw, setRaw] = useState(false)
  const [copied, setCopied] = useState(false)
  const call = row.call
  const started = call?.started.event
  const input = started ? inputJSON(started) : ''
  const out = outputText(call?.finished?.event)
  const shape = useMemo(() => shapeOutput(out, { test: row.test }), [out, row.test])
  const id = call ? callIdOf(call) : ''

  const meta: { k: string; v: string }[] = [
    { k: 'started', v: [row.at, turnLabel].filter(Boolean).join(' · ') },
    { k: 'decision', v: [row.decision || (call?.finished ? 'allow' : live ? 'pending' : 'unknown'), row.rule].filter(Boolean).join(' · ') },
    { k: 'duration', v: row.duration || (call?.finished ? '' : live ? 'running' : 'no result recorded') },
    {
      k: 'output',
      v: out ? `${bytesLabel(byteLength(out))} · ${lineCount(out)} lines · ${shape.kind === 'json' ? 'application/json' : 'text/plain'}` : '',
    },
  ].filter((m) => m.v)

  const copy = () => {
    void navigator.clipboard?.writeText(out).then(
      () => {
        setCopied(true)
        setTimeout(() => setCopied(false), 2000)
      },
      () => {},
    )
  }

  return (
    <div className="wb-xp" data-testid="tool-step" aria-label={`${row.tool} call`}>
      <div className="wb-xp__pane">
        <div className="wb-xp__ph">
          <b>Input</b>
          <span className="wb-mono" title={id}>
            {row.tool}
            {id ? ` · ${id.length > 20 ? `${id.slice(0, 20)}…` : id}` : ''}
          </span>
        </div>
        {row.reason ? <div className="wb-xp__reason">{row.reason}</div> : null}
        {input ? <JSONBlock text={input} /> : <p className="wb-xp__none">The call carried no arguments.</p>}
        <dl className="wb-meta">
          {meta.map((m) => (
            <div key={m.k} className="wb-meta__row">
              <dt>{m.k}</dt>
              <dd>{m.v}</dd>
            </div>
          ))}
        </dl>
      </div>
      <div className="wb-xp__pane">
        <div className="wb-xp__ph">
          <b>Output</b>
          <span className="wb-mono">{out ? shapeLabel(shape) : ''}</span>
          <span className="wb-xp__acts">
            <button type="button" className="wb-linkbtn" aria-pressed={wrap} onClick={() => setWrap((v) => !v)}>
              {wrap ? 'wrap on' : 'wrap off'}
            </button>
            <button type="button" className="wb-linkbtn" onClick={copy} disabled={!out}>
              {copied ? 'copied' : 'copy'}
            </button>
            <button type="button" className="wb-linkbtn" aria-pressed={raw} onClick={() => setRaw((v) => !v)} disabled={!out || shape.kind === 'text'}>
              {raw ? 'shaped' : 'open raw'}
            </button>
            {onOpenInTools ? (
              <button type="button" className="wb-linkbtn" onClick={onOpenInTools}>
                Open in Tools
              </button>
            ) : null}
          </span>
        </div>
        <div className="wb-xp__scroll">
          {out ? (
            raw ? (
              <pre className="wb-mono-block" data-wrap={wrap ? 'true' : undefined}>
                {out}
              </pre>
            ) : (
              <Shaped shape={shape} wrap={wrap} />
            )
          ) : (
            <p className="wb-xp__none">
              {call?.finished ? 'The tool returned nothing.' : live ? 'Waiting for the result.' : 'No result was recorded.'}
            </p>
          )}
        </div>
      </div>
    </div>
  )
}
