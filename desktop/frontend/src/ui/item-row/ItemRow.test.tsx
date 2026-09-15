import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import ItemRow from './index'

describe('ItemRow', () => {
  it('lays out a title over its facts, with an action at the end', () => {
    render(
      <ItemRow
        title="Reorder reminder fires twice"
        meta="SBX-4 · zendesk · started"
        action={<button type="button">Triage</button>}
      />,
    )
    expect(screen.getByText('Reorder reminder fires twice')).toBeInTheDocument()
    expect(screen.getByText('SBX-4 · zendesk · started')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Triage' })).toBeInTheDocument()
  })

  it.each(['plain', 'live', 'blocked', 'done', 'failed'] as const)('marks the %s tone', (tone) => {
    const { container } = render(<ItemRow title="x" tone={tone} />)
    expect(container.querySelector('.sd-item')).toHaveAttribute('data-tone', tone)
  })

  it('opens as one control, and keeps its action a separate one', () => {
    const onOpen = vi.fn()
    const onTriage = vi.fn()
    render(
      <ItemRow
        title="Monthly statement shows a paid invoice as overdue"
        openLabel="Open SBX-7"
        onOpen={onOpen}
        action={
          <button type="button" onClick={onTriage}>
            Triage
          </button>
        }
      />,
    )
    fireEvent.click(screen.getByRole('button', { name: 'Open SBX-7' }))
    expect(onOpen).toHaveBeenCalledOnce()
    fireEvent.click(screen.getByRole('button', { name: 'Triage' }))
    expect(onTriage).toHaveBeenCalledOnce()
    expect(onOpen).toHaveBeenCalledOnce()
  })

  it('lets an Arabic title lay itself out, and hides the icon from the tree', () => {
    const { container } = render(
      <ItemRow title="العميل لا يستطيع تصدير كشف الحساب" dir="auto" icon={<svg />} wrap />,
    )
    const title = screen.getByText('العميل لا يستطيع تصدير كشف الحساب')
    expect(title).toHaveAttribute('dir', 'auto')
    expect(title).toHaveAttribute('data-wrap', 'true')
    expect(container.querySelector('.sd-item__icon')).toHaveAttribute('aria-hidden', 'true')
  })
})
