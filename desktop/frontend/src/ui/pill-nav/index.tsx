import './PillNav.css'

export interface PillNavItem {
  id: string
  label: string
  /** A nav that navigates by URL passes one; the app's nav does not. */
  href?: string
  /** A count beside the label, in the ledger face. Zero is still shown. */
  count?: number
}

export interface PillNavProps {
  items: PillNavItem[]
  /** The item the reader is on. Nothing is current when this names no item. */
  current?: string
  onSelect?: (id: string) => void
  /** Names the nav for a screen reader; two navs on a page need two names. */
  label: string
  /** The landing page's bar floats over whatever band it is on; the app's does not. */
  floating?: boolean
}

/**
 * The row of places you can go.
 *
 * It is a pill row on a surface-coloured bar rather than a set of tabs with an
 * underline, and the current item is a soft accent fill rather than a border:
 * a border under the current item moves the text of every other item by a
 * pixel when the bar reflows, and on a landing page that crosses three colour
 * bands there is no single line for it to sit on. The floating variant is what
 * carries the page through paper, band and paper again as one document.
 */
export default function PillNav({
  items,
  current,
  onSelect,
  label,
  floating = false,
}: PillNavProps): JSX.Element {
  return (
    <nav className="sd-pill-nav" data-floating={floating ? 'true' : undefined} aria-label={label}>
      <ul className="sd-pill-nav__list">
        {items.map((item) => {
          const isCurrent = item.id === current
          const content = (
            <>
              <span>{item.label}</span>
              {item.count !== undefined && (
                <span className="sd-pill-nav__count">{item.count}</span>
              )}
            </>
          )
          return (
            <li key={item.id} className="sd-pill-nav__slot">
              {item.href ? (
                <a
                  className="sd-pill-nav__item"
                  href={item.href}
                  aria-current={isCurrent ? 'page' : undefined}
                  onClick={() => onSelect?.(item.id)}
                >
                  {content}
                </a>
              ) : (
                <button
                  type="button"
                  className="sd-pill-nav__item"
                  aria-current={isCurrent ? 'page' : undefined}
                  onClick={() => onSelect?.(item.id)}
                >
                  {content}
                </button>
              )}
            </li>
          )
        })}
      </ul>
    </nav>
  )
}
