/** Three small dots marking which of the triage/RCA/resolution notes exist. */
export default function NoteDots({
  triage,
  rca,
  resolution,
}: {
  triage: boolean
  rca: boolean
  resolution: boolean
}) {
  return (
    <span className="note-dots">
      <span className={triage ? 'note-dot note-dot--on' : 'note-dot'} title="Triage note" />
      <span className={rca ? 'note-dot note-dot--on' : 'note-dot'} title="RCA note" />
      <span className={resolution ? 'note-dot note-dot--on' : 'note-dot'} title="Resolution note" />
    </span>
  )
}
