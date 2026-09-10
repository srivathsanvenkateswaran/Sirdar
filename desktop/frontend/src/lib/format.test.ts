import { describe, expect, it } from 'vitest'
import { duration, elapsedSince, parseKeys, percent, relativeTime, tokens, usd } from './format'

const NOW = Date.parse('2026-09-10T12:00:00Z')

describe('relativeTime', () => {
  it('reads in the units a support engineer thinks in', () => {
    expect(relativeTime('2026-09-10T11:59:40Z', NOW)).toBe('just now')
    expect(relativeTime('2026-09-10T11:56:00Z', NOW)).toBe('4m ago')
    expect(relativeTime('2026-09-10T09:00:00Z', NOW)).toBe('3h ago')
    expect(relativeTime('2026-09-08T12:00:00Z', NOW)).toBe('2d ago')
    expect(relativeTime('2026-09-10T12:04:00Z', NOW)).toBe('in 4m')
  })

  it('says nothing when there is no timestamp', () => {
    expect(relativeTime('', NOW)).toBe('')
    expect(relativeTime(undefined, NOW)).toBe('')
    expect(relativeTime('not a date', NOW)).toBe('')
  })
})

describe('duration', () => {
  it('pads so a lane of clocks stays aligned', () => {
    expect(duration(42_000)).toBe('0:42')
    expect(duration(439_000)).toBe('7:19')
    expect(duration(3_870_000)).toBe('1:04:30')
    expect(duration(-5)).toBe('0:00')
  })
})

describe('elapsedSince', () => {
  it('counts from the run start', () => {
    expect(elapsedSince('2026-09-10T11:57:30Z', NOW)).toBe('2:30')
    expect(elapsedSince(undefined, NOW)).toBe('')
  })
})

describe('usd', () => {
  it('keeps sub-cent runs legible and everything else to cents', () => {
    expect(usd(0)).toBe('$0.00')
    expect(usd(0.004)).toBe('$0.004')
    expect(usd(0.12)).toBe('$0.12')
    expect(usd(13.5)).toBe('$13.50')
    expect(usd(undefined)).toBe('$0.00')
  })
})

describe('tokens', () => {
  it('shortens once the count stops being readable', () => {
    expect(tokens(840)).toBe('840')
    expect(tokens(12_400)).toBe('12k')
    expect(tokens(1240)).toBe('1.2k')
    expect(tokens(2_400_000)).toBe('2.4M')
    expect(tokens(undefined)).toBe('0')
  })
})

describe('percent', () => {
  it('accepts both the 0..1 and the 0..100 shapes the providers report', () => {
    expect(percent(0.42)).toBe('42%')
    expect(percent(42)).toBe('42%')
    expect(percent(undefined)).toBe('')
  })
})

describe('parseKeys', () => {
  it('splits on commas, spaces and newlines and drops repeats', () => {
    expect(parseKeys('OMNI-1, OMNI-2 OMNI-3\nOMNI-1')).toEqual(['OMNI-1', 'OMNI-2', 'OMNI-3'])
    expect(parseKeys('   ')).toEqual([])
  })

  it('leaves the key text exactly as typed', () => {
    expect(parseKeys('omni-1')).toEqual(['omni-1'])
  })
})
