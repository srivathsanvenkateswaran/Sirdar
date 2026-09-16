import { memo, type MouseEvent } from 'react'
import { markersForStep, type Marker } from '../../lib/evidence'
import type { PathItem, SessionTurn } from './model'
import Stamp from './Stamp'
import ToolStep from './ToolStep'

export interface TurnGroupProps {
  turn: SessionTurn
  markers: Marker[]
  hotMarker?: string
  /** The steps that are open, by index. */
  open: ReadonlySet<number>
  onToggle: (index: number) => void
  onMarker?: (id: string, event: MouseEvent<HTMLElement>) => void
  onOpenInTools?: (index: number) => void
  hotRefs?: string[]
  live?: boolean
}

/** The operator's own words: a steer, an answer, a review. */
export function YouCard({ at, text, label }: { at: string; text: string; label: string }): JSX.Element {
  return (
    <div className="sn-you" data-testid="you-card">
      <div className="sn-you__who">
        <Stamp tone="you">you</Stamp>
        <span>{at}</span>
        <span>· {label}</span>
      </div>
      <div className="sn-you__say" dir="auto">
        {text}
      </div>
    </div>
  )
}

/** A small grey system line: the provider's rate-limit warning, an error. */
export function SystemLine({ at, text, tone }: { at: string; text: string; tone?: 'failed' }): JSX.Element {
  return (
    <div className="sn-sys" data-tone={tone}>
      <span>{text}</span>
      {at ? <span>· {at}</span> : null}
    </div>
  )
}

/**
 * One turn of the path: the rule with its number and when it began, then
 * the model's words as short quoted annotations between the steps they
 * describe, the steps themselves, and anything the reader did inside the
 * turn as a "you" card. Quiet reads arrive already merged ("turns 3–8") by
 * the model, so the group draws what it is given.
 */
function TurnGroup({ turn, markers, hotMarker, open, onToggle, onMarker, onOpenInTools, hotRefs, live }: TurnGroupProps): JSX.Element {
  return (
    <section aria-label={turn.label} className="sn-turn">
      <div className="sn-turn__rule">
        <span>{turn.label}</span>
        <i className="sn-turn__line" />
        {turn.at ? <span>{turn.at}</span> : null}
      </div>
      {turn.items.map((item) => {
        switch (item.kind) {
          case 'ann':
            return (
              <div key={`ann-${item.index}`} className="sn-ann" dir="auto">
                {item.text}
              </div>
            )
          case 'step':
            return (
              <ToolStep
                key={`step-${item.step.index}`}
                step={item.step}
                markers={markersForStep(item.step.index, markers)}
                hotMarker={hotMarker}
                open={open.has(item.step.index)}
                onToggle={onToggle}
                onMarker={onMarker}
                onOpenInTools={onOpenInTools}
                hotRefs={hotRefs}
                live={live}
              />
            )
          case 'you':
            return <YouCard key={`you-${item.index}`} at={item.at} text={item.text} label={item.label} />
          case 'system':
            return <SystemLine key={`sys-${item.index}`} at={item.at} text={item.text} tone={item.tone} />
        }
      })}
    </section>
  )
}

const MemoTurnGroup = memo(TurnGroup)
export default MemoTurnGroup

/** Draws the whole path: turns, and the cards and lines that sit between them. */
export function PathList({
  path,
  ...rest
}: Omit<TurnGroupProps, 'turn'> & { path: PathItem[] }): JSX.Element {
  return (
    <>
      {path.map((item) => {
        switch (item.kind) {
          case 'turn':
            return <MemoTurnGroup key={item.turn.key} turn={item.turn} {...rest} />
          case 'you':
            return <YouCard key={`you-${item.index}`} at={item.at} text={item.text} label={item.label} />
          case 'system':
            return <SystemLine key={`sys-${item.index}`} at={item.at} text={item.text} tone={item.tone} />
        }
      })}
    </>
  )
}
