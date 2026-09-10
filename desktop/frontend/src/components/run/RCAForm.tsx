import { useState } from 'react'

/**
 * Starts a root-cause run on the same key. Both fields are optional: the PR and
 * the resolution text are what the RCA compares its hypothesis against, and an
 * RCA can still run without them.
 */
export default function RCAForm({
  runKey,
  pending,
  error,
  onStart,
  onCancel,
}: {
  runKey: string
  pending: boolean
  error: string
  onStart: (o: { prUrl: string; resolution: string }) => void
  onCancel: () => void
}) {
  const [prUrl, setPrUrl] = useState('')
  const [resolution, setResolution] = useState('')

  return (
    <form
      className="form form--rca"
      aria-label="Start RCA"
      onSubmit={(e) => {
        e.preventDefault()
        onStart({ prUrl: prUrl.trim(), resolution: resolution.trim() })
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
