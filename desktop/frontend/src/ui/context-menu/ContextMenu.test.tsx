import { fireEvent, render, screen, within } from '@testing-library/react'
import { useRef, useState } from 'react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { GAP, pointAnchor, type Anchorable } from '../../lib/anchor'
import ContextMenu, { type MenuEntry } from './index'

afterEach(() => {
  vi.restoreAllMocks()
})

function entries(picked: (id: string) => void): MenuEntry[] {
  return [
    { id: 'pin', label: 'Pin', onSelect: () => picked('pin') },
    { id: 'settle', label: 'Mark settled', onSelect: () => picked('settle') },
    {
      kind: 'submenu',
      id: 'snooze',
      label: 'Snooze',
      items: [
        { id: 'hour', label: '1 hour', onSelect: () => picked('hour') },
        { id: 'tomorrow', label: 'Until tomorrow 9:00', onSelect: () => picked('tomorrow') },
      ],
    },
    { id: 'rename', label: 'Rename', detail: 'F2', onSelect: () => picked('rename') },
    { id: 'folder', label: 'Open the run folder', disabled: true, onSelect: () => picked('folder') },
    { kind: 'separator', id: 'sep' },
    { id: 'archive', label: 'Archive', onSelect: () => picked('archive') },
    { id: 'delete', label: 'Delete…', tone: 'danger', onSelect: () => picked('delete') },
  ]
}

/** A button that opens the menu under itself, the way the dots button does. */
function Harness({ picked, at }: { picked: (id: string) => void; at?: Anchorable }): JSX.Element {
  const [open, setOpen] = useState(false)
  const button = useRef<HTMLButtonElement | null>(null)
  const point = useRef<Anchorable | null>(at ?? null)
  return (
    <>
      <button type="button" ref={button} onClick={() => setOpen(true)}>
        More
      </button>
      <input aria-label="elsewhere" />
      <ContextMenu
        open={open}
        anchor={at ? point : button}
        items={entries(picked)}
        label="Session menu for OMNI-1"
        onClose={() => setOpen(false)}
      />
    </>
  )
}

function mount(at?: Anchorable) {
  const picked = vi.fn()
  const view = render(<Harness picked={picked} at={at} />)
  const opener = screen.getByRole('button', { name: 'More' })
  opener.focus()
  fireEvent.click(opener)
  return { picked, opener, ...view }
}

const menu = () => screen.getByRole('menu', { name: 'Session menu for OMNI-1' })
const items = () => within(menu()).getAllByRole('menuitem')
const focused = () => document.activeElement

