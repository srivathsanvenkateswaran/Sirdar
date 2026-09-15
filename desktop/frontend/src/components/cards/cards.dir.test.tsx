// @vitest-environment jsdom
import { cleanup, render, screen } from '@testing-library/react'
import '@testing-library/jest-dom/vitest'
import { afterEach, describe, expect, it } from 'vitest'
import type { RunSummary } from '../../api/types'
import RunCard from './RunCard'

const ARABIC_TITLE = 'التصدير لا يعمل للطلبات الكبيرة'

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
 * without turning the card around it. A queued ticket is drawn by the
 * library's run card straight from the board, so this is the one wrapper.
 */
describe('board cards', () => {
  it('marks a run title dir="auto"', () => {
    render(<RunCard run={RUN} title={ARABIC_TITLE} onOpen={() => {}} />)
    expect(screen.getByText(ARABIC_TITLE)).toHaveAttribute('dir', 'auto')
  })

  // The reason can quote the customer, so it stays off the card: it is the
  // session's banner, laid out with its own direction there.
  it('keeps the reason a run stopped off the board', () => {
    render(<RunCard run={RUN} title={ARABIC_TITLE} onOpen={() => {}} />)
    expect(screen.queryByText(RUN.reason)).toBeNull()
    expect(screen.getByText('blocked')).toBeInTheDocument()
  })
})
