import { fireEvent, render, screen, within } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import Library from './Library'

/** The frame is the element the theme and direction switches paint. */
function frame(): HTMLElement {
  return screen.getByTestId('library-frame')
}

describe('the asset library', () => {
  it('shows every one of the fifteen components', () => {
    const { container } = render(<Library />)
    // Section headings only: the note pane specimen has headings of its own,
    // and so does the dialog.
    const headings = [...container.querySelectorAll('.lib-section__name')].map(
      (h) => h.textContent,
    )
    expect(headings).toEqual([
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
    ])
  })

  it('opens in the light theme, laid out left to right', () => {
    render(<Library />)
    expect(frame()).toHaveAttribute('data-theme', 'light')
    expect(frame()).toHaveAttribute('dir', 'ltr')
  })

  it('paints the specimens dark without changing the page around them', () => {
    render(<Library />)
    fireEvent.click(screen.getByRole('radio', { name: 'Dark' }))
    expect(frame()).toHaveAttribute('data-theme', 'dark')
    // The switch bar is outside the frame and keeps the window's own theme.
    expect(screen.getByRole('radio', { name: 'Dark' }).closest('[data-theme]')).toBeNull()
  })

  it('lays the specimens out right to left on demand', () => {
    render(<Library />)
    fireEvent.click(screen.getByRole('radio', { name: 'Right to left' }))
    expect(frame()).toHaveAttribute('dir', 'rtl')
  })

  it('carries Arabic on every component that shows text', () => {
    render(<Library />)
    const within_ = within(frame())
    expect(within_.getByRole('button', { name: 'ابدأ الفرز' })).toBeInTheDocument()
    expect(within_.getByRole('radio', { name: 'المسندة إليّ' })).toBeInTheDocument()
    expect(within_.getByText('بانتظار ردّك')).toBeInTheDocument()
    expect(within_.getAllByText(/العميل لا يستطيع تصدير كشف الحساب/).length).toBeGreaterThan(0)
    expect(within_.getByText('تعذّر بدء التشغيل: لا توجد مساحة عمل.')).toBeInTheDocument()
    // The marquee renders its track twice for a seamless loop, so the item
    // legitimately appears more than once.
    expect(within_.getAllByText('مكتب زوهو').length).toBeGreaterThan(0)
  })

  it('shows every run state as its own badge', () => {
    render(<Library />)
    for (const word of ['Queued', 'Preparing', 'Running', 'Needs input', 'Completed', 'Over budget'])
      expect(within(frame()).getAllByText(word).length).toBeGreaterThan(0)
  })

  it('shows the six board columns with their rails', () => {
    const { container } = render(<Library />)
    expect(container.querySelectorAll('.sd-lane')).toHaveLength(6)
  })

  it('opens the dialog specimen and gives it back its opener', () => {
    render(<Library />)
    const opener = screen.getByRole('button', { name: 'Open the dialog' })
    opener.focus()
    fireEvent.click(opener)
    expect(screen.getByRole('dialog', { name: 'Start a triage' })).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }))
    expect(screen.queryByRole('dialog')).toBeNull()
    expect(opener).toHaveFocus()
  })

  it('shows both the filled and the empty state of the table', () => {
    render(<Library />)
    expect(
      screen.getByText('No run has been recorded in this workspace yet. Start one from the board.'),
    ).toBeInTheDocument()
    expect(screen.getAllByRole('table', { name: 'Register' })).toHaveLength(1)
  })

  it('lets a specimen be driven, not only looked at', () => {
    render(<Library />)
    const filter = screen.getByRole('radiogroup', { name: 'Filter' })
    fireEvent.keyDown(filter, { key: 'ArrowRight' })
    expect(within(filter).getByRole('radio', { name: 'Mine' })).toBeChecked()
  })
})
