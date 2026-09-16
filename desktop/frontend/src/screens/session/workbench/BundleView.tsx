import { useEffect, useMemo, useState, type JSX } from 'react'
import type { Transport } from '../../../api/types'
import { reasonOf } from '../../../lib/format'
import { fact, idFromURL, otherFacts, parseBundle, translations, type Bundle } from './bundle'
import Document, { type OutlineItem } from './Document'

/**
 * The bundle as a document: the tracker's record and the helpdesk's side by
 * side, the thread in its own language with the note's translation under
 * each customer message, the attachments (honestly empty when there are
 * none), and the playbooks the run was handed.
 *
 * A local adapter with the shared `BundleView`'s name. Every field comes off
 * the prompt, the one bundle artefact the transport serves; the page says so
 * where a reader might expect `ticket.json`'s richer record.
 */
export interface BundleViewProps {
  transport: Transport
  workspaceId: string
  runId: string
  bundleDir: string
  /** The helpdesk's number, when the run summary carries it. */
  helpdeskKey?: string
  /** The answer's translated complaint, matched to the customer's messages in order. */
  complaint?: string
  /** Called with the bundle once the prompt is read, for the tab's count. */
  onLoaded?: (doc: Bundle | null) => void
}

function DL({ rows }: { rows: { k: string; v: string; mono?: boolean; wrap?: boolean }[] }): JSX.Element {
  return (
    <dl className="wb-dl">
      {rows
        .filter((r) => r.v)
        .map((r) => (
          <div key={r.k} className="wb-dl__row">
            <dt>{r.k}</dt>
            <dd className={r.mono ? 'wb-mono' : undefined} data-wrap={r.wrap ? 'true' : undefined} dir="auto" title={r.v}>
              {r.v}
            </dd>
          </div>
        ))}
    </dl>
  )
}

