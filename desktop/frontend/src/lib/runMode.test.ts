import { describe, expect, it } from 'vitest'
import { MODES_WITH_ACCESS } from '../components/composer/modes'
import { runModeItems, runModeTitle } from './runMode'

const labelOf = (items: typeof MODES_WITH_ACCESS, id: string) => items.find((m) => m.id === id)?.label

describe('runModeItems', () => {
  it('says a worktree session writes in its worktree', () => {
    expect(labelOf(runModeItems('session', 'worktree'), 'session')).toBe('Session · writes in worktree')
  })

  it('keeps the default words for a read-only session and for every other kind', () => {
    expect(runModeItems('session', 'read-only')).toBe(MODES_WITH_ACCESS)
    expect(runModeItems('session')).toBe(MODES_WITH_ACCESS)
    expect(labelOf(runModeItems('fix'), 'fix')).toBe('Fix · writes in worktree')
    expect(labelOf(runModeItems('triage', 'worktree'), 'triage')).toBe('Triage · read-only')
  })
})

describe('runModeTitle', () => {
  it('explains the worktree for a worktree session', () => {
    expect(runModeTitle('session', 'worktree')).toMatch(/linked worktree/)
    expect(runModeTitle('session', 'read-only')).toMatch(/Nothing is written/)
  })
})
