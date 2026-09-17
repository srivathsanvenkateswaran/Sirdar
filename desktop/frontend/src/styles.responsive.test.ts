import { readFileSync } from 'node:fs'
import { join, resolve } from 'node:path'
import { describe, expect, it } from 'vitest'

/*
 * The breakpoints, read off the stylesheets.
 *
 * `styles/tokens.css` names four bands — wide ≥ 1440, standard 1200–1439,
 * compact 1024–1199, narrow < 1024 — and every screen writes its own rules at
 * those widths. jsdom lays nothing out, so the one thing a test can check is
 * that the rule exists at the width the brief asks for: the lane floor, the
 * modal's margin, the session pane's clamp. What is checked is the source,
 * as `styles.library.test.ts` does for colours.
 */

const SRC = resolve(process.cwd(), 'src')

function sheet(path: string): string {
  return readFileSync(join(SRC, path), 'utf8')
}

/** The body of every `@media (max-width: <px>px)` block in a sheet, joined. */
function atMost(css: string, px: number): string {
  const out: string[] = []
  const open = new RegExp(`@media \\(max-width: ${px}px\\)\\s*\\{`, 'g')
  let match: RegExpExecArray | null
  while ((match = open.exec(css))) {
    let depth = 1
    let i = match.index + match[0].length
    const start = i
    while (i < css.length && depth > 0) {
      if (css[i] === '{') depth += 1
      else if (css[i] === '}') depth -= 1
      i += 1
    }
    out.push(css.slice(start, i - 1))
  }
  return out.join('\n')
}

/** The declarations of one selector inside a block of CSS. */
function rule(css: string, selector: string): string {
  const escaped = selector.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
  const match = css.match(new RegExp(`(?:^|[\\s,}])${escaped}\\s*\\{([^}]*)\\}`))
  return match ? match[1] : ''
}

/** One declaration's value out of a block of declarations, trimmed. */
function decl(declarations: string, prop: string): string {
  const match = declarations.match(new RegExp(`(?:^|[;{\\s])${prop}\\s*:\\s*([^;]+)`))
  return match ? match[1].trim() : ''
}

/**
 * A length in pixels: `12px` as written, `0.875rem` against the 16px root,
 * and `var(--sd-space-3)` against the tokens the whole app reads. Only what
 * the sheets actually use — no calc, no nesting deeper than one var.
 */
function len(value: string): number {
  const varName = value.match(/^var\(\s*(--[\w-]+)\s*\)$/)
  if (varName) {
    const tokens = rule(sheet('styles/tokens.css'), ':root')
    return len(decl(tokens, varName[1]))
  }
  if (value.endsWith('rem')) return Number.parseFloat(value) * 16
  return Number.parseFloat(value)
}

const BANDS = { standard: 1439, compact: 1199, narrow: 1023 } as const

describe('the breakpoints', () => {
  const tokens = sheet('styles/tokens.css')

  it('are named once, in the tokens file', () => {
    expect(tokens).toMatch(/wide\s+≥ 1440/)
    expect(tokens).toMatch(/standard\s+1200 – 1439/)
    expect(tokens).toMatch(/compact\s+1024 – 1199/)
    expect(tokens).toMatch(/narrow\s+< 1024/)
  })

  it('step the sidebar 240 → 208 and the page inset 40/48 → 32/32 → 24/24', () => {
    expect(rule(tokens, ':root')).toContain('--sd-sidebar-w: 240px')
    expect(rule(tokens, ':root')).toContain('--sd-page-pad-block: 40px')
    expect(rule(tokens, ':root')).toContain('--sd-page-pad-inline: 48px')
    const standard = atMost(tokens, BANDS.standard)
    expect(standard).toContain('--sd-page-pad-block: 32px')
    expect(standard).toContain('--sd-page-pad-inline: 32px')
    const compact = atMost(tokens, BANDS.compact)
    expect(compact).toContain('--sd-sidebar-w: 208px')
    expect(compact).toContain('--sd-page-pad-block: 24px')
    expect(compact).toContain('--sd-page-pad-inline: 24px')
  })

  it('are the only widths any sheet writes rules at, apart from the old 720 and 768', () => {
    const sheets = [
      'styles.css',
      'components/shell/shell.css',
      'components/shell/sidebar.css',
      'components/session/session.css',
      'components/composer/composer.css',
      'screens/board.css',
      'screens/eval.css',
      'screens/library.css',
      'screens/new-session.css',
      'screens/register.css',
      'screens/review.css',
      'screens/settings/settings.css',
      'ui/kanban-column/KanbanColumn.css',
      'ui/run-card/RunCard.css',
      'ui/modal-sheet/ModalSheet.css',
    ]
    // 1099 is the session's own stacking point and 1299 the register band's,
    // both named in the brief; everything else is one of the four bands.
    const allowed = new Set([1439, 1299, 1199, 1099, 1023, 768, 720])
    for (const path of sheets) {
      const widths = [...sheet(path).matchAll(/@media \((?:max|min)-width: (\d+)px\)/g)].map((m) =>
        Number(m[1]),
      )
      for (const width of widths) {
        expect(allowed.has(width), `${path} writes a rule at ${width}px`).toBe(true)
      }
    }
  })
})

