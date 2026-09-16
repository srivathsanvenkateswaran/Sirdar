import ReactMarkdown from 'react-markdown'
import { parseAnswer } from '../../lib/events'
import ProviderMark from '../../ui/provider-mark'
import { AnswerFields } from './AnswerCard'

/**
 * What the model said, in full, as prose. The block is headed by the
 * provider's mark and name and the offset at which it spoke, and its body is
 * the text as markdown — the model writes headings, lists and code spans,
 * and a ledger row that flattened them was where the run's reasoning went
 * missing. Streamed deltas grow this one block; `lib/events.conversation`
 * does the merging.
 *
 * A message that is a bare JSON object — Codex hands the structured answer
 * over as one — is drawn as the answer card's field rows rather than as a
 * one-line wall of braces.
 */
export default function AssistantMessage({
  text,
  provider,
  at,
}: {
  text: string
  provider: string
  at: string
}) {
  const answer = parseAnswer(text)
  return (
    <div className="msg" data-testid="assistant-message">
      <span className="msg-at">{at}</span>
      <div className="msg-block">
        <div className="msg-head">
          {provider ? <ProviderMark provider={provider} size="sm" /> : null}
          <span className="msg-who">{provider || 'assistant'}</span>
        </div>
        {answer ? (
          <AnswerFields data={answer} />
        ) : (
          <div className="msg-body md" dir="auto">
            <ReactMarkdown>{text}</ReactMarkdown>
          </div>
        )}
      </div>
    </div>
  )
}
