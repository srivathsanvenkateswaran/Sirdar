import { AskIcon } from './icons'
import type { StepCall } from './model'

/*
 * The agent's question, as a highlighted message in the flow: an amber left
 * bar, the question, the command it wants to run when the pause is about a
 * command, and the reason. The answer is typed into the composer under it,
 * which is already focused; the card says so rather than offering buttons
 * the service has no route for. The one route a blocked run has is
 * `resume(answer)`, and that is the composer's Answer.
 */
export default function AskCard({
  question,
  reason,
  at,
  pendingCall,
}: {
  /** What the agent asked, off the run's reason. */
  question: string
  /** Why the run stopped, when the reason is not a question: interrupted, rate limited. */
  reason?: string
  at: string
  /** The call the run stopped on without a result, when there is one. */
  pendingCall?: StepCall
}): JSX.Element {
  const heading = question ? 'Needs your answer' : 'Waiting on you'
  return (
    <section className="sc-ask" aria-label={heading} data-testid="ask-card">
      <div className="sc-ask__h">
        <AskIcon />
        <span>{heading}</span>
        {at ? <span className="sc-ask__at">{at}</span> : null}
      </div>
      <p className="sc-ask__q" dir="auto">
        {question || reason || 'The run stopped and is waiting for you.'}
      </p>
      {pendingCall ? (
        <div className="sc-ask__cmd" dir="ltr">
          <span>{pendingCall.summary}</span>
          {pendingCall.description ? <span className="sc-ask__d">{pendingCall.description}</span> : null}
        </div>
      ) : null}
      {pendingCall?.suggestedRule ? (
        <p className="sc-ask__why">
          Suggested rule: allow <span className="sc-mono">{pendingCall.suggestedRule}</span> in this workspace.
        </p>
      ) : null}
      <p className="sc-ask__hint">Answer below — the run resumes with your message.</p>
    </section>
  )
}
