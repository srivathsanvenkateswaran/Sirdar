import { useEffect, useId, useRef, useState, type KeyboardEvent } from 'react'
import Button from '../../ui/button'
import ProviderMark from '../../ui/provider-mark'

/** What the composer's one button does right now. */
export type ComposerMode =
  /** The run stopped to ask; the text is the answer and the button resumes it. */
  | { kind: 'answer'; question: string }
  /** The run has finished; the text is an instruction and the button steers it. */
  | { kind: 'steer' }
  /** Nothing can be sent, and `reason` says why. */
  | { kind: 'disabled'; reason: string }

export interface ComposerProps {
  mode: ComposerMode
  /** The send is in flight. */
  busy: boolean
  /** What the last send came back with; shown under the text, and the button stays live. */
  error: string
  onSend: (text: string) => void
  provider: string
  model: string
  /** Clears the text once a send succeeded. Bump it. */
  sentCount: number
}

/**
 * The bottom of the transcript: where the operator talks back.
 *
 * One box, one button, and the button's word is the run's state — Answer
 * while the agent is waiting on a question, Steer once the run has finished,
 * and disabled with the reason while it is working or when the provider
 * refuses to be steered. The button is this screen's one filled control and
 * it stays beside the text it sends; the sidebar's New session steps down
 * while it is on screen.
 *
 * Cmd or Ctrl with Enter sends, so a person typing does not have to reach
 * for the mouse; Enter alone is a new line, because an answer to an agent's
 * question is often more than one.
 */
export default function Composer({
  mode,
  busy,
  error,
  onSend,
  provider,
  model,
  sentCount,
}: ComposerProps) {
  const [text, setText] = useState('')
  const id = useId()
  const box = useRef<HTMLTextAreaElement | null>(null)

  // A send that went through empties the box; one that failed keeps the
  // words so they can be sent again.
  useEffect(() => {
    setText('')
  }, [sentCount])

  const trimmed = text.trim()
  const label = mode.kind === 'answer' ? 'Answer' : mode.kind === 'steer' ? 'Steer' : 'Steer'
  const needsText = mode.kind === 'steer' || (mode.kind === 'answer' && mode.question !== '')
  const disabled = mode.kind === 'disabled' || (needsText && trimmed === '')
  const title =
    mode.kind === 'disabled'
      ? mode.reason
      : needsText && trimmed === ''
        ? mode.kind === 'answer'
          ? 'Type the answer first'
          : 'Type the instruction first'
        : undefined
  const placeholder =
    mode.kind === 'answer'
      ? mode.question
        ? 'Answer the question'
        : 'Anything the agent should know before it goes on (optional)'
      : mode.kind === 'steer'
        ? 'What should the agent do next?'
        : mode.reason

  function send(): void {
    if (disabled || busy) return
    onSend(trimmed)
  }

  function onKeyDown(e: KeyboardEvent<HTMLTextAreaElement>): void {
    if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) {
      e.preventDefault()
      send()
    }
  }

  return (
    <form
      className="composer"
      aria-label={label}
      data-mode={mode.kind}
      onSubmit={(e) => {
        e.preventDefault()
        send()
      }}
    >
      <label htmlFor={id} className="visually-hidden">
        {label}
      </label>
      <textarea
        id={id}
        ref={box}
        className="composer-text"
        value={text}
        placeholder={placeholder}
        disabled={mode.kind === 'disabled'}
        rows={3}
        dir="auto"
        onChange={(e) => setText(e.target.value)}
        onKeyDown={onKeyDown}
      />
      {error ? (
        <p className="composer-error" role="alert">
          {error}
        </p>
      ) : null}
      <div className="composer-bar">
        <span className="chip">
          Model
          <ProviderMark provider={provider} size="sm" />
          <span className="chip-mono">
            {provider}
            {model ? ` · ${model}` : ''}
          </span>
        </span>
        {mode.kind === 'disabled' ? <span className="composer-reason">{mode.reason}</span> : null}
        <Button
          type="submit"
          variant="primary"
          shortcut="⌘↵"
          busy={busy}
          disabled={disabled}
          title={title}
        >
          {busy ? (mode.kind === 'answer' ? 'Answering…' : 'Steering…') : label}
        </Button>
      </div>
    </form>
  )
}
