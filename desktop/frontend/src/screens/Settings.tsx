import { useState, type FormEvent } from 'react'
import type { Check, Transport, Workspace } from '../api/types'
import '../components/panels.css'

type DoctorState =
  | { status: 'loading' }
  | { status: 'done'; checks: Check[] }
  | { status: 'error'; message: string }

export default function Settings(props: {
  transport: Transport
  workspaces: Workspace[]
  onWorkspacesChanged: () => void
}): JSX.Element {
  const { transport, workspaces, onWorkspacesChanged } = props
  const [root, setRoot] = useState('')
  const [adding, setAdding] = useState(false)
  const [addError, setAddError] = useState<string | null>(null)
  const [doctor, setDoctor] = useState<Record<string, DoctorState>>({})

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

  async function handleRemove(ws: Workspace) {
    if (!window.confirm(`Remove workspace "${ws.name}"? Sirdar stops watching ${ws.root}.`)) {
      return
    }
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

  return (
    <div className="panel settings">
      <section className="settings-workspaces">
        <h2 className="panel-heading">Workspaces</h2>
        {workspaces.length === 0 ? (
          <p className="empty-state">No workspaces registered yet. Add one below.</p>
        ) : (
          <ul className="workspace-list">
            {workspaces.map((ws) => {
              const d = doctor[ws.id]
              return (
                <li key={ws.id} className="workspace-row">
                  <div className="workspace-row__main">
                    <span className="workspace-row__name">{ws.name}</span>
                    <span className="workspace-row__root mono">{ws.root}</span>
                    <span className="workspace-row__meta">
                      {ws.provider} / {ws.model}
                    </span>
                    <span className="workspace-row__meta">notes: {ws.notesDir}</span>
                  </div>
                  <div className="workspace-row__actions">
                    <button
                      type="button"
                      onClick={() => handleDoctor(ws)}
                      disabled={d?.status === 'loading'}
                    >
                      {d?.status === 'loading' ? 'Running doctor…' : 'Run doctor'}
                    </button>
                    <button type="button" className="btn-danger" onClick={() => handleRemove(ws)}>
                      Remove
                    </button>
                  </div>
                  {d?.status === 'error' && <p className="form-error">{d.message}</p>}
                  {d?.status === 'done' && (
                    <ul className="doctor-list">
                      {d.checks.map((c) => (
                        <li
                          key={c.name}
                          className={c.ok ? 'doctor-check doctor-check--ok' : 'doctor-check doctor-check--fail'}
                        >
                          <span className="doctor-check__mark mono">{c.ok ? 'OK' : '!!'}</span>
                          <span className="doctor-check__name">{c.name}</span>
                          <span className="doctor-check__detail">{c.detail}</span>
                        </li>
                      ))}
                    </ul>
                  )}
                </li>
              )
            })}
          </ul>
        )}
      </section>

      <section className="settings-add">
        <h2 className="panel-heading">Add workspace</h2>
        <form className="add-workspace-form" onSubmit={handleAdd}>
          <input
            type="text"
            placeholder="/path/to/repo"
            value={root}
            onChange={(e) => setRoot(e.target.value)}
            aria-label="Workspace path"
          />
          <button type="submit" disabled={adding || !root.trim()}>
            {adding ? 'Adding…' : 'Add workspace'}
          </button>
        </form>
        {addError && <p className="form-error">{addError}</p>}
      </section>

      <section className="settings-about">
        <h2 className="panel-heading">About</h2>
        <p className="about-version">Sirdar desktop</p>
        <p>
          Configuration reference: <a href="docs/config.md">docs/config.md</a>
        </p>
        <p className="about-note">
          Sirdar runs your own installed agent CLI with your login. Sirdar never stores
          credentials.
        </p>
      </section>
    </div>
  )
}
