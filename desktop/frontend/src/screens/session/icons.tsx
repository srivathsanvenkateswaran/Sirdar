import type { SVGProps } from 'react'

/*
 * The conversation layout's glyphs: Lucide outlines on the 24 grid at stroke
 * 1.7, `currentColor`, `aria-hidden` — the text beside each carries the
 * word. Sized by the stylesheet through the `sc-i` class.
 */

function Icon(props: SVGProps<SVGSVGElement>): JSX.Element {
  return (
    <svg
      className="sc-i"
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

/** lucide `terminal` — a shell command. */
export function TerminalIcon(): JSX.Element {
  return (
    <Icon>
      <path d="m5 7 5 5-5 5M12 19h7" />
    </Icon>
  )
}

/** lucide `file-text` — a file read. */
export function FileIcon(): JSX.Element {
  return (
    <Icon>
      <path d="M15 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V7z" />
      <path d="M14 2v4a2 2 0 0 0 2 2h4M10 13H8M16 17H8" />
    </Icon>
  )
}

/** lucide `pencil` — an edit. */
export function PencilIcon(): JSX.Element {
  return (
    <Icon>
      <path d="M21.2 6.4a2 2 0 0 0 0-2.8l-.8-.8a2 2 0 0 0-2.8 0L4 16.4V20h3.6z" />
      <path d="m15 5 4 4" />
    </Icon>
  )
}

/** lucide `search` — a grep or a search tool. */
export function SearchIcon(): JSX.Element {
  return (
    <Icon>
      <circle cx="11" cy="11" r="7" />
      <path d="m20 20-3.5-3.5" />
    </Icon>
  )
}

/** lucide `plug` — an MCP tool or anything else. */
export function ToolIcon(): JSX.Element {
  return (
    <Icon>
      <path d="M14.7 6.3a1 1 0 0 0 0 1.4l1.6 1.6a1 1 0 0 0 1.4 0l3.77-3.77a6 6 0 0 1-7.94 7.94l-6.91 6.91a2.12 2.12 0 0 1-3-3l6.91-6.91a6 6 0 0 1 7.94-7.94l-3.76 3.76z" />
    </Icon>
  )
}

/** lucide `chevron-right`; rotated by the stylesheet when open. */
export function ChevronIcon(): JSX.Element {
  return (
    <Icon className="sc-i sc-chev">
      <path d="m9 6 6 6-6 6" />
    </Icon>
  )
}

/** lucide `lightbulb` — a thinking stamp. */
export function ThinkIcon(): JSX.Element {
  return (
    <Icon>
      <path d="M12 3a5 5 0 0 0-5 5c0 1.6.7 2.9 1.7 3.9.6.6.8 1.3.8 2.1h5c0-.8.2-1.5.8-2.1A5.3 5.3 0 0 0 17 8a5 5 0 0 0-5-5ZM10 18h4M11 21h2" />
    </Icon>
  )
}

/** lucide `braces` — the structured answer. */
export function BracesIcon(): JSX.Element {
  return (
    <Icon>
      <path d="M8 3H7a2 2 0 0 0-2 2v5a2 2 0 0 1-2 2 2 2 0 0 1 2 2v5a2 2 0 0 0 2 2h1M16 3h1a2 2 0 0 1 2 2v5a2 2 0 0 0 2 2 2 2 0 0 0-2 2v5a2 2 0 0 1-2 2h-1" />
    </Icon>
  )
}

/** lucide `file` with lines — the note. */
export function NoteIcon(): JSX.Element {
  return (
    <Icon>
      <path d="M8 3h8l4 4v14H4V3h4" />
      <path d="M8 12h8M8 16h5" />
    </Icon>
  )
}

/** lucide `alert-circle` — the agent's question. */
export function AskIcon(): JSX.Element {
  return (
    <Icon>
      <circle cx="12" cy="12" r="9" />
      <path d="M12 8v4M12 16h.01" />
    </Icon>
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

/** lucide `check`. */
export function CheckIcon(): JSX.Element {
  return (
    <Icon>
      <path d="m5 12 5 5 9-10" />
    </Icon>
  )
}

/** lucide `arrow-up-down` — a sortable column. */
export function SortIcon(): JSX.Element {
  return (
    <Icon>
      <path d="m7 15 5 5 5-5M7 9l5-5 5 5" />
    </Icon>
  )
}

/** The glyph a tool card leads with, by tool. */
export function toolIcon(tool: string): JSX.Element {
  if (/^(?:bash|shell|commandexecution|command_execution)$/i.test(tool)) return <TerminalIcon />
  if (/^(?:read|read_file|view_file|cat)$/i.test(tool)) return <FileIcon />
  if (/^(?:edit|multiedit|write|notebookedit|replace|edit_file|apply_patch|filechange)$/i.test(tool)) return <PencilIcon />
  if (/^(?:grep|glob|rg|search|websearch|codebase_search)$/i.test(tool)) return <SearchIcon />
  return <ToolIcon />
}
