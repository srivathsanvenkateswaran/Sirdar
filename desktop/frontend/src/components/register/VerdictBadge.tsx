const LABELS: Record<string, string> = {
  confirmed: 'Confirmed',
  partial: 'Partial',
  wrong: 'Wrong',
}

const CLASSES: Record<string, string> = {
  confirmed: 'badge--confirmed',
  partial: 'badge--partial',
  wrong: 'badge--wrong',
}

/** Small badge for a triage verdict (confirmed/partial/wrong), muted when unknown. */
export default function VerdictBadge({ value }: { value?: string }) {
  if (!value) return <span className="badge badge--muted">—</span>
  return <span className={`badge ${CLASSES[value] ?? 'badge--muted'}`}>{LABELS[value] ?? value}</span>
}
