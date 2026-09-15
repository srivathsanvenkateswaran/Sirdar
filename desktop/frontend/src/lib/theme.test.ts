// @vitest-environment jsdom
import { afterEach, describe, expect, it } from 'vitest'
import { applyTheme, resetTheme, setTheme, subscribeTheme, theme } from './theme'

afterEach(() => {
  localStorage.clear()
  resetTheme()
})

describe('the theme preference', () => {
  it('starts on the system theme, with nothing stamped on the root', () => {
    applyTheme()
    expect(theme()).toBe('system')
    expect(document.documentElement.dataset.theme).toBeUndefined()
  })

  it('stamps a chosen theme on the root and remembers it', () => {
    setTheme('dark')
    expect(document.documentElement.dataset.theme).toBe('dark')
    expect(localStorage.getItem('sirdar.theme')).toBe('dark')

    resetTheme()
    expect(theme()).toBe('dark')
    expect(document.documentElement.dataset.theme).toBe('dark')
  })

  it('going back to system clears the stamp and the stored choice', () => {
    setTheme('light')
    setTheme('system')
    expect(document.documentElement.dataset.theme).toBeUndefined()
    expect(localStorage.getItem('sirdar.theme')).toBeNull()
  })

  it('ignores a stored value that is not a theme', () => {
    localStorage.setItem('sirdar.theme', 'sepia')
    resetTheme()
    expect(theme()).toBe('system')
  })

  it('tells a subscriber once per change and not for a no-op', () => {
    let calls = 0
    const off = subscribeTheme(() => {
      calls += 1
    })
    setTheme('dark')
    setTheme('dark')
    expect(calls).toBe(1)
    off()
    setTheme('light')
    expect(calls).toBe(1)
  })
})
