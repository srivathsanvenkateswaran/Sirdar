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

  it('carries a link to where the outcome can be read, and follows it', () => {
    const onLink = vi.fn()
    render(
      <Toasts
        toasts={[{ id: 4, tone: 'info', text: 'Suggested an addition.', link: { label: 'Open Playbooks' } }]}
        onDismiss={() => {}}
        onLink={onLink}
      />,
    )
    fireEvent.click(screen.getByRole('button', { name: 'Open Playbooks' }))
    expect(onLink).toHaveBeenCalledWith(4)
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

  it('holds still while the reader is on it, and restarts its time on release', () => {
    const onDismiss = vi.fn()
    render(
      <Toasts
        toasts={[{ id: 6, tone: 'info', text: 'Suggested an addition.', link: { label: 'Open Playbooks' } }]}
        onDismiss={onDismiss}
        onLink={() => {}}
      />,
    )
    const link = screen.getByRole('button', { name: 'Open Playbooks' })
    act(() => link.focus())
    act(() => vi.advanceTimersByTime(8000))
    expect(onDismiss).not.toHaveBeenCalled()
    expect(screen.getByText('Suggested an addition.')).toBeInTheDocument()

    act(() => link.blur())
    act(() => vi.advanceTimersByTime(6999))
    expect(onDismiss).not.toHaveBeenCalled()
    act(() => vi.advanceTimersByTime(1))
    expect(onDismiss).toHaveBeenCalledWith(6)
  })

  it('holds still under the pointer too', () => {
    const onDismiss = vi.fn()
    const { container } = render(
      <Toasts toasts={[{ id: 7, tone: 'info', text: 'Done.' }]} onDismiss={onDismiss} dismissAfterMs={500} />,
    )
    const row = container.querySelector('.sd-toast') as HTMLElement
    fireEvent.mouseEnter(row)
    act(() => vi.advanceTimersByTime(2000))
    expect(onDismiss).not.toHaveBeenCalled()
    fireEvent.mouseLeave(row)
    act(() => vi.advanceTimersByTime(500))
    expect(onDismiss).toHaveBeenCalledWith(7)
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
