import { useEffect, useRef, useState, useSyncExternalStore, type FormEvent } from 'react'
import type { ConfigSummary, Transport, Workspace } from '../../api/types'
import { reasonOf } from '../../lib/format'
import {
  sessionsShow,
  setSessionsShow,
  subscribeSessionsShow,
  type SessionsShow,
} from '../../lib/sessionsShow'
import { setTheme, subscribeTheme, theme, type Theme } from '../../lib/theme'
import SegmentedControl from '../../ui/segmented-control'
import SettingRow, { SettingCard } from '../../ui/setting-row'
import { levelOf, MARKS, OpenConfig, type DoctorState, type Loaded } from './shared'

/** How long a Remove button stays armed before it goes back to asking. */
const CONFIRM_MS = 5000

/** Light first: it is the default, and the row reads left to right from it. */
const THEME_OPTIONS: { id: Theme; label: string }[] = [
  { id: 'light', label: 'Light' },
  { id: 'system', label: 'System' },
  { id: 'dark', label: 'Dark' },
]

/** Tracker first: it is the default, and the run's own key is the tracker's. */
const SESSIONS_SHOW_OPTIONS: { id: SessionsShow; label: string }[] = [
  { id: 'tracker', label: 'Tracker number' },
  { id: 'helpdesk', label: 'Helpdesk number' },
]

/** "Notes in en, customer replies in the ticket's language". */
function languageLine(notes: string, customer: string): string {
  const reply = customer === 'auto' ? "the ticket's own language" : customer
  return `Notes in ${notes}, customer replies in ${reply}`
}

/**
 * General: what the workspace is and where, the note directory, the
 * languages, and the theme — the one control on the page the app itself
 * applies. Everything else here is read from config.yaml, and each of those
 * rows offers to open the file.
 *
 * The Workspaces card is the registry: which repositories this copy of the
 * app knows about, with Remove, and the field that registers another. It is
 * the one thing Settings commits, and it was the first page before the
 * modal grew the rest.
 */
