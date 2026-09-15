import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import type { ConfigSummary } from '../../api/types'
import ConfigSummaryPanel from './ConfigSummaryPanel'

const CONFIGURED: ConfigSummary = {
  notify: {
    enabled: true,
    on: ['completed', 'failed'],
    includeTitle: true,
    destinations: [
      { type: 'slack', credential: 'env' },
      {
        type: 'generic',
        credential: 'keychain',
        target: 'https://hooks.example.com',
        headers: ['X-Team'],
        signed: true,
      },
    ],
  },
  webhooks: {
    enabled: true,
    cooldown: '10m0s',
    match: { assignee: 'me', statuses: ['Open'], labels: ['payments'] },
    sources: [
      { name: 'jira', auth: 'secret', credential: 'env' },
      { name: 'zoho', auth: 'basic', credential: 'keychain', proxy: 'https://sirdar.example.com' },
    ],
  },
}

const NOTHING: ConfigSummary = {
  notify: { enabled: false, on: [], includeTitle: false, destinations: [] },
  webhooks: { enabled: false, cooldown: '10m0s', match: {}, sources: [] },
}

describe('ConfigSummaryPanel', () => {
  it('reports the notify block: when it posts, where, and that the body never leaves', () => {
    render(<ConfigSummaryPanel summary={CONFIGURED} error="" />)

    expect(screen.getByText(/Posts on completed, failed/)).toBeInTheDocument()
    expect(screen.getByText(/the ticket title is included/)).toBeInTheDocument()
    expect(screen.getByText(/The note body is never sent/)).toBeInTheDocument()
    expect(screen.getByText('slack')).toBeInTheDocument()
    expect(screen.getByText('https://hooks.example.com')).toBeInTheDocument()
    expect(screen.getByText('headers: X-Team')).toBeInTheDocument()
    expect(screen.getByText('signed')).toBeInTheDocument()
  })

  it('reports the webhooks block: cooldown, filter, and how each source authenticates', () => {
    render(<ConfigSummaryPanel summary={CONFIGURED} error="" />)

    expect(screen.getByText(/Cooldown 10m0s/)).toBeInTheDocument()
    expect(screen.getByText(/only tickets assigned to me/)).toBeInTheDocument()
    expect(screen.getByText(/statuses Open/)).toBeInTheDocument()
    expect(screen.getByText(/labels payments/)).toBeInTheDocument()
    expect(screen.getByText('signed deliveries')).toBeInTheDocument()
    expect(screen.getByText('username and password')).toBeInTheDocument()
    expect(screen.getByText('via https://sirdar.example.com')).toBeInTheDocument()
  })

  /*
   * The guard that matters: the API sends a scheme, never a reference, a
   * variable name or a value, and the panel must not invent one either.
   */
  it('names a credential by its scheme alone', () => {
    const { container } = render(<ConfigSummaryPanel summary={CONFIGURED} error="" />)

    expect(screen.getAllByText('env: reference')).toHaveLength(2)
    expect(screen.getAllByText('keychain: reference')).toHaveLength(2)

    // A reference is written "env:NAME". Nothing on the panel may be one:
    // every colon a scheme appears before is followed by the word it is
    // named with, not by the variable holding the secret.
    const text = container.textContent ?? ''
    expect(/\b(env|keychain|file|cmd):\S/.test(text)).toBe(false)
    expect(text).toContain('Secrets are never shown')
  })

  it('a destination with no credential says so rather than showing a blank', () => {
    render(
      <ConfigSummaryPanel
        summary={{
          ...CONFIGURED,
          notify: { ...CONFIGURED.notify, destinations: [{ type: 'generic', target: 'https://h' }] },
        }}
        error=""
      />,
    )
    expect(screen.getByText('no credential')).toBeInTheDocument()
  })

  it('says plainly when a workspace notifies nowhere and serves no hooks', () => {
    render(<ConfigSummaryPanel summary={NOTHING} error="" />)
    expect(screen.getByText(/posts nothing when a run finishes/)).toBeInTheDocument()
    expect(screen.getByText(/Inbound webhooks are off/)).toBeInTheDocument()
  })

  it('shows the error instead of a loading line when the summary cannot be read', () => {
    render(<ConfigSummaryPanel summary={null} error="workspace not found" />)
    expect(screen.getByText('workspace not found')).toBeInTheDocument()
    expect(screen.queryByText(/Reading the workspace configuration/)).toBeNull()
  })

  it('says it is reading while the summary is on its way', () => {
    render(<ConfigSummaryPanel summary={null} error="" />)
    expect(screen.getByText(/Reading the workspace configuration/)).toBeInTheDocument()
  })
})
