import { useEffect, useRef, useState } from 'react'
import { reasonOf } from './format'

/**
 * One control over a path on the operator's machine: opens it on the
 * desktop, copies it in a browser.
 *
 * Three of the Transport's methods — `openConfig`, `openNote`, `openRunDir`
 * — only the Wails shell can answer: a browser served by `sirdar serve` is
 * not allowed to open a file or a folder on the machine it is talking to.
 * The web UI must not therefore be short a control. Everywhere one of those
 * is offered, the same control copies the path when the method is not
 * there, which is what the Playbooks and Settings rows have always done and
 * what this holds in one place.
 */

/** How long the control says `Copied` before it goes back to offering. */
const COPIED_MS = 2000

export interface PathActionOptions {
  /** The path to copy when there is nothing to open it with; '' leaves the control disabled. */
  path: string
  /** What the desktop does with it. Absent in a browser — that is the whole distinction. */
  open?: () => Promise<void> | void
  /** What the control is called when it opens: `Open run folder`. */
  openLabel: string
  /** What it is called when it copies: `Copy folder path`. */
  copyLabel: string
}

export interface PathAction {
  /** True when the shell can open the path itself. */
  canOpen: boolean
  /** The control's name, whatever it is showing: for `aria-label` and for a menu item's id. */
  name: string
  /** What the control shows now — `name`, or `Copied` for a moment after a copy. */
  label: string
  /** The whole path and what pressing does, for the control's `title`. */
  title: string
  /** Nothing to open and no path to copy. */
  disabled: boolean
  /** Why the last press failed, while it holds. */
  failure: string
  run: () => void
}

export function usePathAction({ path, open, openLabel, copyLabel }: PathActionOptions): PathAction {
  const [copied, setCopied] = useState(false)
  const [failure, setFailure] = useState('')
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null)
  useEffect(
    () => () => {
      if (timer.current) clearTimeout(timer.current)
    },
    [],
  )

  const canOpen = Boolean(open)
  const name = canOpen ? openLabel : copyLabel

  async function act(): Promise<void> {
    setFailure('')
    if (open) {
      try {
        await open()
      } catch (err) {
        setFailure(reasonOf(err))
      }
      return
    }
    if (!path) return
    try {
      await navigator.clipboard?.writeText(path)
      setCopied(true)
      if (timer.current) clearTimeout(timer.current)
      timer.current = setTimeout(() => setCopied(false), COPIED_MS)
    } catch {
      setFailure('The path could not be copied')
    }
  }

  return {
    canOpen,
    name,
    label: !canOpen && copied ? 'Copied' : name,
    title: canOpen ? `Opens ${path}` : path ? `Copies ${path}; the app cannot open folders from a browser` : '',
    disabled: !canOpen && !path,
    failure,
    run: () => void act(),
  }
}

/**
 * A run's own directory under `.sirdar/runs`, which is the bundle
 * directory's parent. The service records the bundle, not the run folder,
 * and the desktop's `OpenRunDir` takes the same step on the Go side.
 */
export function runDirOf(bundleDir: string): string {
  const cut = Math.max(bundleDir.lastIndexOf('/'), bundleDir.lastIndexOf('\\'))
  return cut > 0 ? bundleDir.slice(0, cut) : bundleDir
}
