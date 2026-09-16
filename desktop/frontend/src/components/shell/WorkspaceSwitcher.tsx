import { useEffect, useRef, useState } from 'react'
import type { Workspace } from '../../api/types'
import { useAnchor } from '../../lib/anchor'
import { SwitcherIcon } from './icons'

/**
 * Which repository the board is looking at.
 *
 * It is a button and a popover list, not a native `select`. A select shows one
 * line at a time and cannot show a workspace's path beside its name, and the
 * app-shell language asks for a popover by name — the switcher is Sirdar's
 * version of the account card the reference pins to the foot of its sidebar.
 *
 * Two triggers draw the same list. `badge` (the default) is the lavender
 * chip beside the wordmark. `inline` is a word in a sentence — New session's
 * headline names the workspace with a dotted underline, and that word opens
 * the switcher. The list is pinned to the viewport by `lib/anchor` either
 * way, so it can never push the window into a scroll.
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
  variant?: 'badge' | 'inline'
}): JSX.Element {
  const { workspaces, currentId, onSelect, onAdd, variant = 'badge' } = props
  const [open, setOpen] = useState(false)
  const box = useRef<HTMLDivElement | null>(null)
  const trigger = useRef<HTMLButtonElement | null>(null)
  const list = useRef<HTMLUListElement | null>(null)
  const current = workspaces.find((w) => w.id === currentId)

  useAnchor(open, box, list)

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

  const name = current?.name ?? (variant === 'inline' ? 'this workspace' : 'No workspace')

  return (
    <div className="switcher" data-variant={variant === 'inline' ? 'inline' : undefined} ref={box}>
      <button
        ref={trigger}
        type="button"
        className={variant === 'inline' ? 'switcher-word' : 'switcher-trigger'}
        aria-haspopup="listbox"
        aria-expanded={open}
        aria-label={variant === 'inline' ? `${name}. Change workspace` : `Workspace: ${current?.name ?? 'none'}`}
        title={variant === 'inline' ? 'Change workspace' : current?.root ?? ''}
        onClick={() => setOpen((v) => !v)}
      >
        <span className="switcher-name">{name}</span>
        {variant === 'inline' ? null : <SwitcherIcon />}
      </button>

      {open && (
        <ul ref={list} className="switcher-list" role="listbox" aria-label="Workspaces">
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
