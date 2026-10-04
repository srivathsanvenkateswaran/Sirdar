import { describe, expect, it } from 'vitest'
import type { RunEvent, RunKind } from '../api/types'
import { isReplyRun, latestReply, noteStem } from './replyRun'

const final = (text: string, phase?: 'note'): RunEvent => ({ t: '2026-10-04T10:00:00Z', kind: 'final', payload: { text, ...(phase ? { phase } : {}) } })

describe('isReplyRun', () => {
  const kinds: RunKind[] = ['session', 'triage', 'rca', 'fix']

  it('is true for every session run, flag or not', () => {
    expect(isReplyRun({ kind: 'session' })).toBe(true)
    expect(isReplyRun({ kind: 'session', replyFirst: true })).toBe(true)
  })

  it('is true for any other kind only with replyFirst', () => {
    for (const kind of kinds.filter((k) => k !== 'session')) {
      expect(isReplyRun({ kind })).toBe(false)
      expect(isReplyRun({ kind, replyFirst: false })).toBe(false)
      expect(isReplyRun({ kind, replyFirst: true })).toBe(true)
    }
  })

  it('is false with no run', () => {
    expect(isReplyRun(undefined)).toBe(false)
    expect(isReplyRun(null)).toBe(false)
  })
})

describe('latestReply', () => {
  it('reads the last final that is not the note turn and says something', () => {
    const events = [final('first'), final('  second \n'), final(''), final('{"summary":"x"}', 'note')]
    expect(latestReply(events)).toBe('second')
  })

  it('is empty when the run has not replied', () => {
    expect(latestReply([final('{"a":1}', 'note')])).toBe('')
    expect(latestReply([])).toBe('')
  })
})

describe('noteStem', () => {
  it('drops the folder and the .md', () => {
    expect(noteStem('/notes/OMNI-1 refund-stuck.md')).toBe('OMNI-1 refund-stuck')
    expect(noteStem('/notes/Sessions/run-session-1.md')).toBe('run-session-1')
  })
})
