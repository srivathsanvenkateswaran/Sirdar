import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import CodeBlock, { COLLAPSE_CHARS, COLLAPSE_LINES } from './CodeBlock'

vi.mock('./shikiHighlighter', () => ({ highlightCode: () => Promise.resolve(null) }))

// The 2026-10-05 OMNI-3420 reply carried a 44-line JSON block whose longest
// line was 842 characters; it filled the screen and scrolled sideways.
describe('CodeBlock', () => {
  const lines = (n: number) => Array.from({ length: n }, (_, i) => `line ${i + 1}`).join('\n')

  it('shows a long block folded to its first lines, with a way to see all of it', () => {
    render(<CodeBlock code={lines(44)} language="json" />)
    const body = screen.getByTestId('code-block')
    expect(body).toHaveAttribute('data-folded')
    expect(body.textContent).toContain(`line ${COLLAPSE_LINES}`)
    expect(body.textContent).not.toContain(`line ${COLLAPSE_LINES + 1}`)
    fireEvent.click(screen.getByRole('button', { name: 'Show all 44 lines' }))
    expect(body).not.toHaveAttribute('data-folded')
    expect(body.textContent).toContain('line 44')
    fireEvent.click(screen.getByRole('button', { name: 'Show less' }))
    expect(body.textContent).not.toContain('line 44')
  })

  it('folds a block of a few very long lines, which wrap into a tall block', () => {
    const long = 'x'.repeat(COLLAPSE_CHARS + 1)
    render(<CodeBlock code={`{\n${long}\n}`} language="json" />)
    expect(screen.getByTestId('code-block')).toHaveAttribute('data-folded')
    expect(screen.getByRole('button', { name: 'Show all 3 lines' })).toBeInTheDocument()
  })

  it('leaves a short block whole, with no toggle', () => {
    render(<CodeBlock code={lines(COLLAPSE_LINES)} language="json" />)
    expect(screen.getByTestId('code-block')).not.toHaveAttribute('data-folded')
    expect(screen.queryByRole('button', { name: /Show all/ })).toBeNull()
  })

  it('copies the whole block even while it is folded', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined)
    Object.assign(navigator, { clipboard: { writeText } })
    render(<CodeBlock code={lines(44)} language="json" />)
    fireEvent.click(screen.getByRole('button', { name: /Copy/ }))
    expect(writeText).toHaveBeenCalledWith(lines(44))
  })
})
