import { memo, type MouseEvent } from 'react'
import { markersForStep, type Marker } from '../../lib/evidence'
import type { PathItem, SessionTurn, TurnItem } from './model'
import { RememberButton } from './Remember'
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
  /** Offers an operator message to the playbooks by its log index; absent where the run's messages are not kept. */
  onRemember?: (index: number) => void
  /** The messages whose Remember this is in flight, by log index. */
  remembering?: readonly number[]
}

export interface YouCardProps {
  /** When it was said, as the path's clock; empty for the instruction, which the run was started with. */
  at: string
  text: string
  label: string
  /** Offers these words to the playbooks; the card draws no Remember this without it. */
  onRemember?: () => void
  /** The Remember this call is out. */
  remembering?: boolean
  testId?: string
}

/** The operator's own words: a steer, an answer, a review, the instruction the run started with. */
export function YouCard({ at, text, label, onRemember, remembering = false, testId = 'you-card' }: YouCardProps): JSX.Element {
  return (
    <div className="sn-you" data-testid={testId} data-busy={remembering || undefined}>
      <div className="sn-you__who">
        <Stamp tone="you">you</Stamp>
        {at ? <span>{at}</span> : null}
        <span>{at ? `· ${label}` : label}</span>
      </div>
      <div className="sn-you__say" dir="auto">
        {text}
      </div>
      {onRemember ? <RememberButton busy={remembering} onRemember={onRemember} /> : null}
    </div>
  )
}

/** A path's "you" item as its card, with Remember this where the words can be kept. */
function youCardOf(
  item: Extract<TurnItem, { kind: 'you' }>,
  onRemember: ((index: number) => void) | undefined,
  remembering: readonly number[] | undefined,
): JSX.Element {
  return (
    <YouCard
      key={`you-${item.index}`}
      at={item.at}
      text={item.text}
      label={item.label}
      onRemember={onRemember && item.remember ? () => onRemember(item.index) : undefined}
      remembering={remembering?.includes(item.index)}
    />
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
function TurnGroup({ turn, markers, hotMarker, open, onToggle, onMarker, onOpenInTools, hotRefs, live, onRemember, remembering }: TurnGroupProps): JSX.Element {
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
            return youCardOf(item, onRemember, remembering)
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
            return youCardOf(item, rest.onRemember, rest.remembering)
          case 'system':
            return <SystemLine key={`sys-${item.index}`} at={item.at} text={item.text} tone={item.tone} />
        }
      })}
    </>
  )
}
