import { useEffect, useRef, useState } from 'react'
import { parseKeys } from '../../lib/format'
import type { TriageOptions } from '../../store/appStore'

/**
 * Start triage on one or more ticket keys. Keys are typed the way they get
 * pasted out of a tracker or a standup message — commas, spaces or newlines all
 * separate — and the line under the box repeats what was understood before
 * anything is spent.
 */
export default function NewTriageDialog(props: {
  open: boolean
  defaultProvider?: string
  onClose: () => void
  onSubmit: (keys: string[], opts: TriageOptions) => void
}): JSX.Element | null {
  const { open, defaultProvider, onClose, onSubmit } = props
  const [text, setText] = useState('')
  const [provider, setProvider] = useState('')
  const [model, setModel] = useState('')
  const [dryRun, setDryRun] = useState(false)
  const keysRef = useRef<HTMLTextAreaElement>(null)

  useEffect(() => {
    if (open) keysRef.current?.focus()
  }, [open])

  if (!open) return null

  const keys = parseKeys(text)

  function submit(e: React.SyntheticEvent): void {
    e.preventDefault()
    if (keys.length === 0) return
    onSubmit(keys, {
      provider: provider || undefined,
      model: model.trim() || undefined,
      dryRun: dryRun || undefined,
    })
    setText('')
    setModel('')
    setDryRun(false)
    onClose()
  }

  return (
    <div
      className="scrim"
      onMouseDown={(e) => {
        if (e.target === e.currentTarget) onClose()
      }}
    >
      <form
        className="dialog"
        role="dialog"
        aria-modal="true"
        aria-labelledby="triage-title"
        onSubmit={submit}
        onKeyDown={(e) => {
          if (e.key === 'Escape') onClose()
        }}
      >
        <h2 className="dialog-title" id="triage-title">
          New triage
        </h2>

        <div className="field">
          <label className="field-label" htmlFor="triage-keys">
            Ticket keys
          </label>
          <textarea
            id="triage-keys"
            ref={keysRef}
            className="field-input field-input--keys"
            rows={3}
            value={text}
            placeholder="OMNI-2510, OMNI-2511"
            aria-describedby="triage-keys-help"
            onChange={(e) => setText(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) submit(e)
            }}
          />
          <p className="field-help" id="triage-keys-help">
            {keys.length === 0
              ? 'Separate keys with a comma, a space or a newline.'
              : `${keys.length} ${keys.length === 1 ? 'key' : 'keys'}: ${keys.join(' ')}`}
          </p>
        </div>

        <div className="field-row">
          <div className="field">
            <label className="field-label" htmlFor="triage-provider">
              Provider
            </label>
            <select
              id="triage-provider"
              className="field-input"
              value={provider}
              onChange={(e) => setProvider(e.target.value)}
            >
              <option value="">
                {defaultProvider ? `Workspace default (${defaultProvider})` : 'Workspace default'}
              </option>
              <option value="claude">claude</option>
              <option value="codex">codex</option>
              <option value="openai">openai</option>
              <option value="acp">acp</option>
              <option value="qwen">qwen</option>
            </select>
          </div>

          <div className="field">
            <label className="field-label" htmlFor="triage-model">
              Model
            </label>
            <input
              id="triage-model"
              className="field-input"
              type="text"
              value={model}
              placeholder="Workspace default"
              onChange={(e) => setModel(e.target.value)}
            />
          </div>
        </div>

        <div className="checkbox">
          <input
            id="triage-dry-run"
            type="checkbox"
            checked={dryRun}
            onChange={(e) => setDryRun(e.target.checked)}
          />
          <label htmlFor="triage-dry-run">
            Dry run — build the prompt and bundle, call no provider
          </label>
        </div>

        <div className="dialog-actions">
          <button type="button" className="button button--quiet" onClick={onClose}>
            Cancel
          </button>
          <button type="submit" className="button button--accent" disabled={keys.length === 0}>
            Start triage
          </button>
        </div>
      </form>
    </div>
  )
}
