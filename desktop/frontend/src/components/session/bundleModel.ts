/*
 * What the agent was handed, read off the prompt it was given.
 *
 * `internal/prompt` writes a `# Ticket` section of `Key: value` lines, a
 * `Files:` block of bundle-relative attachment paths (or `(none)`), a
 * `# Conversation` fence holding the thread in its original language with
 * one `## stamp · role · author` heading per message, and a `# Playbooks`
 * section whose H2s are the playbook names with their markdown under them.
 * Everything here is read from that text and nothing is invented: a field
 * the prompt does not carry is absent.
 *
 * The attachments' sizes and types do not come from here — the prompt only
 * names the files. Those come from the Transport's `attachments`, which
 * reads the bundle's own record.
 */

export interface BundleMessage {
  at: string
  role: string
  author: string
  text: string
}

export interface Playbook {
  name: string
  body: string
}

/**
 * One playbook the prompt named but did not load, from the `## Other
 * playbooks` listing: its name, the `about:` frontmatter it carries
 * (empty when it has none), the `when` keyword that matched the ticket
 * (empty when it is listed for some other reason — kinds, say), and the
 * file path a click reads with `transport.playbook`.
 */
export interface OtherPlaybook {
  name: string
  about: string
  matched: string
  path: string
}

export interface Bundle {
  facts: { key: string; value: string }[]
  thread: BundleMessage[]
  /** The Slack thread the session was started from (slack.md); empty when it was started from anything else. */
  slack: BundleMessage[]
  files: string[]
  playbooks: Playbook[]
  /** The `## Other playbooks` listing: playbooks the prompt named but did not load in full. */
  otherPlaybooks: OtherPlaybook[]
  /**
   * What the `# Repositories` section says about other repositories, in the
   * chip's words: "mentions Acme.Web (companion repo)", "look in Acme.Web".
   * Empty when the prompt has no such section.
   */
  repos: string[]
}

/**
 * The `# Repositories` section's mention lines (`- mentions X (…) — named
 * by ref`) and its "The operator asked you to look in X." lines.
 */
