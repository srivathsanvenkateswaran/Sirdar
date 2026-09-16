import { useCallback, useEffect, useRef, useState } from 'react'

/** How long a pointer rests on a row before its card opens: hover intent, not a flicker. */
export const CARD_OPEN_MS = 120
/** How long the card stays after the pointer has left both the row and the card. */
export const CARD_CLOSE_MS = 150

export interface HoverCard {
  /** The row whose card is open, or null. */
  open: string | null
  enterRow: (id: string) => void
  leaveRow: (id: string) => void
  /** Keyboard focus on a row opens the card too; a click's focus is left to the pointer path. */
  focusRow: (id: string) => void
  blurRow: (id: string) => void
  enterCard: () => void
  leaveCard: () => void
  /**
   * A press on a row: the card goes, and a card still on its way is
   * cancelled, so a quick click does not get a card over the run it opened.
   */
  pressRow: () => void
  /** Escape, a scroll, a click anywhere: the card goes and stays gone until the next intent. */
  close: () => void
}

/**
 * The hover card's timing, the way T3 Code's sidebar does it and the way a
 * menu bar does it: a short wait before the first card, no wait at all when
 * the pointer moves to the next row while one is open, and a short grace
 * after leaving so the pointer can cross the gap onto the card itself.
 *
 * Everything that times anything lives in refs. The sidebar re-renders on
 * every store emit — a run's event stream is a firehose — and a timer that
 * lived in state, or a handler that read `open` from a closure, would be
 * reset or misled by each one. The only piece of React state is the id of
 * the open card, which is the one thing the render needs.
 *
 * Two things hold a card open: the pointer (over its row or over the card)
 * and keyboard focus on its row. Focus never gates the pointer: a card the
 * pointer opened closes when the pointer leaves, whatever has focus, unless
 * focus is what opened it. And a click closes it — the row's own click opens
 * the run, and the card has said what it had to.
 */
export function useHoverCard(): HoverCard {
  const [open, setOpenState] = useState<string | null>(null)
  const openRef = useRef<string | null>(null)
  const openTimer = useRef<ReturnType<typeof setTimeout> | null>(null)
  const armedFor = useRef<string | null>(null)
  const closeTimer = useRef<ReturnType<typeof setTimeout> | null>(null)
  /** The row the pointer is in. */
  const pointerRow = useRef<string | null>(null)
  const overCard = useRef(false)
  /** The row keyboard focus opened a card for. */
  const focusHold = useRef<string | null>(null)

  const setOpen = useCallback((id: string | null) => {
    openRef.current = id
    setOpenState(id)
  }, [])

  const clearOpenTimer = useCallback(() => {
    if (openTimer.current) clearTimeout(openTimer.current)
    openTimer.current = null
    armedFor.current = null
  }, [])

  const clearCloseTimer = useCallback(() => {
    if (closeTimer.current) clearTimeout(closeTimer.current)
    closeTimer.current = null
  }, [])

  const show = useCallback(
    (id: string) => {
      clearOpenTimer()
      clearCloseTimer()
      if (openRef.current !== id) setOpen(id)
    },
    [clearOpenTimer, clearCloseTimer, setOpen],
  )

  const close = useCallback(() => {
    clearOpenTimer()
    clearCloseTimer()
    focusHold.current = null
    if (openRef.current !== null) setOpen(null)
  }, [clearOpenTimer, clearCloseTimer, setOpen])

  /** Closes after the grace, unless something takes hold of the card again first. */
  const scheduleClose = useCallback(() => {
    clearCloseTimer()
    if (openRef.current === null) return
    closeTimer.current = setTimeout(() => {
      closeTimer.current = null
      if (pointerRow.current === null && !overCard.current && focusHold.current === null) {
        setOpen(null)
      }
    }, CARD_CLOSE_MS)
  }, [clearCloseTimer, setOpen])

  /** Opens `id` now if a card is up, else after the intent delay while `still` holds. */
  const intend = useCallback(
    (id: string, still: () => boolean) => {
      clearCloseTimer()
      if (openRef.current !== null) {
        show(id)
        return
      }
      if (armedFor.current === id) return
      clearOpenTimer()
      armedFor.current = id
      openTimer.current = setTimeout(() => {
        openTimer.current = null
        armedFor.current = null
        if (still()) show(id)
      }, CARD_OPEN_MS)
    },
    [clearCloseTimer, clearOpenTimer, show],
  )

  const enterRow = useCallback(
    (id: string) => {
      pointerRow.current = id
      intend(id, () => pointerRow.current === id)
    },
    [intend],
  )

  const leaveRow = useCallback(
    (id: string) => {
      if (pointerRow.current === id) pointerRow.current = null
      if (armedFor.current === id && focusHold.current !== id) clearOpenTimer()
      scheduleClose()
    },
    [clearOpenTimer, scheduleClose],
  )

  const focusRow = useCallback(
    (id: string) => {
      // The focus a click gives a row: the pointer is already there and owns it.
      if (pointerRow.current === id) return
      focusHold.current = id
      intend(id, () => focusHold.current === id)
    },
    [intend],
  )

  const blurRow = useCallback(
    (id: string) => {
      if (focusHold.current === id) focusHold.current = null
      if (armedFor.current === id && pointerRow.current !== id) clearOpenTimer()
      scheduleClose()
    },
    [clearOpenTimer, scheduleClose],
  )

  const enterCard = useCallback(() => {
    overCard.current = true
    clearCloseTimer()
  }, [clearCloseTimer])

  const leaveCard = useCallback(() => {
    overCard.current = false
    scheduleClose()
  }, [scheduleClose])

  // While a card is up: Escape, any scroll and any press anywhere close it.
  // Escape is consumed, so the screen behind does not also act on it.
  useEffect(() => {
    if (open === null) return
    function onKey(e: KeyboardEvent): void {
      if (e.key !== 'Escape') return
      e.stopPropagation()
      close()
    }
    function onPress(): void {
      close()
    }
    document.addEventListener('keydown', onKey, true)
    document.addEventListener('scroll', onPress, true)
    document.addEventListener('pointerdown', onPress, true)
    return () => {
      document.removeEventListener('keydown', onKey, true)
      document.removeEventListener('scroll', onPress, true)
      document.removeEventListener('pointerdown', onPress, true)
    }
  }, [open, close])

  useEffect(
    () => () => {
      if (openTimer.current) clearTimeout(openTimer.current)
      if (closeTimer.current) clearTimeout(closeTimer.current)
    },
    [],
  )

  return { open, enterRow, leaveRow, focusRow, blurRow, enterCard, leaveCard, pressRow: close, close }
}
