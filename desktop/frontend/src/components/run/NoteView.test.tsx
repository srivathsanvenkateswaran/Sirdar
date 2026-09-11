// @vitest-environment jsdom
import { act, cleanup, render, screen, waitFor } from '@testing-library/react'
import '@testing-library/jest-dom/vitest'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import type { Transport } from '../../api/types'
import { resetPreferRTL, setPreferRTL } from '../../lib/rtl'
import NoteView from './NoteView'

const ARABIC = 'التصدير لا يعمل عندما يحتوي الطلب على أكثر من ٥٠٠ بند.'

const NOTE = `---
customer: شركة الأمثلة
status: triaged
---

# Export fails for large orders

## Customer Complaint (original)

<div dir="rtl">

${ARABIC}

</div>

## Open Questions

- Is 500 lines the exact threshold?
`

function fakeTransport(text: string): Transport {
  const notImplemented = () => Promise.reject(new Error('not used by NoteView'))
  return {
    workspaces: notImplemented,
    addWorkspace: notImplemented,
    removeWorkspace: notImplemented,
    queue: notImplemented,
    runs: notImplemented,
    run: notImplemented,
    events: notImplemented,
    note: () => Promise.resolve(text),
    prompt: notImplemented,
    startTriage: notImplemented,
    startRCA: notImplemented,
    resume: notImplemented,
    cancel: notImplemented,
    register: notImplemented,
    doctor: notImplemented,
    quota: notImplemented,
    subscribe: () => () => {},
  } as unknown as Transport
}

function renderNote(text = NOTE) {
  return render(
    <NoteView transport={fakeTransport(text)} workspaceId="ws1" runId="run-1" kinds={['triage']} />,
  )
}

beforeEach(() => {
  localStorage.clear()
  resetPreferRTL()
})

afterEach(() => {
  cleanup()
  localStorage.clear()
  resetPreferRTL()
})

describe('NoteView', () => {
  it('lets the browser resolve each block by marking the note dir="auto"', async () => {
    renderNote()
    await waitFor(() => expect(screen.getByTestId('note-markdown')).toBeInTheDocument())

    expect(screen.getByTestId('note-pane')).toHaveAttribute('dir', 'auto')
    expect(screen.getByTestId('note-markdown')).toHaveAttribute('dir', 'auto')
  })

  it('marks frontmatter values dir="auto" so an Arabic customer name reads correctly', async () => {
    renderNote()
    const cell = await screen.findByText('شركة الأمثلة')
    expect(cell).toHaveAttribute('dir', 'auto')
  })

  it('lays the pane out right to left once the preference is set', async () => {
    setPreferRTL(true)
    renderNote()
    await waitFor(() => expect(screen.getByTestId('note-markdown')).toBeInTheDocument())

    expect(screen.getByTestId('note-pane')).toHaveAttribute('dir', 'rtl')
    expect(screen.getByTestId('note-markdown')).toHaveAttribute('dir', 'rtl')
  })

  it('follows the preference as it changes, without a remount', async () => {
    renderNote()
    await waitFor(() => expect(screen.getByTestId('note-pane')).toHaveAttribute('dir', 'auto'))

    act(() => setPreferRTL(true))
    expect(screen.getByTestId('note-pane')).toHaveAttribute('dir', 'rtl')

    act(() => setPreferRTL(false))
    expect(screen.getByTestId('note-pane')).toHaveAttribute('dir', 'auto')
  })

  /*
   * The note templates wrap an Arabic paragraph in a <div dir="rtl"> block for
   * Obsidian, which renders HTML. react-markdown does not, and drops the raw
   * tags — so what must survive here is the Arabic itself, and the container's
   * own `dir` is what lays it out.
   */
  it('keeps the Arabic text when the raw RTL wrapper is dropped', async () => {
    renderNote()
    const md = await screen.findByTestId('note-markdown')
    expect(md.textContent).toContain(ARABIC)
    expect(md.textContent).not.toContain('<div')
  })
})
