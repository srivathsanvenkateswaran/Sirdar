import { afterEach, describe, expect, it, vi } from 'vitest'
import {
  DEFAULT_SESSION_LAYOUT,
  SESSION_LAYOUT_KEY,
  SESSION_LAYOUT_OPTIONS,
  resetSessionLayout,
  sessionLayout,
  setSessionLayout,
  subscribeSessionLayout,
} from './sessionLayout'

describe('the session layout preference', () => {
  afterEach(() => {
    localStorage.clear()
    resetSessionLayout()
  })

  it('is Conversation until chosen otherwise, and the options list it first', () => {
    expect(DEFAULT_SESSION_LAYOUT).toBe('conversation')
    expect(sessionLayout()).toBe('conversation')
    expect(SESSION_LAYOUT_OPTIONS.map((o) => o.id)).toEqual(['conversation', 'document', 'workbench'])
  })

  it('remembers the choice under sirdar.sessionLayout and tells its watchers', () => {
    const watcher = vi.fn()
    const stop = subscribeSessionLayout(watcher)
    setSessionLayout('document')
    expect(sessionLayout()).toBe('document')
    expect(localStorage.getItem(SESSION_LAYOUT_KEY)).toBe('document')
    expect(watcher).toHaveBeenCalledTimes(1)
    // The same value again is not a change.
    setSessionLayout('document')
    expect(watcher).toHaveBeenCalledTimes(1)
    stop()
    setSessionLayout('workbench')
    expect(watcher).toHaveBeenCalledTimes(1)
  })

  it('reads a stored choice back and falls to the default on a value it does not know', () => {
    localStorage.setItem(SESSION_LAYOUT_KEY, 'workbench')
    resetSessionLayout()
    expect(sessionLayout()).toBe('workbench')
    localStorage.setItem(SESSION_LAYOUT_KEY, 'sideways')
    resetSessionLayout()
    expect(sessionLayout()).toBe('conversation')
  })
})
