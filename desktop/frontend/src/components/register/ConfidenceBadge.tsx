/** Small badge for the triage confidence value; plain text, muted when unknown. */
export default function ConfidenceBadge({ value }: { value?: string }) {
  if (!value) return <span className="badge badge--muted">—</span>
  return <span className="badge">{value}</span>
}
