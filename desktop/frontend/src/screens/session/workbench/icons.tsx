import type { JSX } from 'react'

/** Inline stroke icons on the 24 grid, lucide's shapes, 1.6 stroke as the mocks draw them. */
function Icon({ children, ...rest }: { children: React.ReactNode } & React.SVGProps<SVGSVGElement>): JSX.Element {
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
      {...rest}
    >
      {children}
    </svg>
  )
}

export const SearchIcon = (): JSX.Element => (
  <Icon>
    <circle cx="11" cy="11" r="7" />
    <path d="m20 20-3.5-3.5" />
  </Icon>
)
export const ChevronRightIcon = (): JSX.Element => (
  <Icon>
    <path d="m9 18 6-6-6-6" />
  </Icon>
)
export const ChevronDownIcon = (): JSX.Element => (
  <Icon>
    <path d="m6 9 6 6 6-6" />
  </Icon>
)
export const MaximiseIcon = (): JSX.Element => (
  <Icon>
    <path d="M8 3H5a2 2 0 0 0-2 2v3M21 8V5a2 2 0 0 0-2-2h-3M3 16v3a2 2 0 0 0 2 2h3M16 21h3a2 2 0 0 0 2-2v-3" />
  </Icon>
)
export const CollapseIcon = (): JSX.Element => (
  <Icon>
    <rect x="3" y="3" width="18" height="18" rx="2" />
    <path d="M3 15h18" />
  </Icon>
)
export const CheckIcon = (): JSX.Element => (
  <Icon>
    <path d="m5 12 5 5L20 7" />
  </Icon>
)
export const XIcon = (): JSX.Element => (
  <Icon>
    <path d="M18 6 6 18M6 6l12 12" />
  </Icon>
)
export const BranchIcon = (): JSX.Element => (
  <Icon>
    <circle cx="6" cy="6" r="2.5" />
    <circle cx="6" cy="18" r="2.5" />
    <circle cx="18" cy="8" r="2.5" />
    <path d="M6 8.5v7M18 10.5c0 3-3 4-6 4.5-2 .4-4 1-6 2.5" />
  </Icon>
)
export const LockIcon = (): JSX.Element => (
  <Icon>
    <rect x="4" y="11" width="16" height="10" rx="2" />
    <path d="M8 11V7a4 4 0 0 1 8 0v4" />
  </Icon>
)
export const PencilIcon = (): JSX.Element => (
  <Icon>
    <path d="M12 20h9" />
    <path d="M16.5 3.5a2.1 2.1 0 0 1 3 3L7 19l-4 1 1-4Z" />
  </Icon>
)
export const PersonIcon = (): JSX.Element => (
  <Icon>
    <circle cx="12" cy="8" r="4" />
    <path d="M4 21a8 8 0 0 1 16 0" />
  </Icon>
)
export const QuestionIcon = (): JSX.Element => (
  <Icon>
    <circle cx="12" cy="12" r="9" />
    <path d="M9.5 9.5a2.5 2.5 0 1 1 3.5 2.3c-.7.4-1 .9-1 1.7" />
    <path d="M12 17h.01" />
  </Icon>
)
export const CopyIcon = (): JSX.Element => (
  <Icon>
    <rect x="9" y="9" width="11" height="11" rx="2" />
    <path d="M5 15V5a2 2 0 0 1 2-2h10" />
  </Icon>
)
export const WrapIcon = (): JSX.Element => (
  <Icon>
    <path d="M3 6h18M3 12h13a3 3 0 0 1 0 6h-3M3 18h6" />
    <path d="m15 15-2 3 2 3" />
  </Icon>
)
