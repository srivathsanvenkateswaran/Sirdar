import { useState, type JSX } from 'react'
import type { RunSummary } from '../../api/types'
import { noteStem } from '../../lib/replyRun'
import { withoutCode } from './useSessionModel'
import './filed-note.css'

const WARNING_PREFIX = /^note not filed:\s*/i

export interface FiledNoteRowProps {
  run: RunSummary
  /** Opens the Note tab beside the transcript. */
  onOpenNote: () => void
  /** Runs one note turn again; resolves once the job has started. */
  onUpdateNote: () => Promise<void>
  updating: boolean
  /** What the last Update note came back with, when it failed. */
  error: string
}

/**
 * The one line under a triage or RCA reply that says where its note went.
 * The reply is what the operator reads; the note is filed after it for the
 * archive, so it is a quiet row one click from the Note tab rather than a
 * card in the reading path. When the note turn failed, the row says why and
 * offers to run it again.
 */
export default function FiledNoteRow({ run, onOpenNote, onUpdateNote, updating, error }: FiledNoteRowProps): JSX.Element | null {
  // The live activity line says the note is being filed; a row here too
  // would say it twice.
  if (run.phase === 'note') return null

  const update = (quiet: boolean) => (
    <button type="button" className={quiet ? 'sd-filed__btn sd-filed__btn--quiet' : 'sd-filed__btn'} disabled={updating} onClick={() => void onUpdateNote()}>
      {updating ? 'Updating note…' : 'Update note'}
    </button>
  )
  const failed = error ? (
    <span className="sd-filed__error" role="alert" dir="auto">
      {error}
    </span>
  ) : null

  if (run.noteWarning) {
    return (
      <div className="sd-filed" data-testid="filed-note" data-state="warning">
        <span className="sd-filed__label" dir="auto">
          Note not filed: {run.noteWarning.replace(WARNING_PREFIX, '')}
        </span>
        {update(false)}
        {failed}
      </div>
    )
  }

  const last = run.notes[run.notes.length - 1]
  if (last && run.status === 'completed') {
    return (
      <div className="sd-filed" data-testid="filed-note" data-state="filed">
        <span className="sd-filed__label">Filed as a note →</span>
        <button type="button" className="sd-filed__btn" dir="auto" onClick={onOpenNote}>
          {noteStem(last)}
        </button>
        {update(true)}
        {failed}
      </div>
    )
  }
  return null
}

export interface SaveNoteRowProps {
  /** Writes the reply into the notes directory; resolves with the path written. */
  onSave: () => Promise<{ path: string }>
}

/**
 * The line under a finished session's reply. A session files no note of its
 * own, so keeping an answer is the operator's call: Save as note writes the
 * newest reply into the notes directory, and the row then names the file.
 */
export function SaveNoteRow({ onSave }: SaveNoteRowProps): JSX.Element {
  const [saving, setSaving] = useState(false)
  const [saved, setSaved] = useState('')
  const [error, setError] = useState('')

  const save = async () => {
    setSaving(true)
    setError('')
    try {
      const { path } = await onSave()
      setSaved(path)
    } catch (err: unknown) {
      setError(withoutCode(err))
    } finally {
      setSaving(false)
    }
  }

  return (
    <div className="sd-filed" data-testid="save-note" data-state={saved ? 'saved' : 'unsaved'}>
      {saved ? (
        <span className="sd-filed__label" dir="auto">
          Saved → {noteStem(saved)}
        </span>
      ) : null}
      <button type="button" className={saved ? 'sd-filed__btn sd-filed__btn--quiet' : 'sd-filed__btn'} disabled={saving} onClick={() => void save()}>
        {saving ? 'Saving…' : saved ? 'Save again' : 'Save as note'}
      </button>
      {error ? (
        <span className="sd-filed__error" role="alert" dir="auto">
          {error}
        </span>
      ) : null}
    </div>
  )
}
