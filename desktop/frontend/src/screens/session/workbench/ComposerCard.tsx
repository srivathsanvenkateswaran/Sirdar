import { useEffect, useId, useRef, useState, type JSX, type KeyboardEvent } from 'react'
import type { RunKind } from '../../../api/types'
import type { ComposerMode } from '../../../components/run/Composer'
import { accessOf } from '../../../components/composer/modes'
import Button from '../../../ui/button'
import ProviderMark from '../../../ui/provider-mark'
import { ChevronDownIcon, LockIcon, PencilIcon } from './icons'

/**
 * The command bar: the composer as one line at the very bottom of the
 * window — a `›` prompt, a field that grows only as the reader types into
 * it, the model, mode and access as chips, and the screen's one filled
 * button, Send or Answer with its ⌘↵ hint. Blocked, the mode chip says
 * `answer` and the question band above the bar carries the question; the
 * bar itself stays one line.
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
  /** Focused when the reader presses ⌘↵ with nothing typed, or when the run blocks. */
  autoFocus?: boolean
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

  const trimmed = text.trim()
  const label = mode.kind === 'answer' ? 'Answer' : 'Send'
  const needsText = mode.kind === 'steer' || (mode.kind === 'answer' && mode.question !== '')
  const disabled = mode.kind === 'disabled' || (needsText && trimmed === '')
  const title =
    mode.kind === 'disabled'
      ? mode.reason
      : needsText && trimmed === ''
        ? mode.kind === 'answer'
          ? 'Type the answer first'
          : 'Type the instruction first'
        : `${label} (⌘↵)`

  const submit = () => {
    if (disabled || busy) return
    onSend(trimmed)
  }

  const onKeyDown = (e: KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) {
      e.preventDefault()
      submit()
    }
  }

  const access = kind ? accessOf(kind) : undefined

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
        placeholder={mode.kind === 'disabled' ? mode.reason : placeholder}
        disabled={mode.kind === 'disabled'}
        autoFocus={autoFocus}
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
      <span className="wb-chip" title="How the next message continues the run">
        <span className="wb-chip__lab">mode</span>
        <span className="wb-mono">{mode.kind === 'answer' ? 'answer' : 'resume'}</span>
      </span>
      {access ? (
        <span className="wb-chip" title={access === 'worktree' ? 'This run writes in its linked worktree' : 'This run reads the workspace and writes nothing'}>
          {access === 'worktree' ? <PencilIcon /> : <LockIcon />}
          <span className="wb-chip__lab">access</span>
          <span className="wb-mono">{access}</span>
        </span>
      ) : null}
      <Button
        type="submit"
        variant="primary"
        shortcut="⌘↵"
        busy={busy}
        disabled={disabled && !busy}
        title={title}
        aria-keyshortcuts="Meta+Enter"
      >
        {busy ? (mode.kind === 'answer' ? 'Answering…' : 'Sending…') : label}
      </Button>
    </form>
  )
}
