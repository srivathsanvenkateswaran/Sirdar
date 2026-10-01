import { useEffect, useState } from 'react'
import type { PermissionDecision, RunDetail } from '../../api/types'
import ChipMenu from '../composer/ChipMenu'
import ComposerCard from '../composer/ComposerCard'
import { MODES_WITH_ACCESS, modeChipTitle, runningPlaceholder } from '../composer/modes'
import ProviderMark from '../../ui/provider-mark'
import DecisionBar from './DecisionBar'
import { QuestionIcon } from './icons'
import type { ComposerState } from './model'

export interface ComposerStripProps {
  state: ComposerState
  detail: RunDetail
  busy: boolean
  /** What the last send came back with; the button stays live. */
  error: string
  /** Sends the text: the answer while blocked, the instruction once finished. */
  onSend: (text: string) => void
  /** Answers a permission question from the decision bar. */
  onDecide?: (decision: PermissionDecision) => void
  /** Clears the text once a send succeeded. Bump it. */
  sentCount: number
  /** How many playbooks the prompt carried. */
  playbooks?: number
  /** "Your last steer at 02:02 added …" */
  lastSteer?: { at: string; text: string }
  /** Stops the run. The strip's round button while the run works. */
  onStop?: () => void
  /** Only a run this window started can be stopped; the button says so when it cannot. */
  canStop?: boolean
  /** The stop is in flight. */
  stopBusy?: boolean
}

/**
 * The strip under the document: Reply when the run is blocked — the
 * question in the blocked hue and Answer, or, when the question is a
 * permission one, the decision bar (Allow once / Allow for this run / Deny)
 * above the box, whose Allow once is then the one filled button and Answer
 * the bordered way to reply in words — and Steer when the run has finished, with
 * the resume handle, the playbook count, the provider and the mode with
 * its posture as one row of chips that never wraps.
 *
 * While the run works the box says what typing there does and the button
 * beside it is a Stop, wired to the same cancel the window header used to
 * carry.
 */
export default function ComposerStrip({
  state,
  detail,
  busy,
  error,
  onSend,
  onDecide,
  sentCount,
  playbooks,
  lastSteer,
  onStop,
  canStop = false,
  stopBusy = false,
}: ComposerStripProps): JSX.Element {
  const [text, setText] = useState('')
  useEffect(() => {
    setText('')
  }, [sentCount])

  const reply = state.kind === 'reply'
  const ask = reply && onDecide ? detail.question?.decision : undefined
  const running = state.kind === 'running'
  const mode = reply ? 'reply' : state.kind === 'steer' ? 'steer' : running ? 'running' : 'off'
  const trimmed = text.trim()
  const label = reply ? 'Answer' : 'Steer'
  const needsText = state.kind === 'steer' || reply
  const disabled = state.kind === 'disabled' || running || (needsText && trimmed === '')
  const rule = reply && !ask ? state.suggestedRule : undefined
  const title =
    state.kind === 'disabled'
      ? state.reason
      : needsText && trimmed === ''
        ? reply
          ? 'Type the answer first'
          : 'Type the instruction first'
        : `${label} (↵)`
  const placeholder = running
    ? runningPlaceholder(detail.provider)
    : state.kind === 'disabled'
      ? state.reason
      : reply
        ? ask
          ? 'Or answer in words — the call is not run'
          : 'Answer the question'
        : `Steer the agent — it resumes ${detail.kind === 'fix' ? 'in the worktree with the diff and the checks in context' : 'from where it stopped'}.${
            lastSteer ? ` Your last steer at ${lastSteer.at}.` : ''
          }`

  return (
    <div className="sn-strip" data-mode={mode} data-testid="composer-strip">
      {/* While the run works the composer says the whole of it; a heading
          above it would be the same sentence a second time. */}
      {running ? null : (
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
      )}
      {ask && onDecide ? (
        <DecisionBar
          ask={ask}
          busy={busy}
          onDecide={(verdict, reason) => onDecide({ verdict, ...(reason ? { reason } : {}) })}
        />
      ) : reply ? (
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
        disabled={state.kind === 'disabled' || running}
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
              <>
                <span className="sn-chip">
                  <ProviderMark provider={detail.provider} size="sm" />
                  <span className="sn-mono">
                    {detail.provider} · {detail.model || 'model unknown'}
                  </span>
                </span>
                {/* Access is what the mode does to the tree, so it rides in
                    the Mode chip rather than taking a chip of its own. */}
                <ChipMenu label="Mode" value={detail.kind} items={MODES_WITH_ACCESS} readOnly={modeChipTitle(detail.kind)} />
              </>
            ) : null}
          </>
        }
        send={{
          label,
          busyLabel: reply ? 'Answering…' : 'Steering…',
          busy,
          disabled,
          title,
          onClick: () => onSend(trimmed),
          quiet: Boolean(ask),
        }}
        stop={
          running && onStop
            ? {
                onStop,
                disabled: !canStop,
                busy: stopBusy,
                title: canStop ? 'Stop the run' : 'Only a run started from this window can be stopped',
              }
            : undefined
        }
      />
    </div>
  )
}
