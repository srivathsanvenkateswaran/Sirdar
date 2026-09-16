import { useLayoutEffect, type RefObject } from 'react'

/**
 * Where a popover goes, so that it never pushes the window into a scroll.
 *
 * `html`, `body` and `#root` are `overflow: hidden` (styles.css): the app is
 * a fixed frame and every screen scrolls inside its sheet. A popover drawn
 * `position: absolute` under its trigger would be clipped by the sheet, and
 * one taller than the room under the chip would once have grown the page —
 * which is what the user's screenshot showed. So a popover is fixed to the
 * viewport instead, and this module says where: below the trigger when it
 * fits, above when it does not, clamped to the viewport's sides, and no
 * taller than the space on the side it took.
 *
 * `place` is the arithmetic, on plain numbers so it can be tested without a
 * layout engine. `useAnchor` is the hook that measures and applies it, and
 * measures again on resize and on any scroll, since the trigger moves with
 * the sheet it sits in.
 */

export interface Box {
  top: number
  left: number
  width: number
  height: number
}

export interface Size {
  width: number
  height: number
}

export interface Placement {
  top: number
  left: number
  /** The room on the side chosen: what the popover's max-height should be. */
  maxHeight: number
  /** Below or above the trigger; or, for `placeBeside`, at its trailing (`end`) or leading (`start`) edge. */
  side: 'below' | 'above' | 'end' | 'start'
}

export interface PlaceOptions {
  /** Between the trigger and the popover. */
  gap?: number
  /** Kept clear at every viewport edge. */
  margin?: number
  /**
   * Which of the trigger's edges the popover lines up with: its leading edge
   * (`start`, the default) or its trailing one (`end`), read in `dir`.
   */
  align?: 'start' | 'end'
  dir?: 'ltr' | 'rtl'
}

export const GAP = 4
export const MARGIN = 8

/**
 * Below when the popover fits below; otherwise whichever side has more room,
 * with the popover held to that room. The horizontal edge lines up with the
 * trigger's, then gives way to the viewport: a chip near the right edge gets
 * a popover that ends at the margin rather than one cut off by the window.
 */
export function place(
  trigger: Box,
  popover: Size,
  viewport: Size,
  { gap = GAP, margin = MARGIN, align = 'start', dir = 'ltr' }: PlaceOptions = {},
): Placement {
  const below = Math.max(0, viewport.height - (trigger.top + trigger.height) - gap - margin)
  const above = Math.max(0, trigger.top - gap - margin)
  const side: Placement['side'] = popover.height <= below || below >= above ? 'below' : 'above'
  const room = side === 'below' ? below : above
  const height = Math.min(popover.height, room)
  const top = side === 'below' ? trigger.top + trigger.height + gap : trigger.top - gap - height

  const leftEdges = (align === 'start') !== (dir === 'rtl')
  let left = leftEdges ? trigger.left : trigger.left + trigger.width - popover.width
  left = Math.min(left, viewport.width - margin - popover.width)
  left = Math.max(left, margin)

  return { top, left, maxHeight: room, side }
}

/**
 * Beside the trigger rather than under it: the hover card a sidebar row
 * opens into the sheet. The popover's leading edge sits `gap` past the
 * trigger's trailing edge (read in `dir`), its top lined up with the
 * trigger's; when that side has no room it goes to the leading side instead.
 * The top is then held inside the viewport, so a card for the last row does
 * not run off the bottom, and `maxHeight` is the viewport less the margins.
 */
export function placeBeside(
  trigger: Box,
  popover: Size,
  viewport: Size,
  { gap = GAP, margin = MARGIN, dir = 'ltr' }: Pick<PlaceOptions, 'gap' | 'margin' | 'dir'> = {},
): Placement {
  const rightOf = trigger.left + trigger.width + gap
  const leftOf = trigger.left - gap - popover.width
  const fitsRight = rightOf + popover.width <= viewport.width - margin
  const fitsLeft = leftOf >= margin
  // The trailing side first; the leading side when the trailing has no room
  // and the leading has; else the trailing side clamped, since a card that
  // covers its own row is still worse than one held to the margin.
  const trailingIsRight = dir !== 'rtl'
  const wantRight = trailingIsRight ? fitsRight || !fitsLeft : !fitsLeft && fitsRight
  let left = wantRight ? rightOf : leftOf
  left = Math.min(left, viewport.width - margin - popover.width)
  left = Math.max(left, margin)
  const side: Placement['side'] = wantRight === trailingIsRight ? 'end' : 'start'

  const maxHeight = Math.max(0, viewport.height - 2 * margin)
  const height = Math.min(popover.height, maxHeight)
  let top = trigger.top
  top = Math.min(top, viewport.height - margin - height)
  top = Math.max(top, margin)
  return { top, left, maxHeight, side }
}

/** The reading direction an element is laid out in. */
function directionOf(node: HTMLElement): 'ltr' | 'rtl' {
  const computed = typeof getComputedStyle === 'function' ? getComputedStyle(node).direction : ''
  if (computed === 'rtl' || computed === 'ltr') return computed
  return document.dir === 'rtl' ? 'rtl' : 'ltr'
}

/**
 * Pins `popover` to the viewport beside `trigger` for as long as `open`.
 *
 * The styles are written onto the element rather than returned, so a
 * re-measure on scroll does not re-render the popover's contents. The
 * popover's own stylesheet still owns its width; this sets only where it
 * goes and how tall it may be. `max-height` is cleared before each measure
 * so a popover that was held short on one side is measured at its natural
 * height when the window grows.
 */
export function useAnchor(
  open: boolean,
  trigger: RefObject<HTMLElement | null>,
  popover: RefObject<HTMLElement | null>,
  options: Pick<PlaceOptions, 'align' | 'gap' | 'margin'> & {
    /** Beside the trigger (`placeBeside`) instead of under it. */
    beside?: boolean
  } = {},
): void {
  const { align, gap, margin, beside = false } = options
  useLayoutEffect(() => {
    if (!open) return
    function apply(): void {
      const chip = trigger.current
      const pop = popover.current
      if (!chip || !pop) return
      pop.style.position = 'fixed'
      pop.style.maxHeight = ''
      const view = { width: window.innerWidth, height: window.innerHeight }
      const opts = { align, gap, margin, dir: directionOf(chip) }
      const at = beside
        ? placeBeside(chip.getBoundingClientRect(), pop.getBoundingClientRect(), view, opts)
        : place(chip.getBoundingClientRect(), pop.getBoundingClientRect(), view, opts)
      pop.style.top = `${at.top}px`
      pop.style.left = `${at.left}px`
      pop.style.maxHeight = `${at.maxHeight}px`
      pop.dataset.side = at.side
    }
    apply()
    window.addEventListener('resize', apply)
    // Any scroll, anywhere: the sheet the trigger sits in is what scrolls.
    document.addEventListener('scroll', apply, true)
    return () => {
      window.removeEventListener('resize', apply)
      document.removeEventListener('scroll', apply, true)
    }
  }, [open, trigger, popover, align, gap, margin, beside])
}
