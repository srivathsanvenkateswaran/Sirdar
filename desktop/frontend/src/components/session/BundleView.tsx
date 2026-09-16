import type { RunDetail } from '../../api/types'
import { urlTail, type BundleModel } from '../../lib/bundle'
import { bytes, byteLength } from '../../lib/toolOutput'
import { Section } from './Prose'

export interface BundleViewProps {
  /** The prompt, parsed; null while it is being read. */
  bundle: BundleModel | null
  error?: string
  detail: RunDetail
}

function Row({ k, v, mono, wrap, rtl }: { k: string; v: string; mono?: boolean; wrap?: boolean; rtl?: boolean }): JSX.Element | null {
  if (!v) return null
  const link = /^https?:\/\//.test(v)
  return (
    <>
      <span className="sn-kv__k">{k}</span>
      <span className="sn-kv__v" data-mono={mono || link ? 'true' : undefined} data-wrap={wrap ? 'true' : undefined} dir={rtl ? 'rtl' : 'auto'} title={v}>
        {link ? (
          <a href={v} target="_blank" rel="noreferrer">
            {v}
          </a>
        ) : (
          v
        )}
      </span>
    </>
  )
}

const RTL = /[֐-ࣿ]/

/**
 * What the agent was handed: the tracker card, the helpdesk card, the
 * thread in its original language laid out right to left when it is, the
 * attachments (honestly empty when the bundle had none), and the playbooks
 * the prompt carried. Everything is read off the prompt, since no route
 * serves the bundle directory; the directory itself is named so a reader
 * can open it.
 */
export default function BundleView({ bundle, error, detail }: BundleViewProps): JSX.Element {
  if (error) return <p className="sn-empty">{error}</p>
  if (!bundle) return <p className="sn-path__empty">Reading the prompt…</p>
  const t = bundle.ticket
  const helpdeskUrl = t['Helpdesk URL'] ?? ''
  const helpdeskId = detail.helpdeskKey || (helpdeskUrl ? urlTail(helpdeskUrl) : '')
  const customerMessages = bundle.thread.filter((m) => m.role === 'customer')
  const subject = customerMessages[0]?.text.split('\n')[0] ?? ''

  return (
    <div data-testid="bundle-view">
      <Section title="Tracker" tag={t.Key || detail.key}>
        <div className="sn-kv">
          <Row k="Title" v={t.Title ?? ''} wrap />
          <Row k="Priority" v={t.Priority ?? ''} />
          <Row k="Assignee" v={detail.assignee ?? ''} mono />
          <Row k="Customer" v={t.Customer ?? ''} rtl={RTL.test(t.Customer ?? '')} />
          <Row k="Customer ID" v={t['Customer ID'] ?? ''} mono />
          <Row k="URL" v={t['Tracker URL'] ?? ''} wrap />
        </div>
      </Section>
      {helpdeskId || helpdeskUrl ? (
        <Section title="Helpdesk" tag={helpdeskId || undefined}>
          <div className="sn-kv">
            <Row k="Subject" v={subject} rtl={RTL.test(subject)} wrap />
            <Row k="Contact" v={customerMessages[0]?.author ?? ''} rtl={RTL.test(customerMessages[0]?.author ?? '')} />
            <Row k="URL" v={helpdeskUrl} wrap />
          </div>
        </Section>
      ) : null}
      <Section title="Thread" tag={bundle.thread.length > 0 ? `${bundle.thread.length} messages · original language` : undefined}>
        {bundle.thread.length === 0 ? (
          <p className="sn-empty">The prompt carries no conversation.</p>
        ) : (
          bundle.thread.map((m, i) => (
            <div key={i} className="sn-msg" data-role={m.role}>
              <div className="sn-msg__h">
                <b dir="auto">{m.author}</b>
                <span>{m.at}</span>
                <span className="sn-msg__role">{m.role}</span>
              </div>
              <div className="sn-msg__t" dir={RTL.test(m.text) ? 'rtl' : 'auto'}>
                {m.text}
              </div>
            </div>
          ))
        )}
      </Section>
      <Section title="Attachments" tag={bundle.files.length > 0 ? String(bundle.files.length) : undefined}>
        {bundle.files.length === 0 ? (
          <p className="sn-empty">
            None in this bundle. The prompt's Files block says <span className="sn-mono">(none)</span>
            {detail.bundleDir ? (
              <>
                {' '}
                and <span className="sn-mono">{detail.bundleDir}</span> is where they would be.
              </>
            ) : (
              '.'
            )}
          </p>
        ) : (
          <ul className="sn-file-list">
            {bundle.files.map((f) => (
              <li key={f} dir="ltr">
                {f}
              </li>
            ))}
          </ul>
        )}
      </Section>
      <Section title="Playbooks" tag={bundle.playbooks.length > 0 ? 'in the prompt' : undefined}>
        {bundle.playbooks.length === 0 ? (
          <p className="sn-empty">The prompt carries no playbooks.</p>
        ) : (
          <div className="sn-plist">
            {bundle.playbooks.map((p) => (
              <span key={p} className="sn-plist__chip">
                {p}
              </span>
            ))}
          </div>
        )}
      </Section>
      <Section title="Prompt" tag={`${bundle.sections.length} sections · ${bytes(byteLength(bundle.sections.map((s) => s.body).join('\n')))}`}>
        {detail.promptPath ? (
          <p className="sn-p sn-mono" style={{ fontSize: 12.5 }} dir="ltr">
            {detail.promptPath}
          </p>
        ) : null}
        {bundle.sections.map((s, i) => (
          <details key={s.title + i} className="sn-prompt-section">
            <summary>{s.title}</summary>
            <pre className="sn-pre" style={{ maxBlockSize: 'none' }}>
              {s.body.trim()}
            </pre>
          </details>
        ))}
      </Section>
    </div>
  )
}