describe('the sidebar', () => {
  const css = sheet('components/shell/sidebar.css')

  it('is a 56px rail when collapsed, and the sessions list scrolls before the footer card moves', () => {
    expect(rule(css, ".sd-sidebar[data-collapsed='true']")).toContain('flex-basis: 56px')
    expect(rule(css, '.sd-sidebar__sessions')).toContain('overflow-y: auto')
    expect(rule(css, '.sd-sidebar__sessions')).toContain('min-block-size: 0')
    expect(rule(css, '.sd-sidebar__nav')).toContain('flex: 0 0 auto')
  })

  it('keeps each quota chip on one line', () => {
    const chip = sheet('ui/quota-chip/QuotaChip.css')
    expect(rule(chip, '.sd-quota')).toContain('white-space: nowrap')
    expect(rule(chip, '.sd-quota')).toContain('overflow: hidden')
    // One line tall: a countdown with no room wraps onto a second line that
    // is never drawn, so the chip reads "57%" rather than "57% · …".
    expect(rule(chip, '.sd-quota')).toContain('flex-wrap: wrap')
    expect(rule(chip, '.sd-quota')).toContain(
      'max-block-size: calc(var(--sd-text-micro) * 1.5 + 6px)',
    )
    expect(rule(chip, '.sd-quota')).toContain('align-content: flex-start')
    // The countdown is all-or-nothing, so it neither shrinks nor ellipsises.
    expect(rule(chip, '.sd-quota > .sd-quota__reset')).toContain('flex: 0 0 auto')
    expect(rule(chip, '.sd-quota__reset')).not.toContain('text-overflow')
    // The bar is what gives way first; the words and the percentage never do,
    // and nothing inside the group can wrap away.
    expect(rule(chip, '.sd-quota__line > .sd-quota__bar')).toContain('min-inline-size: 24px')
    expect(rule(chip, '.sd-quota__line')).toContain('overflow: hidden')
    expect(rule(css, '.quota-meter__provider')).toContain('flex-direction: column')
  })
})

describe('the board', () => {
  const board = sheet('screens/board.css')
  const lane = sheet('ui/kanban-column/KanbanColumn.css')
  const card = sheet('ui/run-card/RunCard.css')

  it('lets six lanes share the row down to 160 each, which is what fits at 1440', () => {
    expect(rule(lane, '.sd-lane')).toContain('flex: 1 1 0')
    expect(rule(lane, '.sd-lane')).toContain('min-inline-size: 160px')
    expect(rule(board, '.board-lanes')).toContain('overflow-x: auto')
  })

  it('reads its inset from the page-pad tokens', () => {
    expect(rule(board, '.board')).toContain('padding-block: var(--sd-page-pad-block)')
    expect(rule(board, '.board')).toContain('padding-inline: var(--sd-page-pad-inline)')
  })

  it('steps the lane head and the card text down one notch at compact', () => {
    expect(rule(atMost(lane, BANDS.compact), '.sd-lane__head')).toContain('font-size')
    const compact = atMost(card, BANDS.compact)
    expect(rule(compact, '.sd-run-card__title')).toContain('var(--sd-text-meta)')
    expect(rule(compact, '.sd-run-card__key')).toContain('var(--sd-text-micro)')
  })

  it('never wraps the card foot; the state word is what ellipsises', () => {
    expect(rule(card, '.sd-run-card__foot')).toContain('flex-wrap: nowrap')
    expect(rule(card, '.sd-run-card__state .sd-state__word')).toContain('text-overflow: ellipsis')
    expect(rule(card, '.sd-run-card__who')).toContain('flex-shrink: 0')
  })

  it('lays the deliveries out 3 → 2 → 1', () => {
    expect(rule(board, '.board-landed__items')).toContain('repeat(3, minmax(0, 1fr))')
    expect(rule(atMost(board, BANDS.standard), '.board-landed__items')).toContain(
      'repeat(2, minmax(0, 1fr))',
    )
    expect(rule(atMost(board, BANDS.narrow), '.board-landed__items')).toContain(
      'grid-template-columns: 1fr',
    )
  })
})

