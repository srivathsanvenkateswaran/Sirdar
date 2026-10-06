import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import type { GatheredInfo } from '../../api/types'
import { createFakeTransport } from '../../store/fakeTransport'
import { TRIAGE_RUN_ID, triageWithAttachments } from '../../store/fakeSession'
import BundlePane from './BundlePane'

/*
 * What a run gathered before it started working: a PR diff, a past run's
 * history, a configured command, an MCP call. A denied row never shows —
 * there is nothing for a reader to do with a door that stayed shut — and
 * only four rows draw until "+N more" is clicked.
 */

function row(over: Partial<GatheredInfo>): GatheredInfo {
  return {
    name: 'lookup', kind: 'command', status: 'ok', bytes: 0, tookMs: 0, call: 'cmd',
    ...over,
  }
}

function transport(gathered: GatheredInfo[]) {
  return createFakeTransport({
    sessions: { [TRIAGE_RUN_ID]: { ...triageWithAttachments(), gathered } },
  })
}

describe('the gathered block', () => {
  it('shows four rows and a +N more button, leaving the denied row out entirely', async () => {
    const rows: GatheredInfo[] = [
      row({ name: 'pr-820', kind: 'pr', summary: 'Diff for PR 820', bytes: 1200 }),
      row({ name: 'history-1', kind: 'history', summary: 'Prior run on SBX-1' }),
      row({ name: 'cmd-ls', summary: 'Repository listing' }),
      row({ name: 'mcp-call', kind: 'mcp', summary: 'Server status' }),
      row({ name: 'cmd-extra', summary: 'Extra lookup' }),
      row({ name: 'cmd-extra-2', summary: 'Second extra lookup' }),
      row({ name: 'secret-lookup', status: 'denied', reason: 'policy refused the command' }),
    ]
    render(<BundlePane transport={transport(rows)} workspaceId="ws1" runId={TRIAGE_RUN_ID} />)
    const block = await screen.findByTestId('bundle-gathered')
    const items = within(block).getAllByRole('listitem')
    expect(items).toHaveLength(4)
    expect(within(block).queryByText('secret-lookup')).toBeNull()
    const more = within(block).getByRole('button', { name: /\+2 more/ })
    fireEvent.click(more)
    await waitFor(() => expect(within(block).getAllByRole('listitem')).toHaveLength(6))
    expect(within(block).queryByRole('button', { name: /more/ })).toBeNull()
  })

  it('says failed on a row whose lookup failed', async () => {
    const rows: GatheredInfo[] = [row({ name: 'cmd-broken', status: 'failed', reason: 'exit status 1' })]
    render(<BundlePane transport={transport(rows)} workspaceId="ws1" runId={TRIAGE_RUN_ID} />)
    const block = await screen.findByTestId('bundle-gathered')
    expect(within(block).getByText('failed')).toBeInTheDocument()
  })

  it('opens the attachment preview with the gathered file', async () => {
    const rows: GatheredInfo[] = [
      row({ name: 'pr-820', kind: 'pr', summary: 'Diff for PR 820', file: 'context/pr-820.diff', bytes: 400 }),
    ]
    render(<BundlePane transport={transport(rows)} workspaceId="ws1" runId={TRIAGE_RUN_ID} />)
    const block = await screen.findByTestId('bundle-gathered')
    fireEvent.click(within(block).getByText('pr-820'))
    const dialog = await screen.findByRole('dialog')
    expect(dialog).toBeInTheDocument()
  })

  it('draws nothing when there is no visible row', async () => {
    render(<BundlePane transport={transport([])} workspaceId="ws1" runId={TRIAGE_RUN_ID} />)
    await screen.findByTestId('bundle-view')
    expect(screen.queryByTestId('bundle-gathered')).toBeNull()
  })

  it('draws nothing when every row is denied', async () => {
    const rows: GatheredInfo[] = [row({ name: 'secret', status: 'denied', reason: 'policy refused' })]
    render(<BundlePane transport={transport(rows)} workspaceId="ws1" runId={TRIAGE_RUN_ID} />)
    await screen.findByTestId('bundle-view')
    expect(screen.queryByTestId('bundle-gathered')).toBeNull()
  })

  // A pane opened while the run is still gathering reads an empty list. The
  // run moving on (its phase or status changing) is what tells the pane the
  // list is worth reading again, so the rows fill in without a reopen.
  it('fills in when the run moves on past the gather', async () => {
    const session = { ...triageWithAttachments(), gathered: [] as GatheredInfo[] }
    const t = createFakeTransport({ sessions: { [TRIAGE_RUN_ID]: session } })
    const { rerender } = render(
      <BundlePane transport={t} workspaceId="ws1" runId={TRIAGE_RUN_ID} revision="running/gather" />,
    )
    await screen.findByTestId('bundle-view')
    expect(screen.queryByTestId('bundle-gathered')).toBeNull()

    session.gathered = [row({ name: 'pr-820', kind: 'pr', summary: 'Diff for PR 820' })]
    rerender(<BundlePane transport={t} workspaceId="ws1" runId={TRIAGE_RUN_ID} revision="running/" />)
    const block = await screen.findByTestId('bundle-gathered')
    expect(within(block).getByText('pr-820')).toBeInTheDocument()
  })

  // A failed lookup's summary is what it set out to do; the reason is why it
  // did not. The reader of a failed row needs the second.
  it('shows a failed row its reason even when it has a summary', async () => {
    const rows: GatheredInfo[] = [
      row({ name: 'pr-820', kind: 'pr', status: 'failed', summary: 'PR 820', reason: 'gh: not logged in' }),
    ]
    render(<BundlePane transport={transport(rows)} workspaceId="ws1" runId={TRIAGE_RUN_ID} />)
    const block = await screen.findByTestId('bundle-gathered')
    expect(within(block).getByText('gh: not logged in')).toBeInTheDocument()
  })

  // A row that wrote no file has nothing to open, so it is not a button a
  // keyboard or screen-reader user can land on and press for nothing.
  it('draws a row with no file as text, not a button', async () => {
    const rows: GatheredInfo[] = [
      row({ name: 'cmd-broken', status: 'failed', reason: 'exit status 1' }),
      row({ name: 'pr-820', kind: 'pr', summary: 'Diff for PR 820', file: 'context/pr-820.diff' }),
    ]
    render(<BundlePane transport={transport(rows)} workspaceId="ws1" runId={TRIAGE_RUN_ID} />)
    const block = await screen.findByTestId('bundle-gathered')
    expect(within(block).queryByRole('button', { name: /cmd-broken/ })).toBeNull()
    expect(within(block).getByText('cmd-broken')).toBeInTheDocument()
    expect(within(block).getByRole('button', { name: /pr-820/ })).toBeInTheDocument()
  })
})