describe('the context menu', () => {
  it('lists its rows, separators and submenu, and takes focus on the first row', () => {
    mount()
    expect(menu()).toBeInTheDocument()
    expect(items().map((i) => i.textContent)).toEqual([
      'Pin',
      'Mark settled',
      'Snooze',
      'RenameF2',
      'Open the run folder',
      'Archive',
      'Delete…',
    ])
    expect(within(menu()).getByRole('separator')).toBeInTheDocument()
    const snooze = within(menu()).getByRole('menuitem', { name: 'Snooze' })
    expect(snooze).toHaveAttribute('aria-haspopup', 'menu')
    expect(snooze).toHaveAttribute('aria-expanded', 'false')
    expect(within(menu()).getByRole('menuitem', { name: 'Open the run folder' })).toHaveAttribute('aria-disabled', 'true')
    expect(within(menu()).getByRole('menuitem', { name: 'Delete…' })).toHaveAttribute('data-tone', 'danger')
    expect(focused()).toBe(items()[0])
    expect(menu().style.position).toBe('fixed')
  })

  it('moves with the arrows and wraps, jumps with Home and End', () => {
    mount()
    const rows = items()
    fireEvent.keyDown(rows[0], { key: 'ArrowDown' })
    expect(focused()).toBe(rows[1])
    fireEvent.keyDown(rows[1], { key: 'End' })
    expect(focused()).toBe(rows[6])
    fireEvent.keyDown(rows[6], { key: 'ArrowDown' })
    expect(focused()).toBe(rows[0])
    fireEvent.keyDown(rows[0], { key: 'ArrowUp' })
    expect(focused()).toBe(rows[6])
    fireEvent.keyDown(rows[6], { key: 'Home' })
    expect(focused()).toBe(rows[0])
  })

  it('picks on Enter, Space and click, then closes; a disabled row is never picked', () => {
    const { picked } = mount()
    fireEvent.keyDown(items()[1], { key: 'Enter' })
    expect(picked).toHaveBeenCalledWith('settle')
    expect(screen.queryByRole('menu')).toBeNull()

    fireEvent.click(screen.getByRole('button', { name: 'More' }))
    fireEvent.click(within(menu()).getByRole('menuitem', { name: 'Archive' }))
    expect(picked).toHaveBeenLastCalledWith('archive')
    expect(screen.queryByRole('menu')).toBeNull()

    fireEvent.click(screen.getByRole('button', { name: 'More' }))
    const off = within(menu()).getByRole('menuitem', { name: 'Open the run folder' })
    fireEvent.click(off)
    off.focus()
    fireEvent.keyDown(off, { key: ' ' })
    expect(picked).not.toHaveBeenCalledWith('folder')
    expect(menu()).toBeInTheDocument()
  })

  it('closes on Escape and gives focus back to what opened it', () => {
    const { opener } = mount()
    fireEvent.keyDown(items()[0], { key: 'Escape' })
    expect(screen.queryByRole('menu')).toBeNull()
    expect(focused()).toBe(opener)
  })

  it('closes on a press outside, and not on one inside', () => {
    mount()
    fireEvent.pointerDown(items()[2])
    expect(menu()).toBeInTheDocument()
    fireEvent.pointerDown(screen.getByLabelText('elsewhere'))
    expect(screen.queryByRole('menu')).toBeNull()
  })

  it('opens a submenu beside its row on ArrowRight, hover or click; ArrowLeft and Escape come back', () => {
    const { picked } = mount()
    const snooze = within(menu()).getByRole('menuitem', { name: 'Snooze' })
    snooze.focus()
    fireEvent.keyDown(snooze, { key: 'ArrowRight' })
    expect(snooze).toHaveAttribute('aria-expanded', 'true')
    const sub = screen.getByRole('menu', { name: 'Snooze' })
    expect(snooze).toHaveAttribute('aria-controls', sub.id)
    const subRows = within(sub).getAllByRole('menuitem')
    expect(subRows.map((r) => r.textContent)).toEqual(['1 hour', 'Until tomorrow 9:00'])
    expect(focused()).toBe(subRows[0])

    fireEvent.keyDown(subRows[0], { key: 'ArrowLeft' })
    expect(screen.queryByRole('menu', { name: 'Snooze' })).toBeNull()
    expect(focused()).toBe(snooze)

    fireEvent.pointerEnter(snooze)
    expect(screen.getByRole('menu', { name: 'Snooze' })).toBeInTheDocument()
    // Escape inside the submenu closes only the submenu.
    fireEvent.keyDown(within(screen.getByRole('menu', { name: 'Snooze' })).getAllByRole('menuitem')[0], {
      key: 'Escape',
    })
    expect(screen.queryByRole('menu', { name: 'Snooze' })).toBeNull()
    expect(menu()).toBeInTheDocument()

    // The pointer moving to a sibling row closes it too.
    fireEvent.pointerEnter(snooze)
    fireEvent.pointerEnter(within(menu()).getByRole('menuitem', { name: 'Pin' }))
    expect(screen.queryByRole('menu', { name: 'Snooze' })).toBeNull()

    // Picking inside the submenu closes the whole menu.
    fireEvent.click(snooze)
    fireEvent.click(within(screen.getByRole('menu', { name: 'Snooze' })).getByRole('menuitem', { name: '1 hour' }))
    expect(picked).toHaveBeenCalledWith('hour')
    expect(screen.queryByRole('menu')).toBeNull()
  })

  it('jumps to the next row starting with a typed letter, cycling', () => {
    mount()
    const rows = items()
    fireEvent.keyDown(rows[0], { key: 'a' })
    expect(focused()).toBe(within(menu()).getByRole('menuitem', { name: 'Archive' }))
    fireEvent.keyDown(focused()!, { key: 'm' })
    expect(focused()).toBe(within(menu()).getByRole('menuitem', { name: 'Mark settled' }))
    fireEvent.keyDown(focused()!, { key: 'd' })
    expect(focused()).toBe(within(menu()).getByRole('menuitem', { name: 'Delete…' }))
    // A chord is not typeahead.
    fireEvent.keyDown(focused()!, { key: 'a', metaKey: true })
    expect(focused()).toBe(within(menu()).getByRole('menuitem', { name: 'Delete…' }))
  })

  it('closes on Tab rather than letting focus wander inside it', () => {
    const { opener } = mount()
    fireEvent.keyDown(items()[0], { key: 'Tab' })
    expect(screen.queryByRole('menu')).toBeNull()
    expect(focused()).toBe(opener)
  })

  it('opens under the pointer when given a point', () => {
    vi.spyOn(window, 'innerWidth', 'get').mockReturnValue(1470)
    vi.spyOn(window, 'innerHeight', 'get').mockReturnValue(900)
    vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockReturnValue({
      top: 0,
      left: 0,
      width: 220,
      height: 260,
      right: 220,
      bottom: 260,
      x: 0,
      y: 0,
      toJSON: () => ({}),
    })
    mount(pointAnchor(300, 200))
    expect(menu().style.top).toBe(`${200 + GAP}px`)
    expect(menu().style.left).toBe('300px')
  })

  it('draws inline for the gallery, taking no focus and ignoring presses outside', () => {
    const before = document.activeElement
    render(
      <ContextMenu
        open
        inline
        anchor={{ current: null }}
        items={entries(() => {})}
        label="Specimen"
        onClose={() => {}}
      />,
    )
    const specimen = screen.getByRole('menu', { name: 'Specimen' })
    expect(specimen).toHaveAttribute('data-inline', 'true')
    expect(specimen.style.position).toBe('')
    expect(document.activeElement).toBe(before)
    fireEvent.pointerDown(document.body)
    expect(screen.getByRole('menu', { name: 'Specimen' })).toBeInTheDocument()
  })
})
