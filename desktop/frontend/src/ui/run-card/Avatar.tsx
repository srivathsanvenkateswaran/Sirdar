import './Avatar.css'

/**
 * The initials an assignee is drawn as: one letter for a one-word name, two
 * for a name that has two parts to take them from.
 *
 * An address is read by its local part, since the domain is the company and
 * every assignee in a workspace shares it; the separators inside a local part
 * — `sri.v`, `sri_v`, `sri-v` — are word breaks, which is how an address that
 * carries a full name still gives two initials.
 */
export function initialsOf(name: string): string {
  const who = name.trim()
  if (!who) return ''
  const local = who.includes('@') ? who.slice(0, who.indexOf('@')) : who
  const parts = local.split(/[\s._+-]+/).filter(Boolean)
  if (parts.length === 0) return ''
  const letters = parts.length === 1 ? [parts[0][0]] : [parts[0][0], parts[1][0]]
  return letters.join('').toUpperCase()
}

/**
 * Who a ticket belongs to, as a circle of their initials.
 *
 * It is decorative markup: the whole name is in the `title` for a pointer,
 * and the caller is the one that puts it in the accessible name — the run
 * card ends its label with it, the register writes the name in the same cell.
 * An avatar that announced itself would say the name twice in both places.
 *
 * Nothing is drawn for an assignee nobody knows: an empty circle is a person
 * with no initials, not a ticket assigned to no one.
 */
export default function Avatar({ name }: { name: string }): JSX.Element | null {
  const initials = initialsOf(name)
  if (!initials) return null
  return (
    <span className="sd-avatar" title={name} aria-hidden="true" dir="ltr">
      {initials}
    </span>
  )
}
