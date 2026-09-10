import type { Workspace } from '../../api/types'

const ADD = '__add__'

/**
 * Which repository the board is looking at. The last entry is the way to
 * register another one, so the switcher answers "where is my other repo?"
 * without a trip to Settings first.
 */
export default function WorkspaceSwitcher(props: {
  workspaces: Workspace[]
  currentId: string
  onSelect: (id: string) => void
  onAdd: () => void
}): JSX.Element {
  const { workspaces, currentId, onSelect, onAdd } = props
  const current = workspaces.find((w) => w.id === currentId)

  return (
    <label className="switcher">
      <span className="visually-hidden">Workspace</span>
      <select
        className="switcher-select"
        value={currentId}
        title={current?.root ?? ''}
        onChange={(e) => {
          const value = e.target.value
          if (value === ADD) onAdd()
          else onSelect(value)
        }}
      >
        {workspaces.length === 0 && <option value="">No workspace</option>}
        {workspaces.map((w) => (
          <option key={w.id} value={w.id}>
            {w.name}
          </option>
        ))}
        <option value={ADD}>Add workspace…</option>
      </select>
    </label>
  )
}
