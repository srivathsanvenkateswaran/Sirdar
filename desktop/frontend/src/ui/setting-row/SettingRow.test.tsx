import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import SettingRow, { SettingCard } from './index'

describe('SettingRow', () => {
  it('prints the label and what the setting is set to now', () => {
    render(<SettingRow label="Provider" value="claude / sonnet" />)
    expect(screen.getByText('Provider')).toBeInTheDocument()
    expect(screen.getByText('claude / sonnet')).toBeInTheDocument()
  })

  it('leaves no blank line where an absent value would be', () => {
    const { container } = render(<SettingRow label="Provider" />)
    expect(container.querySelector('.sd-setting-row__value')).toBeNull()
  })

  it('lays the value out from its own first letter, so an Arabic path reads', () => {
    const { container } = render(<SettingRow label="Notes" value="ملاحظات/الدعم" />)
    expect(container.querySelector('.sd-setting-row__value')).toHaveAttribute('dir', 'auto')
  })

  it('holds the one control it was given', () => {
    render(
      <SettingRow
        label="Workspace"
        value="/work/omni"
        control={
          <button type="button" className="sd-setting-button">
            Change workspace
          </button>
        }
      />,
    )
    expect(screen.getByRole('button', { name: 'Change workspace' })).toBeInTheDocument()
  })

  it('divides rows after the first, and not after the last', () => {
    const { container } = render(
      <SettingCard heading="Workspace">
        <SettingRow label="One" />
        <SettingRow label="Two" />
        <SettingRow label="Three" />
      </SettingCard>,
    )
    // The divider is a `+` rule, so it lands on rows two and three and never
    // trails the card.
    const rows = container.querySelectorAll('.sd-setting-row')
    expect(rows).toHaveLength(3)
    expect(container.querySelector('.sd-setting-card__heading')?.textContent).toBe('Workspace')
  })
})
