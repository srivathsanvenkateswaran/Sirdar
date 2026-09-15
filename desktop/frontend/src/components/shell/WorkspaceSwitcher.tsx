import { useEffect, useRef, useState } from 'react'
import type { Workspace } from '../../api/types'
import { SwitcherIcon } from './icons'

/**
 * Which repository the board is looking at.
 *
 * It is a button and a popover list, not a native `select`. A select shows one
 * line at a time and cannot show a workspace's path beside its name, and the
 * app-shell language asks for a popover by name — the switcher is Sirdar's
 * version of the account card the reference pins to the foot of its sidebar.
 *
 * The last entry is still the way to register another repository, so the
 * switcher answers "where is my other repo?" without a trip through Settings
 * first.
 */
export default function WorkspaceSwitcher(props: {
  workspaces: Workspace[]
  currentId: string
  onSelect: (id: string) => void
  onAdd: () => void
}): JSX.Element {
  const { workspaces, currentId, onSelect, onAdd } = props
  const [open, setOpen] = useState(false)
  const box = useRef<HTMLDivElement | null>(null)
  const trigger = useRef<HTMLButtonElement | null>(null)
  const current = workspaces.find((w) => w.id === currentId)

  useEffect(() => {
    if (!open) return
    function onDocDown(event: MouseEvent): void {
      if (!box.current?.contains(event.target as Node)) setOpen(false)
    }
    function onKey(event: KeyboardEvent): void {
      if (event.key !== 'Escape') return
      // The popover is the innermost thing open, so it takes the key before
      // the window's own Escape handling reaches it.
      event.stopPropagation()
      setOpen(false)
      trigger.current?.focus()
    }
    document.addEventListener('mousedown', onDocDown)
    document.addEventListener('keydown', onKey, true)
    return () => {
      document.removeEventListener('mousedown', onDocDown)
      document.removeEventListener('keydown', onKey, true)
    }
  }, [open])

  function choose(id: string): void {
    setOpen(false)
    onSelect(id)
    trigger.current?.focus()
  }

  return (
    <div className="switcher" ref={box}>
      <button
        ref={trigger}
        type="button"
        className="switcher-trigger"
        aria-haspopup="listbox"
        aria-expanded={open}
        aria-label={`Workspace: ${current?.name ?? 'none'}`}
        title={current?.root ?? ''}
        onClick={() => setOpen((v) => !v)}
      >
        <span className="switcher-name">{current?.name ?? 'No workspace'}</span>
        <SwitcherIcon />
      </button>

      {open && (
        <ul className="switcher-list" role="listbox" aria-label="Workspaces">
          {workspaces.length === 0 && (
            <li className="switcher-empty">No workspace is registered yet.</li>
          )}
          {workspaces.map((w) => (
            <li key={w.id}>
              <button
                type="button"
                className="switcher-option"
                role="option"
                aria-selected={w.id === currentId}
                onClick={() => choose(w.id)}
              >
                <span className="switcher-option__name">{w.name}</span>
                <span className="switcher-option__root" dir="ltr">
                  {w.root}
                </span>
              </button>
            </li>
          ))}
          <li>
            <button
              type="button"
              className="switcher-option switcher-option--add"
              onClick={() => {
                setOpen(false)
                onAdd()
              }}
            >
              Add workspace…
            </button>
          </li>
        </ul>
      )}
    </div>
  )
}
