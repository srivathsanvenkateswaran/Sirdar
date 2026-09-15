/**
 * New session, at `#/new`.
 *
 * A placeholder so the route resolves and the sidebar's New session button
 * lands somewhere. The screen itself — the search-style bar for a key or URL,
 * the Triage / RCA / Fix segmented control, the playbook, model and workspace
 * chips, Start, and "Landed today" from the queue — is the next wave's; the
 * design is `docs/design/2026-09-15-screens/SessionEmpty.html`.
 */
export default function NewSession(): JSX.Element {
  return (
    <section className="screen" aria-label="New session">
      <h1 className="screen__title">New session</h1>
      <p>
        Enter a ticket key or URL, choose Triage, RCA or Fix, and start. This screen is being
        built; press <kbd className="kbd">n</kbd> to start a triage from the dialog meanwhile.
      </p>
    </section>
  )
}
