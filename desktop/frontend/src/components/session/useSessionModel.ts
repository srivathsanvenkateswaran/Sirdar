import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import type { NoteKind, RunDetail, RunDiff, Transport } from '../../api/types'
import { parseBundle, type BundleModel } from '../../lib/bundle'
import { notePathFor, type IndexedEvent } from '../../lib/events'
import { reasonOf } from '../../lib/format'
import { parseNote, type NoteModel } from '../../lib/note'
import { rekeyAfterDrop } from '../../lib/review'
import { hunkKey, type HunkDecision } from '../../ui/diff-view'
import type { DropRecord } from './ChangesView'
import { buildSessionModel, clock, type SessionModel } from './model'

const TERMINAL = new Set(['completed', 'failed', 'over_budget'])

/** The reason without the code the transport prefixes it with. */
export function withoutCode(err: unknown): string {
  return reasonOf(err).replace(/^(?:conflict|not_found|no_diff|forbidden|internal|unsupported):\s*/, '')
}

function isMissing(err: unknown): boolean {
  return /^not_found:|^404\b|\bnot found\b|no such/i.test(reasonOf(err))
}

export interface NoteState {
  /** null while reading; '' when the run has none yet. */
  text: string | null
  parsed: NoteModel | null
  error: string
  kind: NoteKind
  path: string
}

export interface DiffState {
  diff: RunDiff | null
  loading: boolean
  error: string
  decisions: Record<string, HunkDecision>
  dropping?: string
  refusal: string
  keep: (path: string, hunk: number) => void
  drop: (path: string, hunk: number) => void
}

export interface SessionData {
  model: SessionModel
  note: NoteState
  bundle: { parsed: BundleModel | null; error: string }
  changes: DiffState
  drops: DropRecord[]
  everything: boolean
  setEverything: (v: boolean) => void
}

/**
 * Everything a session layout draws, built once and handed to whichever
 * layout is chosen: the pure model off the run and its log, and the three
 * artefacts that need the transport — the note (re-read when the run
 * finishes), the prompt for the bundle, and a fix run's change with Keep
 * and Drop wired to `dropHunk`. Nothing here fetches on its own schedule:
 * each read is keyed to the run and to `finished`.
 */
export function useSessionModel(
  transport: Transport,
  workspaceId: string,
  runId: string,
  detail: RunDetail | null,
  events: IndexedEvent[],
  finished: number,
): SessionData {
  const [everything, setEverything] = useState(false)

  // ---- the note
  const noteKind: NoteKind = detail?.kind === 'rca' ? 'rca' : detail?.kind === 'fix' ? '' : 'triage'
  const wantNote = detail !== null && detail.kind !== 'fix'
  const [noteText, setNoteText] = useState<string | null>(null)
  const [noteError, setNoteError] = useState('')
  const status = detail?.status ?? ''
  const settled = TERMINAL.has(status) || status === 'blocked'
  useEffect(() => {
    if (!wantNote) {
      setNoteText('')
      setNoteError('')
      return
    }
    let cancelled = false
    setNoteText(null)
    setNoteError('')
    transport
      .note(workspaceId, runId, noteKind)
      .then((text) => {
        if (!cancelled) setNoteText(text)
      })
      .catch((err: unknown) => {
        if (cancelled) return
        setNoteText('')
        setNoteError(isMissing(err) ? '' : reasonOf(err))
      })
    return () => {
      cancelled = true
    }
  }, [transport, workspaceId, runId, noteKind, wantNote, finished, settled])
  const parsedNote = useMemo(() => (noteText ? parseNote(noteText) : null), [noteText])
  const notePath = notePathFor(noteKind, detail?.notes)

  // ---- the bundle (the prompt)
  const [prompt, setPrompt] = useState<string | null>(null)
  const [promptError, setPromptError] = useState('')
  useEffect(() => {
    let cancelled = false
    setPrompt(null)
    setPromptError('')
    transport
      .prompt(workspaceId, runId)
      .then((text) => {
        if (!cancelled) setPrompt(text)
      })
      .catch((err: unknown) => {
        if (cancelled) return
        setPrompt('')
        setPromptError(reasonOf(err))
      })
    return () => {
      cancelled = true
    }
  }, [transport, workspaceId, runId])
  const bundle = useMemo(() => (prompt === null ? null : parseBundle(prompt)), [prompt])

  // ---- the change
  const isFix = detail?.kind === 'fix'
  const [diff, setDiff] = useState<RunDiff | null>(null)
  const [diffLoading, setDiffLoading] = useState(false)
  const [diffError, setDiffError] = useState('')
  const [decisions, setDecisions] = useState<Record<string, HunkDecision>>({})
  const [dropping, setDropping] = useState<string | undefined>()
  const [refusal, setRefusal] = useState('')
  const generation = useRef(0)

  const load = useCallback(async () => {
    const mine = ++generation.current
    setDiffLoading(true)
    setDiffError('')
    try {
      const d = await transport.runDiff(workspaceId, runId)
      if (mine !== generation.current) return
      setDiff(d)
      setDecisions({})
    } catch (err: unknown) {
      if (mine !== generation.current) return
      setDiff(null)
      setDiffError(withoutCode(err))
    } finally {
      if (mine === generation.current) setDiffLoading(false)
    }
  }, [transport, workspaceId, runId])

  useEffect(() => {
    if (!isFix) {
      setDiff(null)
      setDiffError('')
      return
    }
    setRefusal('')
    void load()
    return () => {
      generation.current += 1
    }
  }, [isFix, load, finished])

  const keep = useCallback((path: string, index: number) => {
    setDecisions((prev) => {
      const key = hunkKey(path, index)
      const next = { ...prev }
      if (next[key] === 'kept') delete next[key]
      else next[key] = 'kept'
      return next
    })
  }, [])

  const drop = useCallback(
    async (path: string, index: number) => {
      if (!diff) return
      const key = hunkKey(path, index)
      setDropping(key)
      setRefusal('')
      try {
        const after = await transport.dropHunk(workspaceId, runId, { path, hunk: index, etag: diff.etag })
        setDiff(after)
        setDecisions((prev) => rekeyAfterDrop(prev, path, index))
      } catch (err: unknown) {
        setRefusal(`${path} hunk ${index + 1}: ${withoutCode(err)}`)
        void load()
      } finally {
        setDropping(undefined)
      }
    },
    [diff, transport, workspaceId, runId, load],
  )

  // ---- the model
  const model = useMemo(
    () =>
      buildSessionModel(detail, events, {
        everything,
        noteEvidence: parsedNote?.rootCause?.evidence,
        diff,
      }),
    [detail, events, everything, parsedNote, diff],
  )

  const drops = useMemo<DropRecord[]>(
    () =>
      events
        .filter((e) => e.event.kind === 'review' && e.event.payload?.action === 'drop')
        .map((e) => ({
          path: String(e.event.payload?.path ?? ''),
          hunk: Number(e.event.payload?.hunk ?? 0),
          at: clock(e.event.t, detail?.startedAt),
        })),
    [events, detail?.startedAt],
  )

  return {
    model,
    note: { text: noteText, parsed: parsedNote, error: noteError, kind: noteKind, path: notePath },
    bundle: { parsed: bundle, error: promptError },
    changes: {
      diff,
      loading: diffLoading,
      error: diffError,
      decisions,
      dropping,
      refusal,
      keep,
      drop: (path, hunk) => void drop(path, hunk),
    },
    drops,
    everything,
    setEverything,
  }
}
