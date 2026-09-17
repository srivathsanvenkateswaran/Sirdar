import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { PrimaryActionProvider } from '../components/shell/primaryAction'
import { resetRunJobs } from '../lib/jobs'
import { renderCount, resetRenderCounts } from '../lib/renderProbe'
import { resetSessionLayout, setSessionLayout } from '../lib/sessionLayout'
import { steerEvent, TRIAGE_RUN_ID, triageFixture } from '../store/fakeSession'
import { createFakeTransport, type FakeTransport } from '../store/fakeTransport'
import Session from './Session'

/*
 * The Session dispatches on the layout preference. Conversation, the
 * default, is `screens/session/SessionConversation` and has its own suite;
 * Document is `screens/session/SessionDocument`, tested scene by scene in
 * its own file; Workbench renders the Document layout until its own lands.
 */

function mount(): ReturnType<typeof render> & { transport: FakeTransport } {
  const transport = createFakeTransport({ sessions: { [TRIAGE_RUN_ID]: triageFixture() } })
  const view = render(
    <PrimaryActionProvider>
      <Session transport={transport} workspaceId="ws1" runId={TRIAGE_RUN_ID} onBack={() => {}} onOpenReview={() => {}} />
    </PrimaryActionProvider>,
  )
  return { ...view, transport }
}

/** The one header row, wherever the layout below it has got to. */
function head(): HTMLElement {
  return document.querySelector('.sn-head') as HTMLElement
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

/*
 * The owner's complaint: "whenever I switch views, the header bar is getting
 * refreshed". The header is the Session's own row now — mounted once, above
 * whichever layout the preference names — so these hold the line.
 */
describe('the hoisted run header', () => {
  beforeEach(() => {
    resetRunJobs()
    resetRenderCounts()
    localStorage.clear()
    resetSessionLayout()
  })
  afterEach(() => {
    localStorage.clear()
    resetRenderCounts()
    resetSessionLayout()
  })

  it('is the same DOM node before and after a layout switch', async () => {
    mount()
    await screen.findByRole('heading', { name: 'SBX-1' })
    const before = head()
    const key = before.querySelector('.sn-head__key')
    expect(before).not.toBeNull()

    fireEvent.click(screen.getByRole('radio', { name: 'Workbench' }))
    await screen.findByTestId('session-workbench')
    expect(head()).toBe(before)
    expect(head().querySelector('.sn-head__key')).toBe(key)

    fireEvent.click(screen.getByRole('radio', { name: 'Document' }))
    await screen.findByTestId('session-document')
    expect(head()).toBe(before)
    expect(head().querySelector('.sn-head__key')).toBe(key)
  })

  it('takes the Workbench register as a prop rather than being drawn again for it', async () => {
    mount()
    await screen.findByRole('heading', { name: 'SBX-1' })
    expect(head()).toHaveAttribute('data-variant', 'default')
    fireEvent.click(screen.getByRole('radio', { name: 'Workbench' }))
    await screen.findByTestId('session-workbench')
    await waitFor(() => expect(head()).toHaveAttribute('data-variant', 'workbench'))
  })

  it('does not redraw for the lines the layout below it draws', async () => {
    const { transport } = mount()
    await screen.findByRole('heading', { name: 'SBX-1' })
    // The header reads the run's prompt once, for the other system's link;
    // let that land before the count is taken.
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 0))
    })
    const header = renderCount('RunHeader')
    const layout = renderCount('SessionConversation')
    for (let i = 0; i < 20; i += 1) {
      act(() =>
        transport.emit({
          kind: 'run.event',
          workspaceId: 'ws1',
          runId: TRIAGE_RUN_ID,
          index: 2000 + i,
          event: steerEvent(`line ${i}`, '2026-09-15T12:13:00Z'),
        }),
      )
    }
    // The transcript drew the lines; the header above it did not move.
    await waitFor(() => expect(renderCount('SessionConversation')).toBeGreaterThan(layout))
    expect(renderCount('RunHeader')).toBe(header)
  })
})
