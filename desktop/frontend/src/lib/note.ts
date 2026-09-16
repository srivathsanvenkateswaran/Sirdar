import type { EvidenceSource } from './evidence'
import { splitFrontmatter } from './events'

/**
 * A Sirdar note read as a document rather than as markdown to dump.
 *
 * `internal/note` writes every triage note from one template: frontmatter,
 * an H1 that is the finding, a register line, then H2 sections in a fixed
 * order — the complaint translated and in the original, the conversation,
 * repro steps, the root-cause hypothesis (with a Classification and
 * Confidence line, an "Evidence:" list and a "Blast radius:" paragraph), the
 * proposed fix (with Files, Remediation SQL and Risks), open questions and
 * the customer reply draft in an RTL block. An RCA note uses other headings.
 *
 * This reads what it recognises and keeps the rest as a section of
 * markdown, so an unfamiliar note still renders whole. Nothing here draws.
 */

export interface NoteSection {
  /** A slug of the heading: `root-cause`, `proposed-fix`. */
  id: string
  title: string
  /** The section's markdown, the heading excluded. */
  body: string
  /** What the section is, when the template's heading is recognised. */
  role: SectionRole
}

export type SectionRole =
  | 'complaint'
  | 'complaint-original'
  | 'conversation'
  | 'repro'
  | 'root-cause'
  | 'proposed-fix'
  | 'open-questions'
  | 'reply'
  | 'other'

/** One paragraph of the complaint with the stamp the note put before it. */
export interface ComplaintPart {
  when: string
  text: string
}

export interface NoteModel {
  fields: { key: string; value: string }[]
  title: string
  /** Text between the title and the first section: the register line. */
  lead: string
  sections: NoteSection[]
  complaint?: { translated: ComplaintPart[]; original: ComplaintPart[]; tone: string }
  rootCause?: {
    classification: string
    confidence: string
    /** The hypothesis paragraphs, the labelled lines taken out. */
    body: string
    evidence: EvidenceSource[]
    codeRefs: string
    blastRadius: string
  }
  proposedFix?: { body: string; files: string; remediationSql: string; risks: string }
  reply?: { language: string; text: string; note: string }
}

const ROLES: [RegExp, SectionRole][] = [
  [/complaint.*\(original\)|original.*complaint/i, 'complaint-original'],
  [/complaint/i, 'complaint'],
  [/conversation|timeline/i, 'conversation'],
  [/repro/i, 'repro'],
  [/root cause/i, 'root-cause'],
  [/proposed fix|^fix$/i, 'proposed-fix'],
  [/open questions/i, 'open-questions'],
  [/reply/i, 'reply'],
]

export function sectionRole(title: string): SectionRole {
  for (const [re, role] of ROLES) if (re.test(title)) return role
  return 'other'
}

export function slug(title: string): string {
  return title
    .toLowerCase()
    .replace(/\(.*?\)/g, '')
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '')
}

