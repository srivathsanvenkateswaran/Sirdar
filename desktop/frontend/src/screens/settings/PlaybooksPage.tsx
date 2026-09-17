import {
  useCallback,
  useEffect,
  useId,
  useRef,
  useState,
  type KeyboardEvent as ReactKeyboardEvent,
  type MouseEvent as ReactMouseEvent,
} from 'react'
import type { PlaybookSummary, Transport } from '../../api/types'
import { pointAnchor, type Anchorable } from '../../lib/anchor'
import { reasonOf } from '../../lib/format'
import Button from '../../ui/button'
import ContextMenu, { type MenuEntry } from '../../ui/context-menu'
import Dialog from '../../ui/dialog'
import { SettingCard } from '../../ui/setting-row'
import type { Loaded } from './shared'

/**
 * A new playbook starts from two lines: the heading the list reads as its
 * title, and the sentence it reads as its lede. Anything more would be
 * words somebody has to delete before writing their own.
 */
export function template(name: string): string {
  return `# ${headingFor(name)}\n\nWhat this evidence source gets wrong, and what a negative result here does not prove.\n`
}

/** "20-logs.md" → "Logs": the slug, hyphens opened out, first letter up. */
export function headingFor(name: string): string {
  const slug = name.replace(/\.md$/, '').replace(/^[0-9]{2}-/, '').replace(/-/g, ' ').trim()
  if (!slug) return 'Playbook'
  return slug.charAt(0).toUpperCase() + slug.slice(1)
}

/** The shape a new playbook's filename must have, as internal/app spells it. */
const NAME = /^[0-9]{2}-[a-z0-9-]+\.md$/

/** What is wrong with a typed name, or '' when nothing is. */
export function nameProblem(raw: string, taken: string[]): string {
  const name = raw.trim()
  if (!name) return 'Give it a name'
  if (!NAME.test(name)) {
    return 'Two digits, a hyphen, a lowercase slug and .md — 20-logs.md'
  }
  if (taken.includes(name)) return 'A playbook is already filed under that name'
  return ''
}

/** What the page is doing: the list, or one playbook open in the editor. */
type Mode = { kind: 'list' } | { kind: 'edit'; name: string; created?: boolean }

/** The editor's draft, and where it came from. */
interface Draft {
  name: string
  /** The body as it was read, so the page knows whether anything changed. */
  original: string
  text: string
}

export interface Playbooks {
  list: Loaded<PlaybookSummary[]>
  mode: Mode
  draft: Draft | null
  /** The editor has changes nobody has saved. */
  dirty: boolean
  saving: boolean
  busy: boolean
  /** What the last call refused, under the thing that asked. */
  failure: string
  /** Opens one playbook in the editor. */
  edit: (name: string) => void
  /** Leaves the editor. A dirty draft asks first. */
  cancel: () => void
  save: () => void
  setText: (text: string) => void
  create: (name: string) => Promise<void>
  remove: (name: string) => Promise<void>
  open: (name: string) => Promise<void>
  scaffold: () => void
  /** The label and enabled state of the window's one filled button. */
  primary: { label: string; disabled: boolean; busy: boolean; run: () => void }
  /** Cancel found changes: the confirm is up. */
  discarding: boolean
  confirmDiscard: () => void
  keepEditing: () => void
  /** The New playbook dialog is up. */
  asking: boolean
  ask: () => void
  stopAsking: () => void
  reload: () => void
}

/**
 * The Playbooks page's state, lifted out of the page the way the tool
 * tester's is: Settings publishes Save (or New playbook) as the window's one
 * filled button, and an editor left open survives the modal closing, since
 * Settings stays mounted and the page does not. That is the unsaved-changes
 * guard doing its real work — closing the window does not lose the draft —
 * and Cancel asks before throwing one away.
 */
