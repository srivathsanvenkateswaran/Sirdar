// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import {
  noteDir,
  prefersRTL,
  resetPreferRTL,
  setPreferRTL,
  stripRTLBlocks,
  subscribePreferRTL,
} from './rtl'

beforeEach(() => {
  localStorage.clear()
  resetPreferRTL()
})

afterEach(() => {
  localStorage.clear()
  resetPreferRTL()
})

describe('the reading-direction preference', () => {
  it('is off until it is set', () => {
    expect(prefersRTL()).toBe(false)
    expect(noteDir()).toBe('auto')
  })

  it('survives a reload of the page', () => {
    setPreferRTL(true)
    resetPreferRTL() // what a fresh page load does
    expect(prefersRTL()).toBe(true)
    expect(noteDir()).toBe('rtl')
  })

  it('clears the stored value when it goes back off', () => {
    setPreferRTL(true)
    setPreferRTL(false)
    resetPreferRTL()
    expect(prefersRTL()).toBe(false)
  })

  it('notifies subscribers on a change and not on a no-op', () => {
    const listener = vi.fn()
    const unsubscribe = subscribePreferRTL(listener)

    setPreferRTL(true)
    expect(listener).toHaveBeenCalledTimes(1)

    setPreferRTL(true)
    expect(listener).toHaveBeenCalledTimes(1)

    unsubscribe()
    setPreferRTL(false)
    expect(listener).toHaveBeenCalledTimes(1)
  })
})

describe('stripRTLBlocks', () => {
  it('drops the wrapper the note templates emit and keeps the text', () => {
    const md = ['## Complaint', '', '<div dir="rtl">', '', 'التصدير لا يعمل', '', '</div>', ''].join(
      '\n',
    )
    const got = stripRTLBlocks(md)
    expect(got).toContain('التصدير لا يعمل')
    expect(got).not.toContain('<div')
    expect(got).not.toContain('</div>')
  })

  it('leaves a </div> that closes something else alone', () => {
    const md = ['<div class="callout">', '', 'text', '', '</div>'].join('\n')
    expect(stripRTLBlocks(md)).toBe(md)
  })

  it('leaves a note with no wrappers byte-identical', () => {
    const md = '# Title\n\nA paragraph.\n\n- a list item\n'
    expect(stripRTLBlocks(md)).toBe(md)
  })
})
