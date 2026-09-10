import { useEffect, useMemo, useState } from 'react'
import type { Transport } from '../../api/types'

interface Section {
  title: string
  body: string
}

/**
 * The prompt the run was given. It is long — preamble, playbooks, schema,
 * ticket — so it is split at its top-level headings and every section past the
 * first stays folded until asked for.
 */
function sections(prompt: string): Section[] {
  const lines = prompt.split('\n')
  const out: Section[] = []
  let title = 'Prompt'
  let body: string[] = []
  for (const line of lines) {
    if (/^# \S/.test(line)) {
      if (body.length > 0 || out.length > 0) out.push({ title, body: body.join('\n') })
      title = line.slice(2).trim()
      body = []
      continue
    }
    body.push(line)
  }
  out.push({ title, body: body.join('\n') })
  return out.filter((s) => s.body.trim() !== '' || s.title !== 'Prompt')
}

export default function PromptView({
  transport,
  workspaceId,
  runId,
  path,
}: {
  transport: Transport
  workspaceId: string
  runId: string
  path?: string
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
        if (!cancelled) setError(e instanceof Error ? e.message : String(e))
      })
    return () => {
      cancelled = true
    }
  }, [transport, workspaceId, runId])

  const parts = useMemo(() => (prompt ? sections(prompt) : []), [prompt])

  if (error) return <div className="pane pane-error">{error}</div>
  if (prompt === null) return <div className="pane pane-empty">Loading prompt…</div>

  return (
    <div className="pane">
      {path ? <div className="pane-path pane-section">{path}</div> : null}
      {parts.map((part, i) => (
        <details key={part.title + i} className="pane-section" open={i === 0}>
          <summary>{part.title}</summary>
          <pre className="mono-block">{part.body.trim()}</pre>
        </details>
      ))}
    </div>
  )
}
