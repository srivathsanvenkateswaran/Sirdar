import { useId, type KeyboardEvent, type ReactNode, type Ref } from 'react'
import Button from '../../ui/button'
import './composer.css'

export interface ComposerSend {
  /** The button's word: Start, Answer, Steer. Its accessible name. */
  label: string
  /** The word while the send is in flight: Starting…, Answering…. */
  busyLabel?: string
  busy: boolean
  disabled: boolean
  /** Why it is off, or the shortcut when it is on. */
  title?: string
  onClick: () => void
}

export interface ComposerCardProps {
  /** Names the textarea for a screen reader; the placeholder is not a name. */
  label: string
  value: string
  onChange: (value: string) => void
  placeholder: string
  disabled?: boolean
  autoFocus?: boolean
  rows?: number
  textareaRef?: Ref<HTMLTextAreaElement>
  /** One line under the text, in the failed hue: what the last send came back with. */
  error?: string
  /** The bar's chips, drawn with a hairline between each. */
  chips: ReactNode
  /** Words beside the chips: why nothing can be sent. */
  aside?: ReactNode
  send: ComposerSend
  /** What the card is, for the form's name: `Start`, `Answer`, `Steer`. */
  name?: string
}

/** lucide `arrow-up`. */
function ArrowUpIcon(): JSX.Element {
  return (
    <svg
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="2"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
      focusable="false"
    >
      <path d="M12 19V5M5 12l7-7 7 7" />
    </svg>
  )
}

/**
 * The composer: one card holding a textarea and a bottom bar.
 *
 * The bar is the T3 shape on Sirdar's tokens: chips at the inline start —
 * Model, Mode, Access — separated by hairlines, and at the inline end one
 * round 36px send button carrying an arrow. That button is the screen's one
 * filled control, so the screen that draws this card publishes its action
 * with `placement: 'screen'` and the sidebar's New session steps down.
 *
 * Cmd or Ctrl with Enter sends; Enter alone is a new line, because what is
 * typed here is often more than one — an answer to an agent's question, an
 * instruction, a description of what to look at.
 */
export default function ComposerCard({
  label,
  value,
  onChange,
  placeholder,
  disabled = false,
  autoFocus = false,
  rows = 3,
  textareaRef,
  error,
  chips,
  aside,
  send,
  name,
}: ComposerCardProps): JSX.Element {
  const id = useId()

  function submit(): void {
    if (send.disabled || send.busy) return
    send.onClick()
  }

  function onKeyDown(e: KeyboardEvent<HTMLTextAreaElement>): void {
    if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) {
      e.preventDefault()
      submit()
    }
  }

  return (
    <form
      className="composer"
      aria-label={name ?? label}
      onSubmit={(e) => {
        e.preventDefault()
        submit()
      }}
    >
      <label htmlFor={id} className="visually-hidden">
        {label}
      </label>
      <textarea
        id={id}
        ref={textareaRef}
        className="composer-text"
        value={value}
        placeholder={placeholder}
        disabled={disabled}
        autoFocus={autoFocus}
        rows={rows}
        dir="auto"
        onChange={(e) => onChange(e.target.value)}
        onKeyDown={onKeyDown}
      />
      {error ? (
        <p className="composer-error" role="alert">
          {error}
        </p>
      ) : null}
      <div className="composer-bar">
        <div className="composer-bar__chips">{chips}</div>
        {aside ? <span className="composer-reason">{aside}</span> : null}
        <span className="composer-send">
          <Button
            type="submit"
            variant="primary"
            iconOnly
            icon={<ArrowUpIcon />}
            busy={send.busy}
            disabled={send.disabled && !send.busy}
            title={send.title}
            aria-keyshortcuts="Meta+Enter"
          >
            {send.busy ? (send.busyLabel ?? send.label) : send.label}
          </Button>
        </span>
      </div>
    </form>
  )
}
