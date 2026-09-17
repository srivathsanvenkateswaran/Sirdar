/**
 * What one line typed into the New session box means.
 *
 * The box is a prompt, not a key field: a person types what they want, and
 * the ticket number or its URL goes in wherever it falls. "Triage OMNI-2510,
 * the customer says it started after the 3.2 release" is one line that names
 * a mode, a ticket and an instruction, and nothing about the order of those
 * three is fixed.
 *
 * This module is the whole of the reading, and it is pure: it runs on every
 * keystroke, it calls nothing, and it never decides anything on its own. A
 * helpdesk number is reported rather than resolved, because resolving one is
 * a call to the workspace's helpdesk adapter; a line it cannot settle is
 * reported as ambiguous rather than guessed at, because the caller's answer
 * to an ambiguous line is one short provider call and a Confirm step.
 */

/** The three kinds of session, as the Mode selector lists them. */
export type IntentMode = 'triage' | 'rca' | 'fix'

/** Why a line cannot be read outright; '' when it can. */
export type Ambiguity = '' | 'no-key' | 'two-keys' | 'two-modes'

export interface Intent {
  /** The tracker key, upper-case; '' when the line names none. */
  key: string
  /**
   * The helpdesk number the line named, digits only; '' when it named
   * none. It is not a key: the caller asks the workspace's helpdesk which
   * tracker issue it belongs to, and says so in the status line when the
   * helpdesk cannot say.
   */
  helpdesk: string
  /** The mode a word in the line asked for; '' when no word did. */
  mode: IntentMode | ''
  /** The line with the key, the URL and the mode word taken out. */
  instruction: string
  /** Why the line cannot be read outright; '' when it can. */
  ambiguity: Ambiguity
}

/**
 * A tracker key as the trackers write one: a letter, then letters or
 * digits, a dash, and digits. Upper-case, because that is how every tracker
 * writes a key and how the store files a run — a lower-case scan of prose
 * would read "order-3 never shipped" as a ticket.
 */
const KEY = /\b[A-Z][A-Z0-9]+-\d+\b/g

/** The same shape, case-forgiven, for a line that is nothing but a key. */
const LONE_KEY = /^[A-Za-z][A-Za-z0-9]+-\d+$/

/** A URL, as far as a line of prose goes: up to the first space. */
const URL_LIKE = /https?:\/\/\S+/g

/** A helpdesk number: a hash and four digits or more. */
const HELPDESK = /#(\d{4,})\b/g

/**
 * The words that set the mode, longest first so "root cause" is read as one
 * phrase rather than as the word "cause" with "root" in front of it.
 *
 * "resolution" is here because an RCA run is the one that drafts one, and a
 * person asking for the resolution is asking for that session.
 */
const MODE_WORDS: { pattern: RegExp; mode: IntentMode }[] = [
  { pattern: /\broot\s+cause\b/gi, mode: 'rca' },
  { pattern: /\bresolution\b/gi, mode: 'rca' },
  { pattern: /\btriage\b/gi, mode: 'triage' },
  { pattern: /\bimplement\b/gi, mode: 'fix' },
  { pattern: /\brca\b/gi, mode: 'rca' },
  { pattern: /\bfix\b/gi, mode: 'fix' },
]

/** One matched run of characters, to be cut out of the instruction. */
interface Span {
  start: number
  end: number
}

/** The key at the end of a URL's path, or '' — `…/browse/OMNI-2510`. */
export function keyInURL(text: string): string {
  let url: URL
  try {
    url = new URL(text)
  } catch {
    return ''
  }
  const segments = url.pathname.split('/').filter((s) => s !== '')
  const last = segments[segments.length - 1] ?? ''
  return LONE_KEY.test(last) ? last.toUpperCase() : ''
}

/** Whether the two spans touch at all. */
function overlaps(a: Span, b: Span): boolean {
  return a.start < b.end && b.start < a.end
}

/** The text with every span cut out and the whitespace that is left tidied. */
function withoutSpans(text: string, spans: Span[]): string {
  const sorted = spans.slice().sort((a, b) => a.start - b.start)
  let out = ''
  let at = 0
  for (const span of sorted) {
    if (span.start < at) continue
    out += text.slice(at, span.start) + ' '
    at = span.end
  }
  out += text.slice(at)
  // A key lifted out of the middle of a sentence leaves two spaces and
  // sometimes a stray comma or colon at the join; neither is what the
  // person typed and neither belongs in front of the session.
  return out
    .replace(/\s+/g, ' ')
    .replace(/\s+([,;:.])/g, '$1')
    .replace(/^[\s,;:.—–-]+/, '')
    .replace(/[\s,;:]+$/, '')
    .trim()
}