export default function BundleView({ transport, workspaceId, runId, bundleDir, helpdeskKey, complaint, onLoaded }: BundleViewProps): JSX.Element {
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
        setError(reasonOf(e))
      })
    return () => {
      cancelled = true
    }
  }, [transport, workspaceId, runId])

  const doc = useMemo(() => (prompt ? parseBundle(prompt) : null), [prompt])
  const key = doc ? fact(doc, 'Key') : ''
  const trackerUrl = doc ? fact(doc, 'Tracker URL') : ''
  const helpdeskUrl = doc ? fact(doc, 'Helpdesk URL') : ''
  useEffect(() => {
    if (prompt !== null) onLoaded?.(doc)
  }, [prompt, doc, onLoaded])

  const translated = useMemo(() => translations(complaint), [complaint])
  const helpdeskId = helpdeskKey || idFromURL(helpdeskUrl)

  const outline = useMemo<OutlineItem[]>(() => {
    if (!doc) return []
    const out: OutlineItem[] = [
      { id: 'tracker', title: 'Tracker', n: key || undefined },
      { id: 'helpdesk', title: 'Helpdesk', n: helpdeskId || undefined },
      { id: 'thread', title: 'Thread', n: String(doc.thread.length) },
      { id: 'attachments', title: 'Attachments', n: String(doc.files.length) },
      { id: 'playbooks', title: 'Playbooks', n: String(doc.playbooks.length) },
    ]
    for (const p of doc.playbooks) out.push({ id: `pb-${p.name}`, title: p.name, sub: true })
    return out
  }, [doc, helpdeskId, key])

  if (prompt === null) {
    return (
      <Document outline={[]} label="Bundle">
        <p className="wb-empty">Reading the prompt…</p>
      </Document>
    )
  }
  if (!doc) {
    return (
      <Document outline={[]} label="Bundle">
        <p className="wb-empty">{error || 'The prompt has not been written yet.'}</p>
      </Document>
    )
  }

  let customerSeen = 0
  return (
    <Document outline={outline} label="Bundle">
      <div className="wb-two wb-two--cards">
        <section data-sec="tracker">
          <div className="wb-sec-h">
            Tracker <span className="wb-mono">prompt.md · Ticket</span>
          </div>
          <DL
            rows={[
              { k: 'key', v: key, mono: true },
              { k: 'title', v: fact(doc, 'Title'), wrap: true },
              { k: 'priority', v: fact(doc, 'Priority') },
              { k: 'customer', v: fact(doc, 'Customer') },
              { k: 'customer id', v: fact(doc, 'Customer ID'), mono: true },
              { k: 'url', v: trackerUrl, mono: true },
              ...otherFacts(doc, ['Key', 'Title', 'Priority', 'Customer', 'Customer ID', 'Tracker URL', 'Helpdesk URL', 'Bundle directory']).map(
                (o) => ({ k: o.key.toLowerCase(), v: o.value, wrap: true }),
              ),
              { k: 'bundle', v: fact(doc, 'Bundle directory') || bundleDir, mono: true },
            ]}
          />
        </section>
        <section data-sec="helpdesk">
          <div className="wb-sec-h">
            Helpdesk <span className="wb-mono">prompt.md · Ticket</span>
          </div>
          {helpdeskUrl || helpdeskId ? (
            <DL
              rows={[
                { k: 'id', v: helpdeskId, mono: true },
                { k: 'url', v: helpdeskUrl, mono: true },
                { k: 'messages', v: String(doc.thread.length) },
              ]}
            />
          ) : (
            <p className="wb-empty wb-empty--box">No helpdesk record on this ticket.</p>
          )}
          <p className="wb-aside">The prompt carries these fields; ticket.json's full record stays in the bundle directory.</p>
        </section>
      </div>

      <section className="wb-sec" data-sec="thread">
        <div className="wb-sec-h">
          Thread{' '}
          <span className="wb-mono">
            {doc.thread.length} {doc.thread.length === 1 ? 'message' : 'messages'} · thread.md
          </span>
        </div>
        {doc.thread.length === 0 ? <p className="wb-empty wb-empty--box">The prompt carries no conversation.</p> : null}
        {doc.thread.map((m, i) => {
          const customer = /customer/i.test(m.role)
          const tr = customer ? translated[customerSeen++] : undefined
          return (
            <div key={i} className="wb-msg">
              <div className="wb-msg__meta">
                <b dir="auto">{m.author}</b>
                {m.role}
                <br />
                {m.at.replace('T', ' ').replace(/:\d\d(?=[+-Z]|$)/, '')}
              </div>
              <div>
                <div className="wb-msg__text" dir="auto">
                  {m.text}
                </div>
                <div className="wb-msg__tr">
                  {tr ? (
                    <>
                      <span className="wb-msg__lab">translated in the note · </span>
                      {tr}
                    </>
                  ) : (
                    <span className="wb-msg__lab">{customer ? 'no translation in the note' : 'no translation in the note (agent message)'}</span>
                  )}
                </div>
              </div>
            </div>
          )
        })}
      </section>

      <section className="wb-sec" data-sec="attachments">
        <div className="wb-sec-h">
          Attachments <span className="wb-mono">{doc.files.length}</span>
        </div>
        {doc.files.length === 0 ? (
          <p className="wb-empty wb-empty--box">None on this ticket: the prompt's Files block says (none).</p>
        ) : (
          <ul className="wb-list wb-list--files">
            {doc.files.map((f) => (
              <li key={f} className="wb-mono">
                {f}
              </li>
            ))}
          </ul>
        )}
      </section>

      <section className="wb-sec" data-sec="playbooks">
        <div className="wb-sec-h">
          Playbooks <span className="wb-mono">{doc.playbooks.length}</span>
        </div>
        {doc.playbooks.length === 0 ? (
          <p className="wb-empty wb-empty--box">The prompt carries no playbooks.</p>
        ) : (
          <ul className="wb-list wb-list--files">
            {doc.playbooks.map((p) => (
              <li key={p.name} className="wb-mono" data-sec={`pb-${p.name}`}>
                {p.name}
              </li>
            ))}
          </ul>
        )}
      </section>
    </Document>
  )
}
