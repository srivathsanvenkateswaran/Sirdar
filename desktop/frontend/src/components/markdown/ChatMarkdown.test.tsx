import { fireEvent, render, screen } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import ChatMarkdown, { type Components } from './ChatMarkdown'

/**
 * Shiki is loaded dynamically the first time a code block mounts; a test
 * never wants the real highlighter, which would pull in its wasm grammar
 * and run past a unit test's patience. The mock resolves the way the real
 * package does, so `CodeBlock`'s loading branch runs the same code path.
 */
vi.mock('shiki', () => ({
  getSingletonHighlighter: vi.fn().mockResolvedValue({
    getLoadedLanguages: () => ['ts'],
    loadLanguage: vi.fn().mockResolvedValue(undefined),
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

  it('renders a fenced code block with its language and a Copy button that copies the raw code', async () => {
    const fence = '```ts\nconst answer = 42\n```\n'
    render(<ChatMarkdown>{fence}</ChatMarkdown>)
    expect(screen.getByText('ts')).toBeInTheDocument()
    const button = screen.getByRole('button', { name: 'Copy' })
    fireEvent.click(button)
    expect(navigator.clipboard.writeText).toHaveBeenCalledWith('const answer = 42')
  })

  it('does not render a script tag embedded in the markdown source', () => {
    const { container } = render(<ChatMarkdown>{'before<script>window.pwned = true</script>after'}</ChatMarkdown>)
    expect(container.querySelector('script')).toBeNull()
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
