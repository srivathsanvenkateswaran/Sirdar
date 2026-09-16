import { useCallback, useId, useLayoutEffect, useRef, type KeyboardEvent, type ReactNode, type Ref } from 'react'
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
  /**
   * Draw the word beside the arrow rather than the arrow alone: a blocked
   * run's composer reads "Answer" in full, because that send is the
   * screen's primary action and the word is what the reader is looking for.
   */
  wide?: boolean
}

export interface ComposerCardProps {
  /** Names the textarea for a screen reader; the placeholder is not a name. */
  label: string
  value: string
  onChange: (value: string) => void
  placeholder: string
  disabled?: boolean
  autoFocus?: boolean
  /**
   * How many lines the box grows to before it scrolls inside. It starts one
   * line high and follows the text; the default is eight.
   */
  maxRows?: number
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
  /**
   * `card` is the New session and Conversation shape: a tall textarea and
   * the round arrow send. `strip` is the Document layout's: one line of
   * text until typed into, and the send as a labelled button with its
   * shortcut, because the strip sits under a document and reads as a form.
   */
  variant?: 'card' | 'strip'
  /** Drawn on the bar before the send, after the chips: a decision segment. */
  trailing?: ReactNode
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

/** The most lines the box grows to before the text scrolls inside it. */
export const MAX_ROWS = 8

/**
 * Sizes the box to its text: one line at rest, one more per line typed,
 * `maxRows` at most. Hard newlines are counted from the value; soft wraps
 * are measured off `scrollHeight` against the computed line height, which
 * is zero where nothing lays out (jsdom), so the count of newlines is the
 * floor. An empty box is measured with its placeholder standing in as the
 * text, so a prompt that wraps in a narrow column is read whole rather than
 * cut off — and so every engine sizes it the same, whether or not it counts
 * the placeholder in `scrollHeight` (Chromium does, WebKit does not). The
 * height comes from `rows`, so the card's chrome never moves on focus —
 * nothing here reads the focus state.
 */
export function fitRows(el: HTMLTextAreaElement, maxRows: number): number {
  const hard = el.value.split('\n').length
  // Measure from one row, or a box that shrank never reports it.
  el.rows = 1
  const standIn = el.value === '' && el.placeholder !== ''
  if (standIn) el.value = el.placeholder
  const style = getComputedStyle(el)
  const line = parseFloat(style.lineHeight)
  const pad = (parseFloat(style.paddingBlockStart) || 0) + (parseFloat(style.paddingBlockEnd) || 0)
  const soft = Number.isFinite(line) && line > 0 && el.scrollHeight > 0 ? Math.round((el.scrollHeight - pad) / line) : 0
  if (standIn) el.value = ''
  const rows = Math.min(maxRows, Math.max(1, hard, soft))
  el.rows = rows
  return rows
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
 * instruction, a description of what to look at. The box is one line high
 * at rest, with the chip bar directly under it, and grows a line at a time
 * to `maxRows` before the text scrolls inside it.
 */
export default function ComposerCard({
  label,
  value,
  onChange,
  placeholder,
  disabled = false,
  autoFocus = false,
  maxRows = MAX_ROWS,
  textareaRef,
  error,
  chips,
  aside,
  send,
  name,
  variant = 'card',
  trailing,
}: ComposerCardProps): JSX.Element {
  const id = useId()
  const strip = variant === 'strip'
  const box = useRef<HTMLTextAreaElement | null>(null)
  const setBox = useCallback(
    (el: HTMLTextAreaElement | null) => {
      box.current = el
      if (typeof textareaRef === 'function') textareaRef(el)
      else if (textareaRef) (textareaRef as { current: HTMLTextAreaElement | null }).current = el
    },
    [textareaRef],
  )

  // Before paint, so a box that grew or shrank never shows its old height.
  useLayoutEffect(() => {
    if (box.current) fitRows(box.current, maxRows)
  }, [value, maxRows, disabled, placeholder])

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
      data-variant={strip ? 'strip' : undefined}
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
        ref={setBox}
        className="composer-text"
        value={value}
        placeholder={placeholder}
        disabled={disabled}
        autoFocus={autoFocus}
        rows={1}
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
        {trailing}
        <span className="composer-send" data-wide={send.wide ? 'true' : undefined}>
          {strip ? (
            <Button
              type="submit"
              variant="primary"
              busy={send.busy}
              disabled={send.disabled && !send.busy}
              title={send.title}
              shortcut="⌘↵"
              aria-keyshortcuts="Meta+Enter"
            >
              {send.busy ? (send.busyLabel ?? send.label) : send.label}
            </Button>
          ) : (
            <Button
              type="submit"
              variant="primary"
              iconOnly={!send.wide}
              icon={<ArrowUpIcon />}
              busy={send.busy}
              disabled={send.disabled && !send.busy}
              title={send.title}
              aria-keyshortcuts="Meta+Enter"
            >
              {send.busy ? (send.busyLabel ?? send.label) : send.label}
            </Button>
          )}
        </span>
      </div>
    </form>
  )
}
