/**
 * Change review, at `#/runs/<workspace>/<run>/review`.
 *
 * A placeholder so the route resolves. The screen itself — the run's diff full
 * width with the file rail, the checks, what the agent said and the resolution
 * note, with Keep/Drop per hunk through `Transport.dropHunk` — is the next
 * wave's; the design is `docs/design/2026-09-15-screens/Review.html`.
 */
export default function Review({
  runId,
  onBack,
}: {
  runId: string
  /** Back to the session the change belongs to. */
  onBack: () => void
}): JSX.Element {
  return (
    <section className="screen" aria-label="Change review">
      <h1 className="screen__title">Change review</h1>
      <p>
        The change made by run <code>{runId}</code>, file by file, with Keep and Drop per hunk.
        This screen is being built.
      </p>
      <p>
        <button type="button" className="sd-setting-button" onClick={onBack}>
          Back to the session
        </button>
      </p>
    </section>
  )
}
