import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import SidebarFooterCard from './index'

describe('SidebarFooterCard', () => {
  it('names itself so the pinned block is not a run of loose controls', () => {
    render(<SidebarFooterCard switcher={<button type="button">omni</button>} />)
    expect(screen.getByRole('group', { name: 'Workspace' })).toBeInTheDocument()
  })

  it('draws the three slots in order: switcher, quotas, action', () => {
    const { container } = render(
      <SidebarFooterCard
        switcher={<button type="button">omni</button>}
        quotas={<span>claude 62%</span>}
        action={<button type="button">New triage</button>}
      />,
    )
    const parts = [...container.querySelectorAll('.sd-sidebar-foot > *')].map((el) => el.className)
    expect(parts).toEqual([
      'sd-sidebar-foot__switcher',
      'sd-sidebar-foot__quotas',
      'sd-sidebar-foot__action',
    ])
  })

  it('draws nothing for a slot it was given nothing for', () => {
    const { container } = render(<SidebarFooterCard switcher={<span>omni</span>} />)
    expect(container.querySelector('.sd-sidebar-foot__quotas')).toBeNull()
    expect(container.querySelector('.sd-sidebar-foot__action')).toBeNull()
  })
})
