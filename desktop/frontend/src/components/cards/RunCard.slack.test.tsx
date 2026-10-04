// @vitest-environment jsdom
import { cleanup, render, screen } from '@testing-library/react'
import '@testing-library/jest-dom/vitest'
import { afterEach, describe, expect, it } from 'vitest'
import type { RunSummary } from '../../api/types'
import RunCard from './RunCard'

const RUN: RunSummary = {
  runId: '20261004-0950-slack-d0fakedm01-1791100254',
  key: 'SLACK-D0FAKEDM01-1791100254',
  title: 'إجمالي القسيمة خاطئ في الإيصال',
  kind: 'triage',
  status: 'completed',
  provider: 'claude',
  model: 'claude-fake',
  startedAt: '2026-10-04T09:50:00Z',
  updatedAt: '2026-10-04T09:58:00Z',
  reason: '',
  usage: { turns: 9, inputTokens: 100, outputTokens: 10, costUsd: 0.02 },
  notes: [],
  source: 'slack',
}

afterEach(cleanup)

describe('a run triaged from a Slack thread with no ticket', () => {
  it('shows the Slack mark before its key, with the whole key in the tooltip', () => {
    const { container } = render(<RunCard run={RUN} title={RUN.title} onOpen={() => {}} />)
    expect(screen.getByRole('img', { name: 'Slack' })).toBeInTheDocument()
    const key = container.querySelector('.sd-run-card__key') as HTMLElement
    expect(key).toHaveTextContent('SLACK-D0FAKEDM01-1791100254')
    expect(key).toHaveAttribute('dir', 'ltr')
    expect(key).toHaveAttribute('title', 'Slack thread SLACK-D0FAKEDM01-1791100254')
    expect(container.querySelector('.sd-run-card__title')).toHaveAttribute('dir', 'auto')
  })

  it('draws no source mark on an ordinary ticket', () => {
    const { container } = render(
      <RunCard run={{ ...RUN, key: 'SBX-1', source: undefined }} title="Export fails" onOpen={() => {}} />,
    )
    expect(container.querySelector('.sd-source-mark')).toBeNull()
  })
})
