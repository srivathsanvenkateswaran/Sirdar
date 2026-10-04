/**
 * Which repositories a line names, and which one it asks the session to look
 * in — the composer's copy of internal/repos, so the chip can say so on
 * every keystroke without a call.
 *
 * It is the same reading the service makes when it writes the prompt: both
 * are tested against internal/repos/testdata/mentions.json, so a line the
 * chip reads as "look in Acme.Web" is a line the session is told to look in
 * Acme.Web.
 */
import type { RepoAsk, RepoMention, RepoSummary } from '../api/types'

/** The part of a repository the reading needs. */
export type Repo = Pick<RepoSummary, 'name' | 'path' | 'origin' | 'workspace'>

/** A remote as `host/owner/name`, lower-cased, or '' when it names no hosted repository. */
export function slugOf(remote: string | undefined): string {
  const r = (remote ?? '').trim()
  if (r === '') return ''
  let host = ''
  let path = ''
  if (r.includes('://')) {
    let u: URL
    try {
      u = new URL(r)
    } catch {
      return ''
    }
    if (!u.host) return ''
    host = u.hostname
    path = u.pathname
  } else {
    const at = r.indexOf(':')
    if (at <= 1 || r.slice(0, at).includes('/')) return ''
    host = r.slice(0, at)
    const user = host.lastIndexOf('@')
    if (user >= 0) host = host.slice(user + 1)
    path = r.slice(at + 1)
  }
  path = path.replace(/^\/+|\/+$/g, '').replace(/\.git$/, '')
  const parts = path.split('/')
  if (!host || parts.length < 2) return ''
  const owner = parts[parts.length - 2]
  const name = parts[parts.length - 1]
  if (!owner || !name) return ''
  return `${host}/${owner}/${name}`.toLowerCase()
}

const URL_IN = /https?:\/\/[^\s<>"'`]+/g
const OWNER_NAME_REF = /(?:^|[^A-Za-z0-9._/-])([A-Za-z0-9][A-Za-z0-9-]{0,38})\/([A-Za-z0-9._-]{1,100})#([0-9]+)\b/g

function statusOf(r: Repo): RepoMention['status'] {
  return r.workspace ? 'workspace' : 'companion'
}

function baseOf(path: string): string {
  const parts = path.split(/[\\/]/).filter((p) => p !== '')
  return parts[parts.length - 1] ?? ''
}

function same(a: string, b: string): boolean {
  return a.toLowerCase() === b.toLowerCase()
}

function resolveHosted(owner: string, name: string, ref: string, list: Repo[]): RepoMention {
  const slug = `github.com/${owner}/${name}`.toLowerCase()
  for (const r of list) {
    const s = slugOf(r.origin)
    if (s !== '' && s === slug) return { name: r.name, slug: `${owner}/${name}`, ref, status: statusOf(r) }
  }
  for (const r of list) {
    if (slugOf(r.origin) === '' && (same(r.name, name) || same(baseOf(r.path), name))) {
      return { name: r.name, slug: `${owner}/${name}`, ref, status: statusOf(r) }
    }
  }
  return { name, slug: `${owner}/${name}`, ref, status: 'unknown' }
}

function githubRepoInURL(raw: string): { owner: string; name: string } | null {
  let u: URL
  try {
    u = new URL(raw)
  } catch {
    return null
  }
  const host = u.hostname.toLowerCase()
  if (host !== 'github.com' && host !== 'www.github.com') return null
  const parts = u.pathname.replace(/^\/+|\/+$/g, '').split('/')
  if (parts.length < 2 || !parts[0] || !parts[1]) return null
  return { owner: parts[0], name: parts[1].replace(/\.git$/, '') }
}

function nameChar(c: string): boolean {
  return /[A-Za-z0-9_-]/.test(c)
}

/** Where `name` first stands as a word of its own in `text`, case aside, or -1. */
function wordIndex(text: string, name: string): number {
  if (name.trim() === '') return -1
  const lower = text.toLowerCase()
  const needle = name.toLowerCase()
  let from = 0
  for (;;) {
    const at = lower.indexOf(needle, from)
    if (at < 0) return -1
    const end = at + needle.length
    const prev = at === 0 ? '' : lower[at - 1]
    const before = at === 0 || (!nameChar(prev) && prev !== '.' && prev !== '/')
    const next = end === lower.length ? '' : lower[end]
    const after =
      end === lower.length ||
      (!nameChar(next) && !(next === '.' && end + 1 < lower.length && nameChar(lower[end + 1])))
    if (before && after) return at
    from = at + 1
  }
}

/**
 * Every repository the text names, in the order it names them, each once: a
 * GitHub URL, an `owner/name#N` reference, or a configured repository's bare
 * name. A hosted reference is matched by origin; one no origin matches is
 * unknown.
 */
export function repoMentions(text: string, list: Repo[]): RepoMention[] {
  const hits: { at: number; m: RepoMention }[] = []
  const rest = text.split('')
  const blank = (from: number, to: number): void => {
    for (let i = from; i < to && i < rest.length; i++) rest[i] = ' '
  }
  for (const match of text.matchAll(URL_IN)) {
    const at = match.index ?? 0
    const raw = match[0].replace(/[.,;:)\]}>]+$/, '')
    blank(at, at + match[0].length)
    const repo = githubRepoInURL(raw)
    if (repo) hits.push({ at, m: resolveHosted(repo.owner, repo.name, raw, list) })
  }
  const scanned = rest.join('')
  for (const match of scanned.matchAll(OWNER_NAME_REF)) {
    const [whole, owner, name, num] = match
    const ref = `${owner}/${name}#${num}`
    const at = (match.index ?? 0) + whole.length - ref.length
    hits.push({ at, m: resolveHosted(owner, name, ref, list) })
    blank(at, at + ref.length)
  }
  const plain = rest.join('')
  for (const r of list) {
    const at = wordIndex(plain, r.name)
    if (at >= 0) hits.push({ at, m: { name: r.name, ref: plain.slice(at, at + r.name.length), status: statusOf(r) } })
  }
  hits.sort((a, b) => a.at - b.at)
  const out: RepoMention[] = []
  const seen = new Set<string>()
  for (const { m } of hits) {
    const k = m.name.toLowerCase()
    if (seen.has(k)) continue
    seen.add(k)
    out.push(m)
  }
  return out
}

