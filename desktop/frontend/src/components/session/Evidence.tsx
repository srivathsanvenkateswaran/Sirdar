import type { MouseEvent } from 'react'
import type { EvidenceSource, Marker as MarkerModel } from '../../lib/evidence'
import Marker from '../../ui/marker'
import type { SessionStep } from './model'
import { inline } from './Prose'

export interface EvidenceListProps {
  items: EvidenceSource[]
  markers: MarkerModel[]
  hotMarker?: string
  onMarker?: (id: string, event: MouseEvent<HTMLElement>) => void
  /** The steps, so "step 00:06 →" can say when the call was and go to it. */
  steps: SessionStep[]
  onGoToStep?: (index: number) => void
}

/** The `file:line` references an item cites, joined: `ledger.go:27-34 · 44-46`. */
export function whereOf(marker: MarkerModel | undefined): string {
  if (!marker || marker.refs.length === 0) return ''
  const out: string[] = []
  let lastFile = ''
  for (const r of marker.refs) {
    const range = r.to !== undefined ? `${r.from}-${r.to}` : String(r.from)
    out.push(r.file === lastFile ? range : `${r.file}:${range}`)
    lastFile = r.file
  }
  return out.join(' · ')
}

/**
 * The answer's evidence as callouts: E1…En, where in the code, the finding
 * with its references, and the source chip with the query and the step that
 * produced it ("step 00:06 →"), which is a button when the pairing found one.
 */
export default function EvidenceList({ items, markers, hotMarker, onMarker, steps, onGoToStep }: EvidenceListProps): JSX.Element {
  return (
    <div data-testid="evidence-list">
      {items.map((item, i) => {
        const marker = markers.find((m) => m.kind === 'E' && m.id === `E${i + 1}`)
        const stepIndex = marker?.steps[0]
        const step = stepIndex !== undefined ? steps.find((s) => s.index === stepIndex) : undefined
        const hot = marker !== undefined && marker.id === hotMarker
        return (
          <div key={i} className="sn-evc" data-hot={hot ? 'true' : undefined} data-marker={marker?.id}>
            {marker ? <Marker id={marker.id} hot={hot} onClick={onMarker} title={item.query} /> : <span />}
            <div className="sn-evc__where" dir="ltr">
              {whereOf(marker) || item.query}
            </div>
            <span />
            <div className="sn-evc__find" dir="auto">
              {inline(item.finding, { markers: markers.filter((m) => m !== marker), hotMarker, onMarker })}
            </div>
            <span />
            <div className="sn-evc__from">
              <span className="sn-src">{item.source}</span>
              <span className="sn-evc__q" title={item.query} dir="ltr">
                {item.query}
              </span>
              {step ? (
                <button type="button" className="sn-evc__go" onClick={() => onGoToStep?.(step.index)} disabled={!onGoToStep}>
                  step {step.at} →
                </button>
              ) : (
                <span className="sn-evc__go" aria-disabled="true" title="No call in this run's log matches this item's query">
                  no step
                </span>
              )}
            </div>
          </div>
        )
      })}
    </div>
  )
}
