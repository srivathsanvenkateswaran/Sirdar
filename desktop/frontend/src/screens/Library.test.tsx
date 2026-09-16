import { act, fireEvent, render, screen, within } from '@testing-library/react'
import { createRoot, type Root } from 'react-dom/client'
import { afterAll, beforeAll, describe, expect, it } from 'vitest'
import { PrimaryActionProvider, usePrimaryAction } from '../components/shell/primaryAction'
import Library from './Library'

/** The frame is the element the theme and direction switches paint. */
function frame(): HTMLElement {
  return screen.getByTestId('library-frame')
}

/**
 * Mounts one tree for a whole describe block and hands back its container.
 *
 * Putting the gallery up is the expensive thing in this file — thirty-five
 * sections of specimens, about half a second on an idle machine and several
 * times that while vitest runs the rest of the suite alongside it — so a
 * render per `it` (eighteen of them) ran the slowest tests past the default
 * 5000ms timeout whenever the machine was busy. The setup file calls
 * `@testing-library/react`'s `cleanup()` after every test, which unmounts
 * whatever `render()` put up, so mount the root here instead: `cleanup()` only
 * unmounts roots it made itself, and this one stands for every test in the
 * block. A test that shares the tree must leave it as it found it, or be the
 * last one in the block.
 */
function mountOnce(ui: () => JSX.Element): () => HTMLElement {
  let container: HTMLElement | undefined
  let root: Root | undefined

  beforeAll(() => {
    container = document.body.appendChild(document.createElement('div'))
    root = createRoot(container)
    act(() => {
      root?.render(ui())
    })
  })

  afterAll(() => {
    act(() => {
      root?.unmount()
    })
    container?.remove()
    container = undefined
    root = undefined
  })

  return () => {
    if (!container) throw new Error('the gallery is not mounted')
    return container
  }
}

/** The thirty-five components, in build order, as the sections name them. */
const SECTIONS = [
  'Button',
  'Pill nav',
  'Segmented control',
  'Card',
  'Status badge',
  'Kanban column',
  'Run card',
  'Event row',
  'Note pane',
  'Data table',
  'Toast',
  'Dialog',
  'Quota chip',
  'Hero band',
  'Ring text and marquee',
  'Sidebar nav item',
  'Sidebar footer card',
  'Modal sheet with secondary nav',
  'Setting row',
  'Heatmap',
  'Badge',
  'Provider mark',
  'Source mark',
  'Banner',
  'Group label',
  'Item row',
  'Search bar',
  'Stat card',
  'Toggle',
  'Panel toggle',
  'Page head',
  'Kind chip',
  'State glyph',
  'Model picker',
  'Brand mark',
]

/**
 * The three grids the library README asks of every component — light, dark,
 * right to left — as the two switches paint them.
 */
const FRAMES: [string, 'light' | 'dark', 'ltr' | 'rtl'][] = [
  ['light, left to right', 'light', 'ltr'],
  ['dark, left to right', 'dark', 'ltr'],
  ['light, right to left', 'light', 'rtl'],
]

/**
 * Paints the frame, whatever it was painted before: the three frames share one
 * gallery, so each switch is clicked to the state it wants rather than toggled
 * out of the one it opened in.
 */
function paint(theme: 'light' | 'dark', dir: 'ltr' | 'rtl'): void {
  fireEvent.click(screen.getByRole('radio', { name: theme === 'dark' ? 'Dark' : 'Light' }))
  fireEvent.click(screen.getByRole('radio', { name: dir === 'rtl' ? 'RTL' : 'LTR' }))
}

/** Reads what the sidebar footer would draw while the gallery is up. */
function PrimaryProbe(): JSX.Element {
  const action = usePrimaryAction()
  return <output data-testid="primary-probe">{action ? action.label : 'none'}</output>
}