export function usePlaybooks(transport: Transport, workspaceId: string | undefined): Playbooks {
  const [list, setList] = useState<Loaded<PlaybookSummary[]>>({ status: 'idle' })
  const [mode, setMode] = useState<Mode>({ kind: 'list' })
  const [draft, setDraft] = useState<Draft | null>(null)
  const [saving, setSaving] = useState(false)
  const [busy, setBusy] = useState(false)
  const [failure, setFailure] = useState('')
  const [discarding, setDiscarding] = useState(false)
  const [asking, setAsking] = useState(false)
  const [reloads, setReloads] = useState(0)
  /** The workspace the state belongs to, so a switch starts over. */
  const readFor = useRef('')

  const ws = workspaceId ?? ''
  useEffect(() => {
    if (readFor.current === ws) return
    readFor.current = ws
    setMode({ kind: 'list' })
    setDraft(null)
    setFailure('')
    setDiscarding(false)
    setAsking(false)
    setList({ status: 'idle' })
  }, [ws])

  useEffect(() => {
    if (!ws) {
      setList({ status: 'idle' })
      return
    }
    let cancelled = false
    setList({ status: 'loading' })
    transport
      .playbooks(ws)
      .then((data) => {
        if (!cancelled) setList({ status: 'done', data })
      })
      .catch((err: unknown) => {
        if (!cancelled) setList({ status: 'error', message: reasonOf(err) })
      })
    return () => {
      cancelled = true
    }
  }, [transport, ws, reloads])

  const reload = useCallback(() => setReloads((n) => n + 1), [])

  const dirty = Boolean(draft && draft.text !== draft.original)

  const edit = useCallback(
    (name: string) => {
      if (!ws) return
      setFailure('')
      setMode({ kind: 'edit', name })
      setDraft(null)
      transport
        .playbook(ws, name)
        .then((text) => setDraft({ name, original: text, text }))
        .catch((err: unknown) => {
          setFailure(reasonOf(err))
          setMode({ kind: 'list' })
        })
    },
    [transport, ws],
  )

  const leave = useCallback(() => {
    setMode({ kind: 'list' })
    setDraft(null)
    setDiscarding(false)
    setFailure('')
  }, [])

  const cancel = useCallback(() => {
    if (dirty) {
      setDiscarding(true)
      return
    }
    leave()
  }, [dirty, leave])

  const save = useCallback(() => {
    if (!ws || !draft || saving) return
    setSaving(true)
    setFailure('')
    transport
      .savePlaybook(ws, draft.name, draft.text)
      .then((row) => {
        setDraft((d) => (d && d.name === row.name ? { ...d, original: d.text } : d))
        setList((prev) =>
          prev.status === 'done'
            ? {
                status: 'done',
                data: prev.data.some((p) => p.name === row.name)
                  ? prev.data.map((p) => (p.name === row.name ? row : p))
                  : [...prev.data, row].sort((a, b) => a.name.localeCompare(b.name)),
              }
            : prev,
        )
        setMode({ kind: 'list' })
        setDraft(null)
      })
      .catch((err: unknown) => setFailure(reasonOf(err)))
      .finally(() => setSaving(false))
  }, [transport, ws, draft, saving])

  const create = useCallback(
    async (name: string) => {
      if (!ws) return
      setBusy(true)
      setFailure('')
      try {
        const row = await transport.addPlaybook(ws, name, template(name))
        setList((prev) =>
          prev.status === 'done'
            ? { status: 'done', data: [...prev.data, row].sort((a, b) => a.name.localeCompare(b.name)) }
            : prev,
        )
        setAsking(false)
        edit(name)
      } catch (err) {
        setFailure(reasonOf(err))
      } finally {
        setBusy(false)
      }
    },
    [transport, ws, edit],
  )

  const remove = useCallback(
    async (name: string) => {
      if (!ws) return
      setBusy(true)
      setFailure('')
      try {
        await transport.deletePlaybook(ws, name)
        setList((prev) =>
          prev.status === 'done'
            ? { status: 'done', data: prev.data.filter((p) => p.name !== name) }
            : prev,
        )
        setMode((m) => (m.kind === 'edit' && m.name === name ? { kind: 'list' } : m))
        setDraft((d) => (d && d.name === name ? null : d))
      } catch (err) {
        setFailure(reasonOf(err))
      } finally {
        setBusy(false)
      }
    },
    [transport, ws],
  )

  const open = useCallback(
    async (name: string) => {
      if (!ws) return
      setFailure('')
      try {
        await transport.openPlaybook(ws, name)
      } catch (err) {
        setFailure(reasonOf(err))
      }
    },
    [transport, ws],
  )

  const scaffold = useCallback(() => {
    if (!ws || busy) return
    setBusy(true)
    setFailure('')
    transport
      .scaffoldPlaybooks(ws)
      .then((data) => setList({ status: 'done', data }))
      .catch((err: unknown) => setFailure(reasonOf(err)))
      .finally(() => setBusy(false))
  }, [transport, ws, busy])

  const empty = list.status === 'done' && list.data.length === 0
  const editing = mode.kind === 'edit'
  const primary = editing
    ? { label: 'Save', disabled: !dirty || saving || !draft, busy: saving, run: save }
    : empty
      ? { label: 'Create the starting set', disabled: !ws || busy, busy, run: scaffold }
      : { label: 'New playbook', disabled: !ws || busy, busy: false, run: () => setAsking(true) }

  return {
    list,
    mode,
    draft,
    dirty,
    saving,
    busy,
    failure,
    edit,
    cancel,
    save,
    setText: (text) => setDraft((d) => (d ? { ...d, text } : d)),
    create,
    remove,
    open,
    scaffold,
    primary,
    discarding,
    confirmDiscard: leave,
    keepEditing: () => setDiscarding(false),
    asking,
    ask: () => setAsking(true),
    stopAsking: () => setAsking(false),
    reload,
  }
}

