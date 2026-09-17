import { useEffect, useId, useRef, useState, type JSX, type KeyboardEvent } from 'react'
import type { RunKind } from '../../../api/types'
import type { ComposerMode } from '../../../components/run/Composer'
import { ACCESS, ACCESS_PHRASE, accessOf, runningPlaceholder } from '../../../components/composer/modes'
import Button from '../../../ui/button'
import ProviderMark from '../../../ui/provider-mark'
import { ChevronDownIcon, LockIcon, PencilIcon } from './icons'

/** Stop: a filled square on the 24 grid, the one glyph that reads as "stop this". */
function StopIcon(): JSX.Element {
  return (
    <svg viewBox="0 0 24 24" fill="currentColor" stroke="none" aria-hidden="true" focusable="false">
      <rect x="7" y="7" width="10" height="10" rx="2" />
    </svg>
  )
}

/**
 * The command bar: the composer as one line at the very bottom of the
 * window — a `›` prompt, a field that grows only as the reader types into
 * it, then one row of chips that never wraps (the model, and the mode
 * carrying the posture it runs under) and the screen's one filled button.
 * Send or Answer with its ↵ hint once the run has stopped; a round Stop
 * while it is working. Blocked, the question band above the bar carries the
 * question; the bar itself stays one line.
 *
 * A local adapter with the shared `ComposerCard`'s name and its `strip`
 * variant's props. Cmd or Ctrl with Enter sends; Enter alone is a new line,
 * as everywhere else in the app.
 */
export interface ComposerStripProps {
  variant: 'strip'
  mode: ComposerMode
  busy: boolean
  error: string
  onSend: (text: string) => void
  provider: string
  model: string
  kind?: RunKind
  /** Clears the text once a send succeeded. Bump it. */
  sentCount: number
  /** What the placeholder says when nothing has been typed. */
  placeholder: string
  /** Text put into the field from outside — a question option the reader picked. Changes on each pick. */
  prefill?: { text: string; n: number }
  /** Focused when the reader presses ↵ with nothing typed, or when the run blocks. */
  autoFocus?: boolean
  /** Stops the run. The bar's round button while the run works. */
  onStop?: () => void
  /** Only a run this window started can be stopped; the button says so when it cannot. */
  canStop?: boolean
  /** The stop is in flight. */
  stopBusy?: boolean
}

export default function ComposerCard({
  mode,
  busy,
  error,
  onSend,
  provider,
  model,
  kind,
  sentCount,
  placeholder,
  prefill,
  autoFocus = false,
  onStop,
  canStop = false,
  stopBusy = false,
}: ComposerStripProps): JSX.Element {
  const [text, setText] = useState('')
  const field = useRef<HTMLTextAreaElement | null>(null)
  const id = useId()

  useEffect(() => {
    setText('')
  }, [sentCount])

  useEffect(() => {
    if (!prefill) return
    setText(prefill.text)
    field.current?.focus()
  }, [prefill])

  // One line until the text needs more.
  useEffect(() => {
    const el = field.current
    if (!el) return
    el.style.blockSize = 'auto'
    el.style.blockSize = `${Math.min(el.scrollHeight, 160)}px`
  }, [text])

  const running = mode.kind === 'running'
  const trimmed = text.trim()
  const label = mode.kind === 'answer' ? 'Answer' : 'Send'
  const needsText = mode.kind === 'steer' || (mode.kind === 'answer' && mode.question !== '')
  const disabled = mode.kind === 'disabled' || running || (needsText && trimmed === '')
  const title =
    mode.kind === 'disabled'
      ? mode.reason
      : needsText && trimmed === ''
        ? mode.kind === 'answer'
          ? 'Type the answer first'
          : 'Type the instruction first'
        : `${label} (↵)`

  const submit = () => {
    // A run that is working has a Stop where its send was.
    if (running || disabled || busy) return
    onSend(trimmed)
  }

  const onKeyDown = (e: KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.key === 'Enter' && !e.shiftKey && !e.nativeEvent.isComposing) {
      e.preventDefault()
      submit()
    }
  }

  const access = kind ? accessOf(kind) : undefined
  const accessNote = ACCESS.find((a) => a.id === access)?.note

  return (
    <form
      className="wb-cmd"
      data-mode={mode.kind}
      aria-label={label}
      onSubmit={(e) => {
        e.preventDefault()
        submit()
      }}
    >
      <span className="wb-cmd__prompt" aria-hidden="true">
        ›
      </span>
      <label htmlFor={id} className="visually-hidden">
        {mode.kind === 'answer' ? 'Your answer' : 'Follow-up instruction'}
      </label>
      <textarea
        id={id}
        ref={field}
        className="wb-cmd__text"
        rows={1}
        value={text}
        placeholder={running ? runningPlaceholder(provider) : mode.kind === 'disabled' ? mode.reason : placeholder}
        disabled={mode.kind === 'disabled' || running}
        autoFocus={autoFocus && !running}
        dir="auto"
        onChange={(e) => setText(e.target.value)}
        onKeyDown={onKeyDown}
      />
      {error ? (
        <span className="wb-cmd__error" role="alert">
          {error}
        </span>
      ) : null}
      <span className="wb-chip" title="A steer resumes the same session, so the provider and model cannot change here">
        <ProviderMark provider={provider} size="sm" />
        <span className="wb-mono" dir="ltr">
          {model || 'model unknown'}
        </span>
        <ChevronDownIcon />
      </span>
      {/* One chip, not two: access is what the mode does to the tree, so
          it is the mode chip's second word. */}
      {kind && access ? (
        <span className="wb-chip" title={accessNote}>
          {access === 'worktree' ? <PencilIcon /> : <LockIcon />}
          <span className="wb-chip__lab">mode</span>
          <span className="wb-mono">
            {kind} · {ACCESS_PHRASE[access]}
          </span>
        </span>
      ) : null}
      {running ? (
        <span className="wb-cmd__stop">
          <Button
            type="button"
            variant="primary"
            iconOnly
            icon={<StopIcon />}
            busy={stopBusy}
            disabled={!canStop && !stopBusy}
            title={canStop ? 'Stop the run' : 'Only a run started from this window can be stopped'}
            onClick={onStop}
          >
            Stop the run
          </Button>
        </span>
      ) : (
        <Button
          type="submit"
          variant="primary"
          shortcut="↵"
          busy={busy}
          disabled={disabled && !busy}
          title={title}
          aria-keyshortcuts="Meta+Enter"
        >
          {busy ? (mode.kind === 'answer' ? 'Answering…' : 'Sending…') : label}
        </Button>
      )}
    </form>
  )
}
