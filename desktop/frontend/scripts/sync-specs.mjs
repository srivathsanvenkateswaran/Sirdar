#!/usr/bin/env node
/**
 * Copies every component spec from the code into the docs library.
 *
 * `src/ui/<component>/SPEC.md` is the original: it sits beside the component
 * it describes, so a change to the component and a change to its spec are one
 * diff and one review. `docs/design/library/<component>/SPEC.md` is a copy, in
 * the place the design library expects to find it — the same rule
 * `docs/design/01-tokens.md` states for the tokens file, for the same reason:
 * the alternative is two spellings of the same component drifting apart for a
 * year before anyone notices.
 *
 *   node scripts/sync-specs.mjs           write the copies
 *   node scripts/sync-specs.mjs --check   fail if any copy is out of date
 *
 * The --check mode is what CI runs. It says which file is stale and what to
 * run, rather than diffing silently.
 */

import { mkdirSync, readFileSync, readdirSync, writeFileSync } from 'node:fs'
import { dirname, join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const HERE = dirname(fileURLToPath(import.meta.url))
const UI = resolve(HERE, '..', 'src', 'ui')
const LIBRARY = resolve(HERE, '..', '..', '..', 'docs', 'design', 'library')

/** The line every copy opens with, so nobody edits the copy by mistake. */
function banner(component) {
  return [
    '<!--',
    '  Copied from desktop/frontend/src/ui/' + component + '/SPEC.md by',
    '  desktop/frontend/scripts/sync-specs.mjs. Edit the original, then run',
    '  `node desktop/frontend/scripts/sync-specs.mjs` from the frontend.',
    '-->',
    '',
  ].join('\n')
}

/** Every component folder that has a spec, in alphabetical order. */
function specs() {
  return readdirSync(UI, { withFileTypes: true })
    .filter((entry) => entry.isDirectory())
    .map((entry) => entry.name)
    .filter((name) => {
      try {
        readFileSync(join(UI, name, 'SPEC.md'))
        return true
      } catch {
        // `motion/` is a shared module, not a component; it has no spec of its
        // own and is described inside the ambient one.
        return false
      }
    })
    .sort()
}

const check = process.argv.includes('--check')
const stale = []
let written = 0

for (const component of specs()) {
  const source = readFileSync(join(UI, component, 'SPEC.md'), 'utf8')
  const want = banner(component) + source
  const target = join(LIBRARY, component, 'SPEC.md')

  let current = null
  try {
    current = readFileSync(target, 'utf8')
  } catch {
    current = null
  }

  if (current === want) continue
  if (check) {
    stale.push(component)
    continue
  }

  mkdirSync(dirname(target), { recursive: true })
  writeFileSync(target, want)
  written += 1
  console.log(`wrote docs/design/library/${component}/SPEC.md`)
}

if (check && stale.length > 0) {
  console.error(
    `${stale.length} spec ${stale.length === 1 ? 'copy is' : 'copies are'} out of date: ` +
      stale.join(', ') +
      '\nRun: node desktop/frontend/scripts/sync-specs.mjs',
  )
  process.exit(1)
}

console.log(
  check
    ? `${specs().length} spec copies are up to date`
    : `${specs().length} specs, ${written} copied`,
)
