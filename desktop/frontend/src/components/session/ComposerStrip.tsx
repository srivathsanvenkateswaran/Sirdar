import { useEffect, useState } from 'react'
import type { RunDetail } from '../../api/types'
import ComposerCard from '../composer/ComposerCard'
import ProviderMark from '../../ui/provider-mark'
import SegmentedControl from '../../ui/segmented-control'
import { QuestionIcon } from './icons'
import type { ComposerState } from './model'

export type Decision = 'once' | 'session' | 'deny'

export interface ComposerStripProps {
  state: ComposerState
  detail: RunDetail
  busy: boolean
  /** What the last send came back with; the button stays live. */
  error: string
  /** Sends the text; while blocked, with the decision the segment holds. */
  onSend: (text: string, decision?: Decision) => void
  /** Clears the text once a send succeeded. Bump it. */
  sentCount: number
  /** How many playbooks the prompt carried. */
  playbooks?: number
  /** "Your last steer at 02:02 added …" */
  lastSteer?: { at: string; text: string }
}

/**
 * The words a decision is sent as. The resume route takes an answer, not a
 * structured verdict, so the decision goes as the first words of the answer
 * and the reader's note follows it.
 */
export function decisionText(decision: Decision, rule: string | undefined, note: string): string {
  const head =
    decision === 'deny'
      ? 'No, do not run it.'
      : decision === 'session' && rule
        ? `Yes, and allow \`${rule}\` for the rest of this session.`
        : 'Yes, run it once.'
  return note ? `${head} ${note}` : head
}

/**
 * The strip under the document: Reply when the run is blocked — the
 * question, its reason and the suggested rule in the blocked hue, a
 * decision segment (Allow once / Allow the rule this run / Deny) and the
 * one filled button, Answer — and Steer when the run has finished, with
 * the resume handle, the playbook count and the provider as chips. While
 * the run works the box is disabled with the reason.
 */
export default function ComposerStrip({ state, detail, busy, error, onSend, sentCount, playbooks, lastSteer }: ComposerStripProps): JSX.Element {
  const [text, setText] = useState('')
  const [decision, setDecision] = useState<Decision>('once')
  useEffect(() => {
    setText('')
  }, [sentCount])

  const reply = state.kind === 'reply'
  const mode = reply ? 'reply' : state.kind === 'steer' ? 'steer' : 'off'
  const trimmed = text.trim()
  const label = reply ? 'Answer' : 'Steer'
  const needsText = state.kind === 'steer' || (reply && !state.pending)
  const disabled = state.kind === 'disabled' || (needsText && trimmed === '')
  const rule = reply ? state.suggestedRule : undefined
  const title =
    state.kind === 'disabled'
      ? state.reason
      : needsText && trimmed === ''
        ? reply
          ? 'Type the answer first'
          : 'Type the instruction first'
        : `${label} (⌘↵)`
  const placeholder =
    state.kind === 'disabled'
      ? state.reason
      : reply
        ? state.pending
          ? 'Add a note for the agent (optional) — it reads it before the command runs.'
          : 'Answer the question'
        : `Steer the agent — it resumes ${detail.kind === 'fix' ? 'in the worktree with the diff and the checks in context' : 'from where it stopped'}.${
            lastSteer ? ` Your last steer at ${lastSteer.at}.` : ''
          }`

  return (
    <div className="sn-strip" data-mode={mode} data-testid="composer-strip">
      <div className="sn-strip__sh">
        <span className="sn-label">{reply ? 'Reply' : 'Steer'}</span>
        <span>
          {reply
            ? `Waiting since ${state.since} · the run is paused until you answer`
            : state.kind === 'steer'
              ? 'The agent resumes this session with the document and its evidence in context.'
              : state.reason}
        </span>
      </div>
      {reply ? (
        <div className="sn-q" role="group" aria-label="The agent's question">
          <span className="sn-q__ic">
            <QuestionIcon />
          </span>
          <div>
            <div className="sn-q__t" dir="auto">
              {state.pending ? (
                <>
                  Run <code className="sn-ref">{state.pending.command || state.pending.object}</code>
                  {detail.kind === 'fix' ? ' in the worktree' : ''}?
                </>
              ) : (
                state.question
              )}
            </div>
            <div className="sn-q__m">
              {state.pending?.description ? <b>{state.pending.description}</b> : null}
              {state.reason ? `${state.pending?.description ? ' · ' : ''}${state.reason.replace(/\.$/, '').toLowerCase()}` : ''}
              {rule ? (
                <>
                  {' '}
                  · suggested rule <b>{rule}</b> for this session
                </>
              ) : null}
            </div>
          </div>
        </div>
      ) : null}
      <ComposerCard
        variant="strip"
        name={label}
        label={label}
        value={text}
        onChange={setText}
        placeholder={placeholder}
        disabled={state.kind === 'disabled'}
        error={error}
        chips={
          <>
            {detail.handle ? (
              <span className="sn-chip">
                Resume <span className="sn-mono">{detail.handle.slice(0, 8)}</span>
              </span>
            ) : null}
            {!reply && playbooks ? (
              <span className="sn-chip">
                Playbooks <span className="sn-mono">{playbooks}</span>
              </span>
            ) : null}
            {!reply ? (
              <span className="sn-chip">
                <ProviderMark provider={detail.provider} size="sm" />
                <span className="sn-mono">
                  {detail.provider} · {detail.model || 'model unknown'}
                </span>
              </span>
            ) : null}
          </>
        }
        trailing={
          reply && state.pending ? (
            <SegmentedControl
              label="Decision"
              value={decision}
              onChange={(id) => setDecision(id as Decision)}
              options={[
                { id: 'once', label: 'Allow once' },
                ...(rule ? [{ id: 'session', label: `Allow ${rule} this run` }] : []),
                { id: 'deny', label: 'Deny' },
              ]}
            />
          ) : undefined
        }
        aside={state.kind === 'disabled' ? undefined : undefined}
        send={{
          label,
          busyLabel: reply ? 'Answering…' : 'Steering…',
          busy,
          disabled,
          title,
          onClick: () => onSend(reply && state.pending ? decisionText(decision, rule, trimmed) : trimmed, reply && state.pending ? decision : undefined),
        }}
      />
    </div>
  )
}
