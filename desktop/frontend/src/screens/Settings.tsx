import { useEffect, useRef, useState, useSyncExternalStore, type FormEvent } from 'react'
import type { Check, CheckLevel, ConfigSummary, Transport, Workspace } from '../api/types'
import { showLibrary, setShowLibrary, subscribeShowLibrary } from '../lib/library'
import { prefersRTL, setPreferRTL, subscribePreferRTL } from '../lib/rtl'
import ConfigSummaryPanel from '../components/shell/ConfigSummaryPanel'
import Badge from '../ui/badge'
import Button from '../ui/button'
import ModalSheet, { type ModalNavGroup } from '../ui/modal-sheet'
import SettingRow, { SettingCard } from '../ui/setting-row'
import '../components/panels.css'

type DoctorState =
  | { status: 'loading' }
  | { status: 'done'; checks: Check[] }
  | { status: 'error'; message: string }

/** The mark each doctor level prints, matching `sirdar doctor`'s own. */
const MARKS: Record<CheckLevel, string> = { ok: 'OK', warn: '!!', fail: 'XX' }

/**
 * A row's level. Older payloads carry only `ok`, so a check with no level
 * is read off the bool — and a warning, which has `ok` true, is never
 * mistaken for a failure.
 */
function levelOf(c: Check): CheckLevel {
  return c.level ?? (c.ok ? 'ok' : 'fail')
}

/**
 * The configuration reference, on GitHub. A relative `docs/config.md` resolves
 * against the asset server the bundle is loaded from, which serves the app's
 * own index.html for it — so the link led back to Sirdar rather than to the
 * documentation.
 */
export const CONFIG_DOCS_URL =
  'https://github.com/srivathsanvenkateswaran/Sirdar/blob/main/docs/config.md'

/** How long a Remove button stays armed before it goes back to asking. */
const CONFIRM_MS = 5000

/**
 * The pages, in two groups.
 *
 * `03-desktop-app.md` section 6 sketches the reference's own groups (General,
 * Providers, Budgets, Sources, Eval; Identity, Keys, Data and privacy). Sirdar
 * has none of those pages: its providers, budgets and sources live in the
 * workspace's `.sirdar/config.yaml`, which this app reads and never writes. So
 * the groups are what Sirdar actually has — what belongs to the workspace, and
 * what belongs to this copy of the app — rather than five empty pages named
 * after somebody else's product.
 */
export const SETTINGS_GROUPS: ModalNavGroup[] = [
  {
    label: 'Workspace',
    items: [
      { id: 'workspaces', label: 'Workspaces' },
      { id: 'notifications', label: 'Notifications' },
    ],
  },
  {
    label: 'This app',
    items: [
      { id: 'reading', label: 'Reading' },
      { id: 'library', label: 'Design library' },
      { id: 'about', label: 'About' },
    ],
  },
]

const TITLES: Record<string, string> = {
  workspaces: 'Workspaces',
  notifications: 'Notifications',
  reading: 'Reading',
  library: 'Design library',
  about: 'About',
}