describe('the settings modal', () => {
  const modal = sheet('ui/modal-sheet/ModalSheet.css')
  const settings = sheet('screens/settings/settings.css')

  it('fits the window with a 24px margin and scrolls inside the panel', () => {
    expect(rule(modal, '.sd-modal')).toContain('inline-size: min(1160px, calc(100vw - 48px))')
    expect(rule(modal, '.sd-modal')).toContain('block-size: min(820px, calc(100vh - 48px))')
    expect(rule(modal, '.sd-modal__body')).toContain('overflow-y: auto')
    expect(rule(modal, '.sd-modal__panel')).toContain('min-block-size: 0')
  })

  it('narrows the nav column 260 → 220 at compact', () => {
    expect(rule(modal, '.sd-modal__nav')).toContain('flex: 0 0 260px')
    expect(rule(atMost(modal, BANDS.compact), '.sd-modal__nav')).toContain('flex-basis: 220px')
  })

  it('drops the Installed table’s Driven by column below 1200', () => {
    expect(rule(atMost(settings, BANDS.compact), '.settings-table__driven')).toContain(
      'display: none',
    )
  })
})

describe('the session', () => {
  const css = sheet('components/session/session.css')

  it('is a 432px path beside the document at wide, 400 at standard and 360 at compact', () => {
    expect(rule(css, '.sn__win')).toContain('grid-template-columns: 432px minmax(0, 1fr)')
    expect(rule(atMost(css, BANDS.standard), '.sn__win')).toContain('grid-template-columns: 400px minmax(0, 1fr)')
    expect(rule(atMost(css, BANDS.compact), '.sn__win')).toContain('grid-template-columns: 360px minmax(0, 1fr)')
  })

  it('puts the path under the document at 40vh below 1024, and stacks the complaint', () => {
    const narrow = atMost(css, BANDS.narrow)
    expect(rule(narrow, '.sn__win')).toContain('grid-template-columns: minmax(0, 1fr)')
    expect(rule(narrow, '.sn__win')).toContain('grid-template-rows: minmax(0, 1fr) 40vh')
    expect(rule(narrow, '.sn-complaint')).toContain('grid-template-columns: minmax(0, 1fr)')
  })

  it('keeps the strip its own height and the drawer no wider than its column', () => {
    expect(rule(css, '.sn-strip')).toContain('flex: 0 0 auto')
    const drawer = sheet('ui/drawer/Drawer.css')
    expect(rule(drawer, '.sd-drawer')).toContain('max-inline-size: 100%')
    const composer = sheet('components/composer/composer.css')
    expect(rule(composer, '.composer')).toContain('flex: 0 0 auto')
  })
})

/*
 * The document column, in all three session layouts: `min(100%, 960px)`,
 * centred in its region when there is spare width and the region itself
 * below that, with the composer on the same width and left edge — never a
 * fixed 640 with the rest of the sheet empty. The inset moves to the
 * region so the centring has one box to happen in.
 */
