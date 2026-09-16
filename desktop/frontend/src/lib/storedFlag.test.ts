import { afterEach, describe, expect, it, vi } from 'vitest'
import { readStoredFlag, writeStoredFlag } from './storedFlag'

afterEach(() => {
  localStorage.clear()
  vi.restoreAllMocks()
})

describe('a stored flag', () => {
  it('is false until written, then reads back either way round', () => {
    expect(readStoredFlag('sirdar.test.flag')).toBe(false)
    writeStoredFlag('sirdar.test.flag', true)
    expect(localStorage.getItem('sirdar.test.flag')).toBe('1')
    expect(readStoredFlag('sirdar.test.flag')).toBe(true)
    writeStoredFlag('sirdar.test.flag', false)
    expect(localStorage.getItem('sirdar.test.flag')).toBe('0')
    expect(readStoredFlag('sirdar.test.flag')).toBe(false)
  })

  it('is false, and does not throw, when storage refuses', () => {
    vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
      throw new Error('locked')
    })
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new Error('locked')
    })
    expect(readStoredFlag('sirdar.test.flag')).toBe(false)
    expect(() => writeStoredFlag('sirdar.test.flag', true)).not.toThrow()
  })
})
