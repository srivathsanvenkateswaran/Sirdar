import { describe, expect, it } from 'vitest'
import type { Screen } from '../store/appStore'
import { parseRoute, routeHash, sameScreen } from './routes'

describe('parseRoute', () => {
  it.each([
    ['', 'board'],
    ['#', 'board'],
    ['#/', 'board'],
    ['#/board', 'board'],
    ['#/register', 'register'],
    ['#/eval', 'eval'],
    ['#/settings', 'settings'],
    ['#/library', 'library'],
  ])('%s opens the %s screen', (hash, name) => {
    expect(parseRoute(hash)).toEqual({ screen: { name } })
  })

  it('reads a run link as its workspace and its run', () => {
    expect(parseRoute('#/runs/ws1/20260910-1000-omni-2510')).toEqual({
      screen: { name: 'run', runId: '20260910-1000-omni-2510' },
      workspaceId: 'ws1',
    })
  })

  it('unescapes both halves of a run link', () => {
    expect(parseRoute('#/runs/my%20repo/r%2F1')).toEqual({
      screen: { name: 'run', runId: 'r/1' },
      workspaceId: 'my repo',
    })
  })

  // Anything this window does not have a screen for is left alone, so a hash
  // meant for something else is not silently rewritten to the board.
  it.each([
    '#/nowhere',
    '#/runs',
    '#/runs/ws1',
    '#/runs/ws1/r1/extra',
    '#/settings/danger',
    '#/library/button',
    '#/runs//r1',
  ])('answers null for %s', (hash) => {
    expect(parseRoute(hash)).toBeNull()
  })

  it('survives a mangled escape rather than throwing', () => {
    expect(() => parseRoute('#/runs/ws1/%E0%A4%A')).not.toThrow()
  })
})

describe('routeHash', () => {
  it.each([
    [{ name: 'board' } as Screen, '#/'],
    [{ name: 'register' } as Screen, '#/register'],
    [{ name: 'eval' } as Screen, '#/eval'],
    [{ name: 'settings' } as Screen, '#/settings'],
    [{ name: 'library' } as Screen, '#/library'],
  ])('writes %o as %s', (screen, hash) => {
    expect(routeHash(screen, 'ws1')).toBe(hash)
  })

  it('names the workspace a run belongs to', () => {
    expect(routeHash({ name: 'run', runId: 'r1' }, 'ws1')).toBe('#/runs/ws1/r1')
  })

  it('escapes what would otherwise split the path', () => {
    expect(routeHash({ name: 'run', runId: 'r/1' }, 'my repo')).toBe('#/runs/my%20repo/r%2F1')
  })

  // A run with no workspace behind it cannot be linked to; the board can.
  it('falls back to the board when no workspace is current', () => {
    expect(routeHash({ name: 'run', runId: 'r1' }, '')).toBe('#/')
  })

  it('round-trips every screen it writes', () => {
    const screens: Screen[] = [
      { name: 'board' },
      { name: 'register' },
      { name: 'eval' },
      { name: 'settings' },
      { name: 'run', runId: '20260910-1000-omni-2510' },
    ]
    for (const screen of screens) {
      const route = parseRoute(routeHash(screen, 'ws1'))
      expect(route, routeHash(screen, 'ws1')).not.toBeNull()
      expect(route?.screen).toEqual(screen)
    }
  })
})

describe('sameScreen', () => {
  it('tells two runs apart', () => {
    expect(sameScreen({ name: 'run', runId: 'r1' }, { name: 'run', runId: 'r1' })).toBe(true)
    expect(sameScreen({ name: 'run', runId: 'r1' }, { name: 'run', runId: 'r2' })).toBe(false)
  })

  it('ignores object identity for the screens that carry nothing', () => {
    expect(sameScreen({ name: 'eval' }, { name: 'eval' })).toBe(true)
    expect(sameScreen({ name: 'eval' }, { name: 'settings' })).toBe(false)
  })
})
