import { describe, expect, it } from 'vitest'
import type { SourcesSummary } from '../api/types'
import { run } from '../store/fakeTransport'
import { STATE_WORDS } from '../ui/status-badge'
import { matchRun, normalizeQuery, splitMatch, splitText } from './sessionSearch'

const SOURCES: SourcesSummary = {
  tracker: { adapter: 'exec', name: 'Janus', host: 'janus.example.com' },
  helpdesk: { adapter: 'zohodesk', name: 'Zoho Desk', host: 'desk.zoho.com' },
}

const RUN = run({
  runId: 'r1',
  key: 'OMNI-2815',
  helpdeskKey: '25312',
  title: 'Login loop after reset',
  kind: 'triage',
  status: 'blocked',
  provider: 'codex',
  model: 'gpt-5-codex',
})

const match = (q: string, alias?: string, show: 'tracker' | 'helpdesk' = 'tracker') =>
  matchRun(RUN, q, { alias, show, sources: SOURCES })

describe('matchRun', () => {
  it('answers nothing for a blank query', () => {
    expect(match('')).toBeNull()
    expect(match('   ')).toBeNull()
    expect(normalizeQuery('  OMNI ')).toBe('omni')
  })

  it('finds the shown number first, case folded, with the range for the mark', () => {
    const m = match('omni-28')!
    expect(m).toEqual({ field: 'number', text: 'OMNI-2815', start: 0, end: 7 })
    expect(splitMatch(m)).toEqual(['', 'OMNI-28', '15'])
    // On the helpdesk preference the shown number is the helpdesk's, and the key is still found.
    expect(match('253', undefined, 'helpdesk')).toEqual({ field: 'number', text: '#25312', start: 1, end: 4 })
    expect(match('2815', undefined, 'helpdesk')?.field).toBe('key')
  })

  it('reaches the helpdesk number, alias, title, kind, provider, model and state word, in that order', () => {
    expect(match('#253')).toMatchObject({ field: 'helpdesk', text: '#25312' })
    expect(match('the one', 'the one that loops')).toMatchObject({ field: 'alias', start: 0, end: 7 })
    expect(match('after RESET')).toMatchObject({ field: 'title', text: 'Login loop after reset', start: 11 })
    expect(match('tria')).toMatchObject({ field: 'kind', text: 'triage' })
    expect(match('codex')).toMatchObject({ field: 'provider', text: 'codex' })
    expect(match('gpt-5')).toMatchObject({ field: 'model', text: 'gpt-5-codex' })
    expect(match(STATE_WORDS.blocked.toLowerCase())).toMatchObject({ field: 'state', text: STATE_WORDS.blocked })
    expect(match('zzz')).toBeNull()
  })

  it('skips fields the run does not have', () => {
    const bare = run({ runId: 'r2', key: 'OMNI-1', helpdeskKey: '', title: '' })
    expect(matchRun(bare, '#', { show: 'tracker' })).toBeNull()
  })
})

describe('splitText', () => {
  it('cuts around the first occurrence, case folded, or leaves the text whole', () => {
    expect(splitText('The Export times out', 'export')).toEqual(['The ', 'Export', ' times out'])
    expect(splitText('nothing here', 'zzz')).toEqual(['nothing here', '', ''])
    expect(splitText('nothing here', '')).toEqual(['nothing here', '', ''])
  })
})
