import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from 'react'

/**
 * The one filled commit button a screen is allowed, and where it is drawn.
 *
 * `docs/design/03-desktop-app.md` section 8 makes "at most one per screen" a
 * rule with teeth, and section 5 pins it to the sidebar footer. A screen
 * cannot reach into the sidebar, and the shell cannot know what Eval's Run
 * suite button should say while a selection is being made, so the screen
 * publishes its action here and the footer reads it.
 *
 * A screen with no commit action publishes nothing and the footer draws
 * nothing — the Register having no button is the proof the rule is real rather
 * than decorative.
 */
export interface PrimaryAction {
  label: string
  onRun: () => void
  disabled?: boolean
  busy?: boolean
  /** A keyboard hint drawn inside the button. */
  shortcut?: string
  title?: string
  /**
   * The screen draws the filled button itself, beside the input it commits
   * — the session's Answer sits in the composer, because a commit button a
   * window away from its own text box is one nobody presses on purpose.
   * The footer then draws New session demoted to a plain button rather
   * than a second filled one, so the screen still has exactly one.
   */
  inline?: boolean
}

interface Slot {
  action: PrimaryAction | null
  publish: (action: PrimaryAction | null) => void
}

const Context = createContext<Slot>({ action: null, publish: () => {} })

export function PrimaryActionProvider({ children }: { children: ReactNode }): JSX.Element {
  const [action, publish] = useState<PrimaryAction | null>(null)
  const value = useMemo(() => ({ action, publish }), [action])
  return <Context.Provider value={value}>{children}</Context.Provider>
}

/** What the sidebar footer should draw right now. */
export function usePrimaryAction(): PrimaryAction | null {
  return useContext(Context).action
}

/**
 * Publishes this screen's commit action for as long as the screen is up, and
 * clears it on the way out — so a screen that has none cannot inherit the
 * previous screen's button.
 *
 * What is published is a stable callback reading the latest action out of a
 * ref, never the caller's own function. A screen that builds its action inline
 * hands over a new `onRun` on every render, and republishing on every render
 * re-renders on every publish: the screen spins until the window stops
 * answering. Holding the callback still is what makes the plain, obvious call
 * — `useProvidePrimaryAction({ label, onRun: () => start() })` — correct.
 */
export function useProvidePrimaryAction(action: PrimaryAction | null): void {
  const { publish } = useContext(Context)
  const latest = useRef(action)
  latest.current = action

  const run = useCallback(() => latest.current?.onRun(), [])

  const label = action?.label ?? ''
  const disabled = action?.disabled ?? false
  const busy = action?.busy ?? false
  const shortcut = action?.shortcut
  const title = action?.title
  const inline = action?.inline ?? false

  useEffect(() => {
    publish(label ? { label, onRun: run, disabled, busy, shortcut, title, inline } : null)
    return () => publish(null)
  }, [publish, run, label, disabled, busy, shortcut, title, inline])
}
