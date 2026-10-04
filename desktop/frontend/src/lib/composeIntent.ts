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

/** The four kinds of session, as the Mode selector lists them. */
export type IntentMode = 'session' | 'triage' | 'rca' | 'fix'

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
  /**
   * A helpdesk link the line carried (a Zoho Desk ticket URL), as typed;
   * '' when none. Like the number, it is resolved by the workspace, not here.
   */
  helpdeskUrl: string
  /**
   * A Slack message link the line carried, as typed; '' when none. The
   * workspace reads the thread and finds the ticket named in it.
   */
  slack: string
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

/** A GitHub reference: owner/name#123. */
const GITHUB_REF = /[A-Za-z0-9][A-Za-z0-9-]*\/[A-Za-z0-9._-]+#\d+\b/g

/** A helpdesk number: a hash and four digits or more. */
const HELPDESK = /#(\d{4,})\b/g

/**
 * A leading slash command: the only way a word in the line picks triage,
 * RCA or fix outright. It is cut from the instruction like any other
 * reference.
 */
const SLASH_MODE = /^\s*\/(triage|rca|fix)\b/i

/**
 * The phrase a line can be, once every reference is cut out of it and its
 * own surrounding punctuation is ignored, for that phrase alone to set the
 * mode. A word that shares the line with anything else — "fix the tax
 * rounding", "triage OMNI-1 then fix it" — is prose, not a command, and
 * stays in the instruction instead: the Mode chip is how a person steers an
 * ambiguous line, not a scan of their wording for the first word it
 * recognises.
 *
 * "resolution" is here because an RCA run is the one that drafts one, and a
 * person asking for the resolution is asking for that session.
 */
const MODE_PHRASE: Record<string, IntentMode> = {
  triage: 'triage',
  rca: 'rca',
  'root cause': 'rca',
  resolution: 'rca',
  fix: 'fix',
  implement: 'fix',
}

/**
 * The mode `text` names when it is nothing but one of `MODE_PHRASE`'s words
 * or phrases, case and surrounding punctuation ignored; '' otherwise.
 */
