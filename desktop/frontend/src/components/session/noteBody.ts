/*
 * The note as a reader wants it, not as Obsidian wants it.
 *
 * A filed note opens with the vault's own scaffolding: the YAML
 * frontmatter, then a wikilink line under the title — `Register:
 * [[_Issue Register]] · RCA: [[…]] · Resolution: <fill: Resolution>` —
 * and, in some templates, a "Working document — …" callout saying the note
 * is still being edited. All three are for the vault. In the inspector the
 * wikilinks go nowhere, the `<fill: …>` placeholders read as broken, and
 * the reader is looking at the note precisely because they are in the app
 * rather than in Obsidian.
 *
 * So the pane renders the note from its first heading down. What is hidden
 * is not thrown away: the footer row says which file this is and offers to
 * open it, where the whole thing is as the vault holds it.
 */

/** Is this line the template's wikilink/related-notes strip? */
function isLinkStrip(line: string): boolean {
  const text = line.trim()
  if (text === '') return false
  // `Register: [[…]] · RCA: [[…]] · Resolution: <fill: Resolution>` and the
  // shapes around it: a run of `Label: value` pairs joined by `·` whose
  // values are all wikilinks or unfilled placeholders.
  if (!/\[\[|<fill:/.test(text)) return false
  return text
    .split('·')
    .every((part) => /^\s*[A-Za-z][A-Za-z ]*:\s*(\[\[[^\]]*\]\]|<fill:[^>]*>|[A-Za-z0-9][^·]*)\s*$/.test(part))
}

/** Is this line the "Working document — …" callout the templates open with? */
function isWorkingCallout(line: string): boolean {
  const text = line.trim().replace(/^>\s?/, '').replace(/^[*_]+|[*_]+$/g, '')
  return /^working document\b/i.test(text)
}

/**
 * The note's body from its first `# ` heading, with the template's link
 * strip and working-document callout dropped from under the title.
 *
 * A note with no heading at all is returned as it is: it is somebody's own
 * markdown and guessing at its shape would lose the top of it.
 */
export function noteFromFirstHeading(body: string): string {
  const lines = body.split('\n')
  const title = lines.findIndex((line) => /^#{1,2}\s+\S/.test(line))
  if (title === -1) return body
  const out = [lines[title]]
  let i = title + 1
  // Only the scaffolding directly under the title is dropped: once a line
  // of the note's own prose is reached, everything after it is the note.
  for (; i < lines.length; i += 1) {
    const line = lines[i]
    if (line.trim() === '') continue
    if (isLinkStrip(line) || isWorkingCallout(line)) continue
    break
  }
  out.push('', ...lines.slice(i))
  return out.join('\n').trimEnd()
}

/**
 * The note's path shortened to the notes directory it sits in, which is
 * what the footer says: `Triage/SBX-1 stock-drift.md`. The notes directory
 * is not configured anywhere the UI can read, so it is inferred — the last
 * two segments are enough to tell two notes apart and short enough to sit
 * on one line — and the full path stays on the row's `title`.
 */
export function noteLabel(path: string, notesDir?: string): string {
  const clean = path.replace(/\\/g, '/')
  if (notesDir) {
    const dir = notesDir.replace(/\\/g, '/').replace(/\/+$/, '')
    if (clean.startsWith(`${dir}/`)) return clean.slice(dir.length + 1)
  }
  const parts = clean.split('/').filter(Boolean)
  return parts.slice(-2).join('/') || clean
}
