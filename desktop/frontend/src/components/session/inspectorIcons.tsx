import type { SVGProps } from 'react'
import type { PreviewKind } from './bundleModel'

/*
 * The inspector's glyphs, shared by all three layouts: Lucide outlines on
 * the 24 grid at stroke 1.7, `currentColor`, `aria-hidden` — every one has
 * a word beside it. Sized by the stylesheet through `si-i`.
 */

function Icon(props: SVGProps<SVGSVGElement>): JSX.Element {
  return (
    <svg
      className="si-i"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.7"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
      focusable="false"
      {...props}
    />
  )
}

/** lucide `external-link`. */
export function ExternalIcon(): JSX.Element {
  return (
    <Icon>
      <path d="M14 4h6v6M20 4l-9 9M18 14v5a1 1 0 0 1-1 1H5a1 1 0 0 1-1-1V7a1 1 0 0 1 1-1h5" />
    </Icon>
  )
}

/** lucide `chevron-right`; the stylesheet turns it when its control is open. */
export function ChevronIcon(): JSX.Element {
  return (
    <Icon className="si-i si-chev">
      <path d="m9 6 6 6-6 6" />
    </Icon>
  )
}

/** lucide `arrow-down-up` — the sort item in a pane's menu. */
export function SortIcon(): JSX.Element {
  return (
    <Icon>
      <path d="m7 15 5 5 5-5M7 9l5-5 5 5" />
    </Icon>
  )
}

/** lucide `more-horizontal` — the pane's own menu. */
export function MoreIcon(): JSX.Element {
  return (
    <Icon>
      <circle cx="5" cy="12" r="1" />
      <circle cx="12" cy="12" r="1" />
      <circle cx="19" cy="12" r="1" />
    </Icon>
  )
}

/** lucide `image`. */
function ImageIcon(): JSX.Element {
  return (
    <Icon>
      <rect x="3" y="4" width="18" height="16" rx="2" />
      <circle cx="9" cy="10" r="1.6" />
      <path d="m4 18 5-5 4 4 3-2 4 3" />
    </Icon>
  )
}

/** lucide `file-text`. */
function TextIcon(): JSX.Element {
  return (
    <Icon>
      <path d="M15 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V7z" />
      <path d="M14 2v4a2 2 0 0 0 2 2h4M10 13H8M16 17H8" />
    </Icon>
  )
}

/** lucide `audio-lines` — a voice note. */
function AudioIcon(): JSX.Element {
  return (
    <Icon>
      <path d="M3 12h1M7 7v10M11 4v16M15 8v8M19 11h2" />
    </Icon>
  )
}

/** lucide `video`. */
function VideoIcon(): JSX.Element {
  return (
    <Icon>
      <rect x="3" y="6" width="13" height="12" rx="2" />
      <path d="m16 11 5-3v8l-5-3z" />
    </Icon>
  )
}

/** lucide `paperclip` — a file of a kind nothing here can draw. */
function ClipIcon(): JSX.Element {
  return (
    <Icon>
      <path d="M20 10.5 11.3 19a5 5 0 0 1-7-7l8.5-8.6a3.3 3.3 0 0 1 4.7 4.7L9 16.6a1.7 1.7 0 0 1-2.4-2.4l8-8" />
    </Icon>
  )
}

/** The glyph an attachment row leads with. */
export function FileGlyph({ kind }: { kind: PreviewKind }): JSX.Element {
  switch (kind) {
    case 'image':
      return <ImageIcon />
    case 'audio':
      return <AudioIcon />
    case 'video':
      return <VideoIcon />
    case 'pdf':
    case 'text':
      return <TextIcon />
    default:
      return <ClipIcon />
  }
}
