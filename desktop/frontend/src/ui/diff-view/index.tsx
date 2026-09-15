import { useEffect, useMemo, useRef } from 'react'
import type { DiffFile } from '../../api/types'
import Button from '../button'
import { hunkKey, parsePatch, type DiffHunk, type DiffLine, type PatchFile } from './patch'
import './DiffView.css'

export { hunkKey, parsePatch } from './patch'
export type { DiffHunk, DiffLine, DiffLineType, PatchFile } from './patch'

export type DiffMode = 'unified' | 'split'

/** What a reviewer has said about one hunk. A hunk with no entry is undecided. */
export type HunkDecision = 'kept'

export interface DiffViewProps {
  /** The unified patch, as `Transport.runDiff` answers it. */
  patch: string
  /**
   * The service's own file list, from the same answer. When given, a file's
   * status and counts come from here rather than from the patch text: the
   * list is whole even when the patch was cut, and it is what the rail shows.
   */
  files?: DiffFile[]
  /** `unified` is the diff; `split` is a stub until the side-by-side view is built. */
  mode?: DiffMode
  /**
   * Keep and Drop are drawn only while the change can still be edited — the
   * worktree is present and the branch is not pushed. A read-only diff shows
   * no buttons rather than buttons that would be refused.
   */
  editable?: boolean
  /** Per-hunk decisions, keyed by `hunkKey(path, index)`. */
  decisions?: Record<string, HunkDecision>
  /** The hunk whose drop is in flight, by key; its buttons wait. */
  dropping?: string
  onKeep?: (path: string, hunk: number) => void
  onDrop?: (path: string, hunk: number) => void
  /** The file to scroll into view, when the rail picks one. */
  activePath?: string
  /** Set when the service cut the patch at its size limit: the file list is whole, the text is not. */
  truncated?: boolean
}

function CheckIcon(): JSX.Element {
  return (
    <svg
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.5"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
      focusable="false"
    >
      <path d="m5 12 5 5 9-10" />
    </svg>
  )
}

/** The mark the reader sees before the text: the same one git prints. */
const MARKS: Record<DiffLine['type'], string> = { add: '+', del: '−', context: ' ', meta: '' }

function Line({ line }: { line: DiffLine }): JSX.Element {
  const number = line.type === 'add' ? line.newNo : line.oldNo
  return (
    <div className="sd-diff__line" data-type={line.type} role="row">
      <span className="sd-diff__no" role="cell">
        {number ?? ''}
      </span>
      <span className="sd-diff__code" role="cell">
        <span className="sd-diff__mark" aria-hidden="true">
          {MARKS[line.type]}
        </span>
        {line.text}
      </span>
    </div>
  )
}

function Hunk({
  file,
  hunk,
  editable,
  decision,
  dropping,
  onKeep,
  onDrop,
}: {
  file: PatchFile
  hunk: DiffHunk
  editable: boolean
  decision?: HunkDecision
  dropping: boolean
  onKeep?: (path: string, hunk: number) => void
  onDrop?: (path: string, hunk: number) => void
}): JSX.Element {
  const kept = decision === 'kept'
  return (
    <section className="sd-diff__hunk" aria-label={`${file.path} hunk ${hunk.index + 1}`}>
      <header className="sd-diff__hunkhead">
        <span className="sd-diff__header">{hunk.header}</span>
        {editable && (
          <span className="sd-diff__acts">
            <Button
              variant="pale"
              size="sm"
              icon={kept ? <CheckIcon /> : undefined}
              aria-pressed={kept}
              disabled={dropping}
              onClick={() => onKeep?.(file.path, hunk.index)}
            >
              {kept ? 'Kept' : 'Keep'}
            </Button>
            <Button
              variant="ghost"
              size="sm"
              busy={dropping}
              onClick={() => onDrop?.(file.path, hunk.index)}
            >
              {dropping ? 'Dropping…' : 'Drop'}
            </Button>
          </span>
        )}
      </header>
      <div className="sd-diff__lines" role="table" aria-label={`Lines of hunk ${hunk.index + 1}`}>
        {hunk.lines.map((line, i) => (
          <Line key={i} line={line} />
        ))}
      </div>
    </section>
  )
}

/** The word the file header carries beside its counts. */
const STATUS_WORDS: Record<PatchFile['status'], string> = {
  added: 'new',
  modified: 'modified',
  deleted: 'deleted',
  renamed: 'renamed',
}

/**
 * A fix run's change, file by file and hunk by hunk, with Keep and Drop on
 * each hunk.
 *
 * It draws what the service answered and nothing more: the patch is parsed
 * here so the screen does not have to, and a hunk's index in this view is the
 * index the service reverts. Adds and deletions are tinted and also marked —
 * the `+` and `−` git prints — so the change reads without its colour.
 *
 * Keep is a decision the reader files; Drop is a call the screen makes. The
 * view holds neither: it shows the decisions it is handed and asks for the
 * drops it is asked for, so the session's Changes pane and the review screen
 * can share it while keeping their own state.
 */
export default function DiffView({
  patch,
  files: listed,
  mode = 'unified',
  editable = false,
  decisions = {},
  dropping,
  onKeep,
  onDrop,
  activePath,
  truncated = false,
}: DiffViewProps): JSX.Element {
  const files = useMemo(() => {
    const parsed = parsePatch(patch)
    if (!listed) return parsed
    return parsed.map((file) => {
      const own = listed.find((f) => f.path === file.path)
      return own
        ? { ...file, status: own.status, additions: own.additions, deletions: own.deletions }
        : file
    })
  }, [patch, listed])
  const root = useRef<HTMLDivElement | null>(null)

  useEffect(() => {
    if (!activePath || !root.current) return
    const target = root.current.querySelector<HTMLElement>(
      `[data-path="${CSS.escape(activePath)}"]`,
    )
    // jsdom draws nothing, so it has no scrollIntoView to call.
    target?.scrollIntoView?.({ block: 'start' })
  }, [activePath, files])

  if (mode === 'split') {
    return (
      <div className="sd-diff sd-diff--stub" ref={root}>
        <p className="sd-diff__stub">Split view is next. Unified shows the change today.</p>
      </div>
    )
  }

  if (files.length === 0) {
    return (
      <div className="sd-diff sd-diff--stub" ref={root}>
        <p className="sd-diff__stub">The change is empty.</p>
      </div>
    )
  }

  return (
    <div className="sd-diff" ref={root} dir="ltr">
      {files.map((file) => (
        <article
          key={file.path}
          className="sd-diff__file"
          data-path={file.path}
          data-active={file.path === activePath ? 'true' : undefined}
          aria-label={file.path}
        >
          <header className="sd-diff__filehead">
            <span className="sd-diff__path">{file.path}</span>
            <span className="sd-diff__counts">
              {file.additions > 0 && <span className="sd-diff__add">+{file.additions}</span>}
              {file.deletions > 0 && <span className="sd-diff__del">−{file.deletions}</span>}
            </span>
            <span className="sd-diff__status">{STATUS_WORDS[file.status]}</span>
          </header>
          {file.hunks.map((hunk) => {
            const key = hunkKey(file.path, hunk.index)
            return (
              <Hunk
                key={key}
                file={file}
                hunk={hunk}
                editable={editable}
                decision={decisions[key]}
                dropping={dropping === key}
                onKeep={onKeep}
                onDrop={onDrop}
              />
            )
          })}
        </article>
      ))}
      {truncated && (
        <p className="sd-diff__stub" role="status">
          The patch was cut at its size limit. Every file is listed; not every line is shown.
        </p>
      )}
    </div>
  )
}
