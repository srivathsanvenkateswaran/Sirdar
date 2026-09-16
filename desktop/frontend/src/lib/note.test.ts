import { describe, expect, it } from 'vitest'
import { evidenceFromNote, field, parseNote, sectionRole, splitSections } from './note'

const NOTE = `---
tags: ["support-duty", "triage"]
tracker_key: "SBX-1"
customer: "متجر الفهد للأدوات المنزلية"
status: "triaged"
---

# Recording a customer return adds its quantity to stock twice

Register: [[_Issue Register]] · RCA: SBX-1 RCA recording

## Customer Complaint (translated)

(2026-09-12 09:14 +03:00) "Peace be upon you. I have a problem with the inventory."

(2026-09-12 10:41 +03:00, answering L1) "The return was last Tuesday."

Tone: polite and calm, not escalated.

## Customer Complaint (original)

<div dir="rtl">

[2026-09-12T09:14:00+03:00]
السلام عليكم ورحمة الله
عندي مشكلة بالمخزون

[2026-09-12T10:41:00+03:00]
المرتجع كان يوم الثلاثاء الماضي

</div>

## Conversation Summary

- **2026-09-12T09:14:00+03:00** (customer): reports the gap.

## Repro Steps

1. \`l := NewLedger()\`

## Root Cause Hypothesis

**Classification:** code
**Confidence:** high

In Ledger.ApplyMovement, the Return case adds the return quantity twice: once at ledger.go:33 and again at ledger.go:34.

Evidence:

- code (\`Read ledger.go\`): ledger.go:27-34: \`case Return:\` runs the increment twice.
- git (\`git log --stat --format='%h %ad %s' --date=iso\`): one commit, a9b28cd.
- helpdesk thread (\`ticket.json Thread[0], Thread[2]\`): the customer says stock matched before the return.

Code references: ledger.go:27-34, ledger.go:44-46

Blast radius: every product with a Return movement has stock overstated.
It builds up with every return.

## Proposed Fix

Apply a return's quantity once in ApplyMovement.

Files: ledger.go, ledger_test.go

Remediation SQL:

\`\`\`sql
None provided. No database source is configured.
\`\`\`

Risks: If the separate "item scanned back in" event still exists, returns may be counted more than twice.

## Open Questions

- Which date is the return?

## Customer reply draft

Language: ar. A draft, not a sent reply: read it before you send it.

<div dir="rtl">

وعليكم السلام أستاذ أحمد،
شكراً على التفاصيل.

</div>
`

describe('parseNote', () => {
  const note = parseNote(NOTE)

  it('lifts the frontmatter, the title and the register line', () => {
    expect(field(note, 'tracker_key')).toBe('SBX-1')
    expect(field(note, 'customer')).toBe('متجر الفهد للأدوات المنزلية')
    expect(note.title).toBe('Recording a customer return adds its quantity to stock twice')
    expect(note.lead).toBe('Register: [[_Issue Register]] · RCA: SBX-1 RCA recording')
  })

  it('names each section by the template heading it carries', () => {
    expect(note.sections.map((s) => s.role)).toEqual([
      'complaint',
      'complaint-original',
      'conversation',
      'repro',
      'root-cause',
      'proposed-fix',
      'open-questions',
      'reply',
    ])
    expect(note.sections[4].id).toBe('root-cause-hypothesis')
    expect(sectionRole('Timeline')).toBe('conversation')
    expect(sectionRole('Something else')).toBe('other')
  })

  it('reads the complaint as stamped paragraphs beside their originals, with the tone apart', () => {
    expect(note.complaint?.translated).toEqual([
      { when: '2026-09-12 09:14 +03:00', text: '"Peace be upon you. I have a problem with the inventory."' },
      { when: '2026-09-12 10:41 +03:00, answering L1', text: '"The return was last Tuesday."' },
    ])
    expect(note.complaint?.original).toEqual([
      { when: '2026-09-12T09:14:00+03:00', text: 'السلام عليكم ورحمة الله\nعندي مشكلة بالمخزون' },
      { when: '2026-09-12T10:41:00+03:00', text: 'المرتجع كان يوم الثلاثاء الماضي' },
    ])
    expect(note.complaint?.tone).toBe('Tone: polite and calm, not escalated.')
  })

  it('takes the verdict, the evidence list and the blast radius out of the root cause', () => {
    const rc = note.rootCause!
    expect(rc.classification).toBe('code')
    expect(rc.confidence).toBe('high')
    expect(rc.body).toBe(
      'In Ledger.ApplyMovement, the Return case adds the return quantity twice: once at ledger.go:33 and again at ledger.go:34.',
    )
    expect(rc.evidence).toEqual([
      { source: 'code', query: 'Read ledger.go', finding: 'ledger.go:27-34: `case Return:` runs the increment twice.' },
      { source: 'git', query: "git log --stat --format='%h %ad %s' --date=iso", finding: 'one commit, a9b28cd.' },
      {
        source: 'helpdesk thread',
        query: 'ticket.json Thread[0], Thread[2]',
        finding: 'the customer says stock matched before the return.',
      },
    ])
    expect(rc.codeRefs).toBe('ledger.go:27-34, ledger.go:44-46')
    expect(rc.blastRadius).toBe('every product with a Return movement has stock overstated.')
  })

  it('takes the files, the SQL fence and the risks out of the proposed fix', () => {
    const fix = note.proposedFix!
    expect(fix.body).toBe("Apply a return's quantity once in ApplyMovement.")
    expect(fix.files).toBe('ledger.go, ledger_test.go')
    expect(fix.remediationSql).toBe('None provided. No database source is configured.')
    expect(fix.risks).toMatch(/^If the separate "item scanned back in" event/)
  })

  it('reads the reply draft as a letter in its language', () => {
    expect(note.reply).toEqual({
      language: 'ar',
      text: 'وعليكم السلام أستاذ أحمد،\nشكراً على التفاصيل.',
      note: 'A draft, not a sent reply: read it before you send it.',
    })
  })

  it('keeps a note with unknown headings whole', () => {
    const rca = parseNote('# Why\n\n## Timeline of the incident\n\n- 09:00 it broke\n\n## Contributing factors\n\nTwo things.\n')
    expect(rca.title).toBe('Why')
    expect(rca.sections.map((s) => [s.role, s.title])).toEqual([
      ['conversation', 'Timeline of the incident'],
      ['other', 'Contributing factors'],
    ])
    expect(rca.rootCause).toBeUndefined()
    expect(rca.complaint).toBeUndefined()
  })

  it('does not split a section at a heading inside a fence', () => {
    const { sections } = splitSections('## One\n\n```\n## not a heading\n```\n\n## Two\n\nb')
    expect(sections.map((s) => s.title)).toEqual(['One', 'Two'])
    expect(sections[0].body).toContain('## not a heading')
  })

  it('reads no evidence from a body without the list', () => {
    expect(evidenceFromNote('Nothing here.')).toEqual([])
  })
})
