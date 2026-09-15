import { act, render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { stubMatchMedia } from './mediaStub'
import { BELOW_COMPACT, BELOW_STANDARD, useMediaQuery } from './useMediaQuery'

function Probe({ query }: { query: string }): JSX.Element {
  return <output>{useMediaQuery(query) ? 'yes' : 'no'}</output>
}

describe('useMediaQuery', () => {
  it('answers false where the window has no matchMedia, which is the wide layout', () => {
    const media = stubMatchMedia()
    media.restore()
    const before = globalThis.matchMedia
    Object.defineProperty(globalThis, 'matchMedia', {
      configurable: true,
      writable: true,
      value: undefined,
    })
    try {
      render(<Probe query={BELOW_STANDARD} />)
      expect(screen.getByRole('status')).toHaveTextContent('no')
    } finally {
      Object.defineProperty(globalThis, 'matchMedia', {
        configurable: true,
        writable: true,
        value: before,
      })
    }
  })

  it('reads the query on mount and follows it as the window changes', () => {
    const media = stubMatchMedia([BELOW_COMPACT])
    try {
      render(<Probe query={BELOW_COMPACT} />)
      expect(screen.getByRole('status')).toHaveTextContent('yes')
      act(() => media.set(BELOW_COMPACT, false))
      expect(screen.getByRole('status')).toHaveTextContent('no')
      act(() => media.set(BELOW_COMPACT, true))
      expect(screen.getByRole('status')).toHaveTextContent('yes')
    } finally {
      media.restore()
    }
  })

  it('names the bands the tokens file draws', () => {
    expect(BELOW_STANDARD).toBe('(max-width: 1199px)')
    expect(BELOW_COMPACT).toBe('(max-width: 1023px)')
  })
})
