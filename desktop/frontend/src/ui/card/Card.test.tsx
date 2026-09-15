import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import Card from './index'

describe('Card', () => {
  it('is a plain box when nothing opens from it', () => {
    const { container } = render(<Card title="Doctor">Two checks failed.</Card>)
    expect(container.querySelector('button')).toBeNull()
    expect(screen.getByText('Doctor')).toBeInTheDocument()
  })

  it('renders as one button, one tab stop, when it opens something', () => {
    const onOpen = vi.fn()
    render(<Card title="OMNI-2510" openLabel="Open OMNI-2510" onOpen={onOpen} />)
    const card = screen.getByRole('button', { name: 'Open OMNI-2510' })
    card.focus()
    expect(card).toHaveFocus()
    fireEvent.click(card)
    expect(onOpen).toHaveBeenCalledOnce()
  })

  it('paints a leading edge only for a state that is a fact', () => {
    const { container, rerender } = render(<Card title="a" />)
    expect(container.querySelector('.sd-card')).toHaveAttribute('data-tone', 'plain')
    for (const tone of ['live', 'blocked', 'failed'] as const) {
      rerender(<Card title="a" tone={tone} />)
      expect(container.querySelector('.sd-card')).toHaveAttribute('data-tone', tone)
    }
  })

  it('shows head, body and foot when it is given all three', () => {
    render(
      <Card title="OMNI-2510" meta="4m 12s" footer={<span>Completed</span>}>
        The statement export times out.
      </Card>,
    )
    expect(screen.getByText('4m 12s')).toBeInTheDocument()
    expect(screen.getByText('The statement export times out.')).toBeInTheDocument()
    expect(screen.getByText('Completed')).toBeInTheDocument()
  })

  it('lets a bilingual title resolve its own direction', () => {
    render(
      <Card title="العميل لا يستطيع تصدير كشف الحساب" dir="auto" meta="OMNI-2511">
        The customer cannot export a statement.
      </Card>,
    )
    expect(screen.getByText('العميل لا يستطيع تصدير كشف الحساب')).toHaveAttribute('dir', 'auto')
  })
})