export default function Settings(props: {
  open: boolean
  /** The page the address names, when it names one. */
  page?: string
  /** Called with the page the reader chose, so the address can follow. */
  onSelectPage?: (page: string) => void
  transport: Transport
  workspaces: Workspace[]
  /** The workspace whose notify and webhooks blocks are summarised. */
  currentWorkspaceId?: string
  onClose: () => void
  onWorkspacesChanged: () => void
}): JSX.Element | null {
  const {
    open,
    transport,
    workspaces,
    currentWorkspaceId,
    onClose,
    onWorkspacesChanged,
    onSelectPage,
  } = props
  // The page comes from the address when a link named one (`#/settings/<page>`)
  // and from here otherwise. A page the modal does not have falls back to the
  // first, so a stale link opens the modal rather than an empty panel.
  const [localPage, setLocalPage] = useState('workspaces')
  const wanted = props.page && props.page in TITLES ? props.page : localPage
  const page = wanted in TITLES ? wanted : 'workspaces'
  function setPage(id: string): void {
    setLocalPage(id)
    onSelectPage?.(id)
  }
  const [root, setRoot] = useState('')
  const [adding, setAdding] = useState(false)
  const [addError, setAddError] = useState<string | null>(null)
  const [doctor, setDoctor] = useState<Record<string, DoctorState>>({})
  /** Desktop build version, when the transport exposes one (Wails only). */
  const [version, setVersion] = useState<string | null>(null)
  /** The workspace whose Remove button is armed, if any. */
  const [confirming, setConfirming] = useState('')
  const confirmTimer = useRef<ReturnType<typeof setTimeout> | null>(null)
  const rtl = useSyncExternalStore(subscribePreferRTL, prefersRTL, () => false)
  const library = useSyncExternalStore(subscribeShowLibrary, showLibrary, () => false)
  const [summary, setSummary] = useState<ConfigSummary | null>(null)
  const [summaryError, setSummaryError] = useState('')

  useEffect(() => {
    let cancelled = false
    transport
      .version?.()
      .then((v) => {
        if (!cancelled) setVersion(v)
      })
      .catch(() => {
        // The HTTP transport has no Version to fail; the Wails one rarely
        // does either. Either way the About page just omits the version.
      })
    return () => {
      cancelled = true
    }
  }, [transport])

  useEffect(() => {
    if (!currentWorkspaceId) {
      setSummary(null)
      return
    }
    let cancelled = false
    setSummary(null)
    setSummaryError('')
    transport
      .configSummary(currentWorkspaceId)
      .then((got) => {
        if (!cancelled) setSummary(got)
      })
      .catch((err: unknown) => {
        if (!cancelled) setSummaryError(err instanceof Error ? err.message : String(err))
      })
    return () => {
      cancelled = true
    }
  }, [transport, currentWorkspaceId])

  function disarm(): void {
    if (confirmTimer.current) clearTimeout(confirmTimer.current)
    confirmTimer.current = null
  }

  useEffect(() => disarm, [])

  async function handleAdd(e: FormEvent<HTMLFormElement>) {
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
      setAddError(err instanceof Error ? err.message : String(err))
    } finally {
      setAdding(false)
    }
  }

  /**
   * Removing a workspace takes two presses. A native `confirm()` blocks the
   * webview's whole event loop — the run stream included — so the button arms
   * itself instead and disarms again after CONFIRM_MS.
   */
  async function handleRemove(ws: Workspace) {
    disarm()
    if (confirming !== ws.id) {
      setConfirming(ws.id)
      confirmTimer.current = setTimeout(() => setConfirming(''), CONFIRM_MS)
      return
    }
    setConfirming('')
    await transport.removeWorkspace(ws.id)
    onWorkspacesChanged()
  }

  async function handleDoctor(ws: Workspace) {
    setDoctor((d) => ({ ...d, [ws.id]: { status: 'loading' } }))
    try {
      const checks = await transport.doctor(ws.id)
      setDoctor((d) => ({ ...d, [ws.id]: { status: 'done', checks } }))
    } catch (err) {
      setDoctor((d) => ({
        ...d,
        [ws.id]: { status: 'error', message: err instanceof Error ? err.message : String(err) },
      }))
    }
  }

  const workspacesPage = (
    <>
      {workspaces.length === 0 ? (
        <p className="empty-state">No workspaces registered yet. Add one below.</p>
      ) : (
        workspaces.map((ws) => {
          const d = doctor[ws.id]
          return (
            <SettingCard key={ws.id} heading={ws.name}>
              <SettingRow
                label="Repository"
                value={<span className="mono">{ws.root}</span>}
                control={
                  <button
                    type="button"
                    className="sd-setting-button"
                    onClick={() => handleRemove(ws)}
                    title={
                      confirming === ws.id
                        ? `Sirdar stops watching ${ws.root}. Nothing on disk is deleted.`
                        : `Remove ${ws.name} from the workspace list`
                    }
                  >
                    {confirming === ws.id ? 'Confirm remove' : 'Remove'}
                  </button>
                }
              />
              <SettingRow label="Provider" value={`${ws.provider} / ${ws.model}`} />
              <SettingRow label="Notes" value={ws.notesDir} />
              <SettingRow label="Billing" value={<Badge title="Billing mode">{ws.billing}</Badge>} />
              <SettingRow
                label="Checks"
                value={
                  d?.status === 'done'
                    ? `${d.checks.length} ${d.checks.length === 1 ? 'check' : 'checks'} read`
                    : 'Not run in this session'
                }
                control={
                  <button
                    type="button"
                    className="sd-setting-button"
                    onClick={() => handleDoctor(ws)}
                    disabled={d?.status === 'loading'}
                  >
                    {d?.status === 'loading' ? 'Running doctor…' : 'Run doctor'}
                  </button>
                }
              />
              {d?.status === 'error' && <p className="form-error">{d.message}</p>}
              {d?.status === 'done' && (
                <ul className="doctor-list">
                  {d.checks.map((c) => (
                    <li key={c.name} className={`doctor-check doctor-check--${levelOf(c)}`}>
                      <span className="doctor-check__mark mono">{MARKS[levelOf(c)]}</span>
                      <span className="doctor-check__name">{c.name}</span>
                      <span className="doctor-check__detail">{c.detail}</span>
                    </li>
                  ))}
                </ul>
              )}
            </SettingCard>
          )
        })
      )}

      <SettingCard heading="Add workspace">
        <form className="add-workspace-form" onSubmit={handleAdd}>
          <input
            type="text"
            className="sd-setting-input"
            placeholder="/path/to/repo"
            value={root}
            onChange={(e) => setRoot(e.target.value)}
            aria-label="Workspace path"
          />
          {/*
            The one filled button on this page. Registering a repository is the
            only thing Settings commits; everything else here applies as it is
            switched.
          */}
          <Button type="submit" variant="primary" disabled={adding || !root.trim()}>
            {adding ? 'Adding…' : 'Add workspace'}
          </Button>
        </form>
        {addError && <p className="form-error">{addError}</p>}
      </SettingCard>
    </>
  )

  const notificationsPage = currentWorkspaceId ? (
    <ConfigSummaryPanel summary={summary} error={summaryError} />
  ) : (
    <p className="empty-state">
      Choose a workspace from the switcher to see its notify and webhook blocks.
    </p>
  )

  const readingPage = (
    <SettingCard heading="Note pane">
      <SettingRow
        label="Reading direction"
        value={rtl ? 'Right to left' : 'Each block from its own first letter'}
        help="Notes mix an English body with the customer's own Arabic, and each block is laid out from its own first letter either way. This lays the whole note pane out right to left. It is remembered in this browser and changes nothing in the workspace or in the note on disk; the run's event log stays left to right, where paths and tool names are readable."
        control={
          <label className="settings-toggle">
            <input type="checkbox" checked={rtl} onChange={(e) => setPreferRTL(e.target.checked)} />
            <span>Prefer right-to-left layout for Arabic content</span>
          </label>
        }
      />
    </SettingCard>
  )

  const libraryPage = (
    <SettingCard heading="Design library">
      <SettingRow
        label="Show the design library"
        value={library ? 'On' : 'Off'}
        help={
          <>
            Adds a Library row and the <code>#/library</code> address, where every interface
            component is shown in each of its states, in both themes and in both reading
            directions. It is for whoever is building the interface; it changes nothing about a
            run. On by default in a development build.
          </>
        }
        control={
          <label className="settings-toggle">
            <input
              type="checkbox"
              checked={library}
              onChange={(e) => setShowLibrary(e.target.checked)}
            />
            <span>Show the design library</span>
          </label>
        }
      />
    </SettingCard>
  )

  const aboutPage = (
    <SettingCard heading="About">
      <SettingRow label="Sirdar desktop" value={version ? `v${version}` : 'version unknown'} />
      <SettingRow
        label="Configuration reference"
        value={
          <a href={CONFIG_DOCS_URL} target="_blank" rel="noreferrer noopener">
            docs/config.md
          </a>
        }
      />
      <SettingRow
        label="Credentials"
        value="Never stored by Sirdar"
        help="Sirdar runs your own installed agent CLI with your login."
      />
    </SettingCard>
  )

  const pages: Record<string, JSX.Element> = {
    workspaces: workspacesPage,
    notifications: notificationsPage,
    reading: readingPage,
    library: libraryPage,
    about: aboutPage,
  }

  return (
    <ModalSheet
      open={open}
      title={TITLES[page] ?? 'Settings'}
      groups={SETTINGS_GROUPS}
      current={page}
      onSelect={setPage}
      onClose={onClose}
      navFooter={<>Sirdar desktop{version ? ` v${version}` : ''}</>}
      footer={<Button onClick={onClose}>Close</Button>}
    >
      <div className="settings">{pages[page]}</div>
    </ModalSheet>
  )
}
