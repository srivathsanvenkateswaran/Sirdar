import { act, render } from '@testing-library/react'
import { useRef } from 'react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { GAP, MARGIN, place, placeBeside, useAnchor } from './anchor'

const VIEW = { width: 1470, height: 900 }
const POP = { width: 480, height: 420 }

afterEach(() => {
  vi.restoreAllMocks()
})

describe('place', () => {
  it('opens below the trigger, leading edges lined up, when it fits', () => {
    const at = place({ top: 300, left: 200, width: 120, height: 32 }, POP, VIEW)
    expect(at.side).toBe('below')
    expect(at.top).toBe(300 + 32 + GAP)
    expect(at.left).toBe(200)
    // The room below is what the popover may grow to, not its own height.
    expect(at.maxHeight).toBe(900 - 332 - GAP - MARGIN)
  })

  it('flips above when the room below is short and the room above is more', () => {
    const at = place({ top: 700, left: 200, width: 120, height: 32 }, POP, VIEW)
    expect(at.side).toBe('above')
    expect(at.top).toBe(700 - GAP - 420)
    expect(at.maxHeight).toBe(700 - GAP - MARGIN)
  })

  it('stays below, held to the room, when neither side has enough and below has more', () => {
    const at = place({ top: 300, left: 200, width: 120, height: 32 }, { width: 480, height: 800 }, VIEW)
    expect(at.side).toBe('below')
    expect(at.maxHeight).toBe(900 - 332 - GAP - MARGIN)
  })

  it('holds a popover to the room above once it has flipped', () => {
    const at = place({ top: 600, left: 200, width: 120, height: 32 }, { width: 480, height: 800 }, VIEW)
    expect(at.side).toBe('above')
    expect(at.maxHeight).toBe(600 - GAP - MARGIN)
    expect(at.top).toBe(600 - GAP - at.maxHeight)
  })

  it('clamps to the viewport sides', () => {
    const right = place({ top: 100, left: 1300, width: 120, height: 32 }, POP, VIEW)
    expect(right.left).toBe(1470 - MARGIN - 480)
    const left = place({ top: 100, left: 2, width: 120, height: 32 }, POP, VIEW, { align: 'end' })
    expect(left.left).toBe(MARGIN)
  })

  it('lines up trailing edges for align end, and reads the edges in the reading direction', () => {
    const end = place({ top: 100, left: 600, width: 120, height: 32 }, POP, VIEW, { align: 'end' })
    expect(end.left).toBe(600 + 120 - 480)
    // In an Arabic pane the leading edge is the right one.
    const rtl = place({ top: 100, left: 600, width: 120, height: 32 }, POP, VIEW, { dir: 'rtl' })
    expect(rtl.left).toBe(600 + 120 - 480)
    const rtlEnd = place({ top: 100, left: 600, width: 120, height: 32 }, POP, VIEW, {
      dir: 'rtl',
      align: 'end',
    })
    expect(rtlEnd.left).toBe(600)
  })
})

describe('placeBeside', () => {
  const CARD = { width: 320, height: 160 }
  const ROW = { top: 400, left: 16, width: 216, height: 40 }

  it('opens at the trailing edge of the row, tops lined up', () => {
    const at = placeBeside(ROW, CARD, VIEW)
    expect(at.side).toBe('end')
    expect(at.left).toBe(16 + 216 + GAP)
    expect(at.top).toBe(400)
    expect(at.maxHeight).toBe(900 - 2 * MARGIN)
  })

  it('goes to the leading side when the trailing has no room, and clamps when neither has', () => {
    const at = placeBeside({ ...ROW, left: 1300 }, CARD, VIEW)
    expect(at.side).toBe('start')
    expect(at.left).toBe(1300 - GAP - 320)
    const narrow = placeBeside(ROW, CARD, { width: 400, height: 900 })
    expect(narrow.left).toBe(400 - MARGIN - 320)
  })

  it('holds the card inside the viewport for a row near the bottom', () => {
    const at = placeBeside({ ...ROW, top: 860 }, CARD, VIEW)
    expect(at.top).toBe(900 - MARGIN - 160)
  })

  it('reads the trailing edge as the left one in an Arabic pane', () => {
    const at = placeBeside({ ...ROW, left: 1200 }, CARD, VIEW, { dir: 'rtl' })
    expect(at.side).toBe('end')
    expect(at.left).toBe(1200 - GAP - 320)
  })
})

function Harness({ open }: { open: boolean }): JSX.Element {
  const chip = useRef<HTMLButtonElement | null>(null)
  const pop = useRef<HTMLDivElement | null>(null)
  useAnchor(open, chip, pop)
  return (
    <>
      <button ref={chip} type="button">
        chip
      </button>
      {open ? (
        <div ref={pop} data-testid="pop">
          popover
        </div>
      ) : null}
    </>
  )
}

/** jsdom lays nothing out, so the rects are given. */
function rect(box: { top: number; left: number; width: number; height: number }): DOMRect {
  return { ...box, right: box.left + box.width, bottom: box.top + box.height, x: box.left, y: box.top, toJSON: () => box }
}

describe('useAnchor', () => {
  it('pins the popover to the viewport under the trigger and re-measures on resize', () => {
    vi.spyOn(window, 'innerWidth', 'get').mockReturnValue(1470)
    vi.spyOn(window, 'innerHeight', 'get').mockReturnValue(900)
    const rects = vi
      .spyOn(HTMLElement.prototype, 'getBoundingClientRect')
      .mockImplementation(function (this: HTMLElement) {
        return this.tagName === 'BUTTON'
          ? rect({ top: 300, left: 200, width: 120, height: 32 })
          : rect({ top: 0, left: 0, width: 480, height: 420 })
      })
    const { getByTestId } = render(<Harness open />)
    const pop = getByTestId('pop')
    expect(pop.style.position).toBe('fixed')
    expect(pop.style.top).toBe(`${300 + 32 + GAP}px`)
    expect(pop.style.left).toBe('200px')
    expect(pop.style.maxHeight).toBe(`${900 - 332 - GAP - MARGIN}px`)
    expect(pop).toHaveAttribute('data-side', 'below')

    // The window shrinks under the chip: the popover flips above it.
    vi.spyOn(window, 'innerHeight', 'get').mockReturnValue(500)
    act(() => {
      window.dispatchEvent(new Event('resize'))
    })
    expect(pop).toHaveAttribute('data-side', 'above')
    expect(rects).toHaveBeenCalled()
  })

  it('does nothing while closed', () => {
    const rects = vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect')
    render(<Harness open={false} />)
    expect(rects).not.toHaveBeenCalled()
  })
})
