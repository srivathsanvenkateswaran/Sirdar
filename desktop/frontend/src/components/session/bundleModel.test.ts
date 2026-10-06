import { describe, expect, it } from 'vitest'
import { parseBundle } from './bundleModel'

/*
 * The Playbooks section of the prompt, in its two shapes: `# Playbooks`
 * when every named playbook loaded in full, and `# Workspace knowledge —
 * consult when relevant` when some were only listed under `## Other
 * playbooks`. The body of a loaded playbook can carry its own headings, so
 * only the listing (or the prompt's next top-level section) ends it.
 */

describe('parseBundle playbooks', () => {
  it('parses the loaded playbooks and the listing under Workspace knowledge', () => {
    const prompt = [
      '# Workspace knowledge — consult when relevant',
      '',
      '## 10-helpdesk',
      '',
      'How to read a helpdesk ticket.',
      '',
      '## 50-code',
      '',
      '# Environment',
      '',
      'Node 20, npm workspaces.',
      '',
      '## How to use it',
      '',
      'Read the file before editing.',
      '',
      '## Other playbooks',
      '',
      '- 60-database — Query patterns for the ledger. Matched "database"; read before querying. `60-database.md`',
      '- 70-billing `70-billing.md`',
      '',
      '# Ticket',
      '',
      'Key: SBX-1',
      '',
    ].join('\n')

    const bundle = parseBundle(prompt)

    expect(bundle.playbooks.map((p) => p.name)).toEqual(['10-helpdesk', '50-code'])
    expect(bundle.otherPlaybooks).toEqual([
      { name: '60-database', about: 'Query patterns for the ledger.', matched: 'database', path: '60-database.md' },
      { name: '70-billing', about: '', matched: '', path: '70-billing.md' },
    ])
    // `## Other playbooks` ends 50-code's body; its own headings do not
    // swallow the listing that follows.
    const fifty = bundle.playbooks.find((p) => p.name === '50-code')
    expect(fifty?.body).not.toContain('Other playbooks')
    expect(fifty?.body).not.toContain('60-database')
  })

  it('parses a `# Playbooks` prompt with no listing as it always has', () => {
    const prompt = ['# Playbooks', '', '## 10-helpdesk', '', 'How to read a helpdesk ticket.', '', '# Ticket', '', 'Key: SBX-1', ''].join(
      '\n',
    )

    const bundle = parseBundle(prompt)

    expect(bundle.playbooks.map((p) => p.name)).toEqual(['10-helpdesk'])
    expect(bundle.otherPlaybooks).toEqual([])
  })
})
