import { render, screen, within } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import type { QueuedSteer } from '../../api/types'
import QueuedSteers, { queuedSteerWord, visibleQueuedSteers } from './QueuedSteers'

const q = (id: string, status: QueuedSteer['status'], over: Partial<QueuedSteer> = {}): QueuedSteer => ({
  id,
  at: '2026-10-01T09:00:00Z',
  text: `steer ${id}`,
  status,
  ...over,
})

describe('QueuedSteers', () => {
  it('says what became of each steer in words', () => {
    expect(queuedSteerWord(q('a', 'queued'))).toBe('queued for the next turn')
    expect(queuedSteerWord(q('a', 'delivered', { turn: 7 }))).toBe('delivered at turn 7')
    expect(queuedSteerWord(q('a', 'held'))).toBe('waiting for the run to finish')
    expect(queuedSteerWord(q('a', 'dropped', { reason: 'the run was stopped' }))).toBe('not delivered: the run was stopped')
  })

  it('shows the waiting ones, the undelivered ones, and the delivered ones only while the run works — the newest three', () => {
    const all = [q('a', 'applied'), q('b', 'delivered', { turn: 2 }), q('c', 'held'), q('d', 'dropped'), q('e', 'queued')]
    expect(visibleQueuedSteers(all, true).map((x) => x.id)).toEqual(['c', 'd', 'e'])
    expect(visibleQueuedSteers(all.slice(0, 3), true).map((x) => x.id)).toEqual(['b', 'c'])
    expect(visibleQueuedSteers(all.slice(0, 3), false).map((x) => x.id)).toEqual(['c'])
    expect(visibleQueuedSteers(undefined, true)).toEqual([])
  })

  it('draws a labelled list whose text takes its own direction, and nothing when empty', () => {
    const { container, rerender } = render(<QueuedSteers items={[q('a', 'held', { text: 'تحقق من التصدير' })]} />)
    const list = screen.getByRole('list', { name: 'Queued steers' })
    const text = within(list).getByText('تحقق من التصدير')
    expect(text).toHaveAttribute('dir', 'auto')
    expect(within(list).getByText('waiting for the run to finish')).toBeInTheDocument()
    rerender(<QueuedSteers items={[]} />)
    expect(container).toBeEmptyDOMElement()
  })
})
