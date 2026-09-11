// @vitest-environment jsdom
import { cleanup, render, screen } from '@testing-library/react'
import '@testing-library/jest-dom/vitest'
import { afterEach, describe, expect, it } from 'vitest'
import type { RunSummary, Ticket } from '../../api/types'
import RunCard from './RunCard'
import TicketCard from './TicketCard'

const ARABIC_TITLE = 'التصدير لا يعمل للطلبات الكبيرة'

const TICKET: Ticket = {
  key: 'OMNI-2510',
  title: ARABIC_TITLE,
  priority: 'high',
  status: 'Open',
  assignee: 'me',
  url: 'https://tracker.example/OMNI-2510',
  helpdeskRef: '88213',
  updatedAt: '2026-09-10T10:00:00Z',
}

const RUN: RunSummary = {
  runId: '20260910-1000-omni-2510',
  key: 'OMNI-2510',
  kind: 'triage',
  status: 'blocked',
  provider: 'claude',
  model: 'claude-haiku-4-5',
  startedAt: '2026-09-10T10:00:00Z',
  updatedAt: '2026-09-10T10:04:00Z',
  reason: 'asked: أي رقم عميل تقصد؟',
  usage: { turns: 3, inputTokens: 100, outputTokens: 10, costUsd: 0.01 },
  notes: [],
}

afterEach(cleanup)

/*
 * A board lane is a column of Latin keys and badges with a ticket title from
 * the tracker in whatever language it was filed in. `dir="auto"` puts each
 * title's own direction on the title alone, so an Arabic one reads correctly
 * without turning the card around it.
 */
describe('board cards', () => {
  it('marks a ticket title dir="auto"', () => {
    render(<TicketCard ticket={TICKET} onTriage={() => {}} />)
    expect(screen.getByText(ARABIC_TITLE)).toHaveAttribute('dir', 'auto')
  })

  it('marks a run title dir="auto"', () => {
    render(<RunCard run={RUN} title={ARABIC_TITLE} priority="high" onOpen={() => {}} />)
    expect(screen.getByText(ARABIC_TITLE)).toHaveAttribute('dir', 'auto')
  })

  it('marks the reason a run stopped dir="auto"; it can quote the customer', () => {
    render(<RunCard run={RUN} title={ARABIC_TITLE} priority="high" onOpen={() => {}} />)
    expect(screen.getByText(RUN.reason)).toHaveAttribute('dir', 'auto')
  })
})
