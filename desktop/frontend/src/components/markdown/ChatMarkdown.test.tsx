import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import ChatMarkdown, { type Components } from './ChatMarkdown'

/**
 * Shiki is loaded dynamically the first time a code block mounts; a test
 * never wants the real highlighter, which would pull in its wasm grammar
 * and run past a unit test's patience. The mock resolves the way the real
 * package does, so `CodeBlock`'s loading branch runs the same code path.
 * `bogus` is the one language it refuses to load, standing in for a
 * language Shiki genuinely does not know.
 */
vi.mock('shiki', () => ({
  getSingletonHighlighter: vi.fn().mockResolvedValue({
    getLoadedLanguages: () => ['ts'],
    loadLanguage: vi.fn((lang: string) => (lang === 'bogus' ? Promise.reject(new Error('unknown language')) : Promise.resolve())),
    codeToHtml: (code: string) => `<pre class="shiki"><code>${code}</code></pre>`,
  }),
}))

beforeEach(() => {
  Object.defineProperty(navigator, 'clipboard', {
    configurable: true,
    value: { writeText: vi.fn().mockResolvedValue(undefined) },
  })
})

describe('ChatMarkdown', () => {
  it('renders a GFM table with its header cells', () => {
    const table = '| Name | Count |\n| --- | --- |\n| alpha | 1 |\n'
    render(<ChatMarkdown>{table}</ChatMarkdown>)
    expect(screen.getByRole('table')).toBeInTheDocument()
    expect(screen.getByRole('columnheader', { name: 'Name' })).toBeInTheDocument()
    expect(screen.getByRole('columnheader', { name: 'Count' })).toBeInTheDocument()
    expect(screen.getByRole('cell', { name: 'alpha' })).toBeInTheDocument()
  })

  it('renders a task list as disabled checkboxes', () => {
    const list = '- [ ] todo\n- [x] done\n'
    render(<ChatMarkdown>{list}</ChatMarkdown>)
    const boxes = screen.getAllByRole('checkbox') as HTMLInputElement[]
    expect(boxes).toHaveLength(2)
    expect(boxes[0].checked).toBe(false)
    expect(boxes[1].checked).toBe(true)
    for (const box of boxes) expect(box).toBeDisabled()
  })

  it('renders a fenced code block with its language and a Copy button that copies the raw code', () => {
    const fence = '```ts\nconst answer = 42\n```\n'
    render(<ChatMarkdown>{fence}</ChatMarkdown>)
    expect(screen.getByText('ts')).toBeInTheDocument()
    const button = screen.getByRole('button', { name: 'Copy' })
    fireEvent.click(button)
    expect(navigator.clipboard.writeText).toHaveBeenCalledWith('const answer = 42')
  })

  it('shows Copied once the Copy button has written to the clipboard', async () => {
    const fence = '```ts\nconst answer = 42\n```\n'
    render(<ChatMarkdown>{fence}</ChatMarkdown>)
    fireEvent.click(screen.getByRole('button', { name: 'Copy' }))
    expect(await screen.findByRole('button', { name: 'Copied' })).toBeInTheDocument()
  })

  it('swaps the plain fallback for Shiki markup once highlighting resolves', async () => {
    const fence = '```ts\nconst answer = 42\n```\n'
    const { container } = render(<ChatMarkdown>{fence}</ChatMarkdown>)
    await waitFor(() => expect(container.querySelector('pre.shiki')).not.toBeNull())
    expect(container.querySelector('pre.shiki')?.textContent).toBe('const answer = 42')
  })

  it('keeps the plain, unhighlighted block for a language Shiki does not know', async () => {
    const fence = '```bogus\nunknown lang\n```\n'
    const { container } = render(<ChatMarkdown>{fence}</ChatMarkdown>)
    expect(screen.getByText('bogus')).toBeInTheDocument()
    // `loadLanguage('bogus')` rejects inside `highlightCode`'s try/catch; give
    // that microtask a turn to settle before asserting the fallback held.
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 0))
    })
    expect(container.querySelector('pre.shiki')).toBeNull()
    expect(container.textContent).toContain('unknown lang')
  })

  it('renders an untagged fence as a code block that keeps its line break', () => {
    const fence = '```\nline one\nline two\n```\n'
    const { container } = render(<ChatMarkdown>{fence}</ChatMarkdown>)
    expect(screen.getByTestId('code-block')).toBeInTheDocument()
    expect(container.querySelector('.cmd-code__body code')?.textContent).toBe('line one\nline two')
  })

  it('copies a rendered table back out as Markdown, escaping a literal pipe', () => {
    const table = '| Name | Note |\n| --- | --- |\n| alpha | has \\| pipe |\n'
    render(<ChatMarkdown>{table}</ChatMarkdown>)
    fireEvent.click(screen.getByRole('button', { name: 'Copy table' }))
    expect(navigator.clipboard.writeText).toHaveBeenCalledWith('| Name | Note |\n| --- | --- |\n| alpha | has \\| pipe |')
  })

  it('opens a link in a new tab without a referrer', () => {
    render(<ChatMarkdown>{'[docs](https://example.com)'}</ChatMarkdown>)
    const link = screen.getByRole('link', { name: 'docs' })
    expect(link).toHaveAttribute('target', '_blank')
    expect(link).toHaveAttribute('rel', 'noreferrer')
  })

  it('strips a javascript: link rather than render it as a navigable href', () => {
    const { container } = render(<ChatMarkdown>{'[click me](javascript:alert(1))'}</ChatMarkdown>)
    expect(container.querySelector('a[href^="javascript:" i]')).toBeNull()
    expect(screen.getByText('click me')).toBeInTheDocument()
  })

  it('renders a generic type that looks like an HTML tag as literal text rather than dropping it', () => {
    const { container } = render(<ChatMarkdown>{'Returns Task<IActionResult> from the controller.'}</ChatMarkdown>)
    expect(container.textContent).toContain('Returns Task<IActionResult> from the controller.')
  })

  it('keeps an RTL-wrapped block of Arabic text visible rather than dropping it', () => {
    const block = '<div dir="rtl" lang="ar">\nمرحبا بالعميل\n</div>'
    const { container } = render(<ChatMarkdown>{block}</ChatMarkdown>)
    expect(container.textContent).toContain('مرحبا بالعميل')
  })

  it('keeps a one-line RTL div of Arabic text visible rather than dropping it', () => {
    const { container } = render(<ChatMarkdown>{'<div dir="rtl">مرحبا</div>'}</ChatMarkdown>)
    expect(container.textContent).toContain('مرحبا')
  })

  it('draws the reference override in place of plain inline code', () => {
    const components: Partial<Components> = {
      code: ({ children }) => <button type="button">{`ref:${children}`}</button>,
    }
    render(<ChatMarkdown components={components}>{'See `src/a.ts:12` for the check.'}</ChatMarkdown>)
    expect(screen.getByRole('button', { name: 'ref:src/a.ts:12' })).toBeInTheDocument()
  })

  it('turns a single newline into a hard line break', () => {
    const { container } = render(<ChatMarkdown>{'first line\nsecond line'}</ChatMarkdown>)
    expect(container.querySelector('br')).not.toBeNull()
  })
})
