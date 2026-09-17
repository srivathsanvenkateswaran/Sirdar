import './asked-for.css'

/**
 * What the operator asked for when they started the run, in their own
 * words, beside the run's own facts in the session header.
 *
 * The run's prompt carries the same text as its Operator's request section,
 * and this is the header's short answer to "why is this session doing
 * that": a session started with "check the tax rounding first" reads
 * differently from one started with nothing, and nothing else on the screen
 * says so. Nothing is drawn for a run started without one — most are.
 *
 * It is one line, clipped, with the whole of it as the title, because a
 * request can be a paragraph and the header is a row.
 */
export default function AskedFor({ instruction }: { instruction?: string }): JSX.Element | null {
  const text = (instruction ?? '').trim()
  if (text === '') return null
  return (
    <span className="sd-asked-for" title={text} dir="auto">
      <span className="sd-asked-for__label">Asked</span>{' '}
      <span className="sd-asked-for__text">{text}</span>
    </span>
  )
}
