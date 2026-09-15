const NOTES: { id: 'triage' | 'rca' | 'resolution'; name: string }[] = [
  { id: 'triage', name: 'Triage note' },
  { id: 'rca', name: 'RCA note' },
  { id: 'resolution', name: 'Resolution note' },
]

/**
 * Three dots marking which of a key's triage, RCA and resolution notes exist.
 *
 * The dots are one image with one name — "Triage note, RCA note" or "No
 * notes" — so a screen reader hears the fact once rather than three unnamed
 * glyphs, and the accent is never the only copy of it.
 */
export default function NoteDots({
  triage,
  rca,
  resolution,
}: {
  triage: boolean
  rca: boolean
  resolution: boolean
}): JSX.Element {
  const on = { triage, rca, resolution }
  const written = NOTES.filter((n) => on[n.id]).map((n) => n.name)
  const name = written.length > 0 ? written.join(', ') : 'No notes'
  return (
    <span className="register-notes" role="img" aria-label={name} title={name}>
      {NOTES.map((n) => (
        <span key={n.id} className="register-notes__dot" data-on={on[n.id] ? 'true' : 'false'} />
      ))}
    </span>
  )
}
