import type { StepState } from './model'

export type StampTone = 'deny' | 'allow' | 'wait' | 'list' | 'you' | 'done' | 'fail' | 'plain'

/**
 * A stamp: the policy's word on a call ("denied", "allowed", "waiting on
 * you", "allow-list"), or who did a thing ("you"). A thin bordered word in
 * the mono face, coloured by what it says and never by colour alone.
 */
export default function Stamp({ tone = 'plain', children }: { tone?: StampTone; children: string }): JSX.Element {
  return (
    <span className="sn-stamp" data-tone={tone === 'plain' ? undefined : tone}>
      {children}
    </span>
  )
}

/** The stamp a step's state and rule call for, or null when the step earns none. */
export function stepStamp(step: { state: StepState; decision?: string; rule?: string }): { tone: StampTone; word: string } | null {
  if (step.state === 'denied') return { tone: 'deny', word: 'denied' }
  if (step.state === 'waiting') return { tone: 'wait', word: 'waiting on you' }
  if (step.decision === 'allow') return { tone: 'allow', word: 'allowed' }
  if (step.rule === 'allow-list') return { tone: 'list', word: 'allow-list' }
  return null
}
