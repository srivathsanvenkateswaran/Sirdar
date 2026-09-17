import type { ReactNode } from 'react'

/** Inline stroke icons on the 24 grid, lucide shapes at 1.6 stroke. */
function Icon({ children }: { children: ReactNode }): JSX.Element {
  return (
    <svg
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.6"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
      focusable="false"
    >
      {children}
    </svg>
  )
}

/** lucide `file-text`: the bundle. */
export function BundleIcon(): JSX.Element {
  return (
    <Icon>
      <path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z" />
      <path d="M14 2v6h6M8 13h8M8 17h5" />
    </Icon>
  )
}

/** lucide `terminal`: the tools. */
export function ToolsIcon(): JSX.Element {
  return (
    <Icon>
      <path d="m4 17 6-6-6-6M12 19h8" />
    </Icon>
  )
}

/** lucide `arrow-up-right`: opens elsewhere. */
export function OpenIcon(): JSX.Element {
  return (
    <Icon>
      <path d="M7 17 17 7M8 7h9v9" />
    </Icon>
  )
}

/** lucide `copy`. */
export function CopyIcon(): JSX.Element {
  return (
    <Icon>
      <rect x="9" y="9" width="11" height="11" rx="2" />
      <path d="M5 15V5a2 2 0 0 1 2-2h10" />
    </Icon>
  )
}

/** lucide `info`: what the header keeps behind a click. */
export function InfoIcon(): JSX.Element {
  return (
    <Icon>
      <circle cx="12" cy="12" r="9" />
      <path d="M12 16v-5M12 8h.01" />
    </Icon>
  )
}

/** lucide `message-circle-question`: the agent's question. */
export function QuestionIcon(): JSX.Element {
  return (
    <Icon>
      <path d="M7.9 20A9 9 0 1 0 4 16.1L2 22Z" />
      <path d="M9.09 9a3 3 0 0 1 5.83 1c0 2-3 3-3 3M12 17h.01" />
    </Icon>
  )
}

/** The three layouts, each as a small diagram of its window. */
export function ConversationLayoutIcon(): JSX.Element {
  return (
    <Icon>
      <rect x="3" y="4" width="18" height="16" rx="2" />
      <path d="M15 4v16M6 9h6M6 13h4" />
    </Icon>
  )
}

export function DocumentLayoutIcon(): JSX.Element {
  return (
    <Icon>
      <rect x="3" y="4" width="18" height="16" rx="2" />
      <path d="M9 4v16M13 9h5M13 13h5M13 17h3" />
    </Icon>
  )
}

export function WorkbenchLayoutIcon(): JSX.Element {
  return (
    <Icon>
      <rect x="3" y="4" width="18" height="16" rx="2" />
      <path d="M3 14h18M7 4v10" />
    </Icon>
  )
}
