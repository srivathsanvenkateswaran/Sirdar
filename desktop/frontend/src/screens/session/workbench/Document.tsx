import { useCallback, useEffect, useRef, useState, type JSX, type ReactNode } from 'react'

/** One line of the outline rail: a section of the document, or a sub-entry under one. */
export interface OutlineItem {
  id: string
  title: string
  /** The count or word at the right: `9`, `2 files`, `ar · en`. */
  n?: string
  sub?: boolean
  /** `dropped` strikes the entry through — a hunk the reviewer removed. */
  tone?: 'dropped'
}

/**
 * The document area: an outline rail of the document's sections beside the
 * page itself. The page is the scroll container; the outline follows it,
 * marking the section at the top, and clicking an entry scrolls to it.
 *
 * A document is a tree of `<section data-sec="id">`; that attribute is all
 * the rail needs to find them, so every document (answer, note, diff,
 * bundle) renders in here without knowing about the rail. Inside the
 * scroller the page sits in one column, 960 wide at most and centred when
 * the area is wider — the rule every layout's document column follows.
 */
export default function Document({
  outline,
  children,
  label,
  padTop,
}: {
  outline: OutlineItem[]
  children: ReactNode
  /** Names the page for a screen reader. */
  label: string
  /** The diff and bundle pages sit a little higher than the answer's title. */
  padTop?: 'tight'
}): JSX.Element {
  const body = useRef<HTMLDivElement | null>(null)
  const [active, setActive] = useState<string>(outline[0]?.id ?? '')
  const pinned = useRef<string | null>(null)

  const sectionsOf = () =>
    body.current ? [...body.current.querySelectorAll<HTMLElement>('[data-sec]')] : []

  const follow = useCallback(() => {
    const el = body.current
    if (!el) return
    if (pinned.current) return
    const top = el.scrollTop + 12
    let found = ''
    for (const s of sectionsOf()) {
      if (s.offsetTop - el.offsetTop <= top) found = s.dataset.sec ?? ''
    }
    if (found) setActive(found)
  }, [])

  // A new document starts at its first section.
  useEffect(() => {
    setActive(outline[0]?.id ?? '')
    pinned.current = null
    if (body.current) body.current.scrollTop = 0
  }, [outline])

  const pick = (id: string) => {
    setActive(id)
    const target = sectionsOf().find((s) => s.dataset.sec === id)
    if (!target || !body.current) return
    // The choice holds until the reader scrolls on their own.
    pinned.current = id
    target.scrollIntoView?.({ block: 'start' })
    setTimeout(() => {
      pinned.current = null
    }, 300)
  }

  return (
    <div className="wb-doc">
      <nav className="wb-outline" aria-label="Sections">
        {outline.map((item) => (
          <button
            key={item.id}
            type="button"
            className="wb-ol"
            data-sub={item.sub ? 'true' : undefined}
            data-on={item.id === active ? 'true' : undefined}
            data-tone={item.tone}
            aria-current={item.id === active ? 'true' : undefined}
            onClick={() => pick(item.id)}
          >
            <span className="wb-ol__t">{item.title}</span>
            {item.n ? <span className="wb-ol__n">{item.n}</span> : null}
          </button>
        ))}
      </nav>
      <div
        className="wb-docbody"
        data-pad={padTop}
        ref={body}
        onScroll={follow}
        role="region"
        aria-label={label}
        tabIndex={0}
      >
        <div className="wb-doccol">{children}</div>
      </div>
    </div>
  )
}
