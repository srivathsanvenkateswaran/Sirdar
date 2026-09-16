import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import {
  DEFAULT_SESSION_LAYOUT,
  SESSION_LAYOUT_OPTIONS,
  resetSessionLayout,
  sessionLayout,
  setSessionLayout,
  subscribeSessionLayout,
} from './sessionLayout'

describe('the session layout preference', () => {
  beforeEach(() => {
    localStorage.clear()
    resetSessionLayout()
  })
  afterEach(() => {
    localStorage.clear()
    resetSessionLayout()
  })

  it('opens on Conversation, which the Settings row lists first', () => {
    expect(DEFAULT_SESSION_LAYOUT).toBe('conversation')
    expect(sessionLayout()).toBe('conversation')
    expect(SESSION_LAYOUT_OPTIONS.map((o) => o.id)).toEqual(['conversation', 'document', 'workbench'])
  })

  it('remembers a choice under sirdar.sessionLayout and tells its watchers', () => {
    const watcher = vi.fn()
    const off = subscribeSessionLayout(watcher)
    setSessionLayout('document')
    expect(sessionLayout()).toBe('document')
    expect(localStorage.getItem('sirdar.sessionLayout')).toBe('document')
    expect(watcher).toHaveBeenCalledTimes(1)
    setSessionLayout('document')
    expect(watcher).toHaveBeenCalledTimes(1)
    off()
  })

  it('reads a stored choice back and ignores a value it does not know', () => {
    localStorage.setItem('sirdar.sessionLayout', 'workbench')
    resetSessionLayout()
    expect(sessionLayout()).toBe('workbench')
    localStorage.setItem('sirdar.sessionLayout', 'spreadsheet')
    resetSessionLayout()
    expect(sessionLayout()).toBe('conversation')
  })
})
