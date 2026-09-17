import { useEffect, useState } from 'react'
import type { RunKind } from '../../api/types'
import ChipMenu from '../composer/ChipMenu'
import ComposerCard from '../composer/ComposerCard'
import { MODES_WITH_ACCESS, modeChipTitle, runningPlaceholder } from '../composer/modes'
import ModelPicker from '../../ui/model-picker'

/** What the composer's one button does right now. */
export type ComposerMode =
  /** The run stopped to ask; the text is the answer and the button resumes it. */
  | { kind: 'answer'; question: string }
  /** The run has finished; the text is an instruction and the button steers it. */
  | { kind: 'steer' }
  /**
   * The run is working. The button is a Stop, not a send that is off, and
   * the box says what typing here does — once, in the placeholder, with
   * nothing repeating it beside the button.
   */
  | { kind: 'running' }
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
  /** The run's kind: the Mode chip's word, and what decides the Access chip's. */
  kind?: RunKind
  /** Clears the text once a send succeeded. Bump it. */
  sentCount: number
  /** Takes focus on mount: a blocked run's composer is already waiting to be typed into. */
  autoFocus?: boolean
  /** The layout's own words for the empty box, in place of the defaults. */
  placeholder?: string
  /** Draw the send's word beside its arrow while the run is blocked. */
  wideWhenAnswering?: boolean
  /** Stops the run. Drawn as the round button while the run works. */
  onCancel?: () => void
  /** Only a run this window started can be stopped; the button says so when it cannot. */
  canCancel?: boolean
  /** The cancel is in flight. */
  cancelBusy?: boolean
  /**
   * The model the next send asks for, when a reader has chosen one that is
   * not the run's own. Empty means the run's, which is what the chip shows.
   */
  pickedModel?: string
  /**
   * Given, the Model chip is a control rather than a fact: what it picks is
   * what the next answer or steer runs under. It is given on a run that has
   * stopped — blocked or finished — because that is when a model can still
   * be chosen; a live run's session already has one.
   */
  onPickModel?: (model: string) => void
}

/**
 * The bottom of the transcript: where the operator talks back.
 *
 * The same card New session draws, with the bar's chips turned into facts:
 * Model is the run's provider and model. While the run works it is a fact;
 * on a run that has stopped — blocked or finished — the chip picks what the
 * next answer or steer runs under, since Claude Code takes a different
 * --model on --resume. Mode is the run's kind, carrying the posture that
 * kind runs with as its secondary text. Two chips, one row, never wrapping.
 * The round button's word is the run's state — Answer while the agent is
 * waiting on a question, Steer once the run has finished, Stop while it is
 * working, and off with the reason when the provider refuses to be steered.
 * The button is this screen's one filled control; the sidebar's New session
 * steps down while it is on screen.
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
  kind,
  sentCount,
  autoFocus = false,
  placeholder: ownPlaceholder,
  wideWhenAnswering = false,
  onCancel,
  canCancel = false,
  cancelBusy = false,
  pickedModel = '',
  onPickModel,
}: ComposerProps) {
  const [text, setText] = useState('')

  // A send that went through empties the box; one that failed keeps the
  // words so they can be sent again.
  useEffect(() => {
    setText('')
  }, [sentCount])

  const running = mode.kind === 'running'
  const trimmed = text.trim()
  const label = mode.kind === 'answer' ? 'Answer' : 'Steer'
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
  // Said once: while the run works the box carries the whole of it, and the
  // button beside it is the Stop.
  const placeholder = running
    ? runningPlaceholder(provider)
    : mode.kind === 'disabled'
      ? mode.reason
      : ownPlaceholder ??
        (mode.kind === 'answer'
          ? mode.question
            ? 'Answer the question'
            : 'Anything the agent should know before it goes on (optional)'
          : 'What should the agent do next?')

  return (
    <div className="session-composer" data-mode={mode.kind}>
      <ComposerCard
        name={label}
        label={label}
        value={text}
        onChange={setText}
        placeholder={placeholder}
        disabled={mode.kind === 'disabled' || running}
        autoFocus={autoFocus && mode.kind !== 'disabled' && !running}
        error={error}
        chips={
          <>
            {/* On a run that has stopped the chip is a control: Claude Code
                takes a different --model on --resume and answers under it, so
                the next answer or steer can be a different model on the same
                session. While the run is working there is nothing to choose —
                its session already has a model — and the chip states the pair
                instead. */}
            <ModelPicker
              provider={provider}
              model={onPickModel ? pickedModel || model : model}
              defaultProvider={provider}
              defaultModel={model}
              unknownAs="model unknown"
              readOnly={
                onPickModel
                  ? undefined
                  : 'A steer resumes the same session, so the provider and model cannot change here'
              }
              // A run's provider cannot change on a resume — the session
              // handle names a session that CLI holds — so a row from
              // another provider, which only the popover's search can
              // reach, is not a model this run could ask for.
              onChange={
                onPickModel
                  ? (choice) => {
                      if (!choice.provider || choice.provider === provider) onPickModel(choice.model)
                    }
                  : undefined
              }
            />
            {/* Access is what the mode does to the tree, so it is the mode
                chip's second word rather than a chip of its own. */}
            {kind ? <ChipMenu label="Mode" value={kind} items={MODES_WITH_ACCESS} readOnly={modeChipTitle(kind)} /> : null}
          </>
        }
        aside={mode.kind === 'disabled' ? mode.reason : undefined}
        send={{
          label,
          busyLabel: mode.kind === 'answer' ? 'Answering…' : 'Steering…',
          busy,
          disabled,
          title,
          onClick: () => onSend(trimmed),
          wide: wideWhenAnswering && mode.kind === 'answer',
        }}
        stop={
          running && onCancel
            ? {
                onStop: onCancel,
                disabled: !canCancel,
                busy: cancelBusy,
                title: canCancel ? 'Stop the run' : 'Only a run started from this window can be stopped',
              }
            : undefined
        }
      />
    </div>
  )
}
