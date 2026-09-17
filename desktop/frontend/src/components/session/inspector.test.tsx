import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { Attachment } from '../../api/types'
import { createFakeTransport } from '../../store/fakeTransport'
import { TRIAGE_ATTACHMENTS, TRIAGE_RUN_ID, triageWithAttachments } from '../../store/fakeSession'
import AnswerClosing from './AnswerClosing'
import AttachmentPreview from './AttachmentPreview'
import BundlePane from './BundlePane'
import NoteFooter from './NoteFooter'
import ToolsPane, { took, toolsHeadline, type ToolRow } from './ToolsPane'
import { fileSize, middleEllipsis, previewKind } from './bundleModel'
import { noteFromFirstHeading, noteLabel } from './noteBody'

/*
 * The blocks all three layouts draw, on their own.
 *
 * The layout suites check that a pane is wired; this one checks what the
 * blocks show and — the point of the 2026-09-17 round — what they do not.
 */

function transport(over: { attachments?: Attachment[] } = {}) {
  return createFakeTransport({
    sessions: { [TRIAGE_RUN_ID]: { ...triageWithAttachments(), attachments: over.attachments ?? TRIAGE_ATTACHMENTS } },
  })
}

describe('the bundle model', () => {
  it('shortens a file name in the middle, so the extension survives', () => {
    expect(middleEllipsis('short.png')).toBe('short.png')
    const long = middleEllipsis('movement-report-for-product-00219-september.pdf')
    expect(long).toHaveLength(34)
    expect(long.endsWith('.pdf')).toBe(true)
    expect(long).toContain('…')
    // Arabic is shortened on characters, not bytes, and is not reordered.
    expect(middleEllipsis('ملاحظة-صوتية-من-العميل-أحمد-الفهد-الطويلة.m4a')).toContain('…')
  })

  it('says a size a person reads, and nothing when nothing said', () => {
    expect(fileSize(0)).toBe('')
    expect(fileSize(940)).toBe('940 B')
    expect(fileSize(184_320)).toBe('184 kB')
    expect(fileSize(2_400_000)).toBe('2.4 MB')
  })

  it('picks a preview by the type, then by the extension', () => {
    expect(previewKind('image/png', 'a.png')).toBe('image')
    expect(previewKind('application/pdf', 'a.pdf')).toBe('pdf')
    expect(previewKind('audio/mp4', 'a.m4a')).toBe('audio')
    expect(previewKind('video/mp4', 'a.mp4')).toBe('video')
    expect(previewKind('text/plain', 'a.txt')).toBe('text')
    // The route downgrades an .html attachment to text/plain; either way it
    // is read, never framed as a document.
    expect(previewKind('text/plain; charset=utf-8', 'a.html')).toBe('text')
    expect(previewKind('', 'voice.m4a')).toBe('audio')
    expect(previewKind('application/zip', 'logs.zip')).toBe('other')
  })
})

describe('the note body', () => {
  const NOTE = [
    'Some preamble the template wrote.',
    '',
    '# Recording a return adds its quantity twice',
    '',
    'Register: [[_Issue Register]] · RCA: [[SBX-1 RCA]] · Resolution: <fill: Resolution>',
    '',
    '> **Working document — edited by hand after filing.**',
    '',
    '## Customer Complaint',
    '',
    'The stock is one higher than the report.',
  ].join('\n')

  it('renders from the first heading, without the vault scaffolding', () => {
    const out = noteFromFirstHeading(NOTE)
    expect(out.startsWith('# Recording a return')).toBe(true)
    expect(out).not.toContain('Register:')
    expect(out).not.toContain('_Issue Register')
    expect(out).not.toContain('Working document')
    expect(out).toContain('## Customer Complaint')
    expect(out).toContain('The stock is one higher than the report.')
  })

  it('leaves a note with no heading alone rather than guessing at its shape', () => {
    const plain = 'Just some markdown.\n\nWith a second paragraph.'
    expect(noteFromFirstHeading(plain)).toBe(plain)
  })

  it('keeps a wikilink that is part of the note rather than the strip', () => {
    const note = '# Title\n\nThe fix is described in [[SBX-1 RCA]] and needs review.'
    expect(noteFromFirstHeading(note)).toContain('[[SBX-1 RCA]]')
  })

  it('names the note as the vault does, relative to the notes directory', () => {
    expect(noteLabel('/vault/Support/Triage/SBX-1 drift.md', '/vault/Support')).toBe('Triage/SBX-1 drift.md')
    expect(noteLabel('/vault/Support/Triage/SBX-1 drift.md')).toBe('Triage/SBX-1 drift.md')
  })
})

