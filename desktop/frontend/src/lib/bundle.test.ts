import { describe, expect, it } from 'vitest'
import { parseBundle, promptSections, urlTail } from './bundle'

const PROMPT = `You are a support engineer.

# Language

- Write the note in en.

# Playbooks

## 00-environment

# Environment

- Build: \`go build ./...\`

## 10-helpdesk

Read the thread.

# Ticket

Key: SBX-1
Title: Product 00219 stock shows 1 more than the movement report
Priority: normal
Tracker URL: https://sandbox.local/tracker/SBX-1
Helpdesk URL: https://sandbox.local/desk/88341
Customer: متجر الفهد للأدوات المنزلية
Customer ID: SBX-CUST-1
Bundle directory: /repos/app/.sirdar/runs/SBX-1/r1/bundle

Files:
(none)

## Conversation

\`\`\`
# Conversation (original language)

## 2026-09-12T09:14:00+03:00 · customer · أحمد الفهد

السلام عليكم ورحمة الله
عندي مشكلة بالمخزون

## 2026-09-12T10:05:00+03:00 · agent · Layla (L1)

وعليكم السلام، شكراً تواصلكم معنا.
\`\`\`

# Output

- ticket identifies the record.
`

describe('parseBundle', () => {
  const bundle = parseBundle(PROMPT)

  it('reads the ticket lines by their labels', () => {
    expect(bundle.ticket).toEqual({
      Key: 'SBX-1',
      Title: 'Product 00219 stock shows 1 more than the movement report',
      Priority: 'normal',
      'Tracker URL': 'https://sandbox.local/tracker/SBX-1',
      'Helpdesk URL': 'https://sandbox.local/desk/88341',
      Customer: 'متجر الفهد للأدوات المنزلية',
      'Customer ID': 'SBX-CUST-1',
      'Bundle directory': '/repos/app/.sirdar/runs/SBX-1/r1/bundle',
    })
  })

  it('reads an empty Files block as no attachments, and a list as paths', () => {
    expect(bundle.files).toEqual([])
    expect(parseBundle('# Ticket\n\nKey: X\n\nFiles:\n- bundle/attachments/a.png\n- bundle/attachments/b.pdf\n').files).toEqual([
      'bundle/attachments/a.png',
      'bundle/attachments/b.pdf',
    ])
  })

  it('reads the thread out of its fence, one message per stamped heading', () => {
    expect(bundle.thread).toEqual([
      {
        at: '2026-09-12T09:14:00+03:00',
        role: 'customer',
        author: 'أحمد الفهد',
        text: 'السلام عليكم ورحمة الله\nعندي مشكلة بالمخزون',
      },
      { at: '2026-09-12T10:05:00+03:00', role: 'agent', author: 'Layla (L1)', text: 'وعليكم السلام، شكراً تواصلكم معنا.' },
    ])
  })

  it('names the playbooks from the section H2s and keeps the whole prompt in sections', () => {
    expect(bundle.playbooks).toEqual(['00-environment', '10-helpdesk'])
    expect(promptSections(PROMPT).map((s) => s.title)).toEqual([
      'Prompt',
      'Language',
      'Playbooks',
      'Environment',
      'Ticket',
      'Output',
    ])
  })

  it('reads the helpdesk number off its URL', () => {
    expect(urlTail('https://sandbox.local/desk/88341')).toBe('88341')
    expect(urlTail('https://sandbox.local/desk/88341/')).toBe('88341')
  })

  it('is empty, not broken, for a prompt with no ticket', () => {
    expect(parseBundle('')).toEqual({ ticket: {}, files: [], thread: [], playbooks: [], sections: [] })
  })
})
