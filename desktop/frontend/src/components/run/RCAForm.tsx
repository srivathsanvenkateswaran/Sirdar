import { useState } from 'react'
import ProviderFields from './ProviderFields'

/**
 * Starts a root-cause run on the same key. Both fields are optional: the PR and
 * the resolution text are what the RCA compares its hypothesis against, and an
 * RCA can still run without them.
 */
export default function RCAForm({
  runKey,
  pending,
  error,
  defaultProvider,
  onStart,
  onCancel,
}: {
  runKey: string
  pending: boolean
  error: string
  defaultProvider?: string
  onStart: (o: { prUrl: string; resolution: string; provider: string; model: string }) => void
  onCancel: () => void
}) {
  const [prUrl, setPrUrl] = useState('')
  const [resolution, setResolution] = useState('')
  const [provider, setProvider] = useState('')
  const [model, setModel] = useState('')

  return (
    <form
      className="form form--rca"
      aria-label="Start RCA"
      onSubmit={(e) => {
        e.preventDefault()
        onStart({
          prUrl: prUrl.trim(),
          resolution: resolution.trim(),
          provider,
          model: model.trim(),
        })
      }}
    >
      <p className="form-question">Root-cause run for {runKey}</p>
      <div className="form-row">
        <div className="form-field">
          <label htmlFor="rca-pr">Pull request URL</label>
          <input
            id="rca-pr"
            value={prUrl}
            placeholder="https://github.com/owner/repo/pull/123"
            onChange={(e) => setPrUrl(e.target.value)}
          />
        </div>
      </div>
      <div className="form-row" style={{ marginTop: 8 }}>
        <div className="form-field">
          <label htmlFor="rca-resolution">How it was resolved</label>
          <textarea
            id="rca-resolution"
            value={resolution}
            placeholder="What actually fixed it, and how you know"
            onChange={(e) => setResolution(e.target.value)}
          />
        </div>
      </div>
      <ProviderFields
        idPrefix="rca"
        provider={provider}
        model={model}
        defaultProvider={defaultProvider}
        disabled={pending}
        onProvider={setProvider}
        onModel={setModel}
      />
      <div className="form-row" style={{ marginTop: 8 }}>
        <button type="submit" className="run-btn run-btn--primary" disabled={pending}>
          {pending ? 'Starting…' : 'Start RCA'}
        </button>
        <button type="button" className="run-btn" onClick={onCancel} disabled={pending}>
          Cancel
        </button>
      </div>
      {error ? <div className="form-error">{error}</div> : null}
    </form>
  )
}
