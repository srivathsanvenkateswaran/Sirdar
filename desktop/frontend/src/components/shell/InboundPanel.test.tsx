import { render, screen, within } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import type { InboundDelivery } from '../../store/appStore'
import InboundPanel from './InboundPanel'

function delivery(over: Partial<InboundDelivery> = {}): InboundDelivery {
  return {
    id: 1,
    source: 'jira',
    key: 'OMNI-2510',
    outcome: 'started',
    at: '2026-09-10T10:00:00Z',
    ...over,
  }
}

describe('InboundPanel', () => {
  it('says nothing has arrived rather than showing an empty list', () => {
    render(<InboundPanel deliveries={[]} />)
    expect(screen.getByText(/No webhook delivery has arrived/)).toBeInTheDocument()
    expect(screen.queryByRole('list')).toBeNull()
  })

  it('lists each delivery with its source, key, outcome and what that outcome meant', () => {
    render(
      <InboundPanel
        deliveries={[
          delivery({ id: 2, key: 'OMNI-2511', outcome: 'filtered' }),
          delivery({ id: 1, outcome: 'started' }),
        ]}
      />,
    )

    const rows = screen.getAllByRole('listitem')
    expect(rows).toHaveLength(2)
    expect(within(rows[0]).getByText('OMNI-2511')).toBeInTheDocument()
    expect(within(rows[0]).getByText('filtered')).toBeInTheDocument()
    expect(within(rows[0]).getByText("did not match this workspace's filter")).toBeInTheDocument()
    expect(within(rows[1]).getByText('started a triage')).toBeInTheDocument()
    expect(rows[0]).toHaveAttribute('data-outcome', 'filtered')
  })

  it('a delivery that named no ticket shows a dash rather than a blank column', () => {
    render(<InboundPanel deliveries={[delivery({ key: '', outcome: 'ignored' })]} />)
    const row = screen.getByRole('listitem')
    expect(within(row).getByText('—')).toBeInTheDocument()
    expect(within(row).getByText('named no ticket')).toBeInTheDocument()
  })

  it('counts the deliveries in the heading', () => {
    render(
      <InboundPanel
        deliveries={[delivery({ id: 1 }), delivery({ id: 2 }), delivery({ id: 3 })]}
      />,
    )
    expect(within(screen.getByRole('heading', { name: /Inbound/ })).getByText('3')).toBeInTheDocument()
  })
})
