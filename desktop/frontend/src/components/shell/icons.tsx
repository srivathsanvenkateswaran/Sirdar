import type { SVGProps } from 'react'

/*
 * The five sidebar icons, drawn as Lucide draws them: a 24-unit box scaled to
 * 16, stroke 1.5, round caps and joins, `currentColor`.
 *
 * They are written out rather than pulled from a package because the desktop
 * app has to work offline and five outlines are not worth a dependency, a
 * bundle and a tree-shaking config. Each one is `aria-hidden`: the row it sits
 * in carries the word.
 */

function Icon(props: SVGProps<SVGSVGElement>): JSX.Element {
  return (
    <svg
      xmlns="http://www.w3.org/2000/svg"
      width="16"
      height="16"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.5"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
      focusable="false"
      {...props}
    />
  )
}

/** lucide `columns-3` — the board. */
export function BoardIcon(): JSX.Element {
  return (
    <Icon>
      <rect width="18" height="18" x="3" y="3" rx="2" />
      <path d="M9 3v18M15 3v18" />
    </Icon>
  )
}

/** lucide `rows-3` — the register. */
export function RegisterIcon(): JSX.Element {
  return (
    <Icon>
      <rect width="18" height="18" x="3" y="3" rx="2" />
      <path d="M3 9h18M3 15h18" />
    </Icon>
  )
}

/** lucide `flask-conical` — the eval suite. */
export function EvalIcon(): JSX.Element {
  return (
    <Icon>
      <path d="M10 2v7.5a2 2 0 0 1-.3 1L4 20a1 1 0 0 0 .9 1.5h14.2A1 1 0 0 0 20 20l-5.7-9.5a2 2 0 0 1-.3-1V2" />
      <path d="M8.5 2h7M7 16h10" />
    </Icon>
  )
}

/** lucide `book-marked` — the design library. */
export function LibraryIcon(): JSX.Element {
  return (
    <Icon>
      <path d="M4 19.5v-15A2.5 2.5 0 0 1 6.5 2H20v20H6.5a2.5 2.5 0 0 1 0-5H20" />
      <path d="M10 2v8l3-2 3 2V2" />
    </Icon>
  )
}

/** lucide `sliders-horizontal` — settings. */
export function SettingsIcon(): JSX.Element {
  return (
    <Icon>
      <path d="M21 4h-8M8 4H3M21 12h-4M12 12H3M21 20h-9M7 20H3" />
      <path d="M8 2v4M17 10v4M12 18v4" />
    </Icon>
  )
}

/** lucide `chevrons-up-down` — the workspace switcher. */
export function SwitcherIcon(): JSX.Element {
  return (
    <Icon width="14" height="14">
      <path d="m7 15 5 5 5-5M7 9l5-5 5 5" />
    </Icon>
  )
}
