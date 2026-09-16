import { describe, expect, it } from 'vitest'
import type { MeSummary, RunSummary, Ticket } from '../api/types'
import { run, ticket } from '../store/fakeTransport'
import {
  assigneeOptions,
  foldName,
  identityOf,
  matchingOptions,
  ME,
  selectedNames,
  triggerLabel,
} from './assignees'

const me: MeSummary = { email: 'sri@acme.com', names: ['Sri Venkateswaran'], source: 'me' }

/** Runs by three people: two the reader's, two Rana's, one Noura's. */
const RUNS: RunSummary[] = [
  run({ runId: 'r1', key: 'OMNI-1' }),
  run({ runId: 'r2', key: 'OMNI-2' }),
  run({ runId: 'r3', key: 'OMNI-3', assignee: 'Rana K', mine: false }),
  run({ runId: 'r4', key: 'OMNI-4', assignee: 'rana k', mine: false }),
  run({ runId: 'r5', key: 'OMNI-5', assignee: 'Noura A', mine: false }),
]

const QUEUED: Ticket[] = [
  ticket({ key: 'OMNI-9', assignee: 'sri' }),
  ticket({ key: 'OMNI-8', assignee: 'Layla M' }),
]
const MINE_KEYS = new Set(['OMNI-9'])

describe('foldName', () => {
  it('makes one key of the spellings a form let through', () => {
    expect(foldName('  Rana   K ')).toBe('rana k')
    expect(foldName('RANA K')).toBe(foldName('rana k'))
    expect(foldName('   ')).toBe('')
  })
})

describe('identityOf', () => {
  it('is the address, else the first name, else nobody', () => {
    expect(identityOf(me)).toBe('sri@acme.com')
    expect(identityOf({ email: '', names: ['Noura A'], source: 'git' })).toBe('Noura A')
    expect(identityOf({ email: '', names: [], source: '' })).toBe('')
    expect(identityOf(undefined)).toBe('')
  })
})

describe('assigneeOptions', () => {
  it('pins Me first and counts each person’s runs', () => {
    const options = assigneeOptions(RUNS, QUEUED, MINE_KEYS, me)
    expect(options.map((o) => [o.label, o.runs])).toEqual([
      ['Me', 2],
      ['Rana K', 2],
      ['Noura A', 1],
      ['Layla M', 0],
    ])
    expect(options[0].id).toBe(ME)
    expect(options[0].who).toBe('sri@acme.com')
  })

  it('never lists the reader twice, whatever their ticket calls them', () => {
    const options = assigneeOptions(RUNS, QUEUED, MINE_KEYS, me)
    // OMNI-9 is the reader's, and its assignee 'sri' is not a row of its own.
    expect(options.some((o) => o.label === 'sri')).toBe(false)
  })

  it('leaves Me nameless, and still first, when nobody is set', () => {
    const options = assigneeOptions(RUNS, QUEUED, new Set(), {
      email: '',
      names: [],
      source: '',
    })
    expect(options[0].id).toBe(ME)
    expect(options[0].who).toBe('')
  })

  it('drops a run nobody owns rather than giving it a nameless row', () => {
    const orphan = [run({ runId: 'r9', key: 'OMNI-99', assignee: '', mine: false })]
    expect(assigneeOptions(orphan, [], new Set(), me)).toHaveLength(1)
  })

  it('merges in people the tracker knows about who have no runs yet', () => {
    const options = assigneeOptions(RUNS, QUEUED, MINE_KEYS, me, [
      { name: 'Omar S', email: 'omar@acme.com' },
      // The reader is already Me, however the tracker spells them.
      { name: 'Sri Venkateswaran', email: 'sri@acme.com' },
    ])
    expect(options.map((o) => o.label)).toContain('Omar S')
    expect(options.filter((o) => o.label === 'Sri Venkateswaran')).toHaveLength(0)
  })
})

describe('triggerLabel', () => {
  const options = assigneeOptions(RUNS, QUEUED, MINE_KEYS, me)

  it('reads Anyone, Me, or the names with a count of the rest', () => {
    expect(triggerLabel(new Set(), options)).toBe('Anyone')
    expect(triggerLabel(new Set([ME]), options)).toBe('Me')
    expect(triggerLabel(new Set(['rana k', 'noura a']), options)).toBe('Rana K, Noura A')
    expect(triggerLabel(new Set(['rana k', 'noura a', 'layla m']), options)).toBe(
      'Rana K, Noura A +1',
    )
  })

  it('reads Anyone again when what was picked is no longer on the board', () => {
    expect(triggerLabel(new Set(['someone who left']), options)).toBe('Anyone')
  })
})

describe('selectedNames', () => {
  const options = assigneeOptions(RUNS, QUEUED, MINE_KEYS, me)

  it('names the reader by their identity, not by the word Me', () => {
    expect(selectedNames(new Set([ME]), options)).toBe('sri@acme.com')
    expect(selectedNames(new Set([ME, 'rana k']), options)).toBe('sri@acme.com, Rana K')
  })
})

describe('matchingOptions', () => {
  const options = assigneeOptions(RUNS, QUEUED, MINE_KEYS, me)

  it('answers a search on the row’s words or the name behind them', () => {
    expect(matchingOptions(options, '').length).toBe(options.length)
    expect(matchingOptions(options, 'rana').map((o) => o.label)).toEqual(['Rana K'])
    // Me is found by the identity the row is drawn for.
    expect(matchingOptions(options, 'sri@').map((o) => o.label)).toEqual(['Me'])
    expect(matchingOptions(options, 'nobody')).toEqual([])
  })
})
