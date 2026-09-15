import { useState } from 'react'
import ProviderFields from './ProviderFields'
import Button from '../../ui/button'

/**
 * Starts the one Sirdar flow that writes.
 *
 * The approval is not here: a fix runs from a triage note a person read and
 * agreed with, and the core refuses one whose status says otherwise. What this
 * form carries is the same set of flags `sirdar fix` takes, so a run started
 * from the app is the run the command would have made.
 */
export default function FixForm({
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
  onStart: (o: {
    dryRun: boolean
    noPr: boolean
    base: string
    provider: string
    model: string
  }) => void
  onCancel: () => void
}) {
  const [dryRun, setDryRun] = useState(false)
  const [noPr, setNoPr] = useState(false)
  const [base, setBase] = useState('')
  const [provider, setProvider] = useState('')
  const [model, setModel] = useState('')

  return (
    <form
      className="form form--fix"
      aria-label="Start fix"
      onSubmit={(e) => {
        e.preventDefault()
        onStart({ dryRun, noPr, base: base.trim(), provider, model: model.trim() })
      }}
    >
      <p className="form-question">Fix run for {runKey}</p>
      <p className="form-note">
        Sirdar cuts a branch from the base, lets the agent write inside the workspace only,
        commits what it wrote, and pushes. It stops before the push if the agent reports
        doing something other than the note's proposed fix.
      </p>
      <div className="form-row">
        <div className="form-field">
          <label htmlFor="fix-base">Base branch</label>
          <input
            id="fix-base"
            value={base}
            placeholder="origin's default branch"
            disabled={pending}
            onChange={(e) => setBase(e.target.value)}
          />
        </div>
      </div>
      <ProviderFields
        idPrefix="fix"
        provider={provider}
        model={model}
        defaultProvider={defaultProvider}
        disabled={pending}
        onProvider={setProvider}
        onModel={setModel}
      />
      <div className="form-row" style={{ marginTop: 8 }}>
        <label className="form-check" htmlFor="fix-dry-run">
          <input
            id="fix-dry-run"
            type="checkbox"
            checked={dryRun}
            disabled={pending}
            onChange={(e) => setDryRun(e.target.checked)}
          />
          <span>Dry run — cut the branch and write the prompt, start no agent</span>
        </label>
      </div>
      <div className="form-row">
        <label className="form-check" htmlFor="fix-no-pr">
          <input
            id="fix-no-pr"
            type="checkbox"
            checked={noPr}
            disabled={pending}
            onChange={(e) => setNoPr(e.target.checked)}
          />
          <span>Push the branch but open no pull request</span>
        </label>
      </div>
      <div className="form-row" style={{ marginTop: 8 }}>
        <Button type="submit" variant="primary" disabled={pending}>
          {pending ? 'Starting…' : 'Start fix'}
        </Button>
        <Button onClick={onCancel} disabled={pending}>
          Cancel
        </Button>
      </div>
      {error ? <div className="form-error">{error}</div> : null}
    </form>
  )
}
