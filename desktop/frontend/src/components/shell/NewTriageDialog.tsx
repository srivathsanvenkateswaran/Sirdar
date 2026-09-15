import { useState } from 'react'
import { parseKeys } from '../../lib/format'
import type { TriageOptions } from '../../store/appStore'
import Button from '../../ui/button'
import Dialog from '../../ui/dialog'

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

  // The library Dialog moves focus to the first control when it opens, which
  // is this form's keys box, so there is no focus call here any more.
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
    <Dialog open={open} title="New triage" onClose={onClose}>
      <form className="triage-form" onSubmit={submit}>
        <div className="field">
          <label className="field-label" htmlFor="triage-keys">
            Ticket keys
          </label>
          <textarea
            id="triage-keys"
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
              <option value="cursor">cursor</option>
              {/* agy is disabled: Google's Antigravity terms do not allow driving
                  the CLI from another program, and every start refuses it. */}
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

        <div className="sd-dialog__actions">
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          {/* The dialog's one commit action, and the only thing in it that
              spends the provider. */}
          <Button type="submit" variant="primary" disabled={keys.length === 0}>
            Start triage
          </Button>
        </div>
      </form>
    </Dialog>
  )
}