/** The chip's words for a mention; '' for the workspace's own repository. */
export function mentionPhrase(m: RepoMention): string {
  if (m.status === 'companion') return `mentions ${m.name} (companion repo)`
  if (m.status === 'unknown') return `mentions ${m.name} (not configured — add it under repos:)`
  return ''
}

const ASK_VERB =
  /\b(?:look(?:\s+(?:into|in|at|through))?|check(?:\s+(?:into|in))?|search(?:\s+(?:in|through))?|dig\s+into|go\s+through)\s+(?:the\s+)?/gi
const WORD = '[A-Za-z0-9_][A-Za-z0-9_./-]*[A-Za-z0-9_]|[A-Za-z0-9_]'
const ASK_IN = new RegExp(`\\bin\\s+(?:the\\s+)?(${WORD})\\s+(?:repo|repository|codebase|project)\\b`, 'gi')
const WORD_AT = new RegExp(`^(?:${WORD})`)
const REPO_NOUNS = new Set(['repo', 'repository', 'codebase', 'project', 'app', 'frontend', 'backend'])

function wordsAfter(text: string, n: number): string[] {
  const out: string[] = []
  let rest = text
  while (out.length < n) {
    const trimmed = rest.replace(/^[ \t]+/, '')
    if (out.length > 0 && trimmed.length === rest.length) break
    const w = WORD_AT.exec(trimmed)?.[0] ?? ''
    if (w === '') break
    out.push(w)
    rest = trimmed.slice(w.length)
  }
  return out
}

function isLetter(c: string): boolean {
  return /[A-Za-z]/.test(c)
}

function repoShaped(w: string): boolean {
  const slashes = w.split('/').length - 1
  if (slashes === 1 && !w.startsWith('/') && !w.endsWith('/')) return true
  if (!w.includes('.') || w.includes('/')) return false
  const parts = w.split('.')
  if (parts.some((p) => p === '' || !isLetter(p[0]))) return false
  const last = parts[parts.length - 1][0]
  return last >= 'A' && last <= 'Z'
}

function shortName(name: string): string {
  const i = Math.max(name.lastIndexOf('.'), name.lastIndexOf('-'), name.lastIndexOf('_'))
  if (i < 0 || i === name.length - 1) return ''
  return name.slice(i + 1)
}

function lookup(word: string, list: Repo[]): RepoAsk | null {
  const slash = word.indexOf('/')
  if (slash > 0 && word.split('/').length === 2) {
    const m = resolveHosted(word.slice(0, slash), word.slice(slash + 1), word, list)
    return m.status !== 'unknown' ? { phrase: word, name: m.name, status: m.status } : null
  }
  for (const r of list) {
    if (same(r.name, word) || (r.path !== '' && same(baseOf(r.path), word))) {
      return { phrase: word, name: r.name, status: statusOf(r) }
    }
  }
  const hits = list.filter((r) => {
    const alias = shortName(r.name)
    return alias !== '' && same(alias, word)
  })
  if (hits.length === 0) return null
  if (hits.length === 1) return { phrase: word, name: hits[0].name, status: statusOf(hits[0]) }
  return { phrase: word, status: 'ambiguous', candidates: hits.map((r) => r.name) }
}

function askOf(words: string[], list: Repo[]): RepoAsk | null {
  for (const w of words) {
    if (REPO_NOUNS.has(w.toLowerCase())) break
    const a = lookup(w, list)
    if (a) return a
  }
  for (let i = 0; i < words.length; i++) {
    const w = words[i]
    if (REPO_NOUNS.has(w.toLowerCase())) {
      return i > 0 ? { phrase: words[i - 1], status: 'unknown' } : null
    }
    if (repoShaped(w)) return { phrase: w, status: 'unknown' }
  }
  return null
}

/**
 * The repositories the line asks the session to look in — "look in
 * Acme.Web", "check the POS app", "in the reports repo" — in order, each
 * once. A phrase that names no repository counts only when it looks like one.
 */
export function repoAsks(text: string, list: Repo[]): RepoAsk[] {
  const hits: { at: number; a: RepoAsk }[] = []
  for (const match of text.matchAll(ASK_VERB)) {
    const at = match.index ?? 0
    const a = askOf(wordsAfter(text.slice(at + match[0].length), 3), list)
    if (a) hits.push({ at, a })
  }
  for (const match of text.matchAll(ASK_IN)) {
    const a = askOf([match[1], 'repo'], list)
    if (a) hits.push({ at: match.index ?? 0, a })
  }
  hits.sort((x, y) => x.at - y.at)
  const out: RepoAsk[] = []
  const seen = new Set<string>()
  for (const { a } of hits) {
    const k = (a.name ?? a.phrase).toLowerCase()
    if (seen.has(k)) continue
    seen.add(k)
    out.push(a)
  }
  return out
}

/** Why an ask stops a start; '' for one that resolved. */
export function askReason(a: RepoAsk): string {
  if (a.status === 'unknown') return `${a.phrase} is not a configured repository`
  if (a.status === 'ambiguous') return `${a.phrase} matches more than one repository: ${(a.candidates ?? []).join(', ')}`
  return ''
}