describe('the session column', () => {
  const COLUMN = ['inline-size: min(100%, 960px)', 'margin-inline: auto'] as const

  it('is the Conversation flow and its composer, inside the stream inset', () => {
    const css = sheet('screens/session/session-conversation.css')
    for (const d of COLUMN) {
      expect(rule(css, '.sc-flow')).toContain(d)
      expect(rule(css, '.sc-composer > .session-composer')).toContain(d)
    }
    expect(rule(css, '.sc-flow')).not.toContain('640px')
    expect(rule(css, '.sc-stream')).toContain('padding-inline: var(--sd-space-6) 40px')
    expect(rule(css, '.sc-composer')).toContain('padding-inline: var(--sd-space-6) 40px')
    expect(rule(css, '.sc-composer')).not.toContain('margin-inline')
    expect(rule(atMost(css, BANDS.compact), '.sc-composer')).toContain('padding-inline: var(--sd-space-5)')
    // The inspector keeps its clamp; only the flow beside it changed.
    expect(rule(css, '.sc-body')).toContain('grid-template-columns: minmax(0, 1fr) clamp(360px, 34vw, 500px)')
    expect(rule(css, ".sc-body[data-pane='collapsed']")).toContain('grid-template-columns: minmax(0, 1fr) 36px')
  })

  it('is the Document note and everything in its strip, inside the scroller inset', () => {
    const css = sheet('components/session/session.css')
    for (const d of COLUMN) {
      expect(rule(css, '.sn-note')).toContain(d)
      expect(rule(css, '.sn-strip > *')).toContain(d)
    }
    expect(rule(css, '.sn-note')).not.toContain('max-inline-size')
    expect(rule(css, '.sn-doc__scroll')).toContain('padding-inline: 44px 40px')
    expect(rule(css, '.sn-strip')).toContain('padding-inline: 44px 40px')
    expect(rule(atMost(css, BANDS.standard), '.sn-doc__scroll')).toContain('padding-inline: 32px')
    expect(rule(atMost(css, BANDS.standard), '.sn-strip')).toContain('padding-inline: 32px')
  })

  it('is the Workbench page column inside its scroller', () => {
    const css = sheet('screens/session/session-workbench.css')
    for (const d of COLUMN) expect(rule(css, '.wb-doccol')).toContain(d)
    expect(rule(css, '.wb-docbody')).toContain('overflow: auto')
  })

  it('has no layout capping its column at the old 640', () => {
    for (const path of ['screens/session/session-conversation.css', 'components/session/session.css', 'screens/session/session-workbench.css']) {
      expect(sheet(path), path).not.toMatch(/max-inline-size:\s*640px/)
    }
  })
})

describe('the review', () => {
  const css = sheet('screens/review.css')

  it('is rail 280 + diff + pane 360 at wide, 240 + diff + 320 at standard', () => {
    expect(rule(css, '.review-body')).toContain('280px minmax(0, 1fr) 360px')
    expect(rule(atMost(css, BANDS.standard), '.review-body')).toContain(
      '240px minmax(0, 1fr) 320px',
    )
  })

  it('drops the pane under the diff at compact and goes to one column below 1024', () => {
    const compact = atMost(css, BANDS.compact)
    expect(rule(compact, '.review-body')).toContain('grid-template-columns: 240px minmax(0, 1fr)')
    expect(rule(compact, '.review-rail')).toContain('grid-row: 1 / -1')
    expect(rule(atMost(css, BANDS.narrow), '.review-body')).toContain(
      'grid-template-columns: minmax(0, 1fr)',
    )
    expect(rule(css, '.review-pick__select')).toContain('inline-size: 100%')
  })
})

