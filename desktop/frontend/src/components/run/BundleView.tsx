import { useEffect, useState } from 'react'
import type { Transport } from '../../api/types'
import { promptAttachments } from '../../lib/events'

/**
 * What the agent was handed. The API cannot list the bundle directory in this
 * version, so the file names come from the prompt's own `Files:` block — the
 * same list the agent saw — and the directory is shown as a path to open.
 */
export default function BundleView({
  transport,
  workspaceId,
  runId,
  bundleDir,
}: {
  transport: Transport
  workspaceId: string
  runId: string
  bundleDir: string
}) {
  const [files, setFiles] = useState<string[] | null>(null)

  useEffect(() => {
    let cancelled = false
    setFiles(null)
    transport
      .prompt(workspaceId, runId)
      .then((text) => {
        if (!cancelled) setFiles(promptAttachments(text))
      })
      .catch(() => {
        if (!cancelled) setFiles([])
      })
    return () => {
      cancelled = true
    }
  }, [transport, workspaceId, runId])

  return (
    <div className="pane">
      <div className="pane-section">
        <div className="pane-label">Bundle directory</div>
        <div className="pane-path">{bundleDir || 'not recorded'}</div>
      </div>
      <div className="pane-section">
        <div className="pane-label">Attachments</div>
        {files === null ? (
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
    </div>
  )
}
