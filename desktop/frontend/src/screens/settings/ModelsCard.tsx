import { useEffect, useState } from 'react'
import type { ModelList, Transport } from '../../api/types'
import { reasonOf, relativeTime } from '../../lib/format'
import { GROUP_HEADINGS, sourceNote } from '../../lib/modelCatalog'
import { PICKABLE_PROVIDERS } from '../../lib/models'
import { providerName } from '../../ui/provider-mark'
import SegmentedControl from '../../ui/segmented-control'
import { SettingCard } from '../../ui/setting-row'

type Loaded = { status: 'loading' } | { status: 'done'; list: ModelList } | { status: 'error'; message: string }

/**
 * Settings › Providers › Models: the list the model picker offers, one
 * provider at a time — what the CLI resolved its aliases to on this login,
 * what the workspace's runs reported, and the config's pins — with Refresh.
 * Reading it starts nothing; only Refresh asks the CLI.
 */
export default function ModelsCard({
  transport,
  workspaceId,
  defaultProvider,
}: {
  transport: Transport
  workspaceId?: string
  defaultProvider: string
}): JSX.Element {
  const [provider, setProvider] = useState(defaultProvider || 'claude')
  const [state, setState] = useState<Loaded>({ status: 'loading' })
  const [probing, setProbing] = useState(false)

  useEffect(() => {
    if (defaultProvider) setProvider(defaultProvider)
  }, [defaultProvider])

  useEffect(() => {
    if (!workspaceId) return
    let live = true
    setState({ status: 'loading' })
    transport
      .models(workspaceId, provider)
      .then((list) => live && setState({ status: 'done', list }))
      .catch((err: unknown) => live && setState({ status: 'error', message: reasonOf(err) }))
    return () => {
      live = false
    }
  }, [transport, workspaceId, provider])

  function refresh(): void {
    if (!workspaceId) return
    setProbing(true)
    transport
      .refreshModels(workspaceId, provider)
      .then((list) => setState({ status: 'done', list }))
      .catch((err: unknown) => setState({ status: 'error', message: reasonOf(err) }))
      .finally(() => setProbing(false))
  }

  const list = state.status === 'done' ? state.list : null
  const status = probing
    ? 'Asking the CLI which models this login has…'
    : !list
      ? ''
      : !list.canProbe
        ? 'From runs and config'
        : list.probedAt
          ? `Probed ${relativeTime(list.probedAt)}`
          : 'Not probed yet'

  return (
    <SettingCard heading="Models">
      {!workspaceId ? (
        <p className="empty-state">Choose a workspace from the switcher to see its models.</p>
      ) : (
        <div className="settings-models">
          <div className="settings-models__head">
            <SegmentedControl
              label="Provider"
              value={provider}
              options={PICKABLE_PROVIDERS.map((p) => ({ id: p, label: providerName(p) }))}
              onChange={setProvider}
            />
            <span className="settings-models__status" role="status">
              {status}
            </span>
            <button
              type="button"
              className="sd-setting-button"
              disabled={probing || state.status === 'loading'}
              onClick={refresh}
            >
              Refresh
            </button>
          </div>
          {state.status === 'loading' ? (
            <p className="settings-note">Loading the list…</p>
          ) : state.status === 'error' ? (
            <p className="form-error">{state.message}</p>
          ) : state.list.models.length === 0 ? (
            <p className="settings-note">
              {state.list.canProbe
                ? 'Nothing found yet. Refresh asks the CLI what opus, sonnet and haiku mean on this login.'
                : 'No run has reported a model, and the config pins none.'}
            </p>
          ) : (
            <ul className="settings-models__list" aria-label={`${providerName(provider)} models`}>
              {state.list.models.map((m) => (
                <li key={m.id} className="settings-models__row">
                  <span className="settings-models__label" dir="auto">{m.label}</span>
                  <code className="settings-models__id" dir="ltr">
                    {m.id}
                  </code>
                  <span className="settings-models__note" title={GROUP_HEADINGS[m.source]}>
                    {sourceNote(m.source, m.seenAt)}
                  </span>
                </li>
              ))}
            </ul>
          )}
          {list && list.probeErrors && list.probeErrors.length > 0 ? (
            <p className="settings-note">Not resolved on the last probe: {list.probeErrors.join('; ')}</p>
          ) : null}
        </div>
      )}
    </SettingCard>
  )
}