/** Splits a body at its H2s. Fenced code is left alone: a `##` inside a fence is code. */
export function splitSections(body: string): { lead: string; sections: NoteSection[] } {
  const lines = body.split('\n')
  const sections: NoteSection[] = []
  let lead: string[] = []
  let current: { title: string; lines: string[] } | undefined
  let fence = false
  for (const line of lines) {
    if (/^```/.test(line)) fence = !fence
    const m = !fence ? /^##\s+(.+?)\s*$/.exec(line) : null
    if (m) {
      if (current) sections.push(finish(current))
      current = { title: m[1], lines: [] }
      continue
    }
    if (current) current.lines.push(line)
    else lead.push(line)
  }
  if (current) sections.push(finish(current))
  return { lead: lead.join('\n').trim(), sections }

  function finish(c: { title: string; lines: string[] }): NoteSection {
    return { id: slug(c.title), title: c.title, body: c.lines.join('\n').trim(), role: sectionRole(c.title) }
  }
}

/** The text inside the template's `<div dir="rtl">…</div>`, or the whole body when there is none. */
export function rtlBlock(body: string): string {
  const m = /<div dir="rtl">\s*([\s\S]*?)\s*<\/div>/.exec(body)
  return (m ? m[1] : body).trim()
}

/** Paragraphs: blank-line separated runs. */
function paragraphs(text: string): string[] {
  return text
    .split(/\n\s*\n/)
    .map((p) => p.trim())
    .filter(Boolean)
}

/**
 * The translated complaint: `(stamp) "text"` paragraphs and a `Tone:` line.
 * The original: `[stamp]` on its own line, then the text.
 */
function parseComplaint(translated: string, original: string): NonNullable<NoteModel['complaint']> {
  const out: ComplaintPart[] = []
  let tone = ''
  for (const p of paragraphs(translated)) {
    if (/^tone:/i.test(p)) {
      tone = p
      continue
    }
    const m = /^\(([^)]+)\)\s*([\s\S]*)$/.exec(p)
    out.push(m ? { when: m[1].trim(), text: m[2].trim() } : { when: '', text: p })
  }
  const orig: ComplaintPart[] = []
  for (const p of paragraphs(rtlBlock(original))) {
    const m = /^\[([^\]]+)\]\s*\n?([\s\S]*)$/.exec(p)
    orig.push(m ? { when: m[1].trim(), text: m[2].trim() } : { when: '', text: p })
  }
  return { translated: out, original: orig, tone }
}

/** `**Label:** value` or `Label: value` at the start of a line. */
function labelled(body: string, label: string): string {
  const re = new RegExp(`^(?:\\*\\*)?${label}:(?:\\*\\*)?\\s*(.*)$`, 'im')
  return re.exec(body)?.[1]?.trim() ?? ''
}

/** Removes the line `label:` starts, and the paragraph it opens when `whole` is set. */
function without(body: string, label: string, whole = false): string {
  const re = whole
    ? new RegExp(`^(?:\\*\\*)?${label}:(?:\\*\\*)?[^\\n]*(?:\\n(?!\\s*\\n)[^\\n]*)*`, 'im')
    : new RegExp(`^(?:\\*\\*)?${label}:(?:\\*\\*)?[^\\n]*\\n?`, 'im')
  return body.replace(re, '')
}

/**
 * The "Evidence:" list: `- source (\`query\`): finding` per item. The list
 * ends at the first blank line after it.
 */
export function evidenceFromNote(body: string): EvidenceSource[] {
  const at = /^Evidence:\s*$/im.exec(body)
  if (!at) return []
  const rest = body.slice(at.index + at[0].length)
  const out: EvidenceSource[] = []
  for (const line of rest.split('\n')) {
    const trimmed = line.trim()
    if (trimmed === '') {
      if (out.length > 0) break
      continue
    }
    const m = /^[-*]\s+([^(:]+?)\s*(?:\(`?([^`)]*)`?\))?\s*:\s*(.*)$/.exec(trimmed)
    if (!m) {
      if (out.length > 0) break
      continue
    }
    out.push({ source: m[1].trim(), query: (m[2] ?? '').trim(), finding: m[3].trim() })
  }
  return out
}

/** The evidence list removed from a body, so the hypothesis reads on its own. */
function withoutEvidence(body: string): string {
  const at = /^Evidence:\s*$/im.exec(body)
  if (!at) return body
  const before = body.slice(0, at.index)
  const rest = body.slice(at.index + at[0].length)
  const lines = rest.split('\n')
  let i = 0
  while (i < lines.length && lines[i].trim() === '') i += 1
  while (i < lines.length && /^\s*[-*]\s/.test(lines[i])) i += 1
  return `${before.trimEnd()}\n\n${lines.slice(i).join('\n').trim()}`.trim()
}

function parseRootCause(body: string): NonNullable<NoteModel['rootCause']> {
  const classification = labelled(body, 'Classification')
  const confidence = labelled(body, 'Confidence')
  const evidence = evidenceFromNote(body)
  const codeRefs = labelled(body, 'Code references')
  const blastRadius = labelled(body, 'Blast radius')
  let rest = without(without(body, 'Classification'), 'Confidence')
  rest = withoutEvidence(rest)
  rest = without(rest, 'Code references')
  rest = without(rest, 'Blast radius', true)
  return { classification, confidence, body: rest.trim(), evidence, codeRefs, blastRadius }
}

function parseProposedFix(body: string): NonNullable<NoteModel['proposedFix']> {
  const files = labelled(body, 'Files')
  const risks = labelled(body, 'Risks')
  let remediationSql = ''
  const sql = /^Remediation SQL:\s*\n+```(?:sql)?\s*\n([\s\S]*?)\n```/im.exec(body)
  if (sql) remediationSql = sql[1].trim()
  else remediationSql = labelled(body, 'Remediation SQL')
  let rest = without(body, 'Files')
  rest = rest.replace(/^Remediation SQL:\s*\n+```(?:sql)?\s*\n[\s\S]*?\n```\s*/im, '')
  rest = without(rest, 'Remediation SQL')
  rest = without(rest, 'Risks', true)
  return { body: rest.trim(), files, remediationSql, risks }
}

function parseReply(body: string): NonNullable<NoteModel['reply']> {
  const language = /Language:\s*([a-z]{2,3})\b/i.exec(body)?.[1] ?? ''
  const rtl = /<div dir="rtl">\s*([\s\S]*?)\s*<\/div>/.exec(body)
  let text = rtl ? rtl[1].trim() : ''
  let note = ''
  if (rtl) {
    note = body
      .slice(0, rtl.index)
      .replace(/^Language:\s*[a-z]{2,3}\.?\s*/im, '')
      .trim()
  } else {
    // No RTL block: the first line says the language, the rest is the draft.
    const lines = body.split('\n')
    const langAt = lines.findIndex((l) => /^Language:/i.test(l))
    if (langAt !== -1) {
      note = lines[langAt].replace(/^Language:\s*[a-z]{2,3}\.?\s*/i, '').trim()
      text = lines.slice(langAt + 1).join('\n').trim()
    } else text = body.trim()
  }
  return { language, text, note }
}

export function parseNote(markdown: string): NoteModel {
  const { fields, body } = splitFrontmatter(markdown)
  const titleMatch = /^#\s+(.+?)\s*$/m.exec(body)
  const title = titleMatch?.[1] ?? ''
  const afterTitle = titleMatch ? body.slice(titleMatch.index + titleMatch[0].length) : body
  const { lead, sections } = splitSections(afterTitle)

  const model: NoteModel = { fields, title, lead, sections }
  const translated = sections.find((s) => s.role === 'complaint')
  const original = sections.find((s) => s.role === 'complaint-original')
  if (translated || original) model.complaint = parseComplaint(translated?.body ?? '', original?.body ?? '')
  const rootCause = sections.find((s) => s.role === 'root-cause')
  if (rootCause) model.rootCause = parseRootCause(rootCause.body)
  const fix = sections.find((s) => s.role === 'proposed-fix')
  if (fix) model.proposedFix = parseProposedFix(fix.body)
  const reply = sections.find((s) => s.role === 'reply')
  if (reply) model.reply = parseReply(reply.body)
  return model
}

/** A frontmatter value, by key; '' when absent. */
export function field(model: Pick<NoteModel, 'fields'>, key: string): string {
  return model.fields.find((f) => f.key === key)?.value ?? ''
}
