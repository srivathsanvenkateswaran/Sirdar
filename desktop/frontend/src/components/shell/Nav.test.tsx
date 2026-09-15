import { act, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { resetShowLibrary, setShowLibrary } from '../../lib/library'
import Nav from './Nav'

afterEach(() => {
  localStorage.removeItem('sirdar.showLibrary')
  resetShowLibrary()
})

describe('Nav', () => {
  it('lists the four screens an engineer works in', () => {
    setShowLibrary(false)
    render(<Nav screen={{ name: 'board' }} onNavigate={() => {}} />)
    expect(screen.getAllByRole('button').map((b) => b.textContent)).toEqual([
      'Board',
      'Register',
      'Eval',
      'Settings',
    ])
  })

  it('adds the Library tab while the switch is on', () => {
    setShowLibrary(true)
    render(<Nav screen={{ name: 'library' }} onNavigate={() => {}} />)
    expect(screen.getByRole('button', { name: 'Library' })).toHaveAttribute('aria-current', 'page')
  })

  it('drops the tab the moment the switch is turned off', () => {
    setShowLibrary(true)
    render(<Nav screen={{ name: 'board' }} onNavigate={() => {}} />)
    expect(screen.getByRole('button', { name: 'Library' })).toBeInTheDocument()
    act(() => setShowLibrary(false))
    expect(screen.queryByRole('button', { name: 'Library' })).toBeNull()
  })

  it('marks the Board tab while a run is open, since a run is reached from it', () => {
    setShowLibrary(false)
    render(<Nav screen={{ name: 'run', runId: 'r1' }} onNavigate={() => {}} />)
    expect(screen.getByRole('button', { name: 'Board' })).toHaveAttribute('aria-current', 'page')
  })

  it('reports the screen that was asked for', () => {
    const onNavigate = vi.fn()
    setShowLibrary(true)
    render(<Nav screen={{ name: 'board' }} onNavigate={onNavigate} />)
    screen.getByRole('button', { name: 'Library' }).click()
    expect(onNavigate).toHaveBeenCalledWith({ name: 'library' })
  })
})