export default function GeneralPage({
  transport,
  workspaces,
  currentWorkspaceId,
  summary,
  doctor,
  onRunDoctor,
  onWorkspacesChanged,
}: {
  transport: Transport
  workspaces: Workspace[]
  currentWorkspaceId?: string
  summary: Loaded<ConfigSummary>
  doctor: DoctorState
  onRunDoctor: () => void
  onWorkspacesChanged: () => void
}): JSX.Element {
  const current = useSyncExternalStore(subscribeTheme, theme, () => 'system' as Theme)
  const show = useSyncExternalStore(
    subscribeSessionsShow,
    sessionsShow,
    () => 'tracker' as SessionsShow,
  )
  const [root, setRoot] = useState('')
  const [adding, setAdding] = useState(false)
  const [addError, setAddError] = useState<string | null>(null)
  /** Why the last Remove was refused, under the row it was pressed on. */
  const [removeError, setRemoveError] = useState<{ id: string; reason: string } | null>(null)
  /** The workspace whose Remove button is armed, if any. */
  const [confirming, setConfirming] = useState('')
  const confirmTimer = useRef<ReturnType<typeof setTimeout> | null>(null)

  function disarm(): void {
    if (confirmTimer.current) clearTimeout(confirmTimer.current)
    confirmTimer.current = null
  }
  useEffect(() => disarm, [])

  async function handleAdd(e: FormEvent<HTMLFormElement>): Promise<void> {
    e.preventDefault()
    const path = root.trim()
    if (!path) return
    setAdding(true)
    setAddError(null)
    try {
      await transport.addWorkspace(path)
      setRoot('')
      onWorkspacesChanged()
    } catch (err) {
      setAddError(reasonOf(err))
    } finally {
      setAdding(false)
    }
  }

  /**
   * Removing a workspace takes two presses. A native `confirm()` blocks the
   * webview's whole event loop — the run stream included — so the button
   * arms itself instead and disarms again after CONFIRM_MS.
   */
  async function handleRemove(ws: Workspace): Promise<void> {
    disarm()
    if (confirming !== ws.id) {
      setConfirming(ws.id)
      confirmTimer.current = setTimeout(() => setConfirming(''), CONFIRM_MS)
      return
    }
    setConfirming('')
    setRemoveError(null)
    try {
      await transport.removeWorkspace(ws.id)
    } catch (err) {
      // The row stays, with the reason under it: a registry that refused is
      // not a workspace that went away.
      setRemoveError({ id: ws.id, reason: reasonOf(err) })
      return
    }
    onWorkspacesChanged()
  }

  const general = summary.status === 'done' ? summary.data.general : null
  const configPath = general?.configPath
  const open = (setting: string) => (
    <OpenConfig
      transport={transport}
      workspaceId={currentWorkspaceId}
      path={configPath}
      setting={setting}
    />
  )
  const ws = workspaces.find((w) => w.id === currentWorkspaceId)

  return (
    <>
      <SettingCard heading="Workspace">
        {!currentWorkspaceId ? (
          <p className="empty-state">Choose a workspace from the switcher to read its config.</p>
        ) : summary.status === 'error' ? (
          <p className="form-error">{summary.message}</p>
        ) : (
          <>
            <SettingRow
              label="Workspace name"
              value={general?.workspace ?? ws?.name ?? 'Reading config.yaml…'}
              control={open('Workspace name')}
            />
            <SettingRow
              label="Root"
              value={<code className="settings-mono">{general?.root ?? ws?.root}</code>}
              control={open('Root')}
            />
            <SettingRow
              label="Notes directory"
              value={
                <code className="settings-mono">
                  {summary.status === 'done' ? summary.data.notes.dir : ws?.notesDir}
                </code>
              }
              control={open('Notes directory')}
            />
            <SettingRow
              label="Language"
              value={
                general ? languageLine(general.notesLanguage, general.customerLanguage) : undefined
              }
              help="The engineer's note and anything shown to the customer are rarely in the same language."
              control={open('Language')}
            />
          </>
        )}
      </SettingCard>

      <SettingCard heading="Appearance">
        <SettingRow
          label="Theme"
          value={
            current === 'system'
              ? 'Follows the system'
              : current === 'dark'
                ? 'Dark'
                : 'Light'
          }
          help="Light unless you choose otherwise; System follows the desktop. Remembered in this browser, and it changes nothing in the workspace."
          control={
            <SegmentedControl
              label="Theme"
              options={THEME_OPTIONS}
              value={current}
              onChange={(id) => setTheme(id as Theme)}
            />
          }
        />
        <SettingRow
          label="Sessions show"
          value={show === 'helpdesk' ? 'The helpdesk number' : 'The tracker number'}
          help="Which ticket number the sessions list, the board cards and a session's title carry. A run with no helpdesk number shows its tracker key either way. Remembered in this browser."
          control={
            <SegmentedControl
              label="Sessions show"
              options={SESSIONS_SHOW_OPTIONS}
              value={show}
              onChange={(id) => setSessionsShow(id as SessionsShow)}
            />
          }
        />
      </SettingCard>

      <SettingCard heading="Checks">
        <SettingRow
          label="Doctor"
          value={
            doctor.status === 'done'
              ? `${doctor.checks.length} ${doctor.checks.length === 1 ? 'check' : 'checks'} read`
              : doctor.status === 'error'
                ? doctor.message
                : 'Not run in this session'
          }
          help="Reaches the provider CLI, every configured source and the MCP servers, the way `sirdar doctor` does."
          control={
            <button
              type="button"
              className="sd-setting-button"
              onClick={onRunDoctor}
              disabled={doctor.status === 'loading' || !currentWorkspaceId}
            >
              {doctor.status === 'loading' ? 'Running doctor…' : 'Run doctor'}
            </button>
          }
        />
        {doctor.status === 'done' && (
          <ul className="settings-doctor">
            {doctor.checks.map((c) => (
              <li key={c.name} className="settings-doctor__row" data-level={levelOf(c)}>
                <span className="settings-doctor__mark">{MARKS[levelOf(c)]}</span>
                <span className="settings-doctor__name">{c.name}</span>
                <span className="settings-doctor__detail">{c.detail}</span>
              </li>
            ))}
          </ul>
        )}
      </SettingCard>

      <SettingCard heading="Workspaces">
        {workspaces.length === 0 ? (
          <p className="empty-state">No workspaces registered yet. Add one below.</p>
        ) : (
          workspaces.map((w) => (
            <SettingRow
              key={w.id}
              label={w.name}
              value={
                <>
                  <code className="settings-mono">{w.root}</code>
                  {removeError?.id === w.id && (
                    <span className="form-error" role="alert">
                      Could not remove: {removeError.reason}
                    </span>
                  )}
                </>
              }
              control={
                <button
                  type="button"
                  className="sd-setting-button"
                  onClick={() => void handleRemove(w)}
                  aria-label={confirming === w.id ? `Confirm remove ${w.name}` : `Remove ${w.name}`}
                  title={
                    confirming === w.id
                      ? `Sirdar stops watching ${w.root}. Nothing on disk is deleted.`
                      : `Remove ${w.name} from the workspace list`
                  }
                >
                  {confirming === w.id ? 'Confirm remove' : 'Remove'}
                </button>
              }
            />
          ))
        )}
        <form className="settings-add" onSubmit={(e) => void handleAdd(e)}>
          <input
            type="text"
            className="sd-setting-input settings-add__input"
            placeholder="/path/to/repo"
            value={root}
            onChange={(e) => setRoot(e.target.value)}
            aria-label="Workspace path"
          />
          {/*
            Pale, not filled: the modal's footer holds the page's primary, and
            registering a repository applies as it is pressed.
          */}
          <button
            type="submit"
            className="sd-setting-button"
            disabled={adding || !root.trim()}
          >
            {adding ? 'Adding…' : 'Add workspace'}
          </button>
        </form>
        {addError && <p className="form-error">{addError}</p>}
      </SettingCard>
    </>
  )
}