/** lucide `ellipsis`, the dots at a row's end. */
function DotsIcon(): JSX.Element {
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
      <circle cx="12" cy="12" r="1" />
      <circle cx="19" cy="12" r="1" />
      <circle cx="5" cy="12" r="1" />
    </svg>
  )
}

/** One playbook: the order it is read in, its title, and what it is about. */
function PlaybookRow({
  playbook,
  onEdit,
  onOpen,
  onMenu,
  menuOpen,
}: {
  playbook: PlaybookSummary
  onEdit: () => void
  onOpen: () => void
  onMenu: (anchor: Anchorable) => void
  menuOpen: boolean
}): JSX.Element {
  function contextMenu(event: ReactMouseEvent): void {
    event.preventDefault()
    onMenu(pointAnchor(event.clientX, event.clientY))
  }
  function onKeyDown(event: ReactKeyboardEvent<HTMLDivElement>): void {
    if (event.key === 'ContextMenu' || (event.key === 'F10' && event.shiftKey)) {
      event.preventDefault()
      onMenu(event.currentTarget)
    }
  }

  return (
    <div
      className="playbook-row"
      data-menu={menuOpen ? 'true' : undefined}
      onContextMenu={contextMenu}
      onKeyDown={onKeyDown}
    >
      <span className="playbook-row__order" dir="ltr" aria-hidden={!playbook.order}>
        {playbook.order || '··'}
      </span>
      <span className="playbook-row__text">
        <span className="playbook-row__title" dir="auto">
          {playbook.title}
        </span>
        {playbook.lede && (
          <span className="playbook-row__lede" dir="auto">
            {playbook.lede}
          </span>
        )}
      </span>
      <span className="playbook-row__actions">
        <Button size="sm" variant="ghost" onClick={onOpen} aria-label={`Open ${playbook.title} in editor`}>
          Open in editor
        </Button>
        <Button size="sm" variant="pale" onClick={onEdit} aria-label={`Edit ${playbook.title}`}>
          Edit
        </Button>
        <button
          type="button"
          className="playbook-row__dots"
          aria-label={`More for ${playbook.title}`}
          aria-haspopup="menu"
          aria-expanded={menuOpen}
          onClick={(event) => onMenu(event.currentTarget)}
        >
          <DotsIcon />
        </button>
      </span>
    </div>
  )
}

/**
 * Settings › Playbooks: the markdown files under `.sirdar/playbooks/` that
 * every run's prompt is built from.
 *
 * A row is the three things worth seeing at once — the order the agent reads
 * it in, what it is called, and the sentence it opens with. The file's path,
 * its size and when it changed are not on the row: the title is the link,
 * and the rest is in the editor's own header.
 */
