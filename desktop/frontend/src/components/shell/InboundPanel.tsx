import type { InboundDelivery } from '../../store/appStore'

/** The clock time a delivery landed, or its raw stamp when unparseable. */
function at(stamp: string): string {
  const ms = Date.parse(stamp)
  return Number.isNaN(ms) ? stamp : new Date(ms).toLocaleTimeString()
}

/** What each outcome means, on the row itself rather than in a legend. */
const MEANING: Record<InboundDelivery['outcome'], string> = {
  started: 'started a triage',
  skipped: 'skipped — that key is busy or in its cooldown',
  filtered: "did not match this workspace's filter",
  ignored: 'named no ticket',
  rejected: 'rejected — the delivery did not verify',
}

/**
 * Inbound webhook deliveries, newest first.
 *
 * A tracker fires on every field change, so most deliveries do nothing. The
 * panel exists for the case that is otherwise invisible: telling a hook that
 * never arrives apart from one that arrives and is filtered out.
 */
export default function InboundPanel({
  deliveries,
}: {
  deliveries: InboundDelivery[]
}): JSX.Element {
  return (
    <section className="inbound" aria-label="Inbound">
      <h2 className="inbound-head">
        <span className="inbound-name">Inbound</span>
        <span className="inbound-count">{deliveries.length}</span>
      </h2>
      {deliveries.length === 0 ? (
        <p className="inbound-empty">
          No webhook delivery has arrived in this session. Hooks are off unless the workspace
          enables them.
        </p>
      ) : (
        <ul className="inbound-list">
          {deliveries.map((d) => (
            <li className="inbound-row" key={d.id} data-outcome={d.outcome}>
              <span className="inbound-source">{d.source}</span>
              <span className="inbound-key mono">{d.key || '—'}</span>
              <span className="inbound-outcome">{d.outcome}</span>
              <span className="inbound-why">{MEANING[d.outcome]}</span>
              <time className="inbound-at" dateTime={d.at}>
                {at(d.at)}
              </time>
            </li>
          ))}
        </ul>
      )}
    </section>
  )
}
