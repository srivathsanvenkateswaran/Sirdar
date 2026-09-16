import { fireEvent, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { PrimaryActionProvider } from '../components/shell/primaryAction'
import { resetRunJobs } from '../lib/jobs'
import { resetSessionLayout, setSessionLayout } from '../lib/sessionLayout'
import { TRIAGE_RUN_ID, triageFixture } from '../store/fakeSession'
import { createFakeTransport } from '../store/fakeTransport'
import Session from './Session'

/*
 * The Session dispatches on the layout preference. Conversation, the
 * default, is `screens/session/SessionConversation` and has its own suite;
 * Document is `screens/session/SessionDocument`, tested scene by scene in
 * its own file; Workbench renders the Document layout until its own lands.
 */

function mount() {
  const transport = createFakeTransport({ sessions: { [TRIAGE_RUN_ID]: triageFixture() } })
  return render(
    <PrimaryActionProvider>
      <Session transport={transport} workspaceId="ws1" runId={TRIAGE_RUN_ID} onBack={() => {}} onOpenReview={() => {}} />
    </PrimaryActionProvider>,
  )
}

describe('the layout dispatch', () => {
  beforeEach(() => {
    resetRunJobs()
    localStorage.clear()
    resetSessionLayout()
  })
  afterEach(() => {
    localStorage.clear()
    resetSessionLayout()
  })

  it('draws the Conversation layout by default', async () => {
    const { container } = mount()
    await screen.findByRole('heading', { name: 'SBX-1' })
    expect(container.querySelector('.sc[data-layout="conversation"]')).not.toBeNull()
    expect(screen.queryByTestId('session-document')).toBeNull()
  })

  it('draws the Document layout when chosen', async () => {
    setSessionLayout('document')
    const { container } = mount()
    await screen.findByRole('heading', { name: 'SBX-1' })
    expect(screen.getByTestId('session-document')).toBeInTheDocument()
    expect(container.querySelector('[data-layout="conversation"]')).toBeNull()
  })

  it('draws the Workbench layout when chosen', async () => {
    setSessionLayout('workbench')
    mount()
    await screen.findByRole('heading', { name: 'SBX-1' })
    expect(screen.getByTestId('session-workbench')).toBeInTheDocument()
    expect(screen.queryByTestId('session-document')).toBeNull()
  })

  it('the header switcher moves between the layouts and writes the preference', async () => {
    setSessionLayout('document')
    mount()
    await screen.findByRole('heading', { name: 'SBX-1' })
    expect(screen.getByRole('radio', { name: 'Document' })).toBeChecked()
    fireEvent.click(screen.getByRole('radio', { name: 'Conversation' }))
    expect(localStorage.getItem('sirdar.sessionLayout')).toBe('conversation')
    await screen.findByRole('heading', { name: 'SBX-1' })
    expect(screen.queryByTestId('session-document')).toBeNull()
    expect(screen.getByRole('radio', { name: 'Conversation' })).toBeChecked()
  })
})
