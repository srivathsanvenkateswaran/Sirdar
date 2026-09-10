import type { RunDetail } from '../../api/types'

/** The run record as the watcher read it, unedited, for when a field matters. */
export default function StateView({ detail }: { detail: RunDetail }) {
  return (
    <div className="pane">
      <pre className="mono-block">{JSON.stringify(detail, null, 2)}</pre>
    </div>
  )
}
