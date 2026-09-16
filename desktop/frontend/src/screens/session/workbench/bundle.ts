import type { Bundle } from '../BundleView'

/**
 * What the Bundle document adds to the Conversation layout's reading of the
 * prompt (`parseBundle` in `../BundleView`): a fact by name, the helpdesk's
 * number off its URL, and the answer's translations matched to the
 * customer's messages.
 */

export type { Bundle, BundleMessage, Playbook } from '../BundleView'
export { parseBundle } from '../BundleView'

/** One `Key: value` line of the ticket block, by its key, case aside. */
export function fact(bundle: Bundle, key: string): string {
  return bundle.facts.find((f) => f.key.toLowerCase() === key.toLowerCase())?.value ?? ''
}

/** The ticket's other facts: everything the two cards do not already name. */
export function otherFacts(bundle: Bundle, named: string[]): { key: string; value: string }[] {
  const skip = new Set(named.map((k) => k.toLowerCase()))
  return bundle.facts.filter((f) => !skip.has(f.key.toLowerCase()))
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
