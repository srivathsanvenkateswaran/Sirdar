import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import GroupLabel from './index'

describe('GroupLabel', () => {
  it('is a paragraph with the dashed rule by default', () => {
    render(<GroupLabel>Landed today</GroupLabel>)
    const label = screen.getByText('Landed today')
    expect(label.tagName).toBe('P')
    expect(label).toHaveAttribute('data-rule', 'true')
  })

  it('can head a section, and can go without the rule', () => {
    render(
      <GroupLabel as="h2" rule={false} id="workspace">
        Workspace
      </GroupLabel>,
    )
    const heading = screen.getByRole('heading', { name: 'Workspace' })
    expect(heading).toHaveAttribute('id', 'workspace')
    expect(heading).not.toHaveAttribute('data-rule')
  })

  it('carries Arabic', () => {
    render(<GroupLabel>وصل اليوم</GroupLabel>)
    expect(screen.getByText('وصل اليوم')).toBeInTheDocument()
  })
})
