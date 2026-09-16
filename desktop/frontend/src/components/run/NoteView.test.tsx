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

/**
 * The pane is the library's Note pane, so it is found by its class rather than
 * by a test id: the component carries the reader's direction on its own
 * article element, which is the thing every case here is about.
 */
/** The transport refusing the note, with the reason it gives. */
function failingTransport(err: Error): Transport {
  const t = fakeTransport('')
  t.note = () => Promise.reject(err)
  return t
}

function pane(): HTMLElement {
  const el = document.querySelector('.sd-note')
  if (!el) throw new Error('the note pane is not rendered')
  return el as HTMLElement
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
  it('sits inside the pane that scrolls, whatever state it is in', async () => {
    const { container } = render(
      <NoteView transport={fakeTransport(NOTE)} workspaceId="ws1" runId="r1" kinds={['triage']} />,
    )
    // Loading: the placeholder is already inside the scroll container.
    expect(container.querySelector('.pane.pane--note')).toBeInTheDocument()
    await screen.findByText('Export fails for large orders')
    const pane = screen.getByTestId('note-pane')
    expect(pane).toHaveClass('pane')
    expect(pane.querySelector('.sd-note')).toBeInTheDocument()
  })

  it('lets the browser resolve each block by marking the note dir="auto"', async () => {
    renderNote()
    await waitFor(() => expect(screen.getByTestId('note-markdown')).toBeInTheDocument())

    expect(pane()).toHaveAttribute('dir', 'auto')
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

    expect(pane()).toHaveAttribute('dir', 'rtl')
    expect(screen.getByTestId('note-markdown')).toHaveAttribute('dir', 'rtl')
  })

  it('follows the preference as it changes, without a remount', async () => {
    renderNote()
    await waitFor(() => expect(pane()).toHaveAttribute('dir', 'auto'))

    act(() => setPreferRTL(true))
    expect(pane()).toHaveAttribute('dir', 'rtl')

    act(() => setPreferRTL(false))
    expect(pane()).toHaveAttribute('dir', 'auto')
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

  it('says a missing note is not written yet', async () => {
    render(
      <NoteView transport={failingTransport(new Error('not_found: no such note'))} workspaceId="ws1" runId="r1" kinds={['triage']} />,
    )
    expect(await screen.findByText('No note yet. It is written when the run completes.')).toBeInTheDocument()
  })

  it('shows the reason when the note cannot be read for any other cause', async () => {
    render(
      <NoteView transport={failingTransport(new Error('internal: notes dir is not readable'))} workspaceId="ws1" runId="r1" kinds={['triage']} />,
    )
    expect(await screen.findByText('internal: notes dir is not readable')).toBeInTheDocument()
  })

  describe('opening the file', () => {
    const NOTES = ['/w/.sirdar/runs/K/r1/note.md', '/vault/Triage/OMNI-2510.md']

    it('offers Open file on the desktop and hands the bridge the filed copy', async () => {
      const t = fakeTransport(NOTE)
      const opened: unknown[] = []
      t.openNote = async (...args) => {
        opened.push(args)
      }
      render(<NoteView transport={t} workspaceId="ws1" runId="r1" kinds={['triage']} notePaths={NOTES} />)
      const button = await screen.findByRole('button', { name: 'Open file' })
      expect(screen.getByText('OMNI-2510.md')).toBeInTheDocument()
      button.click()
      await waitFor(() => expect(opened).toEqual([['ws1', 'r1', '/vault/Triage/OMNI-2510.md']]))
    })

    it('copies the path in a browser, where nothing can open a file', async () => {
      const written: string[] = []
      Object.defineProperty(navigator, 'clipboard', {
        configurable: true,
        value: { writeText: async (s: string) => void written.push(s) },
      })
      render(<NoteView transport={fakeTransport(NOTE)} workspaceId="ws1" runId="r1" kinds={['triage']} notePaths={NOTES} />)
      const button = await screen.findByRole('button', { name: 'Copy path' })
      expect(screen.queryByRole('button', { name: 'Open file' })).toBeNull()
      button.click()
      await waitFor(() => expect(written).toEqual(['/vault/Triage/OMNI-2510.md']))
      expect(await screen.findByRole('button', { name: 'Copied' })).toBeInTheDocument()
    })

    it('says why when the desktop could not open it', async () => {
      const t = fakeTransport(NOTE)
      t.openNote = () => Promise.reject(new Error('open: no application registered'))
      render(<NoteView transport={t} workspaceId="ws1" runId="r1" kinds={['triage']} notePaths={NOTES} />)
      ;(await screen.findByRole('button', { name: 'Open file' })).click()
      expect(await screen.findByRole('alert')).toHaveTextContent('open: no application registered')
    })

    it('offers nothing when the run recorded no path for the note', async () => {
      render(<NoteView transport={fakeTransport(NOTE)} workspaceId="ws1" runId="r1" kinds={['triage']} />)
      await screen.findByText('Export fails for large orders')
      expect(screen.queryByRole('button')).toBeNull()
    })
  })
})
