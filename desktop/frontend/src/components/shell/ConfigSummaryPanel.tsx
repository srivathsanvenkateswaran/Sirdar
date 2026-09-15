import type { ConfigSummary } from '../../api/types'

/** How a credential reference is named once its value is gone. */
function credential(scheme?: string): string {
  return scheme ? `${scheme}: reference` : 'no credential'
}

/**
 * The workspace's notify and webhooks blocks, read-only.
 *
 * Nothing here is resolved and nothing here is a secret. Every credential a
 * workspace configures is a reference, and what the API sends is the scheme
 * alone — env, keychain, file, cmd — which says where a secret comes from
 * and names neither it nor the variable holding it. Changing any of this
 * means editing `.sirdar/config.yaml`.
 */
export default function ConfigSummaryPanel({
  summary,
  error,
}: {
  summary: ConfigSummary | null
  error: string
}): JSX.Element {
  return (
    <section className="settings-config" aria-label="Notifications and webhooks">
      <h2 className="panel-heading">Notifications and webhooks</h2>
      {error ? <p className="form-error">{error}</p> : null}
      {!summary ? (
        error ? null : (
          <p className="empty-state">Reading the workspace configuration…</p>
        )
      ) : (
        <>
          <h3 className="config-sub">Notify</h3>
          {!summary.notify.enabled ? (
            <p className="empty-state">
              This workspace posts nothing when a run finishes. Add a <code>notify</code> block
              to its config to change that.
            </p>
          ) : (
            <>
              <p className="config-line">
                Posts on {summary.notify.on.join(', ') || 'every terminal state'}
                {summary.notify.includeTitle
                  ? '; the ticket title is included'
                  : '; the ticket title is left out'}
                . The note body is never sent.
              </p>
              <ul className="config-list">
                {summary.notify.destinations.map((d, i) => (
                  <li className="config-row" key={`${d.type}-${i}`}>
                    <span className="config-row__type">{d.type}</span>
                    {d.target ? <span className="config-row__meta mono">{d.target}</span> : null}
                    <span className="config-row__meta">{credential(d.credential)}</span>
                    {d.headers && d.headers.length > 0 ? (
                      <span className="config-row__meta">headers: {d.headers.join(', ')}</span>
                    ) : null}
                    {d.signed ? <span className="config-row__meta">signed</span> : null}
                  </li>
                ))}
              </ul>
            </>
          )}

          <h3 className="config-sub">Webhooks</h3>
          {!summary.webhooks.enabled ? (
            <p className="empty-state">
              Inbound webhooks are off: there is no <code>/hooks</code> endpoint to find.
            </p>
          ) : (
            <>
              <p className="config-line">
                Cooldown {summary.webhooks.cooldown}
                {summary.webhooks.match.assignee
                  ? `; only tickets assigned to ${summary.webhooks.match.assignee}`
                  : ''}
                {summary.webhooks.match.statuses?.length
                  ? `; statuses ${summary.webhooks.match.statuses.join(', ')}`
                  : ''}
                {summary.webhooks.match.labels?.length
                  ? `; labels ${summary.webhooks.match.labels.join(', ')}`
                  : ''}
                .
              </p>
              <ul className="config-list">
                {summary.webhooks.sources.map((src) => (
                  <li className="config-row" key={src.name}>
                    <span className="config-row__type">{src.name}</span>
                    <span className="config-row__meta">
                      {src.auth === 'basic' ? 'username and password' : 'signed deliveries'}
                    </span>
                    <span className="config-row__meta">{credential(src.credential)}</span>
                    {src.proxy ? (
                      <span className="config-row__meta mono">via {src.proxy}</span>
                    ) : null}
                  </li>
                ))}
              </ul>
            </>
          )}
          <p className="about-note">
            Read-only. Secrets are never shown: a workspace names a reference, and only the
            scheme of that reference reaches this window.
          </p>
        </>
      )}
    </section>
  )
}
