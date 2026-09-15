import { useRef, useState, type ReactNode } from 'react'
import type { DiffFile } from '../../api/types'
import { lineNumber, type FilePatch } from '../../lib/diff'
import Button from '../button'
import './DiffView.css'

/** The key a hunk is remembered by: the file's path and the hunk's 0-based index in it. */
export function hunkKey(path: string, index: number): string {
  return `${path}#${index}`
}

export interface DiffViewProps {
  /** The patch, already read into files and hunks by `lib/diff`. */
  files: FilePatch[]
  /** The service's own file list, for each row's status word and its counts. */
  meta?: DiffFile[]
  /** Hunks a person has marked Keep, by `hunkKey`. */
  kept?: ReadonlySet<string>
  /** The hunk whose Drop is in flight, by `hunkKey`. */
  dropping?: string
  /** Why a Drop was refused, by `hunkKey`; shown under the hunk it was for. */
  refusals?: Readonly<Record<string, string>>
  /** False once the change can no longer be edited: Drop is shown, disabled, with the reason. */
  editable?: boolean
  /** Why the change is not editable, as Drop's title. */
  readOnlyReason?: string
  onKeep?: (path: string, index: number) => void
  onDrop?: (path: string, index: number) => void
  /** True when the patch was cut at the service's size cap; the file list is whole either way. */
  truncated?: boolean
  /** Names the region for a screen reader. */
  label?: string
  /** Something to draw under the diff, inside the scroll: the checks, a note. */
  children?: ReactNode
}

function CheckIcon(): JSX.Element {
  return (
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true" focusable="false">
      <path d="m5 12 5 5 9-10" />
    </svg>
  )
}

/** The word at the end of a file row: what the file is, or that its hunks are all kept. */
function statusWord(file: FilePatch, meta: DiffFile | undefined, kept: ReadonlySet<string>): string {
  switch (meta?.status) {
    case 'added':
      return 'new'
    case 'deleted':
      return 'deleted'
    case 'renamed':
      return 'renamed'
    default:
  }
  if (file.hunks.length > 0 && file.hunks.every((h) => kept.has(hunkKey(file.path, h.index)))) {
    return 'reviewed'
  }
  return ''
}

/**
 * A fix run's change as a reviewer reads it: the file rows above, the hunks
 * below, and Keep or Drop on every hunk.
 *
 * Keep is a mark the reader makes for themself — it changes nothing on disk,
 * and the file row says "reviewed" once every hunk in it is kept. Drop is the
 * one control here that changes something: it hands the hunk to the service,
 * which reverts it out of the commit, and the diff that comes back replaces
 * this one. Both stay on the hunk they belong to rather than in a toolbar,
 * so what a click does is never a question of which hunk was selected.
 *
 * The rows and the lines are `dir="ltr"`: a path or a line of code read
 * right to left is a different path, and a different line.
 */
export default function DiffView({
  files,
  meta = [],
  kept = new Set<string>(),
  dropping,
  refusals = {},
  editable = true,
  readOnlyReason,
  onKeep,
  onDrop,
  truncated = false,
  label = 'Changes',
  children,
}: DiffViewProps): JSX.Element {
  const [current, setCurrent] = useState(0)
  const body = useRef<HTMLDivElement | null>(null)
  const metaByPath = new Map(meta.map((m) => [m.path, m]))

  function show(i: number): void {
    setCurrent(i)
    const target = body.current?.querySelector<HTMLElement>(`[data-file-index="${i}"]`)
    target?.scrollIntoView({ block: 'start' })
  }

  if (files.length === 0) {
    return (
      <div className="sd-diff" role="region" aria-label={label} dir="ltr">
        <p className="sd-diff__empty">No change to show.</p>
        {children}
      </div>
    )
  }

  return (
    <div className="sd-diff" role="region" aria-label={label} dir="ltr">
      <div className="sd-diff__files" role="list">
        {files.map((file, i) => {
          const m = metaByPath.get(file.path)
          const additions = m?.additions ?? file.additions
          const deletions = m?.deletions ?? file.deletions
          const word = statusWord(file, m, kept)
          return (
            <button
              key={file.path || i}
              type="button"
              role="listitem"
              className="sd-diff__file"
              data-on={i === current ? 'true' : undefined}
              aria-current={i === current ? 'true' : undefined}
              onClick={() => show(i)}
            >
              <span className="sd-diff__path">{file.path || '(unnamed)'}</span>
              <span className="sd-diff__add">+{additions}</span>
              <span className="sd-diff__del">−{deletions}</span>
              {word ? <span className="sd-diff__word">{word}</span> : null}
            </button>
          )
        })}
      </div>

      <div className="sd-diff__body" ref={body}>
        {files.map((file, i) => (
          <section
            key={file.path || i}
            className="sd-diff__filesec"
            data-file-index={i}
            aria-label={file.path || '(unnamed)'}
          >
            {files.length > 1 ? (
              <div className="sd-diff__filehead">
                <span>{file.path || '(unnamed)'}</span>
                <span className="sd-diff__add">+{file.additions}</span>
                <span className="sd-diff__del">−{file.deletions}</span>
              </div>
            ) : null}
            {file.hunks.map((hunk) => {
              const key = hunkKey(file.path, hunk.index)
              const isKept = kept.has(key)
              const refusal = refusals[key]
              return (
                <section key={key} className="sd-diff__hunksec" aria-label={hunk.header}>
                  <div className="sd-diff__hunk">
                    <span className="sd-diff__header">{hunk.header}</span>
                    {onKeep || onDrop ? (
                      <span className="sd-diff__acts">
                        <Button
                          variant="pale"
                          size="sm"
                          aria-pressed={isKept}
                          icon={isKept ? <CheckIcon /> : undefined}
                          onClick={() => onKeep?.(file.path, hunk.index)}
                        >
                          {isKept ? 'Kept' : 'Keep'}
                        </Button>
                        <Button
                          variant="ghost"
                          size="sm"
                          busy={dropping === key}
                          disabled={!editable}
                          title={editable ? undefined : readOnlyReason}
                          onClick={() => onDrop?.(file.path, hunk.index)}
                        >
                          {dropping === key ? 'Dropping…' : 'Drop'}
                        </Button>
                      </span>
                    ) : null}
                  </div>
                  {refusal ? (
                    <p className="sd-diff__refused" role="alert">
                      {refusal}
                    </p>
                  ) : null}
                  {hunk.lines.map((line, n) => (
                    <div key={n} className="sd-diff__line" data-t={line.kind === 'context' ? undefined : line.kind}>
                      <span className="sd-diff__n" aria-hidden="true">
                        {lineNumber(line) ?? ''}
                      </span>
                      <span className="sd-diff__c">
                        {line.kind === 'add' ? '+' : line.kind === 'del' ? '-' : ' '}
                        {line.text}
                      </span>
                    </div>
                  ))}
                </section>
              )
            })}
          </section>
        ))}
        {truncated ? (
          <p className="sd-diff__note">The patch was cut at the size cap; the file list above is whole.</p>
        ) : null}
        {children}
      </div>
    </div>
  )
}
