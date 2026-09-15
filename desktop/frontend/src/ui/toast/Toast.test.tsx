import { act, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import Toasts from './index'

beforeEach(() => vi.useFakeTimers())
afterEach(() => vi.useRealTimers())

describe('Toasts', () => {
  it('renders nothing at all when there is nothing to report', () => {
    const { container } = render(<Toasts toasts={[]} onDismiss={() => {}} />)
    expect(container.firstChild).toBeNull()
  })

  it('is a polite live region, so it never interrupts', () => {
    render(
      <Toasts toasts={[{ id: 1, tone: 'info', text: 'Triage started.' }]} onDismiss={() => {}} />,
    )
    const list = screen.getByRole('status')
    expect(list).toHaveAttribute('aria-live', 'polite')
    expect(list).toHaveTextContent('Triage started.')
  })

  it('marks an error with the failed hue and the word that came with it', () => {
    const { container } = render(
      <Toasts
        toasts={[{ id: 2, tone: 'error', text: 'Add a workspace before starting a run.' }]}
        onDismiss={() => {}}
      />,
    )
    expect(container.querySelector('.sd-toast')).toHaveAttribute('data-tone', 'error')
  })

  it('dismisses on click', () => {
    const onDismiss = vi.fn()
    render(<Toasts toasts={[{ id: 3, tone: 'info', text: 'Done.' }]} onDismiss={onDismiss} />)
    const dismiss = screen.getByRole('button', { name: 'Dismiss' })
    dismiss.focus()
    expect(dismiss).toHaveFocus()
    fireEvent.click(dismiss)
    expect(onDismiss).toHaveBeenCalledWith(3)
  })

  it('dismisses itself after its own time is up', () => {
    const onDismiss = vi.fn()
    render(
      <Toasts
        toasts={[{ id: 4, tone: 'info', text: 'Fix opened a pull request.' }]}
        onDismiss={onDismiss}
        dismissAfterMs={500}
      />,
    )
    expect(onDismiss).not.toHaveBeenCalled()
    act(() => vi.advanceTimersByTime(500))
    expect(onDismiss).toHaveBeenCalledWith(4)
  })

  it('lets an Arabic message lay itself out', () => {
    render(
      <Toasts
        toasts={[{ id: 5, tone: 'error', text: 'تعذّر بدء التشغيل: لا توجد مساحة عمل.' }]}
        onDismiss={() => {}}
      />,
    )
    expect(screen.getByText('تعذّر بدء التشغيل: لا توجد مساحة عمل.')).toHaveAttribute('dir', 'auto')
  })
})
