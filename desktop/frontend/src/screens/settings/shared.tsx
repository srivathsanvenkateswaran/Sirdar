import { type ReactNode } from 'react'
import type { Check, CheckLevel, MCPVerdict, Transport } from '../../api/types'
import { usePathAction } from '../../lib/pathAction'

/**
 * The control on a row whose value lives in `.sirdar/config.yaml`.
 *
 * The app reads the file and never writes it, so the row does not get an
 * editor. On the desktop the bridge opens the file in whatever the machine
 * associates with YAML; in a browser there is no way to open a file on the
 * operator's machine, so the control copies the path instead and says so.
 */
export function OpenConfig({
  transport,
  workspaceId,
  path,
  setting,
}: {
  transport: Transport
  workspaceId?: string
  /** The file, for the copy fallback and the title. */
  path?: string
  /** The setting the row is about, so six buttons are not all called "Open config". */
  setting: string
}): JSX.Element {
  const config = usePathAction({
    path: path ?? '',
    open: transport.openConfig && workspaceId ? () => transport.openConfig!(workspaceId) : undefined,
    openLabel: 'Open config',
    copyLabel: 'Copy config path',
  })

  return (
    <>
      <button
        type="button"
        className="sd-setting-button"
        aria-label={`${config.name}: ${setting}`}
        title={
          config.canOpen
            ? `Opens ${path ?? '.sirdar/config.yaml'}`
            : `Copies ${path ?? 'the config path'}; the app cannot open files from a browser`
        }
        disabled={config.disabled}
        onClick={config.run}
      >
        {config.label}
      </button>
      {config.failure && (
        <span className="form-error" role="alert">
          {config.failure}
        </span>
      )}
    </>
  )
}

/** A list of mono chips: allow-list patterns, filenames, tool names. */
export function Chips({ items, empty }: { items: string[]; empty: ReactNode }): JSX.Element {
  if (items.length === 0) return <>{empty}</>
  return (
    <span className="settings-chips">
      {items.map((item) => (
        <code className="settings-chip" key={item}>
          {item}
        </code>
      ))}
    </span>
  )
}

/**
 * A verdict, as a word in its hue. "allowed" reads in the done hue and
 * "denied" in the failed one, and the word is always there: colour alone
 * never carries a state.
 */
export function VerdictChip({ verdict }: { verdict: MCPVerdict }): JSX.Element {
  return (
    <span className="settings-verdict" data-verdict={verdict}>
      {verdict}
    </span>
  )
}

/** The mark each doctor level prints, matching `sirdar doctor`'s own. */
export const MARKS: Record<CheckLevel, string> = { ok: 'OK', warn: '!!', fail: 'XX' }

/**
 * A row's level. Older payloads carry only `ok`, so a check with no level
 * is read off the bool — and a warning, which has `ok` true, is never
 * mistaken for a failure.
 */
export function levelOf(c: Check): CheckLevel {
  return c.level ?? (c.ok ? 'ok' : 'fail')
}

/** What a doctor run is up to, shared by the General and Providers pages. */
export type DoctorState =
  | { status: 'idle' }
  | { status: 'loading' }
  | { status: 'done'; checks: Check[] }
  | { status: 'error'; message: string }

/** What a fetch of one payload is up to. */
export type Loaded<T> =
  | { status: 'idle' }
  | { status: 'loading' }
  | { status: 'done'; data: T }
  | { status: 'error'; message: string }

/** The last path segment, for a file named in a row. */
export function basename(path: string): string {
  const parts = path.split(/[\\/]/).filter(Boolean)
  return parts[parts.length - 1] ?? path
}

/** "3 tools" or "1 tool". */
export function count(n: number, noun: string): string {
  return `${n} ${n === 1 ? noun : `${noun}s`}`
}
