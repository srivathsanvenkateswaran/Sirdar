import { readFileSync } from 'node:fs'
import { join, resolve } from 'node:path'
import { describe, expect, it } from 'vitest'

/*
 * No window scroll, anywhere.
 *
 * The rule is stated in styles.css — `html, body, #root` are the window's
 * height and `overflow: hidden` — and every screen then has to scroll inside
 * the sheet, in one container of its own, with `min-block-size: 0` down the
 * flex chain so the container can shrink to the room it has. jsdom lays
 * nothing out, so `scrollHeight <= clientHeight` cannot be asserted here;
 * what can be read off the source is that the rule exists and that each
 * screen names its scroll container. The screenshot pass in the round's
 * report is the check that the layout engine agrees.
 */

const SRC = resolve(process.cwd(), 'src')

function sheet(path: string): string {
  return readFileSync(join(SRC, path), 'utf8')
}

/** The declarations of one selector, wherever it first appears. */
function rule(css: string, selector: string): string {
  const escaped = selector.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
  const match = css.match(new RegExp(`(?:^|[\\s,}])${escaped}\\s*\\{([^}]*)\\}`))
  return match ? match[1] : ''
}

describe('the window', () => {
  const css = sheet('styles.css')

  it('is a fixed frame: html, body and #root are the window height and never scroll', () => {
    const frame = css.match(/html,\s*body,\s*#root\s*\{([^}]*)\}/)?.[1] ?? ''
    expect(frame).toContain('block-size: 100%')
    expect(frame).toContain('overflow: hidden')
  })

  it('keeps the shell and the sheet to the window', () => {
    const shell = sheet('components/shell/shell.css')
    expect(rule(shell, '.app')).toContain('block-size: 100%')
    expect(rule(shell, '.app')).toContain('min-block-size: 0')
    expect(rule(shell, '.main')).toContain('min-block-size: 0')
    expect(rule(shell, '.main')).toContain('overflow: hidden')
    expect(rule(shell, '.sd-page')).toContain('min-block-size: 0')
  })
})

describe('each screen scrolls inside the sheet', () => {
  it.each([
    ['screens/new-session.css', '.new-session'],
    ['screens/board.css', '.board'],
    ['screens/register.css', '.register'],
    ['screens/eval.css', '.eval'],
    ['screens/library.css', '.lib'],
    ['screens/review.css', '.review-rail'],
    ['screens/review.css', '.review-pane'],
    ['components/run/run.css', '.stream-scroll'],
    // The session's right pane: one container per tab.
    ['components/run/run.css', '.pane'],
    ['components/run/run.css', '.tools'],
    ['components/run/run.css', '.changes-diff'],
  ])('%s: %s is a scroll container that can shrink', (path, selector) => {
    const declarations = rule(sheet(path), selector)
    expect(declarations, `${selector} in ${path}`).toMatch(/overflow(-y)?: auto/)
    expect(declarations, `${selector} in ${path}`).toContain('min-block-size: 0')
  })

  /*
   * The session's right pane lost its scroll when the window became a fixed
   * frame: the Note tab drew the library's article straight into the panel,
   * and nothing between the sheet and the note could shrink. Every flex and
   * grid ancestor from the screen down to the tab's container has to give
   * up its content height, and the grid row itself has to be `minmax(0, 1fr)`
   * — an `auto` row grows with the taller pane instead.
   */
  it('the session panes can shrink all the way down from the sheet', () => {
    const css = sheet('components/run/run.css')
    for (const selector of [
      '.session',
      '.session-body',
      '.session-left',
      '.session-right',
      '.session-panel',
      '.changes',
      '.stream',
    ]) {
      expect(rule(css, selector), selector).toContain('min-block-size: 0')
    }
    expect(rule(css, '.session-body')).toMatch(/grid-template-rows: minmax\(0, 1fr\)/)
    // The note's scroll container adds no padding of its own: the article
    // inside it carries the measure.
    expect(rule(css, '.pane--note')).toContain('padding: 0')
  })

  it('the sidebar scrolls its sessions list, not the window', () => {
    const css = sheet('components/shell/sidebar.css')
    expect(rule(css, '.sd-sidebar')).toContain('min-block-size: 0')
    expect(rule(css, '.sd-sidebar__sessions')).toContain('overflow-y: auto')
    expect(rule(css, '.sd-sidebar__sessions')).toContain('min-block-size: 0')
    expect(rule(css, '.sd-sidebar__sessions')).toContain('flex: 1 1 auto')
  })
})

describe('what floats over the sheet', () => {
  it('the page enter does not keep a transform once it ends, so fixed popovers stay pinned to the window', () => {
    const css = sheet('ui/motion/motion.css')
    const page = rule(css, '.sd-motion-page')
    expect(page).toMatch(/animation:.*backwards/)
    expect(page).not.toMatch(/animation:.*\b(both|forwards)\b/)
  })

  it('the dialog is held to the window and scrolls its body', () => {
    const css = sheet('ui/dialog/Dialog.css')
    expect(rule(css, '.sd-dialog')).toMatch(/max-block-size: calc\(92vh/)
    expect(rule(css, '.sd-dialog__body')).toContain('overflow-y: auto')
    expect(rule(css, '.sd-dialog__body')).toContain('min-block-size: 0')
  })

  it('the model picker popover is pinned to the viewport, not hung under the chip', () => {
    const css = sheet('ui/model-picker/ModelPicker.css')
    expect(rule(css, '.sd-model-picker__popover')).toContain('position: fixed')
    expect(rule(css, '.sd-model-picker__popover')).not.toContain('inset-block-start')
    expect(rule(css, '.sd-model-picker__list')).toContain('overflow-y: auto')
    expect(rule(css, '.sd-model-picker__list')).toContain('min-block-size: 0')
  })
})