export default function PlaybooksPage({
  playbooks,
  currentWorkspaceId,
}: {
  playbooks: Playbooks
  currentWorkspaceId?: string
}): JSX.Element {
  const { list, mode, draft, dirty, failure } = playbooks
  const [menuFor, setMenuFor] = useState<PlaybookSummary | null>(null)
  const menuAnchor = useRef<Anchorable | null>(null)
  const [deleting, setDeleting] = useState<PlaybookSummary | null>(null)
  const [newName, setNewName] = useState('')
  const textareaId = useId()
  const nameId = useId()

  const rows = list.status === 'done' ? list.data : []
  const taken = rows.map((p) => p.name)
  const problem = playbooks.asking ? nameProblem(newName, taken) : ''

  useEffect(() => {
    if (playbooks.asking) setNewName('')
  }, [playbooks.asking])

  if (!currentWorkspaceId) {
    return <p className="empty-state">Choose a workspace from the switcher to read its playbooks.</p>
  }

  const note = (
    <p className="settings-note">
      A playbook is read into the prompt of every run this workspace starts, in the order its
      filename gives. A change here applies to the next run, not to one already going.
    </p>
  )

  if (mode.kind === 'edit') {
    const row = rows.find((p) => p.name === mode.name)
    return (
      <>
        <SettingCard heading={row?.title ?? mode.name}>
          {draft === null ? (
            <p className="empty-state">Reading {mode.name}…</p>
          ) : (
            <>
              <label className="playbook-editor__label" htmlFor={textareaId}>
                {mode.name}
              </label>
              <textarea
                id={textareaId}
                className="playbook-editor"
                value={draft.text}
                spellCheck={false}
                dir="auto"
                onChange={(e) => playbooks.setText(e.target.value)}
              />
            </>
          )}
          {note}
          {failure && (
            <p className="form-error" role="alert">
              {failure}
            </p>
          )}
          <div className="playbook-editor__actions">
            <Button variant="ghost" onClick={playbooks.cancel}>
              Cancel
            </Button>
            <Button
              variant="primary"
              busy={playbooks.saving}
              disabled={!dirty || draft === null}
              onClick={playbooks.save}
            >
              Save
            </Button>
          </div>
        </SettingCard>
        <Dialog
          open={playbooks.discarding}
          title={`Discard the changes to ${mode.name}?`}
          onClose={playbooks.keepEditing}
          actions={
            <>
              <Button onClick={playbooks.keepEditing}>Keep editing</Button>
              <Button variant="primary" onClick={playbooks.confirmDiscard}>
                Discard
              </Button>
            </>
          }
        >
          <p>What you typed has not been written to the file. Discarding leaves it as it was.</p>
        </Dialog>
      </>
    )
  }

  return (
    <>
      <SettingCard heading="Files in .sirdar/playbooks">
        {list.status === 'error' ? (
          <p className="form-error" role="alert">
            {list.message}
          </p>
        ) : list.status !== 'done' ? (
          <p className="empty-state">Reading the playbooks…</p>
        ) : rows.length === 0 ? (
          <div className="playbook-empty">
            <p>
              A playbook is a markdown file this workspace keeps its hard-won gotchas in — which
              query language a log system really speaks, what a negative result there does not
              prove.
            </p>
            <p>
              Every run reads all of them, in filename order. Sirdar can write a starting set for
              you to edit.
            </p>
            <Button variant="primary" busy={playbooks.busy} onClick={playbooks.scaffold}>
              Create the starting set
            </Button>
          </div>
        ) : (
          <div className="playbook-rows">
            {rows.map((playbook) => (
              <PlaybookRow
                key={playbook.name}
                playbook={playbook}
                menuOpen={menuFor?.name === playbook.name}
                onEdit={() => playbooks.edit(playbook.name)}
                onOpen={() => void playbooks.open(playbook.name)}
                onMenu={(anchor) => {
                  menuAnchor.current = anchor
                  setMenuFor(playbook)
                }}
              />
            ))}
          </div>
        )}
        {rows.length > 0 && note}
        {failure && (
          <p className="form-error" role="alert">
            {failure}
          </p>
        )}
        {rows.length > 0 && (
          <div className="playbook-editor__actions">
            <Button variant="primary" onClick={playbooks.ask} disabled={playbooks.busy}>
              New playbook
            </Button>
          </div>
        )}
      </SettingCard>

      {menuFor && (
        <ContextMenu
          open
          anchor={menuAnchor}
          label={`Playbook menu for ${menuFor.title}`}
          onClose={() => setMenuFor(null)}
          items={
            [
              {
                id: 'edit',
                label: 'Edit',
                onSelect: () => playbooks.edit(menuFor.name),
              },
              {
                id: 'open',
                label: 'Open in editor',
                onSelect: () => void playbooks.open(menuFor.name),
              },
              { kind: 'separator', id: 'sep' },
              {
                id: 'delete',
                label: 'Delete',
                tone: 'danger',
                onSelect: () => setDeleting(menuFor),
              },
            ] satisfies MenuEntry[]
          }
        />
      )}

      <Dialog
        open={deleting !== null}
        title={deleting ? `Delete ${deleting.name}?` : ''}
        onClose={() => setDeleting(null)}
        actions={
          <>
            <Button onClick={() => setDeleting(null)}>Cancel</Button>
            <Button
              variant="primary"
              busy={playbooks.busy}
              onClick={() => {
                const target = deleting
                setDeleting(null)
                if (target) void playbooks.remove(target.name)
              }}
            >
              Delete
            </Button>
          </>
        }
      >
        <p>
          Runs from now on will not read it. The file is moved into the playbooks directory&rsquo;s
          .trash rather than removed, so you can put it back by hand.
        </p>
      </Dialog>

      <Dialog
        open={playbooks.asking}
        title="New playbook"
        onClose={playbooks.stopAsking}
        actions={
          <>
            <Button onClick={playbooks.stopAsking}>Cancel</Button>
            <Button
              variant="primary"
              busy={playbooks.busy}
              disabled={Boolean(problem)}
              onClick={() => void playbooks.create(newName.trim())}
            >
              Create
            </Button>
          </>
        }
      >
        <label className="playbook-name__label" htmlFor={nameId}>
          Filename
        </label>
        <input
          id={nameId}
          className="playbook-name"
          value={newName}
          dir="ltr"
          placeholder="40-database.md"
          aria-describedby={`${nameId}-help`}
          onChange={(e) => setNewName(e.target.value)}
        />
        <p className="settings-note" id={`${nameId}-help`}>
          {problem && newName.trim()
            ? problem
            : 'The two digits order it against the others: 10 is read before 20.'}
        </p>
      </Dialog>
    </>
  )
}