function repos(prompt: string): string[] {
  const lines = prompt.split('\n')
  const start = lines.findIndex((l) => /^# Repositories\s*$/.test(l))
  if (start === -1) return []
  const out: string[] = []
  for (const line of lines.slice(start + 1)) {
    if (/^# /.test(line)) break
    const mention = /^- (mentions .+?)(?: — named by .*)?$/.exec(line)
    if (mention) {
      out.push(mention[1])
      continue
    }
    const asked = /^The operator asked you to look in ([^,.]+(?:\.[^\s,.]+)*)[.,]/.exec(line)
    if (asked) {
      const unknown = line.includes('which is not a configured repository')
      out.push(`look in ${asked[1]}${unknown ? ' (not configured)' : ''}`)
    }
  }
  return out
}

/** The lines under `# Ticket` up to the first sub-heading, as key–value pairs. */
function facts(prompt: string): { key: string; value: string }[] {
  const lines = prompt.split('\n')
  const start = lines.findIndex((l) => /^# Ticket\s*$/.test(l))
  if (start === -1) return []
  const out: { key: string; value: string }[] = []
  for (const line of lines.slice(start + 1)) {
    if (/^#/.test(line)) break
    if (line.trim() === 'Files:') break
    const m = /^([A-Za-z][A-Za-z ]*?):\s*(.+)$/.exec(line)
    if (m) out.push({ key: m[1].trim(), value: m[2].trim() })
  }
  return out
}

function thread(prompt: string): BundleMessage[] {
  const out: BundleMessage[] = []
  const lines = prompt.split('\n')
  let current: BundleMessage | undefined
  let inside = false
  for (const line of lines) {
    if (/^# Conversation\b/.test(line)) {
      inside = true
      continue
    }
    if (!inside) continue
    if (/^```/.test(line) || /^# /.test(line)) {
      if (current) out.push(current)
      current = undefined
      if (/^# /.test(line)) inside = false
      continue
    }
    const head = /^## (.+?) · (.+?) · (.+)$/.exec(line)
    if (head) {
      if (current) out.push(current)
      current = { at: head[1].trim(), role: head[2].trim(), author: head[3].trim(), text: '' }
      continue
    }
    if (current) current.text = current.text ? `${current.text}\n${line}` : line
  }
  if (current) out.push(current)
  return out.map((m) => ({ ...m, text: m.text.trim() }))
}

/**
 * The `## From Slack` section: slack.md in a fence of its own length (one
 * backtick longer than any run inside, since Slack messages carry code
 * blocks), with the same `## stamp · role · author` heading per message the
 * conversation uses. Only the fence that opened the section closes it.
 */
function slack(prompt: string): BundleMessage[] {
  const lines = prompt.split('\n')
  const start = lines.findIndex((l) => /^## From Slack\b/.test(l))
  if (start === -1) return []
  let i = start + 1
  while (i < lines.length && lines[i].trim() === '') i++
  const fence = /^(`{3,})\s*$/.exec(lines[i] ?? '')?.[1]
  if (!fence) return []
  const out: BundleMessage[] = []
  let current: BundleMessage | undefined
  for (const line of lines.slice(i + 1)) {
    if (line === fence) break
    const head = /^## (.*?) · (.+?) · (.+)$/.exec(line)
    if (head) {
      if (current) out.push(current)
      current = { at: head[1].trim(), role: head[2].trim(), author: head[3].trim(), text: '' }
      continue
    }
    if (current) current.text = current.text ? `${current.text}\n${line}` : line
  }
  if (current) out.push(current)
  return out.map((m) => ({ ...m, text: m.text.trim() }))
}

/**
 * A top-level heading that ends the Playbooks section, wherever it starts:
 * `# Playbooks` for a prompt with none to list, `# Workspace knowledge —
 * consult when relevant` for one that lists some it chose not to load.
 */
const PLAYBOOKS_START = /^# (Playbooks|Workspace knowledge\b.*)$/
/**
 * The sections that can follow Playbooks in a prompt. A playbook's own body
 * carries `# Environment` and `## How to use it` headings, so only one of
 * these, not any `#`-heading, ends the list.
 */
const PLAYBOOKS_STOP =
  /^# (Ticket|Output|Language|Schema|Conversation|Gathered context|Reproduce on staging|Task|Operator's request|Repositories|Access)\b/
/** One `## Other playbooks` listing line: name, optional about, optional matched keyword, and the file path. */
const OTHER_PLAYBOOK_LINE =
  /^- (\S+)(?: — (.*?))?(?: Matched "([^"]+)"; read before querying\.)? `([^`]+)`$/

/**
 * The Playbooks section: the playbooks loaded in full, and — after a
 * `## Other playbooks` heading — the ones the prompt only listed. The
 * heading ends whichever loaded playbook's body came before it, so that
 * body never swallows the listing that follows.
 */
function playbooksSection(prompt: string): { playbooks: Playbook[]; otherPlaybooks: OtherPlaybook[] } {
  const lines = prompt.split('\n')
  const start = lines.findIndex((l) => PLAYBOOKS_START.test(l))
  if (start === -1) return { playbooks: [], otherPlaybooks: [] }
  const playbooksOut: Playbook[] = []
  const otherOut: OtherPlaybook[] = []
  let current: Playbook | undefined
  let inOther = false
  for (const line of lines.slice(start + 1)) {
    if (PLAYBOOKS_STOP.test(line)) break
    if (/^## Other playbooks\s*$/.test(line)) {
      if (current) playbooksOut.push(current)
      current = undefined
      inOther = true
      continue
    }
    if (inOther) {
      const m = OTHER_PLAYBOOK_LINE.exec(line)
      if (m) otherOut.push({ name: m[1], about: m[2] ?? '', matched: m[3] ?? '', path: m[4] })
      continue
    }
    const head = /^## (\d{2}-[\w-]+)\s*$/.exec(line)
    if (head) {
      if (current) playbooksOut.push(current)
      current = { name: head[1], body: '' }
      continue
    }
    if (current) current.body = current.body ? `${current.body}\n${line}` : line
  }
  if (current) playbooksOut.push(current)
  return {
    playbooks: playbooksOut.map((p) => ({ ...p, body: p.body.trim() })),
    otherPlaybooks: otherOut,
  }
}

/** The `Files:` block: one bundle-relative path per line, `(none)` for an empty bundle. */
function files(prompt: string): string[] {
  const lines = prompt.split('\n')
  const start = lines.findIndex((l) => l.trim() === 'Files:')
  if (start === -1) return []
  const out: string[] = []
  for (const line of lines.slice(start + 1)) {
    const trimmed = line.trim()
    if (trimmed === '') continue
    if (trimmed === '(none)') break
    if (!trimmed.startsWith('- ')) break
    out.push(trimmed.slice(2).trim())
  }
  return out
}

/** Everything the Bundle pane draws, read out of the prompt. */
export function parseBundle(prompt: string): Bundle {
  const { playbooks, otherPlaybooks } = playbooksSection(prompt)
  return {
    facts: facts(prompt),
    thread: thread(prompt),
    slack: slack(prompt),
    files: files(prompt),
    playbooks,
    otherPlaybooks,
    repos: repos(prompt),
  }
}

/** One `Key: value` line of the ticket block, by its key, case aside. */
export function fact(bundle: Bundle, key: string): string {
  return bundle.facts.find((f) => f.key.toLowerCase() === key.toLowerCase())?.value ?? ''
}

/** The last segment of a URL: `https://…/desk/88341` → `88341`. */
export function idFromURL(url: string): string {
  const m = /\/([^/?#]+)\/?(?:[?#].*)?$/.exec(url.trim())
  return m ? m[1] : ''
}

/**
 * A file name shortened in the middle rather than at the end, so the
 * extension survives: `voice-note-2026-09-12-ahmed.m4a` →
 * `voice-note-20…-ahmed.m4a`. The name is never reordered — the caller draws
 * it with `dir="auto"` — and an Arabic file name is shortened the same way,
 * on characters rather than on bytes.
 */
export function middleEllipsis(name: string, max = 34): string {
  const chars = [...name]
  if (chars.length <= max) return name
  const head = Math.ceil((max - 1) / 2)
  const tail = max - 1 - head
  return `${chars.slice(0, head).join('')}…${chars.slice(chars.length - tail).join('')}`
}

/** `12.4 kB`, `980 B`; '' when nothing said how big the file is. */
export function fileSize(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes <= 0) return ''
  if (bytes < 1000) return `${bytes} B`
  const units = ['kB', 'MB', 'GB']
  let n = bytes / 1000
  let unit = 0
  while (n >= 1000 && unit < units.length - 1) {
    n /= 1000
    unit += 1
  }
  return `${n < 10 ? n.toFixed(1) : Math.round(n)} ${units[unit]}`
}

/** What a preview should draw for a file: by its type, then by its extension. */
export type PreviewKind = 'image' | 'pdf' | 'audio' | 'video' | 'text' | 'other'

export function previewKind(mime: string, name: string): PreviewKind {
  const type = mime.split(';')[0].trim().toLowerCase()
  if (type.startsWith('image/')) return 'image'
  if (type === 'application/pdf') return 'pdf'
  if (type.startsWith('audio/')) return 'audio'
  if (type.startsWith('video/')) return 'video'
  if (type.startsWith('text/') || type === 'application/json') return 'text'
  const ext = /\.([a-z0-9]+)$/i.exec(name)?.[1]?.toLowerCase() ?? ''
  if (['png', 'jpg', 'jpeg', 'gif', 'webp', 'bmp', 'avif'].includes(ext)) return 'image'
  if (ext === 'pdf') return 'pdf'
  if (['m4a', 'mp3', 'wav', 'ogg', 'oga', 'opus', 'aac'].includes(ext)) return 'audio'
  if (['mp4', 'mov', 'webm', 'm4v'].includes(ext)) return 'video'
  if (['txt', 'log', 'csv', 'json', 'md', 'yaml', 'yml'].includes(ext)) return 'text'
  return 'other'
}
