import { render, screen } from '@testing-library/react'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'
import BrandMark from './index'

const FINAL = resolve(process.cwd(), '..', '..', 'docs', 'design', '2026-09-16-logo', 'final')

/** The fills and strokes of one master SVG, in document order. */
function paint(file: string): string[] {
  const svg = readFileSync(resolve(FINAL, file), 'utf8')
  return [...svg.matchAll(/(?:fill|stroke)="(#[0-9A-Fa-f]{6})"/g)].map((m) => m[1])
}

/** The same, read off one rendered variant. */
function painted(group: Element): string[] {
  return [...group.querySelectorAll('path, circle')].flatMap((node) =>
    ['fill', 'stroke']
      .map((name) => node.getAttribute(name))
      .filter((value): value is string => !!value && value.startsWith('#')),
  )
}

function group(container: HTMLElement, variant: 'light' | 'dark'): Element {
  const found = container.querySelector(`[data-variant="${variant}"]`)
  expect(found, `the ${variant} variant is drawn`).not.toBeNull()
  return found as Element
}

describe('BrandMark', () => {
  it('is named for the product', () => {
    render(<BrandMark />)
    expect(screen.getByRole('img', { name: 'Sirdar' })).toBeInTheDocument()
  })

  it('goes silent beside a word that already says it', () => {
    const { container } = render(<BrandMark decorative />)
    expect(screen.queryByRole('img')).toBeNull()
    expect(container.querySelector('.sd-brand-mark')).toHaveAttribute('aria-hidden', 'true')
  })

  it('draws both variants and leaves the choice to the theme', () => {
    // The theme switches them through `--sd-brand-light` / `--sd-brand-dark`
    // in BrandMark.css, which is what lets the gallery's dark specimen frame
    // hold a dark mark inside a light page. Both are in the markup either way.
    const { container } = render(<BrandMark />)
    expect(container.querySelectorAll('[data-variant]')).toHaveLength(2)
    group(container, 'light')
    group(container, 'dark')
  })

  it('swaps blue for white between the two, and keeps the crimson', () => {
    const { container } = render(<BrandMark />)
    expect(painted(group(container, 'light'))).toEqual([
      '#003893',
      '#DC143C',
      '#FFFFFF',
      '#FFFFFF',
    ])
    expect(painted(group(container, 'dark'))).toEqual(['#FAF7EC', '#DC143C', '#003893', '#003893'])
  })

  it('draws the mark the logo round settled on, not a second copy of it', () => {
    // The masters in docs/design/2026-09-16-logo/final/ are what the app icon,
    // the favicons and the landing page are cut from. If this component's
    // geometry or colour ever drifts from them, the app wears a logo nothing
    // else does.
    const { container } = render(<BrandMark />)
    for (const variant of ['light', 'dark'] as const) {
      const master = readFileSync(resolve(FINAL, `sirdar-mark-${variant}.svg`), 'utf8')
      const shapes = [...master.matchAll(/\sd="([^"]+)"/g)].map((m) => m[1])
      const drawn = [...group(container, variant).querySelectorAll('path')].map((node) =>
        node.getAttribute('d'),
      )
      expect(drawn, `${variant} geometry`).toEqual(shapes)
      expect(painted(group(container, variant)), `${variant} colour`).toEqual(
        paint(`sirdar-mark-${variant}.svg`),
      )
    }
  })

  it.each(['sm', 'md', 'lg'] as const)('sizes the box %s', (size) => {
    const { container } = render(<BrandMark size={size} />)
    expect(container.querySelector('.sd-brand-mark')).toHaveAttribute('data-size', size)
  })

  it('hides the drawing from the accessibility tree, so the name is read once', () => {
    const { container } = render(<BrandMark />)
    expect(container.querySelector('svg')).toHaveAttribute('aria-hidden', 'true')
  })
})
