import './QuotaChip.css'

export interface QuotaChipProps {
  /** The provider whose limit this is: claude, codex, a model endpoint. */
  provider: string
  /** Which window the bar measures: "5h", "7d", "used". */
  window: string
  /** 0 to 100. Anything outside that is clamped, never wrapped. */
  percent: number
  /**
   * The countdown to the reset, already formatted: "2h 14m". Drawn after a
   * middot at the chip's end, and said in full ("resets in 2h 14m") in the
   * chip's title and the bar's name. Where the chip is too narrow for the
   * whole countdown it is dropped, middot and all, rather than cut down to
   * a stub; the title still carries it. This component does no arithmetic.
   */
  resetsIn?: string
  /** When the workspace has stopped starting runs against this provider. */
  overBudget?: boolean
}

function clamp(value: number): number {
  if (!Number.isFinite(value)) return 0
  return Math.min(100, Math.max(0, value))
}

/** The threshold at which a quota stops being background information. */
export const WARN_AT = 80

/**
 * How much of a provider's rate limit is gone.
 *
 * Every part of it is a number a person acts on — whether to start another run
 * now or after lunch — so the whole chip is monospace with tabular figures and
 * the bar never moves the text beside it as it fills.
 *
 * Over budget says "over budget". The bar turning red is the same fact stated
 * a second time for people who can see it, not the first time it is stated.
 */
export default function QuotaChip({
  provider,
  window: quotaWindow,
  percent,
  resetsIn,
  overBudget = false,
}: QuotaChipProps): JSX.Element {
  const pct = clamp(percent)
  const level = overBudget || pct >= 100 ? 'over' : pct >= WARN_AT ? 'warn' : 'ok'
  const rounded = Math.round(pct)
  // The chip is one line at every width, so what does not fit is cut and
  // the title says the whole of it.
  const resets = resetsIn ? `resets in ${resetsIn}` : ''
  const sentence = `${provider} ${quotaWindow}: ${rounded}% used${resets ? `, ${resets}` : ''}`

  return (
    <div className="sd-quota" data-level={level} title={resets || undefined}>
      {/* Everything that is never dropped sits in one group, so the
          countdown is the only thing the chip can wrap away. */}
      <span className="sd-quota__line">
        <span className="sd-quota__provider">{provider}</span>
        <span className="sd-quota__window">{quotaWindow}</span>
        <span className="sd-quota__bar" role="img" aria-label={sentence}>
          <span className="sd-quota__fill" style={{ inlineSize: `${pct}%` }} />
        </span>
        <span className="sd-quota__pct" dir="ltr">
          {rounded}%
        </span>
        {level === 'over' && <span className="sd-quota__word">over budget</span>}
      </span>
      {resetsIn && (
        <span className="sd-quota__reset" dir="ltr">
          {resetsIn}
        </span>
      )}
    </div>
  )
}
