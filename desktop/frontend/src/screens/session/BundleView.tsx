import { useEffect, useMemo, useState } from 'react'
import type { Transport } from '../../api/types'
import { promptAttachments } from '../../lib/events'
import { reasonOf } from '../../lib/format'
import { ExternalIcon } from './icons'

/*
 * What the agent was handed, as the ticket it is: a tracker card, a
 * helpdesk card, the conversation as bubbles in the customer's own
 * direction, the attachments (honestly empty when there are none), and the
 * playbooks the prompt carried.
 *
 * The bundle directory itself is not served, so everything here is read out
 * of the prompt — the same text the agent saw — whose `# Ticket` section
 * lists the record's facts as `Key: value` lines and quotes the thread as
 * `## <at> · <role> · <author>` headings.
 */

export interface BundleMessage {
  at: string
  role: string
  author: string
  text: string
}

export interface Playbook {
  name: string
  body: string
}

export interface Bundle {
  facts: { key: string; value: string }[]
  thread: BundleMessage[]
  files: string[]
  playbooks: Playbook[]
}

/** The lines under `# Ticket` up to the first sub-heading, as key–value pairs. */
function facts(prompt: string): { key: string; value: string }[] {
  const lines = prompt.split('\n')
  const start = lines.findIndex((l) => /^# Ticket\s*$/.test(l))
  if (start === -1) return []
  const out: { key: string; value: string }[] = []
  for (const line of lines.slice(start + 1)) {
    if (/^#/.test(line)) break
    if (line.trim() === 'Files:') break
    const m = /^([A-Za-z][A-Za-z ]*?):\s*(.+)$/.exec(line)
    if (m) out.push({ key: m[1].trim(), value: m[2].trim() })
  }
  return out
}

function thread(prompt: string): BundleMessage[] {
  const out: BundleMessage[] = []
  const lines = prompt.split('\n')
  let current: BundleMessage | undefined
  let inside = false
  for (const line of lines) {
    if (/^# Conversation\b/.test(line)) {
      inside = true
      continue
    }
    if (!inside) continue
    if (/^```/.test(line) || /^# /.test(line)) {
      if (current) out.push(current)
      current = undefined
      if (/^# /.test(line)) inside = false
      continue
    }
    const head = /^## (.+?) · (.+?) · (.+)$/.exec(line)
    if (head) {
      if (current) out.push(current)
      current = { at: head[1].trim(), role: head[2].trim(), author: head[3].trim(), text: '' }
      continue
    }
    if (current) current.text = current.text ? `${current.text}\n${line}` : line
  }
  if (current) out.push(current)
  return out.map((m) => ({ ...m, text: m.text.trim() }))
}

function playbooks(prompt: string): Playbook[] {
  const lines = prompt.split('\n')
  const start = lines.findIndex((l) => /^# Playbooks\s*$/.test(l))
  if (start === -1) return []
  const out: Playbook[] = []
  let current: Playbook | undefined
  // A playbook's own body carries `# Environment` and `## How to use it`
  // headings, so only the prompt's next top-level section ends the list.
  for (const line of lines.slice(start + 1)) {
    if (/^# (Ticket|Output|Language|Schema)\b/.test(line)) break
    const head = /^## (\d{2}-[\w-]+)\s*$/.exec(line)
    if (head) {
      if (current) out.push(current)
      current = { name: head[1], body: '' }
      continue
    }
    if (current) current.body = current.body ? `${current.body}\n${line}` : line
  }
  if (current) out.push(current)
  return out.map((p) => ({ ...p, body: p.body.trim() }))
}

/** Everything the Bundle tab draws, read out of the prompt. */
export function parseBundle(prompt: string): Bundle {
  return { facts: facts(prompt), thread: thread(prompt), files: promptAttachments(prompt), playbooks: playbooks(prompt) }
}

/** The first paragraph of a playbook that is prose, not a heading. */
function excerpt(body: string, max = 420): string {
  const paras = body
    .split(/\n{2,}/)
    .map((p) => p.trim())
    .filter((p) => p && !p.startsWith('#'))
  const text = paras.join(' ').replace(/\s+/g, ' ')
  return text.length > max ? `${text.slice(0, max - 1)}…` : text
}

function fact(bundle: Bundle, key: string): string {
  return bundle.facts.find((f) => f.key.toLowerCase() === key.toLowerCase())?.value ?? ''
}

function host(url: string): string {
  return url.replace(/^https?:\/\//, '')
}

export interface BundleViewProps {
  transport: Transport
  workspaceId: string
  runId: string
  bundleDir: string
  promptPath?: string
}

export default function BundleView({ transport, workspaceId, runId, bundleDir, promptPath }: BundleViewProps): JSX.Element {
  const [prompt, setPrompt] = useState<string | null>(null)
  const [error, setError] = useState('')
  const [playbook, setPlaybook] = useState(0)

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
        setError(reasonOf(e))
      })
    return () => {
      cancelled = true
    }
  }, [transport, workspaceId, runId])

  const bundle = useMemo(() => parseBundle(prompt ?? ''), [prompt])

  if (prompt === null) return <div className="sc-pane-empty">Reading the bundle…</div>
  if (error) return <div className="sc-pane-empty sc-pane-error">{error}</div>

  const key = fact(bundle, 'Key')
  const title = fact(bundle, 'Title')
  const priority = fact(bundle, 'Priority')
  const trackerUrl = fact(bundle, 'Tracker URL')
  const helpdeskUrl = fact(bundle, 'Helpdesk URL')
  const customer = fact(bundle, 'Customer')
  const customerId = fact(bundle, 'Customer ID')
  const contact = bundle.thread.find((m) => m.role === 'customer')?.author ?? ''
  const shown = bundle.playbooks[playbook]

  return (
    <div className="sc-bundle" data-testid="bundle-view">
      {key || title ? (
        <section className="sc-tk" aria-label="Tracker">
          <div className="sc-tk__row">
            {key ? <span className="sc-tk__key">{key}</span> : null}
            {priority ? <span className="sc-tk__chip">{priority}</span> : null}
            {trackerUrl ? (
              <a className="sc-tk__ext" href={trackerUrl} target="_blank" rel="noreferrer noopener" dir="ltr">
                {host(trackerUrl)} <ExternalIcon />
              </a>
            ) : null}
          </div>
          {title ? (
            <h3 className="sc-tk__title" dir="auto">
              {title}
            </h3>
          ) : null}
          <div className="sc-kvg">
            {customer ? (
              <div>
                <div className="sc-kvg__k">Customer</div>
                <div className="sc-kvg__v" dir="auto">
                  {customer}
                </div>
              </div>
            ) : null}
            {customerId ? (
              <div>
                <div className="sc-kvg__k">Customer id</div>
                <div className="sc-kvg__v sc-mono">{customerId}</div>
              </div>
            ) : null}
            <div>
              <div className="sc-kvg__k">Bundle</div>
              <div className="sc-kvg__v sc-mono sc-kvg__path" title={bundleDir || undefined}>
                {bundleDir || 'not recorded'}
              </div>
            </div>
          </div>
        </section>
      ) : null}
      {helpdeskUrl || contact ? (
        <section className="sc-tk" aria-label="Helpdesk">
          <div className="sc-tk__row">
            <span className="sc-tk__key">{helpdeskUrl ? `#${helpdeskUrl.split('/').pop()}` : 'Helpdesk'}</span>
            {helpdeskUrl ? (
              <a className="sc-tk__ext" href={helpdeskUrl} target="_blank" rel="noreferrer noopener" dir="ltr">
                {host(helpdeskUrl)} <ExternalIcon />
              </a>
            ) : null}
          </div>
          <div className="sc-kvg">
            {contact ? (
              <div>
                <div className="sc-kvg__k">Contact</div>
                <div className="sc-kvg__v" dir="auto">
                  {contact}
                </div>
              </div>
            ) : null}
            {customer ? (
              <div>
                <div className="sc-kvg__k">Customer</div>
                <div className="sc-kvg__v" dir="auto">
                  {customer}
                </div>
              </div>
            ) : null}
          </div>
        </section>
      ) : null}
      <section className="sc-grp" aria-label="Conversation">
        <div className="sc-grp__h">
          <span>Conversation</span>
          {bundle.thread.length > 0 ? (
            <span className="sc-mono sc-grp__n">
              {bundle.thread.length} {bundle.thread.length === 1 ? 'message' : 'messages'} · original language
            </span>
          ) : null}
        </div>
        {bundle.thread.length === 0 ? (
          <div className="sc-empty">The prompt carried no conversation.</div>
        ) : (
          <div className="sc-th">
            {bundle.thread.map((m, i) => (
              <div key={i} className="sc-bub" data-role={m.role} dir="auto">
                <div className="sc-bub__who">
                  <span>{m.author}</span>
                  <span className="sc-mono" dir="ltr">
                    {m.at}
                  </span>
                </div>
                {m.text.split('\n').map((line, j) => (
                  <span key={j}>
                    {line}
                    <br />
                  </span>
                ))}
              </div>
            ))}
          </div>
        )}
      </section>
      <section className="sc-grp" aria-label="Attachments">
        <div className="sc-grp__h">
          <span>Attachments</span>
        </div>
        {bundle.files.length === 0 ? (
          <div className="sc-empty">None in this bundle. The prompt listed no files.</div>
        ) : (
          <ul className="sc-files">
            {bundle.files.map((f) => (
              <li key={f} className="sc-mono">
                {f}
              </li>
            ))}
          </ul>
        )}
      </section>
      <section className="sc-grp" aria-label="Playbooks">
        <div className="sc-grp__h">
          <span>Playbooks</span>
          {bundle.playbooks.length > 0 ? <span className="sc-mono sc-grp__n">{bundle.playbooks.length} in the prompt</span> : null}
        </div>
        {bundle.playbooks.length === 0 ? (
          <div className="sc-empty">{prompt ? 'The prompt carried no playbooks.' : 'The prompt has not been written yet.'}</div>
        ) : (
          <>
            <div className="sc-pb" role="tablist" aria-label="Playbooks">
              {bundle.playbooks.map((p, i) => (
                <button
                  key={p.name}
                  type="button"
                  role="tab"
                  className="sc-pb__k"
                  aria-selected={i === playbook}
                  data-on={i === playbook ? 'true' : undefined}
                  onClick={() => setPlaybook(i)}
                >
                  {p.name}
                </button>
              ))}
            </div>
            {shown ? (
              <div className="sc-pbx" role="tabpanel" aria-label={shown.name}>
                {excerpt(shown.body) || 'Nothing but headings.'}
              </div>
            ) : null}
          </>
        )}
      </section>
      {promptPath ? (
        <div className="sc-bundle__path sc-mono" title={promptPath}>
          {promptPath}
        </div>
      ) : null}
    </div>
  )
}