function bareModeWord(text: string): IntentMode | '' {
  const stripped = text
    .trim()
    .replace(/^[\s,;:.!?'"()-]+|[\s,;:.!?'"()-]+$/g, '')
    .toLowerCase()
  return MODE_PHRASE[stripped] ?? ''
}

/** One matched run of characters, to be cut out of the instruction. */
interface Span {
  start: number
  end: number
}

/** A Zoho Desk host: desk.zoho.com, desk.zoho.eu, desk.zoho.com.au, … */
const ZOHO_HOST = /^desk\.zoho\.[a-z]{2,3}(\.[a-z]{2})?$/

/** A Slack message permalink, host on a .slack.com dot boundary. */
const SLACK_LINK =
  /^https:\/\/[a-z0-9][a-z0-9-]*(\.[a-z0-9][a-z0-9-]*)*\.slack\.com\/archives\/[A-Z0-9]{6,}\/p\d{16}(?:[?#]|$)/

/** Whether a URL is on github.com: a pull request, an issue, a file. */
function isGitHubLink(text: string): boolean {
  try {
    const host = new URL(text).hostname.toLowerCase()
    return host === 'github.com' || host === 'www.github.com'
  } catch {
    return false
  }
}

/** Whether a URL is a Slack message link the workspace can read. */
export function isSlackLink(text: string): boolean {
  return SLACK_LINK.test(text)
}

/**
 * The record id in a Zoho Desk ticket link, or '' — the agent UI's
 * `…/tickets/details/<id>` and the older `ShowHomePage.do#Cases/dv/<id>`.
 */
export function helpdeskIdInURL(text: string): string {
  let url: URL
  try {
    url = new URL(text)
  } catch {
    return ''
  }
  if (!ZOHO_HOST.test(url.hostname.toLowerCase())) return ''
  const fragment = /(?:^|\/)Cases\/dv\/(\d{4,})(?:\/|$)/.exec(url.hash.replace(/^#/, ''))
  if (fragment) return fragment[1]
  const path = /\/tickets\/(?:details\/)?(\d{4,})(?:\/|$)/.exec(url.pathname)
  return path ? path[1] : ''
}

/**
 * What the workspace is asked to resolve for this line: the key, else the
 * helpdesk number, else the helpdesk link, else the Slack link — the same
 * order the service prefers them in — or '' when the line names none. It is
 * the reference alone and not the line, so typing the instruction around it
 * does not ask again.
 */
export function intentRef(intent: Intent): string {
  if (intent.key) return intent.key
  if (intent.helpdesk) return `#${intent.helpdesk}`
  return intent.helpdeskUrl || intent.slack
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
 * The mode is set by a leading slash command (`/triage`, `/rca`, `/fix`),
 * or by the line being nothing else once its references are cut out — one
 * mode word or phrase, on its own. Anything less bare is a session with
 * that word left in its instruction: "fix the tax rounding" does not set
 * the mode, because "fix" is not what the line is, it is one word in what
 * somebody asked for. `intentKind` is what decides between a session, a
 * triage and nothing, from this `mode` and `instruction`.
 *
 * The instruction is everything else, with the key, the URL, the helpdesk
 * number and (for a slash command or a bare mode word) the mode's own
 * words taken out. Empty is fine and common: "OMNI-2510" on its own names
 * no instruction, and `intentKind` reads that as a triage of OMNI-2510.
 *
 * `ambiguity` is set when the line cannot be settled here: two different
 * keys, always; `'no-key'` only when the mode is triage, RCA or fix (by
 * slash or by the bare word) and the line names no reference — a session
 * never needs one. The key still carries the first of two, so a caller
 * that has no fallback available can still show something.
 */
export function parseIntent(text: string): Intent {
  const trimmed = text.trim()
  if (trimmed === '') {
    return { key: '', helpdesk: '', helpdeskUrl: '', slack: '', mode: '', instruction: '', ambiguity: '' }
  }

  const spans: Span[] = []
  /**
   * What stays in the instruction and is read as nothing else: a GitHub
   * link, and a GitHub reference like acme/web#1234 — a pull request, not
   * helpdesk ticket #1234, and no mode word either.
   */
  const kept: Span[] = []
  for (const match of text.matchAll(GITHUB_REF)) {
    const at = match.index ?? 0
    kept.push({ start: at, end: at + match[0].length })
  }
  const taken = (span: Span): boolean => spans.some((s) => overlaps(s, span)) || kept.some((s) => overlaps(s, span))

  // A slash command is cut out like any other reference, and its mode
  // stands regardless of what else the line says.
  let mode: IntentMode | '' = ''
  const slashMatch = SLASH_MODE.exec(text)
  if (slashMatch) {
    mode = slashMatch[1].toLowerCase() as IntentMode
    spans.push({ start: slashMatch.index, end: slashMatch.index + slashMatch[0].length })
  }

  /** Every key the line names, with where it was found, so "first" is first in the line. */
  const keys: { at: number; key: string }[] = []
  let helpdeskUrl = ''
  let slack = ''

  // URLs first: a key inside one is the URL's, and the whole URL comes out
  // of the instruction rather than leaving a naked host behind. A Slack
  // link and a helpdesk link are references of their own, resolved by the
  // workspace.
  for (const match of text.matchAll(URL_LIKE)) {
    const at = match.index ?? 0
    const raw = match[0].replace(/[.,;:)\]]+$/, '')
    // A GitHub link is evidence, not a reference: it stays in the
    // instruction, where the session reads it and the service matches it
    // to a companion repository.
    if (isGitHubLink(raw)) {
      kept.push({ start: at, end: at + raw.length })
      continue
    }
    spans.push({ start: at, end: at + raw.length })
    if (isSlackLink(raw)) {
      if (!slack) slack = raw
      continue
    }
    if (helpdeskIdInURL(raw)) {
      if (!helpdeskUrl) helpdeskUrl = raw
      continue
    }
    const key = keyInURL(raw)
    if (key && !keys.some((k) => k.key === key)) keys.push({ at, key })
  }

  // A line that is nothing but a key is read in any case.
  if (LONE_KEY.test(trimmed)) {
    return {
      key: trimmed.toUpperCase(),
      helpdesk: '',
      helpdeskUrl: '',
      slack: '',
      mode: '',
      instruction: '',
      ambiguity: '',
    }
  }

  for (const match of text.matchAll(KEY)) {
    const at = match.index ?? 0
    const span = { start: at, end: at + match[0].length }
    if (taken(span)) continue
    spans.push(span)
    if (!keys.some((k) => k.key === match[0])) keys.push({ at, key: match[0] })
  }

  const helpdeskNumbers: string[] = []
  for (const match of text.matchAll(HELPDESK)) {
    const at = match.index ?? 0
    const span = { start: at, end: at + match[0].length }
    if (taken(span)) continue
    spans.push(span)
    const number = match[1] ?? ''
    if (number && !helpdeskNumbers.includes(number)) helpdeskNumbers.push(number)
  }

  keys.sort((a, b) => a.at - b.at)

  const key = keys[0]?.key ?? ''
  const helpdesk = helpdeskNumbers[0] ?? ''

  // What the line says once the slash command (if there was one) and every
  // reference are cut out. A mode already set by a slash command stands as
  // it is; otherwise this remainder becomes the mode, with the instruction
  // cleared, only when it is nothing but one mode word or phrase — the one
  // case where a word in the line is a command rather than prose.
  const remainder = withoutSpans(text, spans)
  let instruction = remainder
  if (!mode) {
    const bare = bareModeWord(remainder)
    if (bare) {
      mode = bare
      instruction = ''
    }
  }

  let ambiguity: Ambiguity = ''
  if (keys.length > 1) {
    ambiguity = 'two-keys'
  } else if (
    (mode === 'triage' || mode === 'rca' || mode === 'fix') &&
    key === '' &&
    helpdesk === '' &&
    helpdeskUrl === '' &&
    slack === ''
  ) {
    ambiguity = 'no-key'
  }

  return { key, helpdesk, helpdeskUrl, slack, mode, instruction, ambiguity }
}

/**
 * What kind of run this line starts, once `parseIntent` has read it: the
 * mode it set, outright; else a session, when there is an instruction to
 * answer; else a triage, when there is a reference and nothing to say about
 * it; else '', for the empty box. A session needs no reference — "Why is
 * the refund stuck?" is a session with no ticket in it at all — which is
 * the one way this reading differs from the Mode chip's own default before
 * task F2: triage no longer wins a line that is really a question.
 */
export function intentKind(intent: Intent): IntentMode | '' {
  if (intent.mode) return intent.mode
  if (intent.instruction.trim() !== '') return 'session'
  if (intentRef(intent) !== '') return 'triage'
  return ''
}

/** What the box says while it is empty. */
export const COMPOSER_PLACEHOLDER =
  'Ask anything, or paste a ticket key, #helpdesk number, or ticket or Slack link'

/** Why nothing can be started, when nothing resolved. */
export const NO_KEY_REASON =
  'No ticket yet — type a key like SBX-1 or a helpdesk number like #28310, or paste a ticket or Slack link'

/** The Mode selector's word for each mode, for the chips. */
export const MODE_LABEL: Record<IntentMode, string> = {
  session: 'Session',
  triage: 'Triage',
  rca: 'RCA',
  fix: 'Fix',
}

/** The Access chip's word, in the spot `intentChips` puts it for a session. */
const ACCESS_WORD: Record<'read-only' | 'worktree', string> = {
  'read-only': 'read-only',
  worktree: 'writes in worktree',
}

/**
 * The chips under the box: what was understood, in the order a person reads
 * it — the mode, then the ticket, then whether anything else was asked for.
 * The note chip appears only when there is an instruction, because "with
 * your note" on a line that carries none says something untrue.
 *
 * A session's chips are different in kind, not just in word: its
 * instruction is its task, so there is no "with your note" chip for it —
 * and its last chip is the access it will run with, since that is the one
 * thing about a session the Mode chip does not already say.
 */
export function intentChips(o: {
  mode: IntentMode
  key: string
  instruction: string
  /**
   * How the key was found, when the workspace resolved it — "#28310 →
   * SBX-1 · matched by title", "SBX-1 · #28310". It stands in the key's
   * place, since it names the key.
   */
  resolution?: string
  /** Session only: the posture it will run with. */
  access?: 'read-only' | 'worktree'
}): string[] {
  if (o.mode === 'session') {
    const chips = [MODE_LABEL.session]
    const named = o.resolution || o.key
    if (named) chips.push(named)
    if (o.access) chips.push(ACCESS_WORD[o.access])
    return chips
  }
  const chips = [MODE_LABEL[o.mode], o.resolution || o.key]
  if (o.instruction.trim() !== '') chips.push('with your note')
  return chips
}
