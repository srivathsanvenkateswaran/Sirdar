import type { DecisionAsk } from '../../api/types'
import { askWhy } from '../../components/session/DecisionBar'
import { AskIcon } from './icons'
import type { StepCall } from './model'

/*
 * The agent's question, as a highlighted message in the flow: an amber left
 * bar, the question, the command it wants to run when the pause is about a
 * command, and the reason. The answer is typed into the composer under it,
 * which is already focused.
 *
 * A permission question is answered from the decision bar above the
 * composer, which already names the call; the card then says only why the
 * run is asking, so the command is not printed twice on one screen.
 */
export default function AskCard({
  question,
  reason,
  at,
  pendingCall,
  ask,
}: {
  /** What the agent asked, off the run's reason. */
  question: string
  /** Why the run stopped, when the reason is not a question: interrupted, rate limited. */
  reason?: string
  at: string
  /** The call the run stopped on without a result, when there is one. */
  pendingCall?: StepCall
  /** The permission question, when that is what the run is waiting on. */
  ask?: DecisionAsk
}): JSX.Element {
  if (ask) {
    return (
      <section className="sc-ask" aria-label="Asks for permission" data-testid="ask-card">
        <div className="sc-ask__h">
          <AskIcon />
          <span>Asks for permission</span>
          {at ? <span className="sc-ask__at">{at}</span> : null}
        </div>
        <p className="sc-ask__q" title={ask.reason}>
          {askWhy(ask)}.
        </p>
        <p className="sc-ask__hint">Allow or deny it below. An answer in words runs nothing.</p>
      </section>
    )
  }
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
