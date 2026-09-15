import { afterEach, describe, expect, it } from 'vitest'
import {
  resetShowLibrary,
  setShowLibrary,
  showLibrary,
  subscribeShowLibrary,
} from './library'

afterEach(() => {
  localStorage.removeItem('sirdar.showLibrary')
  resetShowLibrary()
})

describe('the library switch', () => {
  it('is on in a development build, which is what the tests run as', () => {
    expect(showLibrary()).toBe(true)
  })

  it('remembers being turned off, which a default alone cannot express', () => {
    setShowLibrary(false)
    expect(localStorage.getItem('sirdar.showLibrary')).toBe('0')
    resetShowLibrary()
    expect(showLibrary()).toBe(false)
  })

  it('remembers being turned on', () => {
    setShowLibrary(false)
    setShowLibrary(true)
    resetShowLibrary()
    expect(showLibrary()).toBe(true)
  })

  it('tells its watchers, and stops telling them once they leave', () => {
    let calls = 0
    const stop = subscribeShowLibrary(() => {
      calls += 1
    })
    setShowLibrary(false)
    expect(calls).toBe(1)
    stop()
    setShowLibrary(true)
    expect(calls).toBe(1)
  })

  it('says nothing when the value does not change', () => {
    let calls = 0
    const stop = subscribeShowLibrary(() => {
      calls += 1
    })
    setShowLibrary(showLibrary())
    expect(calls).toBe(0)
    stop()
  })
})