/**
 * Reads one composer line.
 *
 * The key is the first the line names: a bare key, or the last path segment
 * of a tracker URL. A line that is nothing but a key is read in any case,
 * because somebody typing only a key has typed a key whatever their shift
 * finger did; a key inside a sentence has to be upper-case, or every
 * hyphenated word with a number after it would be a ticket.
 *
 * The mode is the first mode word the line uses. It is a suggestion to the
 * caller and not a decision: the Mode selector's own value wins when a
 * person has set it, which is the rule the screen applies, not this one.
 *
 * The instruction is everything else, with the key, the URL, the helpdesk
 * number and the mode word taken out. Empty is fine and common: "OMNI-2510"
 * on its own is a triage of OMNI-2510 with nothing else asked for.
 *
 * `ambiguity` is set when the line cannot be settled here — a line with
 * words in it and no ticket anywhere, two different keys, or two different
 * mode words. The key and the mode still carry the first of each, so a
 * caller that has no fallback available can still show something.
 */
export function parseIntent(text: string): Intent {
  const trimmed = text.trim()
  if (trimmed === '') {
    return { key: '', helpdesk: '', mode: '', instruction: '', ambiguity: '' }
  }

  const spans: Span[] = []
  /** Every key the line names, with where it was found, so "first" is first in the line. */
  const keys: { at: number; key: string }[] = []

  // URLs first: a key inside one is the URL's, and the whole URL comes out
  // of the instruction rather than leaving a naked host behind.
  for (const match of text.matchAll(URL_LIKE)) {
    const at = match.index ?? 0
    const raw = match[0].replace(/[.,;:)\]]+$/, '')
    spans.push({ start: at, end: at + raw.length })
    const key = keyInURL(raw)
    if (key && !keys.some((k) => k.key === key)) keys.push({ at, key })
  }

  // A line that is nothing but a key is read in any case.
  if (LONE_KEY.test(trimmed)) {
    return {
      key: trimmed.toUpperCase(),
      helpdesk: '',
      mode: '',
      instruction: '',
      ambiguity: '',
    }
  }

  for (const match of text.matchAll(KEY)) {
    const at = match.index ?? 0
    const span = { start: at, end: at + match[0].length }
    if (spans.some((s) => overlaps(s, span))) continue
    spans.push(span)
    if (!keys.some((k) => k.key === match[0])) keys.push({ at, key: match[0] })
  }

  const helpdeskNumbers: string[] = []
  for (const match of text.matchAll(HELPDESK)) {
    const at = match.index ?? 0
    const span = { start: at, end: at + match[0].length }
    if (spans.some((s) => overlaps(s, span))) continue
    spans.push(span)
    const number = match[1] ?? ''
    if (number && !helpdeskNumbers.includes(number)) helpdeskNumbers.push(number)
  }

  // Longest phrase first, so "root cause" is claimed before "cause" could
  // be; ordered by position afterwards, so the mode the line settles on is
  // the first one a reader meets rather than the first in this list.
  const modes: { at: number; mode: IntentMode }[] = []
  for (const { pattern, mode } of MODE_WORDS) {
    for (const match of text.matchAll(pattern)) {
      const at = match.index ?? 0
      const span = { start: at, end: at + match[0].length }
      if (spans.some((s) => overlaps(s, span))) continue
      spans.push(span)
      if (!modes.some((m) => m.mode === mode)) modes.push({ at, mode })
    }
  }
  keys.sort((a, b) => a.at - b.at)
  modes.sort((a, b) => a.at - b.at)

  const key = keys[0]?.key ?? ''
  const helpdesk = helpdeskNumbers[0] ?? ''
  const mode = modes[0]?.mode ?? ''
  const instruction = withoutSpans(text, spans)

  let ambiguity: Ambiguity = ''
  if (keys.length > 1) ambiguity = 'two-keys'
  else if (modes.length > 1) ambiguity = 'two-modes'
  else if (key === '' && helpdesk === '') ambiguity = 'no-key'

  return { key, helpdesk, mode, instruction, ambiguity }
}

/** What the box says while it is empty. */
export const COMPOSER_PLACEHOLDER = 'Ticket key or URL, e.g. OMNI-2510'

/** Why nothing can be started, when nothing resolved. */
export const NO_KEY_REASON =
  'No ticket key yet — type one like OMNI-2510, or paste the ticket’s URL'

/** The Mode selector's word for each mode, for the chips. */
export const MODE_LABEL: Record<IntentMode, string> = {
  triage: 'Triage',
  rca: 'RCA',
  fix: 'Fix',
}

/**
 * The chips under the box: what was understood, in the order a person reads
 * it — the mode, then the ticket, then whether anything else was asked for.
 * The note chip appears only when there is an instruction, because "with
 * your note" on a line that carries none says something untrue.
 */
export function intentChips(o: {
  mode: IntentMode
  key: string
  instruction: string
}): string[] {
  const chips = [MODE_LABEL[o.mode], o.key]
  if (o.instruction.trim() !== '') chips.push('with your note')
  return chips
}
