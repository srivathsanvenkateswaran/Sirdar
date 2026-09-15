import { useState } from 'react'
import Button from '../../ui/button'

/**
 * The run stopped and is waiting on a person. When it stopped to ask something,
 * `reason` carries the question and it is shown verbatim above the answer box;
 * otherwise the box just restarts the run where it left off.
 */
export default function ResumeBox({
  question,
  reason,
  pending,
  error,
  onResume,
}: {
  question: string
  reason: string
  pending: boolean
  error: string
  onResume: (answer: string) => void
}) {
  const [answer, setAnswer] = useState('')

  return (
    <form
      className="form"
      aria-label="Resume run"
      onSubmit={(e) => {
        e.preventDefault()
        onResume(answer.trim())
      }}
    >
      <p className="form-question">
        {question ? (
          <>
            The agent asked: <strong>{question}</strong>
          </>
        ) : (
          reason || 'This run is waiting to be resumed.'
        )}
      </p>
      <div className="form-row">
        <div className="form-field">
          <label htmlFor="resume-answer">Answer</label>
          <input
            id="resume-answer"
            value={answer}
            placeholder={question ? 'Answer the question' : 'Optional note for the agent'}
            onChange={(e) => setAnswer(e.target.value)}
          />
        </div>
        {/*
          Run detail's one filled button. It stays beside the answer field
          rather than going to the sidebar footer, because the answer is what
          it commits and a commit button a window away from its own input is a
          button nobody presses on purpose.
        */}
        <Button type="submit" variant="primary" disabled={pending}>
          {pending ? 'Resuming…' : 'Resume run'}
        </Button>
      </div>
      {error ? <div className="form-error">{error}</div> : null}
    </form>
  )
}
