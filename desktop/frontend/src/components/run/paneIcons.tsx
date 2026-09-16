import type { SVGProps } from 'react'

/*
 * The artefacts pane's tab glyphs, for the 36px rail the pane leaves when it
 * is folded away: Lucide outlines on the 24 grid at stroke 1.6, drawn at
 * 16px, `currentColor`, `aria-hidden` — the button they sit in carries the
 * word. Written out for the same reason the sidebar's are (`shell/icons.tsx`):
 * four outlines are not worth a dependency in an app that has to work
 * offline.
 */

function Icon(props: SVGProps<SVGSVGElement>): JSX.Element {
  return (
    <svg
      xmlns="http://www.w3.org/2000/svg"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.6"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
      focusable="false"
      {...props}
    />
  )
}

/** lucide `file-diff` — Changes. */
export function ChangesIcon(): JSX.Element {
  return (
    <Icon>
      <path d="M15 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V7z" />
      <path d="M9 10h6M12 13V7M9 17h6" />
    </Icon>
  )
}

/** lucide `file-text` — Note. */
export function NoteIcon(): JSX.Element {
  return (
    <Icon>
      <path d="M15 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V7z" />
      <path d="M14 2v4a2 2 0 0 0 2 2h4M10 9H8M16 13H8M16 17H8" />
    </Icon>
  )
}

/** lucide `package` — Bundle. */
export function BundleIcon(): JSX.Element {
  return (
    <Icon>
      <path d="M11 21.73a2 2 0 0 0 2 0l7-4A2 2 0 0 0 21 16V8a2 2 0 0 0-1-1.73l-7-4a2 2 0 0 0-2 0l-7 4A2 2 0 0 0 3 8v8a2 2 0 0 0 1 1.73z" />
      <path d="M12 22V12M3.3 7l8.7 5 8.7-5" />
    </Icon>
  )
}

/** lucide `wrench` — Tools. */
export function ToolsIcon(): JSX.Element {
  return (
    <Icon>
      <path d="M14.7 6.3a1 1 0 0 0 0 1.4l1.6 1.6a1 1 0 0 0 1.4 0l3.77-3.77a6 6 0 0 1-7.94 7.94l-6.91 6.91a2.12 2.12 0 0 1-3-3l6.91-6.91a6 6 0 0 1 7.94-7.94l-3.76 3.76z" />
    </Icon>
  )
}
