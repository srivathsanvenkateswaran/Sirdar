import { act, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it } from 'vitest'
import { resetPreferRTL, setPreferRTL } from '../../lib/rtl'
import NotePane from './index'

afterEach(() => {
  setPreferRTL(false)
  resetPreferRTL()
})

const ARABIC = 'العميل لا يستطيع تصدير كشف الحساب منذ تحديث الأسبوع الماضي.'

describe('NotePane', () => {
  it('resolves each block from its own first letter by default', () => {
    const { container } = render(
      <NotePane title="OMNI-2510 triage">
        <p>{ARABIC}</p>
      </NotePane>,
    )
    expect(container.querySelector('.sd-note')).toHaveAttribute('dir', 'auto')
  })

  it('lays the whole pane out right to left when the reader prefers it', () => {
    setPreferRTL(true)
    const { container } = render(
      <NotePane>
        <p>{ARABIC}</p>
      </NotePane>,
    )
    expect(container.querySelector('.sd-note')).toHaveAttribute('dir', 'rtl')
  })

  it('follows the preference while it is open', () => {
    const { container } = render(
      <NotePane>
        <p>{ARABIC}</p>
      </NotePane>,
    )
    expect(container.querySelector('.sd-note')).toHaveAttribute('dir', 'auto')
    // The preference is a store outside React, so the subscription is what is
    // under test here, not the initial read.
    act(() => setPreferRTL(true))
    expect(container.querySelector('.sd-note')).toHaveAttribute('dir', 'rtl')
  })

  it('takes an explicit direction, which is how the gallery shows both', () => {
    const { container } = render(
      <NotePane dir="rtl">
        <p>{ARABIC}</p>
      </NotePane>,
    )
    expect(container.querySelector('.sd-note')).toHaveAttribute('dir', 'rtl')
  })

  it('keeps the note source left to right whichever way the prose runs', () => {
    render(
      <NotePane dir="rtl" title="فرز التذكرة" source="notes/OMNI-2510-triage.md">
        <p>{ARABIC}</p>
      </NotePane>,
    )
    expect(screen.getByText('notes/OMNI-2510-triage.md')).toHaveAttribute('dir', 'ltr')
  })

  it('gives the note one heading at the top of the document outline', () => {
    render(
      <NotePane title="OMNI-2510 triage">
        <h2>Root cause</h2>
        <p>The export job holds one connection per page.</p>
      </NotePane>,
    )
    expect(screen.getByRole('heading', { level: 1 })).toHaveTextContent('OMNI-2510 triage')
    expect(screen.getByRole('heading', { level: 2 })).toHaveTextContent('Root cause')
  })

  it('renders a link the sweep can run under', () => {
    render(
      <NotePane>
        <p>
          See <a href="https://example.test/ticket">the ticket</a>.
        </p>
      </NotePane>,
    )
    const link = screen.getByRole('link', { name: 'the ticket' })
    link.focus()
    expect(link).toHaveFocus()
  })
})