describe('the asset library', () => {
  describe('in every frame the library README asks for', () => {
    mountOnce(() => <Library />)

    describe.each(FRAMES)('painted %s', (_, theme, dir) => {
      it('renders every one of the thirty-five sections, each with a specimen', () => {
        paint(theme, dir)
        expect(frame()).toHaveAttribute('data-theme', theme)
        expect(frame()).toHaveAttribute('dir', dir)

        // Section headings only: the note pane specimen has headings of its
        // own, and so does the dialog.
        const sections = [...frame().querySelectorAll('.lib-section')]
        expect(sections.map((s) => s.querySelector('.lib-section__name')?.textContent)).toEqual(
          SECTIONS,
        )
        for (const section of sections) {
          const name = section.querySelector('.lib-section__name')?.textContent
          expect(
            section.querySelector('.lib-section__body')?.children.length,
            name,
          ).toBeGreaterThan(0)
        }
      })
    })
  })

  describe('as it opens', () => {
    const gallery = mountOnce(() => <Library />)

    it('opens in the light theme, laid out left to right', () => {
      expect(frame()).toHaveAttribute('data-theme', 'light')
      expect(frame()).toHaveAttribute('dir', 'ltr')
    })

    it('names each section once and says one sentence about it', () => {
      for (const note of frame().querySelectorAll('.lib-section__note')) {
        const text = note.textContent ?? ''
        expect(text.endsWith('.'), text).toBe(true)
        // A full stop followed by more words is a second sentence.
        expect(text.slice(0, -1), text).not.toMatch(/[.!?]\s/)
      }
      // The page's own head is its one page-head h1; the page-head specimens
      // are all h2 (the hero band and note pane specimens carry h1s of their
      // own, as they do on the landing page and in a note).
      expect(frame().querySelectorAll('h1.sd-page-head__title')).toHaveLength(0)
      expect(screen.getByRole('heading', { level: 1, name: 'Library' })).toHaveClass(
        'sd-page-head__title',
      )
    })

    it('carries Arabic on every component that shows text', () => {
      const within_ = within(frame())
      expect(within_.getByRole('button', { name: 'ابدأ الفرز' })).toBeInTheDocument()
      expect(within_.getByRole('radio', { name: 'المسندة إليّ' })).toBeInTheDocument()
      // The status badge and the state glyph both carry the blocked word.
      expect(within_.getAllByText('بانتظار ردّك').length).toBeGreaterThan(0)
      expect(within_.getAllByText(/العميل لا يستطيع تصدير كشف الحساب/).length).toBeGreaterThan(0)
      expect(within_.getByText('تعذّر بدء التشغيل: لا توجد مساحة عمل.')).toBeInTheDocument()
      // The marquee renders its track twice for a seamless loop, so the item
      // legitimately appears more than once.
      expect(within_.getAllByText('مكتب زوهو').length).toBeGreaterThan(0)
    })

    it('shows every provider mark by the vendor it stands for', () => {
      for (const name of ['Claude', 'Codex', 'GitHub Copilot', 'Antigravity', 'Qwen', 'Cursor'])
        expect(within(frame()).getAllByRole('img', { name }).length).toBeGreaterThan(0)
      // A provider with no mark gets its initials and keeps its id as the name.
      expect(within(frame()).getAllByRole('img', { name: 'acp' }).length).toBeGreaterThan(0)
    })

    it('shows every run state as its own badge', () => {
      for (const word of [
        'queued',
        'preparing',
        'running',
        'blocked',
        'completed',
        'done',
        'over budget',
      ])
        expect(within(frame()).getAllByText(word).length).toBeGreaterThan(0)
    })

    it('shows the run card in every state its spec lists', () => {
      const cards = within(frame()).getAllByRole('button', { name: /^(SBX|OMNI)-\d+: / })
      const states = new Set(cards.map((card) => card.getAttribute('data-status')))
      for (const state of [
        'queued',
        'preparing',
        'running',
        'blocked',
        'completed',
        'done',
        'failed',
        'over_budget',
      ])
        expect(states.has(state), state).toBe(true)

      // The live edge and the clock on the preparing card; no clock on the
      // over-budget one, however long it ran.
      const preparing = within(frame()).getByRole('button', { name: /^SBX-9: / })
      expect(preparing).toHaveAttribute('data-live', 'true')
      expect(within(preparing).getByText('0:08')).toBeInTheDocument()
      const overBudget = within(frame()).getByRole('button', { name: /^SBX-10: / })
      expect(within(overBudget).getByText('over budget')).toBeInTheDocument()
      expect(within(overBudget).queryByText('58:12')).toBeNull()
      // A run with no title from the tracker shows its key as the title, once.
      const bare = within(frame()).getByRole('button', { name: /^OMNI-2513, / })
      expect(within(bare).getAllByText('OMNI-2513')).toHaveLength(1)
    })

    it('shows the banner in every tone, and the lead alone without a separator', () => {
      const banners = within(frame()).getAllByRole('status')
      const tones = new Set(banners.map((b) => b.getAttribute('data-tone')).filter(Boolean))
      for (const tone of ['ok', 'done', 'blocked', 'failed', 'live'])
        expect(tones.has(tone), tone).toBe(true)
      const alone = within(frame()).getByText('Branch created').closest('.sd-banner')
      expect(alone).not.toBeNull()
      expect(alone?.querySelector('.sd-banner__sep')).toBeNull()
    })

    it('shows the item row opening and keeping its own control, in every tone', () => {
      const rows = [...frame().querySelectorAll('.sd-item')]
      const tones = new Set(rows.map((r) => r.getAttribute('data-tone')))
      for (const tone of ['plain', 'live', 'blocked', 'done', 'failed'])
        expect(tones.has(tone), tone).toBe(true)
      const done = within(frame()).getByRole('button', { name: 'Open SBX-5' }).closest('.sd-item')
      expect(done).toHaveAttribute('data-tone', 'done')
      expect(
        within(done as HTMLElement).getByRole('button', { name: 'Open note' }),
      ).toBeInTheDocument()
    })

    it('shows the smaller pieces in the states their specs list', () => {
      const within_ = within(frame())
      // Kind chip: the three kinds and an unknown one drawn verbatim.
      const kinds = [...frame().querySelectorAll('.sd-kind')].map((k) => k.getAttribute('data-kind'))
      for (const kind of ['triage', 'rca', 'fix', 'eval']) expect(kinds, kind).toContain(kind)
      // Stat card: nothing to count yet.
      expect(within_.getByText('—')).toBeInTheDocument()
      // Page head: the title alone, at h2 like every other specimen.
      expect(within_.getByRole('heading', { level: 2, name: 'Providers' })).toBeInTheDocument()
      // Search bar: a key typed, and the disabled well passed through.
      expect(within_.getByDisplayValue('OMNI-2510')).toBeInTheDocument()
      expect(
        within_.getAllByRole('searchbox').some((box) => (box as HTMLInputElement).disabled),
      ).toBe(true)
      // Toggle: on, off, and disabled in both positions.
      const switches = within_.getAllByRole('switch')
      expect(
        switches.filter((s) => s.getAttribute('aria-checked') === 'true').length,
      ).toBeGreaterThan(0)
      expect(
        switches.filter((s) => s.getAttribute('aria-checked') === 'false').length,
      ).toBeGreaterThan(0)
      expect(
        switches.filter(
          (s) => (s as HTMLButtonElement).disabled && s.getAttribute('aria-checked') === 'true',
        ),
      ).toHaveLength(1)
      expect(
        switches.filter(
          (s) => (s as HTMLButtonElement).disabled && s.getAttribute('aria-checked') === 'false',
        ),
      ).toHaveLength(1)
      // Segmented control: four options, the most it takes.
      expect(
        within(within_.getByRole('radiogroup', { name: 'Kind' })).getAllByRole('radio'),
      ).toHaveLength(4)
    })

    it('shows the six board columns with their rails', () => {
      expect(gallery().querySelectorAll('.sd-lane')).toHaveLength(6)
    })

    it('shows both the filled and the empty state of the table', () => {
      expect(
        screen.getByText('No run has been recorded in this workspace yet. Start one from the board.'),
      ).toBeInTheDocument()
      expect(screen.getAllByRole('table', { name: 'Register' })).toHaveLength(1)
    })

    // The dialog specimen closes itself again, so it leaves the shared gallery
    // as it found it.
    it('opens the dialog specimen and gives it back its opener', () => {
      const opener = screen.getByRole('button', { name: 'Open the dialog' })
      opener.focus()
      fireEvent.click(opener)
      expect(screen.getByRole('dialog', { name: 'Start a triage' })).toBeInTheDocument()
      fireEvent.click(screen.getByRole('button', { name: 'Cancel' }))
      expect(screen.queryByRole('dialog')).toBeNull()
      expect(opener).toHaveFocus()
    })

    // The last two leave a mark on the shared gallery — a specimen driven off
    // its opening state, and the frame repainted — so they come last.
    it('lets a specimen be driven, not only looked at', () => {
      const filter = screen.getByRole('radiogroup', { name: 'Filter' })
      fireEvent.keyDown(filter, { key: 'ArrowRight' })
      expect(within(filter).getByRole('radio', { name: 'Mine' })).toBeChecked()
    })

    it('heads the page with its name, a lede and the two switches, outside the frame', () => {
      // By class rather than by role: the hero band and the note pane specimens
      // are landmarks of their own.
      const bar = gallery().querySelector<HTMLElement>('.lib__bar')
      if (!bar) throw new Error('no bar')
      expect(within(bar).getByRole('heading', { level: 1, name: 'Library' })).toBeInTheDocument()
      expect(
        within(bar).getByText('Every component the app ships, at the size it ships at.'),
      ).toBeInTheDocument()
      expect(
        within(bar)
          .getAllByRole('radiogroup')
          .map((g) => g.getAttribute('aria-label')),
      ).toEqual(['Theme', 'Direction'])
      expect(within(bar).getAllByRole('radio').map((r) => r.textContent)).toEqual([
        'Light',
        'Dark',
        'LTR',
        'RTL',
      ])
      // The switch bar keeps the window's own theme: painting the specimens
      // dark must not paint the bar.
      fireEvent.click(screen.getByRole('radio', { name: 'Dark' }))
      expect(bar.closest('[data-theme]')).toBeNull()
      expect(bar.contains(frame())).toBe(false)
    })
  })

  // The only test that needs the gallery under a provider, so it renders its
  // own tree rather than sharing one.
  it('publishes no primary action: its filled buttons are specimens', () => {
    render(
      <PrimaryActionProvider>
        <Library />
        <PrimaryProbe />
      </PrimaryActionProvider>,
    )
    expect(within(frame()).getAllByRole('button', { name: 'Start triage' }).length).toBeGreaterThan(
      0,
    )
    expect(screen.getByTestId('primary-probe')).toHaveTextContent('none')
  })
})
