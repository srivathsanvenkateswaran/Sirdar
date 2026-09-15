import { useEffect, useMemo, useState } from 'react'
import type { Transport } from '../../api/types'
import { promptAttachments } from '../../lib/events'
import { sections } from './PromptView'

/**
 * What the agent was handed: the bundle directory, the attachments, and the
 * prompt itself. The API cannot list the bundle directory in this version, so
 * the file names come from the prompt's own `Files:` block — the same list
 * the agent saw — and the directory is shown as a path to open. The prompt
 * is long (preamble, playbooks, schema, ticket), so it is split at its
 * headings and every section past the first stays folded until asked for.
 */
export default function BundleView({
  transport,
  workspaceId,
  runId,
  bundleDir,
  promptPath,
}: {
  transport: Transport
  workspaceId: string
  runId: string
  bundleDir: string
  promptPath?: string
}) {
  const [prompt, setPrompt] = useState<string | null>(null)
  const [error, setError] = useState('')

  useEffect(() => {
    let cancelled = false
    setPrompt(null)
    setError('')
    transport
      .prompt(workspaceId, runId)
      .then((text) => {
        if (!cancelled) setPrompt(text)
      })
      .catch((e: unknown) => {
        if (cancelled) return
        setPrompt('')
        setError(e instanceof Error ? e.message : String(e))
      })
    return () => {
      cancelled = true
    }
  }, [transport, workspaceId, runId])

  const files = useMemo(() => (prompt ? promptAttachments(prompt) : []), [prompt])
  const parts = useMemo(() => (prompt ? sections(prompt) : []), [prompt])

  return (
    <div className="pane">
      <div className="pane-section">
        <div className="pane-label">Bundle directory</div>
        <div className="pane-path">{bundleDir || 'not recorded'}</div>
      </div>
      <div className="pane-section">
        <div className="pane-label">Attachments</div>
        {prompt === null ? (
          <p className="pane-empty">Reading the prompt…</p>
        ) : files.length === 0 ? (
          <p className="pane-empty">This ticket came with no attachments.</p>
        ) : (
          <ul className="file-list">
            {files.map((f) => (
              <li key={f}>{f}</li>
            ))}
          </ul>
        )}
      </div>
      <div className="pane-section">
        <div className="pane-label">Prompt</div>
        {promptPath ? <div className="pane-path">{promptPath}</div> : null}
        {error ? <p className="pane-error">{error}</p> : null}
        {prompt !== null && !error && parts.length === 0 ? (
          <p className="pane-empty">The prompt has not been written yet.</p>
        ) : null}
        {parts.map((part, i) => (
          <details key={part.title + i} className="pane-section" open={i === 0}>
            <summary>{part.title}</summary>
            <pre className="mono-block">{part.body.trim()}</pre>
          </details>
        ))}
      </div>
    </div>
  )
}
