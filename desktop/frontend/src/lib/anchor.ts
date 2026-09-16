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
  side: 'below' | 'above'
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
  options: Pick<PlaceOptions, 'align' | 'gap' | 'margin'> = {},
): void {
  const { align, gap, margin } = options
  useLayoutEffect(() => {
    if (!open) return
    function apply(): void {
      const chip = trigger.current
      const pop = popover.current
      if (!chip || !pop) return
      pop.style.position = 'fixed'
      pop.style.maxHeight = ''
      const at = place(
        chip.getBoundingClientRect(),
        pop.getBoundingClientRect(),
        { width: window.innerWidth, height: window.innerHeight },
        { align, gap, margin, dir: directionOf(chip) },
      )
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
  }, [open, trigger, popover, align, gap, margin])
}