describe('the closing card', () => {
  it('shows the title, at most three chips, and the way into the note', () => {
    render(
      <AnswerClosing
        title="Recording a return adds its quantity twice"
        classification="code"
        confidence="high"
        openQuestions={2}
        at="03:13"
        revised
        onOpenNote={() => {}}
      />,
    )
    const card = screen.getByTestId('answer-card')
    expect(within(card).getByRole('heading', { level: 2 })).toHaveAttribute('dir', 'auto')
    expect(card.querySelectorAll('.si-chip')).toHaveLength(3)
    expect(within(card).getByText('revised after your steer')).toBeInTheDocument()
    expect(within(card).getByRole('button', { name: 'Filed as a note →' })).toBeInTheDocument()
  })

  it('says the note is coming rather than offering a way to a file that is not there', () => {
    render(<AnswerClosing title="Working" />)
    expect(screen.getByText('The note is filed as the run finishes')).toBeInTheDocument()
    expect(screen.queryByRole('button')).toBeNull()
  })
})

describe('the tools pane', () => {
  const rows: ToolRow[] = [
    { index: 1, tool: 'Bash', summary: 'List the ledger package', decision: 'policy', tookMs: 60, input: 'ls -la', output: 'a\nb' },
    { index: 2, tool: 'Read', summary: 'Read the ledger', decision: 'policy', tookMs: 1400 },
    { index: 3, tool: 'Bash', summary: 'Read the git log', decision: 'denied', tookMs: 5 },
  ]

  it('heads with the two numbers a support engineer acts on', () => {
    expect(toolsHeadline(rows)).toBe('3 calls · 1 needed your approval')
    expect(toolsHeadline(rows.slice(0, 1))).toBe('1 call')
  })

  it('says a duration a person reads, and says nothing for nothing', () => {
    expect(took(undefined)).toBe('')
    expect(took(0)).toBe('')
    expect(took(60)).toBe('60 ms')
    expect(took(1400)).toBe('1.4 s')
  })

  it('draws three cells a row and no chip for the ordinary policy call', () => {
    render(<ToolsPane rows={rows} />)
    const drawn = screen.getAllByRole('listitem')
    expect(drawn).toHaveLength(3)
    // The tool and its one line, then the duration. No number, no clock,
    // no output size, no decision chip on a call nobody was asked about.
    const first = within(drawn[0]).getByRole('button')
    expect(first.children).toHaveLength(2)
    expect(first.querySelector('.si-stamp')).toBeNull()
    expect(first.querySelector('.si-row__why')).toHaveAttribute('dir', 'auto')
    expect(within(drawn[2]).getByText('denied')).toBeInTheDocument()
  })

  it('opens the row into a drawer with the input and the output', async () => {
    render(<ToolsPane rows={rows} />)
    fireEvent.click(within(screen.getAllByRole('listitem')[0]).getByRole('button'))
    const detail = await screen.findByTestId('tool-detail')
    expect(within(detail).getByRole('heading', { name: 'Input' })).toBeInTheDocument()
    expect(within(detail).getByText('ls -la')).toBeInTheDocument()
  })

  it('says so honestly when nothing has been called', () => {
    render(<ToolsPane rows={[]} />)
    expect(screen.getByText('No tool has been called yet.')).toBeInTheDocument()
  })
})

describe('the bundle pane', () => {
  it('lists each attachment as one row with its size, and nothing else', async () => {
    render(<BundlePane transport={transport()} workspaceId="ws1" runId={TRIAGE_RUN_ID} />)
    const files = await screen.findByRole('region', { name: 'Attachments' })
    const rows = within(files).getAllByRole('listitem')
    expect(rows).toHaveLength(3)
    expect(rows[0]).toHaveTextContent('stock-screen.png')
    expect(rows[0]).toHaveTextContent('184 kB')
    expect(rows[0]).toHaveTextContent('2026-09-12')
    // The Arabic file name is laid out from its own first character.
    expect(within(files).getByText(/ملاحظة/)).toHaveAttribute('dir', 'auto')
    expect(within(files).getByText(/ملاحظة/)).toHaveClass('sd-bidi')
    // No bundle-relative path drawn beside the name.
    expect(within(files).queryByText(/attachments\//)).toBeNull()
  })

  it('falls back to the prompt when the bundle kept no record of its files', async () => {
    render(<BundlePane transport={transport({ attachments: [] })} workspaceId="ws1" runId={TRIAGE_RUN_ID} />)
    const files = await screen.findByRole('region', { name: 'Attachments' })
    await waitFor(() => expect(within(files).getAllByRole('listitem')).toHaveLength(3))
    expect(within(files).getByText('stock-screen.png')).toBeInTheDocument()
  })

  it('offers the bundle folder in the menu, never as a path in a card', async () => {
    const onOpenFolder = vi.fn()
    render(<BundlePane transport={transport()} workspaceId="ws1" runId={TRIAGE_RUN_ID} onOpenFolder={onOpenFolder} />)
    await screen.findByTestId('bundle-view')
    fireEvent.click(screen.getByRole('button', { name: 'Bundle options' }))
    fireEvent.click(await screen.findByRole('menuitem', { name: 'Open bundle folder' }))
    expect(onOpenFolder).toHaveBeenCalled()
  })

  it('copies the bundle path in a browser, which can open no folder', async () => {
    const writeText = vi.fn(async () => {})
    Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true })
    const dir = '/ws/.sirdar/runs/SBX-1/r1/bundle'
    render(<BundlePane transport={transport()} workspaceId="ws1" runId={TRIAGE_RUN_ID} folderPath={dir} />)
    await screen.findByTestId('bundle-view')
    fireEvent.click(screen.getByRole('button', { name: 'Bundle options' }))
    fireEvent.click(await screen.findByRole('menuitem', { name: 'Copy bundle path' }))
    await waitFor(() => expect(writeText).toHaveBeenCalledWith(dir))
  })

  it('leaves the menu out where there is neither a folder to open nor a path to copy', async () => {
    render(<BundlePane transport={transport()} workspaceId="ws1" runId={TRIAGE_RUN_ID} />)
    await screen.findByTestId('bundle-view')
    expect(screen.queryByRole('button', { name: 'Bundle options' })).toBeNull()
  })
})

