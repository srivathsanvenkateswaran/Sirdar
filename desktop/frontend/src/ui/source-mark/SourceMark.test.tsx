import { render, screen } from '@testing-library/react'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'
import SourceMark, { MARKED_SOURCES, SOURCE_NAMES, sourceInitials, sourceName } from './index'

describe('SourceMark', () => {
  it('names the product, not the config adapter', () => {
    render(<SourceMark adapter="zohodesk" />)
    expect(screen.getByRole('img', { name: 'Zoho Desk' })).toBeInTheDocument()
    expect(sourceName('azdo')).toBe('Azure DevOps')
    expect(sourceName('exec', 'Janus')).toBe('Janus')
    // An exec adapter with no name yet reads as the adapter, never blank.
    expect(sourceName('exec')).toBe('exec')
  })

  it('draws the mark on the brand colour where the product has one', () => {
    const { container } = render(<SourceMark adapter="jira" />)
    const tile = container.querySelector('.sd-source-mark') as HTMLElement
    expect(tile).toHaveAttribute('data-branded', 'true')
    expect(tile.style.getPropertyValue('--sd-source-tile')).toBe('#0052CC')
    expect(tile.querySelector('svg path')).not.toBeNull()
    expect(tile.querySelector('svg')).toHaveAttribute('aria-hidden', 'true')
  })

  it('falls back to initials on the ink for a source with no public mark', () => {
    const { container, rerender } = render(<SourceMark adapter="azdo" />)
    const tile = container.querySelector('.sd-source-mark') as HTMLElement
    expect(tile).not.toHaveAttribute('data-branded')
    expect(tile.style.getPropertyValue('--sd-source-tile')).toBe('')
    expect(tile.querySelector('svg')).toBeNull()
    expect(tile).toHaveTextContent('AD')
    expect(screen.getByRole('img', { name: 'Azure DevOps' })).toBeInTheDocument()

    // A private exec adapter is named by the config summary.
    rerender(<SourceMark adapter="exec" name="Janus" />)
    expect(screen.getByRole('img', { name: 'Janus' })).toHaveTextContent('JA')
  })

  it('cuts two letters from a name', () => {
    expect(sourceInitials('Azure DevOps')).toBe('AD')
    expect(sourceInitials('ServiceNow')).toBe('SN')
    expect(sourceInitials('Janus')).toBe('JA')
    expect(sourceInitials('help-desk')).toBe('HD')
    expect(sourceInitials('  ')).toBe('?')
  })

  it.each(['xs', 'sm', 'md', 'lg'] as const)('sizes the tile %s', (size) => {
    const { container } = render(<SourceMark adapter="linear" size={size} />)
    expect(container.querySelector('.sd-source-mark')).toHaveAttribute('data-size', size)
  })

  it('knows every built-in adapter by its product name', () => {
    for (const adapter of [
      'jira',
      'linear',
      'azdo',
      'rally',
      'servicenow',
      'zohodesk',
      'zendesk',
      'freshdesk',
      'helpscout',
      'intercom',
      'hubspot',
      'front',
      'gorgias',
    ]) {
      expect(SOURCE_NAMES[adapter], adapter).toBeTruthy()
    }
  })

  it('carries each mark Simple Icons had, with its own path', () => {
    const marks = resolve(
      process.cwd(),
      '..',
      '..',
      'docs',
      'design',
      '2026-09-15-screens',
      'marks',
      'sources',
    )
    expect([...MARKED_SOURCES].sort()).toEqual([
      'helpscout',
      'hubspot',
      'intercom',
      'jira',
      'linear',
      'zendesk',
      'zohodesk',
    ])
    for (const adapter of MARKED_SOURCES) {
      const svg = readFileSync(resolve(marks, `${adapter}.svg`), 'utf8')
      const d = /<path[^>]*\sd="([^"]+)"/.exec(svg)?.[1]
      expect(d, `${adapter}.svg has a path`).toBeTruthy()
      const { container, unmount } = render(<SourceMark adapter={adapter} />)
      expect(container.querySelector('svg path')?.getAttribute('d')).toBe(d)
      unmount()
    }
  })
})
