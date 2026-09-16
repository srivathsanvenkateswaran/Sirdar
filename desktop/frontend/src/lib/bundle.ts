/**
 * What the agent was handed, read off the prompt it was given.
 *
 * No route serves the bundle directory itself, and `ticket.json` never
 * crosses to the UI, so the Bundle view reads the prompt: `internal/prompt`
 * writes a `# Ticket` section of `Key: value` lines, a `Files:` block of
 * attachment paths (or `(none)`), a `## Conversation` fence holding the
 * thread in its original language with one `## stamp · role · author`
 * heading per message, and a `# Playbooks` section whose H2s are the
 * playbook names. Everything here is read from that text and nothing is
 * invented: a field the prompt does not carry is absent.
 */

export interface ThreadMessage {
  at: string
  role: string
  author: string
  text: string
}

export interface BundleModel {
  /** The `# Ticket` lines, by their label: Key, Title, Priority, Tracker URL, Helpdesk URL, Customer, Customer ID, Bundle directory. */
  ticket: Record<string, string>
  /** Attachment paths from the `Files:` block; empty when the block says `(none)`. */
  files: string[]
  thread: ThreadMessage[]
  playbooks: string[]
  /** The prompt's top-level sections, for a reader who wants the whole thing. */
  sections: { title: string; body: string }[]
}

/** The prompt at its `# ` headings; a heading inside a fence is code. */
export function promptSections(prompt: string): { title: string; body: string }[] {
  const out: { title: string; body: string }[] = []
  let title = 'Prompt'
  let body: string[] = []
  let fence = false
  for (const line of prompt.split('\n')) {
    if (/^```/.test(line)) fence = !fence
    if (!fence && /^# \S/.test(line)) {
      if (body.length > 0 || out.length > 0) out.push({ title, body: body.join('\n') })
      title = line.slice(2).trim()
      body = []
      continue
    }
    body.push(line)
  }
  out.push({ title, body: body.join('\n') })
  return out.filter((s) => s.body.trim() !== '' || s.title !== 'Prompt')
}

function ticketFields(body: string): Record<string, string> {
  const out: Record<string, string> = {}
  for (const line of body.split('\n')) {
    if (/^(Files:|##\s)/.test(line)) break
    const m = /^([A-Z][A-Za-z ]+?):\s+(.*)$/.exec(line)
    if (m) out[m[1].trim()] = m[2].trim()
  }
  return out
}

function files(body: string): string[] {
  const lines = body.split('\n')
  const start = lines.findIndex((l) => l.trim() === 'Files:')
  if (start === -1) return []
  const out: string[] = []
  for (const line of lines.slice(start + 1)) {
    const trimmed = line.trim()
    if (trimmed === '' || trimmed === '(none)') {
      if (out.length > 0 || trimmed === '(none)') break
      continue
    }
    if (!trimmed.startsWith('- ')) break
    out.push(trimmed.slice(2).trim())
  }
  return out
}

/** The thread inside the `## Conversation` fence: `## stamp · role · author` then the text. */
export function thread(body: string): ThreadMessage[] {
  const fence = /```\s*\n([\s\S]*?)\n```/.exec(body)
  const inner = fence ? fence[1] : body
  const out: ThreadMessage[] = []
  let current: ThreadMessage | undefined
  let text: string[] = []
  const flush = () => {
    if (current) out.push({ ...current, text: text.join('\n').trim() })
    text = []
  }
  for (const line of inner.split('\n')) {
    const m = /^##\s+(.+?)\s+·\s+(\S+)\s+·\s+(.+?)\s*$/.exec(line)
    if (m) {
      flush()
      current = { at: m[1], role: m[2], author: m[3], text: '' }
      continue
    }
    if (/^#\s/.test(line)) continue
    if (current) text.push(line)
  }
  flush()
  return out
}

/**
 * The playbook names: `## 00-environment`, `## 10-helpdesk`. Each playbook's
 * own text is spliced in after its heading, H1s included, so the names are
 * read across the whole prompt rather than out of one section.
 */
function playbookNames(prompt: string): string[] {
  const out: string[] = []
  let fence = false
  for (const line of prompt.split('\n')) {
    if (/^```/.test(line)) fence = !fence
    if (fence) continue
    const m = /^##\s+(\d{2}-[\w-]+)\s*$/.exec(line)
    if (m) out.push(m[1])
  }
  return out
}

export function parseBundle(prompt: string): BundleModel {
  const sections = promptSections(prompt)
  const ticket = sections.find((s) => s.title === 'Ticket')
  return {
    ticket: ticket ? ticketFields(ticket.body) : {},
    files: ticket ? files(ticket.body) : [],
    thread: ticket ? thread(ticket.body) : [],
    playbooks: playbookNames(prompt),
    sections,
  }
}

/** The last path segment of a URL: `88341` from `https://desk.example/desk/88341`. */
export function urlTail(url: string): string {
  const clean = url.replace(/[/#?]+$/, '')
  return clean.slice(clean.lastIndexOf('/') + 1)
}
