import { PROVIDERS } from '../../api/types'

/**
 * The one-off provider and model override every start accepts.
 *
 * Both are empty by default, which means the workspace's own configuration —
 * the select says which that is, so choosing nothing is not a guess. A model
 * is free text because every provider names its models differently and a list
 * here would be out of date by the time it shipped.
 */
export default function ProviderFields({
  idPrefix,
  provider,
  model,
  defaultProvider,
  disabled,
  onProvider,
  onModel,
}: {
  idPrefix: string
  provider: string
  model: string
  defaultProvider?: string
  disabled?: boolean
  onProvider: (value: string) => void
  onModel: (value: string) => void
}) {
  return (
    <div className="form-row form-row--providers">
      <div className="form-field">
        <label htmlFor={`${idPrefix}-provider`}>Provider</label>
        <select
          id={`${idPrefix}-provider`}
          value={provider}
          disabled={disabled}
          onChange={(e) => onProvider(e.target.value)}
        >
          <option value="">
            {defaultProvider ? `Workspace default (${defaultProvider})` : 'Workspace default'}
          </option>
          {PROVIDERS.map((name) => (
            <option key={name} value={name}>
              {name}
            </option>
          ))}
        </select>
      </div>
      <div className="form-field">
        <label htmlFor={`${idPrefix}-model`}>Model</label>
        <input
          id={`${idPrefix}-model`}
          value={model}
          placeholder="Workspace default"
          disabled={disabled}
          onChange={(e) => onModel(e.target.value)}
        />
      </div>
    </div>
  )
}
