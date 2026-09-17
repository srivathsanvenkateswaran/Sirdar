import { afterEach, describe, expect, it } from 'vitest'
import {
  composerPrefs,
  resetComposerPrefs,
  setFixThen,
  setIntentAssist,
  subscribeComposerPrefs,
} from './composerPrefs'

afterEach(() => {
  globalThis.localStorage?.clear()
  resetComposerPrefs()
})

describe('composerPrefs', () => {
  it('reads assist on and a pull request by default', () => {
    expect(composerPrefs('ws1')).toEqual({ intentAssist: true, fixThen: 'pr' })
  })

  it('remembers each workspace on its own', () => {
    setFixThen('ws1', 'local')
    setIntentAssist('ws2', false)
    expect(composerPrefs('ws1')).toEqual({ intentAssist: true, fixThen: 'local' })
    expect(composerPrefs('ws2')).toEqual({ intentAssist: false, fixThen: 'pr' })
  })

  it('survives a reload, and a stored record of the wrong shape falls back', () => {
    setFixThen('ws1', 'noPr')
    resetComposerPrefs()
    expect(composerPrefs('ws1').fixThen).toBe('noPr')

    globalThis.localStorage?.setItem('sirdar.composer.ws3', '{"fixThen":"nonsense"}')
    resetComposerPrefs()
    expect(composerPrefs('ws3')).toEqual({ intentAssist: true, fixThen: 'pr' })
  })

  it('notifies watchers, and only when something changed', () => {
    let seen = 0
    const stop = subscribeComposerPrefs(() => {
      seen += 1
    })
    setFixThen('ws1', 'local')
    setFixThen('ws1', 'local')
    expect(seen).toBe(1)
    stop()
  })
})