describe('the attachment preview', () => {
  beforeEach(() => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response('some text')))
  })

  const cases: [string, Attachment, (dialog: HTMLElement) => void][] = [
    [
      'an image, zoomed to fit',
      TRIAGE_ATTACHMENTS[0],
      (d) => {
        const img = d.querySelector('img')!
        expect(img).toHaveAttribute('alt', 'stock-screen.png')
        expect(img.getAttribute('src')).toContain('attachments/stock-screen.png')
      },
    ],
    ['a voice note, in a player', TRIAGE_ATTACHMENTS[1], (d) => expect(d.querySelector('audio')).toBeInTheDocument()],
    ['a PDF, framed', TRIAGE_ATTACHMENTS[2], (d) => expect(d.querySelector('object')).toHaveAttribute('type', 'application/pdf')],
    [
      'a text file, read',
      { name: 'log.txt', path: 'attachments/log.txt', mime: 'text/plain', size: 90 },
      (d) => {
        const pre = d.querySelector('pre')!
        expect(pre).toHaveAttribute('dir', 'auto')
        expect(pre).toHaveClass('sd-bidi')
      },
    ],
    [
      'and the folder for anything else',
      { name: 'logs.zip', path: 'attachments/logs.zip', mime: 'application/zip', size: 4000 },
      (d) => expect(within(d).getByText(/Nothing here can draw/)).toBeInTheDocument(),
    ],
  ]

  for (const [name, attachment, check] of cases) {
    it(`draws ${name}`, async () => {
      const t = transport({ attachments: [...TRIAGE_ATTACHMENTS, attachment] })
      render(
        <AttachmentPreview attachment={attachment} transport={t} workspaceId="ws1" runId={TRIAGE_RUN_ID} onClose={() => {}} />,
      )
      const dialog = await screen.findByRole('dialog')
      await waitFor(() => expect(within(dialog).queryByText('Reading the file…')).toBeNull())
      check(dialog)
    })
  }

  it('says why when the file cannot be read, rather than drawing an empty frame', async () => {
    const missing: Attachment = { name: 'gone.png', path: 'attachments/gone.png', mime: 'image/png', size: 1 }
    render(
      <AttachmentPreview attachment={missing} transport={transport()} workspaceId="ws1" runId={TRIAGE_RUN_ID} onClose={() => {}} />,
    )
    expect(await screen.findByRole('alert')).toHaveTextContent(/no attachment at/)
  })

  it('offers the bundle folder when the desktop can open one', async () => {
    const onOpenFolder = vi.fn()
    render(
      <AttachmentPreview
        attachment={TRIAGE_ATTACHMENTS[0]}
        transport={transport()}
        workspaceId="ws1"
        runId={TRIAGE_RUN_ID}
        onClose={() => {}}
        onOpenFolder={onOpenFolder}
      />,
    )
    fireEvent.click(await screen.findByRole('button', { name: 'Open in Finder' }))
    expect(onOpenFolder).toHaveBeenCalled()
  })
})

describe('the note footer', () => {
  it('names the file relative to the vault and keeps the whole path on the row', () => {
    render(
      <NoteFooter
        transport={createFakeTransport()}
        workspaceId="ws1"
        runId={TRIAGE_RUN_ID}
        path="/vault/Support/Triage/SBX-1 drift.md"
        notesDir="/vault/Support"
      />,
    )
    const footer = screen.getByTestId('note-footer')
    expect(footer).toHaveTextContent('Triage/SBX-1 drift.md')
    expect(footer.querySelector('.si-notefoot__path')).toHaveAttribute('title', '/vault/Support/Triage/SBX-1 drift.md')
    // A browser cannot open a file on the operator's machine.
    expect(within(footer).getByRole('button', { name: 'Copy path' })).toBeInTheDocument()
  })

  it('opens the file where the desktop can', () => {
    const openNote = vi.fn(async () => {})
    const t = { ...createFakeTransport(), openNote }
    render(<NoteFooter transport={t} workspaceId="ws1" runId={TRIAGE_RUN_ID} path="/vault/Triage/x.md" />)
    fireEvent.click(screen.getByRole('button', { name: 'Open' }))
    expect(openNote).toHaveBeenCalledWith('ws1', TRIAGE_RUN_ID, '/vault/Triage/x.md')
  })
})
