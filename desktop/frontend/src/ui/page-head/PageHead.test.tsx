import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import PageHead from './index'

describe('PageHead', () => {
  it('is an h1 with the screen name, its lede and its actions', () => {
    render(
      <PageHead
        title="Register"
        lede="Every run, newest first."
        actions={<button type="button">Export CSV</button>}
      />,
    )
    expect(screen.getByRole('heading', { level: 1, name: 'Register' })).toBeInTheDocument()
    expect(screen.getByText('Every run, newest first.')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Export CSV' })).toBeInTheDocument()
  })

  it('drops to h2 inside a modal, in the serif when asked', () => {
    render(<PageHead title="MCP servers" level={2} serif id="page-title" />)
    const heading = screen.getByRole('heading', { level: 2, name: 'MCP servers' })
    expect(heading).toHaveAttribute('data-serif', 'true')
    expect(heading).toHaveAttribute('id', 'page-title')
  })

  it('is the sans by default', () => {
    render(<PageHead title="اللوحة" />)
    expect(screen.getByRole('heading', { name: 'اللوحة' })).not.toHaveAttribute('data-serif')
  })
})
