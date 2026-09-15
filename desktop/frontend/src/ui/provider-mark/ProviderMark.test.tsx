import { render, screen } from '@testing-library/react'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'
import ProviderMark, { MARKED_PROVIDERS, providerName } from './index'

describe('ProviderMark', () => {
  it('names the vendor, not the config id', () => {
    render(<ProviderMark provider="claude" />)
    expect(screen.getByRole('img', { name: 'Claude' })).toBeInTheDocument()
    expect(providerName('agy')).toBe('Antigravity')
    expect(providerName('copilot')).toBe('GitHub Copilot')
  })

  it('draws the mark on the brand colour where the vendor has one, and on the ink otherwise', () => {
    const { container, rerender } = render(<ProviderMark provider="claude" />)
    const claude = container.querySelector('.sd-mark') as HTMLElement
    expect(claude).toHaveAttribute('data-branded', 'true')
    expect(claude.style.getPropertyValue('--sd-mark-tile')).toBe('#D97757')

    rerender(<ProviderMark provider="codex" />)
    const codex = container.querySelector('.sd-mark') as HTMLElement
    expect(codex).not.toHaveAttribute('data-branded')
    expect(codex.style.getPropertyValue('--sd-mark-tile')).toBe('')
    expect(codex.querySelector('svg path')).not.toBeNull()
  })

  it('resolves the config alias for Antigravity', () => {
    const { container } = render(<ProviderMark provider="agy" />)
    expect(container.querySelector('.sd-mark')).toHaveAttribute('data-provider', 'antigravity')
    expect(screen.getByRole('img', { name: 'Antigravity' })).toBeInTheDocument()
  })

  it('falls back to two letters of the name for a provider with no mark', () => {
    render(<ProviderMark provider="acp" />)
    const tile = screen.getByRole('img', { name: 'acp' })
    expect(tile.querySelector('svg')).toBeNull()
    expect(tile).toHaveTextContent('AC')
  })

  it.each(['sm', 'md', 'lg'] as const)('sizes the tile %s', (size) => {
    const { container } = render(<ProviderMark provider="qwen" size={size} />)
    expect(container.querySelector('.sd-mark')).toHaveAttribute('data-size', size)
  })

  it('hides the drawing from the accessibility tree, so the name is read once', () => {
    const { container } = render(<ProviderMark provider="cursor" />)
    expect(container.querySelector('svg')).toHaveAttribute('aria-hidden', 'true')
  })

  it('carries every mark the screens round shipped, with its own path', () => {
    const marks = resolve(process.cwd(), '..', '..', 'docs', 'design', '2026-09-15-screens', 'marks')
    for (const vendor of [
      'antigravity',
      'claude',
      'codex',
      'copilot',
      'cursor',
      'gemini',
      'kimi',
      'openai',
      'opencode',
      'qwen',
    ]) {
      expect(MARKED_PROVIDERS).toContain(vendor)
      const svg = readFileSync(resolve(marks, `${vendor}.svg`), 'utf8')
      const d = /<path[^>]*\sd="([^"]+)"/.exec(svg)?.[1]
      expect(d, `${vendor}.svg has a path`).toBeTruthy()
      const { container, unmount } = render(<ProviderMark provider={vendor} />)
      expect(container.querySelector('svg path')?.getAttribute('d')).toBe(d)
      unmount()
    }
  })
})
