import type { SVGProps } from 'react'

/*
 * The settings modal's icons, as the 2026-09-15 mocks draw them: a 24-unit
 * box, stroke 1.6, round caps and joins, `currentColor`. The nav row sizes
 * them to 18px and the footer to 16. Each one is `aria-hidden`: the row or
 * the line it sits in carries the word.
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

/** Three sliders — General. */
export function GeneralIcon(): JSX.Element {
  return (
    <Icon>
      <path d="M4 7h9M17 7h3M4 12h3M11 12h9M4 17h11M19 17h1" />
      <circle cx="15" cy="7" r="2" />
      <circle cx="9" cy="12" r="2" />
      <circle cx="17" cy="17" r="2" />
    </Icon>
  )
}

/** A plug — Providers. */
export function ProvidersIcon(): JSX.Element {
  return (
    <Icon>
      <path d="M9 2v5M15 2v5M6 7h12v4a6 6 0 0 1-12 0zM12 17v5" />
    </Icon>
  )
}

/** Two coins — Budgets. */
export function BudgetsIcon(): JSX.Element {
  return (
    <Icon>
      <circle cx="9" cy="9" r="6" />
      <path d="M15.3 7.3a6 6 0 1 1-8 8" />
    </Icon>
  )
}

/** Two server bays — MCP servers. */
export function ServersIcon(): JSX.Element {
  return (
    <Icon>
      <rect x="3" y="4" width="18" height="7" rx="2" />
      <rect x="3" y="13" width="18" height="7" rx="2" />
      <path d="M7 7.5h.01M7 16.5h.01" />
    </Icon>
  )
}

/** A play triangle — Try a tool. */
export function ToolIcon(): JSX.Element {
  return (
    <Icon>
      <path d="M7 4.5v15l12-7.5z" />
    </Icon>
  )
}

/** A shield — Permissions. */
export function PermissionsIcon(): JSX.Element {
  return (
    <Icon>
      <path d="M12 3l8 3v6c0 4.5-3.5 8-8 9-4.5-1-8-4.5-8-9V6z" />
    </Icon>
  )
}

/** A page with lines — Notes. */
export function NotesIcon(): JSX.Element {
  return (
    <Icon>
      <path d="M14 3H7a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V8z" />
      <path d="M14 3v5h5M9 13h6M9 17h6" />
    </Icon>
  )
}

/** A small closed book — Playbooks. */
export function PlaybooksIcon(): JSX.Element {
  return (
    <Icon>
      <path d="M5 4.5A1.5 1.5 0 0 1 6.5 3H19v14H6.5A1.5 1.5 0 0 0 5 18.5z" />
      <path d="M5 18.5A1.5 1.5 0 0 1 6.5 17H19v4H6.5A1.5 1.5 0 0 1 5 19.5zM9 7h6" />
    </Icon>
  )
}

/** A bell — Notifications. */
export function NotificationsIcon(): JSX.Element {
  return (
    <Icon>
      <path d="M6 16v-5a6 6 0 0 1 12 0v5l2 2H4zM10 21h4" />
    </Icon>
  )
}

/** Two links — Webhooks. */
export function WebhooksIcon(): JSX.Element {
  return (
    <Icon>
      <path d="M10 14a4 4 0 0 0 5.7 0l3-3a4 4 0 0 0-5.7-5.7l-1.5 1.5" />
      <path d="M14 10a4 4 0 0 0-5.7 0l-3 3a4 4 0 0 0 5.7 5.7l1.5-1.5" />
    </Icon>
  )
}

/** An open book — Reading. */
export function ReadingIcon(): JSX.Element {
  return (
    <Icon>
      <path d="M2 5h6a4 4 0 0 1 4 4v12a3 3 0 0 0-3-3H2zM22 5h-6a4 4 0 0 0-4 4v12a3 3 0 0 1 3-3h7z" />
    </Icon>
  )
}

/** Stacked layers — Library. */
export function LibraryIcon(): JSX.Element {
  return (
    <Icon>
      <path d="M12 3l9 5-9 5-9-5zM3 13l9 5 9-5" />
    </Icon>
  )
}

/** A circled i — About. */
export function AboutIcon(): JSX.Element {
  return (
    <Icon>
      <circle cx="12" cy="12" r="9" />
      <path d="M12 11v5M12 8h.01" />
    </Icon>
  )
}

/** A cloud with a check — the version line, when the bridge reported one. */
export function CloudCheckIcon(): JSX.Element {
  return (
    <Icon width="16" height="16">
      <path d="M7 18a4.5 4.5 0 0 1-.6-9A6 6 0 0 1 18 8.5 4 4 0 0 1 17.5 18z" />
      <path d="m9 13.5 2 2 4-4" />
    </Icon>
  )
}
