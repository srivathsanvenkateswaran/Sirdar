import { useEffect, useMemo, useState } from 'react'
import type { Transport } from '../../api/types'
import { CLI_DEFAULT, modelLabel, modelsFor } from '../../lib/models'
import Button from '../../ui/button'
import './model-limit.css'

export interface ModelLimitBannerProps {
  /** Only used to read the workspace's configured fallback list. */
  transport: Transport
  workspaceId: string
  /** The run's provider, which decides the list of models offered. */
  provider: string
  /** The model the login has no room for, as the CLI named it ("Fable"). */
  limited: string
  /** A resume is already in flight; every button waits. */
  busy?: boolean
  /** Continues the run on this model id. */
  onContinue: (model: string) => void
}

/**
 * The model names a reader is offered, in the order they are offered.
 *
 * The provider's own curated list, minus "CLI default" — which is what the
 * run was already on and is no answer to "that model is out" — and minus
 * the model that has just been refused, matched on the family name the CLI
 * writes rather than on the dated id, since the message never carries the
 * id. The workspace's configured fallbacks come first, in their own order,
 * so the one button a reader presses without reading is the one the
 * workspace already chose.
 */
export function offeredModels(provider: string, limited: string, fallbacks: string[] = []): string[] {
  const spent = limited.trim().toLowerCase()
  const known = modelsFor(provider)
    .filter((m) => m.id !== CLI_DEFAULT.id)
    .map((m) => m.id)
  const usable = (id: string) => {
    if (!id) return false
    const label = modelLabel(provider, id).toLowerCase()
    return !spent || !(id.toLowerCase().includes(spent) || label.includes(spent))
  }
  const first = fallbacks.map((m) => m.trim()).filter(usable)
  const rest = known.filter((id) => usable(id) && !first.some((m) => m.toLowerCase() === id.toLowerCase()))
  return [...first, ...rest]
}

/**
 * The line above the composer on a run the login has no room left for.
 *
 * A per-model limit is not a rate limit and not a failure: every other
 * model on the same account is answering right now, so the only thing
 * missing is a choice. The banner makes that choice one click — the
 * configured fallback first, then the rest of the provider's list — and
 * each click resumes the same run, on its own session handle, under the
 * model named.
 *
 * The configured list is read here rather than handed down, because this
 * is the one screen that wants it and it is only wanted when the banner is
 * on screen at all.
 */
export default function ModelLimitBanner({
  transport,
  workspaceId,
  provider,
  limited,
  busy = false,
  onContinue,
}: ModelLimitBannerProps): JSX.Element {
  const [fallbacks, setFallbacks] = useState<string[]>([])

  useEffect(() => {
    let live = true
    void transport
      .configSummary(workspaceId)
      .then((cfg) => {
        if (live) setFallbacks(cfg.general.fallbackModels ?? [])
      })
      // A workspace whose configuration could not be read still offers the
      // provider's own list; losing the preferred order is not a reason to
      // show the reader nothing.
      .catch(() => {})
    return () => {
      live = false
    }
  }, [transport, workspaceId])

  const models = useMemo(
    () => offeredModels(provider, limited, fallbacks),
    [provider, limited, fallbacks],
  )

  return (
    <div className="sn-model-limit" role="status">
      <p className="sn-model-limit__line">
        <b>{limited}’s limit is reached on this login.</b> Continue with:
      </p>
      {models.length === 0 ? (
        <p className="sn-model-limit__none">
          This provider lists no other model. Resume with <code>--model</code> once the limit lifts,
          or start the run again under a provider that has room.
        </p>
      ) : (
        <div className="sn-model-limit__choices">
          {models.map((id, i) => (
            <Button
              key={id}
              variant={i === 0 ? 'primary' : 'secondary'}
              size="sm"
              disabled={busy}
              title={`Continue this run on ${id}`}
              onClick={() => onContinue(id)}
            >
              {modelLabel(provider, id)}
            </Button>
          ))}
        </div>
      )}
    </div>
  )
}
