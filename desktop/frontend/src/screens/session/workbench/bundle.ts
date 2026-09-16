/**
 * What the Bundle document reads out of the prompt.
 *
 * The transport serves no route for `bundle/ticket.json` or `thread.md`;
 * what it serves is `prompt.md`, and `internal/prompt` writes the ticket into
 * it as a `# Ticket` block of `Key: value` lines, a `Files:` list, and the
 * thread verbatim in a fenced `## Conversation` block whose messages are
 * headed `## <at> · <role> · <who>`. The playbooks are the `## NN-name`
 * headings under `# Playbooks`. Everything here is a defensive reading of
 * that text; a prompt shaped differently yields empty fields, not an error.
 */

export interface TicketFields {
  key: string
  title: string
  priority: string
  trackerUrl: string
  helpdeskUrl: string
  customer: string
  customerId: string
  bundleDir: string
  /** Any other `Key: value` line the block carried, in order. */
  other: { key: string; value: string }[]
}

export interface ThreadMessage {
  at: string
  role: string
  who: string
  text: string
}

export interface BundleDoc {
  ticket: TicketFields
  files: string[]
  thread: ThreadMessage[]
  playbooks: string[]
}

const KNOWN: Record<string, keyof Omit<TicketFields, 'other'>> = {
  key: 'key',
  title: 'title',
  priority: 'priority',
  'tracker url': 'trackerUrl',
  'helpdesk url': 'helpdeskUrl',
  customer: 'customer',
  'customer id': 'customerId',
  'bundle directory': 'bundleDir',
}

/**
 * The lines under a top-level heading, up to the next one. A `# ` line
 * inside a fenced block — the thread is quoted with its own heading — is
 * content, not the end of the section.
 */
function section(prompt: string, heading: string): string {
  const lines = prompt.split('\n')
  const start = lines.findIndex((l) => l.trim() === `# ${heading}`)
  if (start === -1) return ''
  const out: string[] = []
  let fenced = false
  for (const line of lines.slice(start + 1)) {
    if (line.trim().startsWith('```')) fenced = !fenced
    else if (!fenced && /^# \S/.test(line)) break
    out.push(line)
  }
  return out.join('\n')
}

export function parseBundlePrompt(prompt: string): BundleDoc {
  const ticket: TicketFields = {
    key: '',
    title: '',
    priority: '',
    trackerUrl: '',
    helpdeskUrl: '',
    customer: '',
    customerId: '',
    bundleDir: '',
    other: [],
  }
  const files: string[] = []
  const thread: ThreadMessage[] = []

  const ticketBlock = section(prompt, 'Ticket')
  const lines = ticketBlock.split('\n')
  let i = 0
  for (; i < lines.length; i += 1) {
    const line = lines[i]
    if (/^## /.test(line) || line.trim() === 'Files:') break
    const at = line.indexOf(':')
    if (at <= 0) continue
    const key = line.slice(0, at).trim()
    const value = line.slice(at + 1).trim()
    const known = KNOWN[key.toLowerCase()]
    if (known) ticket[known] = value
    else if (key && !/\s{2,}/.test(key)) ticket.other.push({ key, value })
  }
  const filesAt = lines.findIndex((l) => l.trim() === 'Files:')
  if (filesAt !== -1) {
    for (const line of lines.slice(filesAt + 1)) {
      const t = line.trim()
      if (t === '') continue
      if (!t.startsWith('- ')) break
      files.push(t.slice(2).trim())
    }
  }

  const convAt = lines.findIndex((l) => l.trim() === '## Conversation')
  if (convAt !== -1) {
    const body = lines.slice(convAt + 1)
    // The thread is fenced; the fence is not part of it.
    const open = body.findIndex((l) => l.trim().startsWith('```'))
    const inner = open === -1 ? body : body.slice(open + 1)
    const close = inner.findIndex((l) => l.trim().startsWith('```'))
    const text = (close === -1 ? inner : inner.slice(0, close)).join('\n')
    let current: ThreadMessage | undefined
    for (const line of text.split('\n')) {
      const head = /^## (.+?) · (.+?) · (.+)$/.exec(line)
      if (head) {
        current = { at: head[1].trim(), role: head[2].trim(), who: head[3].trim(), text: '' }
        thread.push(current)
        continue
      }
      if (/^# /.test(line)) continue
      if (current) current.text += (current.text ? '\n' : '') + line
    }
    for (const m of thread) m.text = m.text.trim()
  }

  // A playbook file carries headings of its own, so the block runs from
  // `# Playbooks` to the ticket rather than to the next top-level heading.
  const pbStart = prompt.indexOf('\n# Playbooks\n')
  const pbEnd = prompt.indexOf('\n# Ticket\n', pbStart)
  const playbookBlock =
    pbStart === -1 ? '' : prompt.slice(pbStart, pbEnd === -1 ? undefined : pbEnd)
  const playbooks = [...playbookBlock.matchAll(/^## (\d{2}-\S+)\s*$/gm)]
    .map((m) => m[1].trim())
    .filter((name) => !/^(how to use it|workspace gotchas)$/i.test(name))

  return { ticket, files, thread, playbooks }
}

/** The helpdesk's number off its URL: `https://…/desk/88341` → `88341`. */
export function idFromURL(url: string): string {
  const m = /\/([^/?#]+)\/?(?:[?#].*)?$/.exec(url.trim())
  return m ? m[1] : ''
}

/**
 * The customer's messages as the answer translated them. `complaint` is
 * written as one quoted passage per message, each introduced by its time in
 * parentheses; the quotes are lifted out in order so the thread can show
 * each Arabic message beside its English.
 */
export function translations(complaint: string | undefined): string[] {
  if (!complaint) return []
  const out: string[] = []
  for (const m of complaint.matchAll(/"([^"]{20,})"/g)) out.push(m[1].trim())
  return out
}