describe('the register', () => {
  const css = sheet('screens/register.css')

  it('puts the stat strip beside the heatmap at 1300 and above, and over it below', () => {
    expect(rule(css, '.register-band')).toContain('display: flex')
    expect(rule(css, '.register-strip')).toContain('display: flex')
    const below = atMost(css, 1299)
    expect(rule(below, '.register-band')).toContain('flex-direction: column')
    // The strip stays one row of two until the phone width.
    expect(rule(atMost(css, 720), '.register-strip')).toContain('flex-direction: column')
  })

  it('draws the grid compact, and lets the table take the rest of the sheet', () => {
    const heat = sheet('ui/heatmap/Heatmap.css')
    expect(rule(heat, ".sd-heatmap[data-size='compact'] .sd-heatmap__grid")).toContain(
      'grid-template-rows: repeat(7, 10px)',
    )
    expect(rule(heat, ".sd-heatmap[data-size='compact'] .sd-heatmap__grid")).toContain('gap: 2px')
    expect(rule(css, '.register-table')).toContain('flex: 1 1 auto')
    expect(rule(css, '.register-table .sd-table__scroll')).toContain('overflow-y: auto')
    expect(rule(css, '.register')).toContain('overflow-y: auto')
  })

  /*
   * The band's height, added up from the boxes the sheets declare.
   *
   * jsdom lays nothing out, so the sum is done here — the arithmetic a
   * browser does, over the numbers the stylesheets carry. At the 2026-09-17
   * density Brave at 1440x900 measures the strip at 65px and the grid card at
   * 142px against the 63.6 and 142 below, and leaves the table 524px: its
   * 36px caption, its 41px header and twelve whole 36px rows with a
   * thirteenth part way up.
   */
  it('adds up to a strip of about 64px and a grid card of about 142px', () => {
    const stat = sheet('ui/stat-card/StatCard.css')
    const heat = sheet('ui/heatmap/Heatmap.css')
    const compactStat = rule(stat, ".sd-stat[data-size='compact']")

    // Figure and label on one baseline (the 20px figure is the taller box,
    // at line-height 1), the detail line under them, 12px padding each side.
    const strip =
      2 * len(decl(compactStat, 'padding-block')) +
      len(decl(rule(stat, ".sd-stat[data-size='compact'] .sd-stat__value"), 'font-size')) +
      len(decl(compactStat, 'row-gap')) +
      len(decl(rule(stat, '.sd-stat__detail'), 'font-size')) *
        Number(decl(rule(stat, '.sd-stat__detail'), 'line-height'))
    expect(strip).toBeCloseTo(63.6, 1)
    expect(strip).toBeGreaterThanOrEqual(56)
    expect(strip).toBeLessThanOrEqual(80)

    // The month row, the grid's seven 10px rows and their 2px gaps.
    const grid =
      len(decl(rule(heat, ".sd-heatmap[data-size='compact'] .sd-heatmap__months"), 'block-size')) +
      len(decl(rule(heat, ".sd-heatmap[data-size='compact'] .sd-heatmap__scroll"), 'gap')) +
      7 * 10 +
      6 * len(decl(rule(heat, ".sd-heatmap[data-size='compact'] .sd-heatmap__grid"), 'gap'))
    expect(grid).toBe(98)

    // The card: its padding, the taller of the title and the legend on the
    // head row (the legend's micro text on the body's 1.5 leading), the gap
    // under it, and the grid.
    const title =
      len(decl(rule(css, '.register-heatcard__title'), 'font-size')) *
      Number(decl(rule(css, '.register-heatcard__title'), 'line-height'))
    const legend =
      len(decl(rule(heat, ".sd-heatmap__legend[data-size='compact']"), 'font-size')) * 1.5
    const card =
      2 * len(decl(rule(css, '.register-heatcard'), 'padding-block')) +
      Math.max(title, legend) +
      len(decl(rule(css, '.register-heatcard__head'), 'margin-block-end')) +
      grid
    expect(card).toBe(142.5)
  })

  it('scrolls the heatmap inside its card', () => {
    expect(rule(sheet('ui/heatmap/Heatmap.css'), '.sd-heatmap__scroll')).toContain(
      'overflow-x: auto',
    )
    expect(rule(css, '.register-heatcard')).toContain('min-inline-size: 0')
  })

  it('hides Confidence, Verdict and Notes below 1200', () => {
    const compact = atMost(css, BANDS.compact)
    for (const column of ['confidence', 'verdict', 'notes']) {
      expect(compact).toContain(`[data-col='${column}']`)
    }
    expect(compact).toContain('display: none')
  })
})

describe('the eval', () => {
  const css = sheet('screens/eval.css')

  it('is two columns at 1200 and above, one below, with the head actions under the title', () => {
    expect(rule(css, '.eval-body')).toContain('display: flex')
    const compact = atMost(css, BANDS.compact)
    expect(rule(compact, '.eval-body')).toContain('flex-direction: column')
    expect(rule(compact, '.eval-head .sd-page-head')).toContain('flex-direction: column')
  })
})

describe('new session', () => {
  const css = sheet('screens/new-session.css')

  it('is a column of min(880px, 100% - 48px)', () => {
    expect(rule(css, '.new-session__col')).toContain('inline-size: min(880px, calc(100% - 48px))')
  })

  // One row, never two: a chip too wide for the bar truncates its own
  // value rather than dropping onto a second line.
  it('holds the composer bar to one row of chips, with the button still at the end', () => {
    const composer = sheet('components/composer/composer.css')
    expect(rule(composer, '.composer-bar__chips')).toContain('flex-wrap: nowrap')
    expect(rule(composer, '.composer-chip')).toContain('min-inline-size: 0')
    expect(rule(composer, '.composer-chip__value')).toContain('text-overflow: ellipsis')
    expect(rule(composer, '.composer-send')).toContain('margin-inline-start: auto')
  })
})

describe('the library', () => {
  it('stacks the name above the specimens below 1200', () => {
    const css = sheet('screens/library.css')
    expect(rule(css, '.lib-section')).toContain('260px minmax(0, 1fr)')
    expect(rule(atMost(css, BANDS.compact), '.lib-section')).toContain(
      'grid-template-columns: minmax(0, 1fr)',
    )
  })
})
