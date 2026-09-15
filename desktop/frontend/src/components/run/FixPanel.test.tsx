import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import type { FixInfo } from '../../api/types'
import FixPanel from './FixPanel'

const COMMITTED: FixInfo = {
  branch: 'sirdar/OMNI-2510',
  base: 'main',
  commit: '9f2c1ab77e4d5c6b',
  deviation:
    'The note proposed widening the column. The column is generated, so the generator template was changed instead.',
}

describe('FixPanel', () => {
  it('names the branch, the base it was cut from and a short commit', () => {
    render(<FixPanel fix={COMMITTED} pending={false} error="" onAccept={() => {}} />)
    expect(screen.getByText('sirdar/OMNI-2510 (from origin/main)')).toBeInTheDocument()
    expect(screen.getByText('9f2c1ab77e')).toBeInTheDocument()
  })

  /*
   * The gate. A deviation means the commit exists and has not been pushed,
   * and the panel has to show what the agent said it did instead before it
   * offers the button that publishes it.
   */
  it('shows the deviation and offers to publish the commit that was reviewed', () => {
    const onAccept = vi.fn()
    render(<FixPanel fix={COMMITTED} pending={false} error="" onAccept={onAccept} />)

    expect(screen.getByText(/did not implement the note's proposed fix/)).toBeInTheDocument()
    expect(screen.getByText(/the generator template was changed instead/)).toBeInTheDocument()
    expect(screen.getByText(/has not been pushed/)).toBeInTheDocument()
    expect(screen.getByText(/no second agent session is started/)).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: 'Accept and publish' }))
    expect(onAccept).toHaveBeenCalledTimes(1)
  })

  it('a pushed fix links the pull request and asks for nothing more', () => {
    render(
      <FixPanel
        fix={{ ...COMMITTED, pushed: true, prUrl: 'https://github.com/acme/api/pull/42' }}
        pending={false}
        error=""
        onAccept={() => {}}
      />,
    )

    const link = screen.getByRole('link', { name: 'https://github.com/acme/api/pull/42' })
    expect(link).toHaveAttribute('href', 'https://github.com/acme/api/pull/42')
    expect(screen.queryByRole('button', { name: 'Accept and publish' })).toBeNull()
  })

  /*
   * The case the panel used to get wrong. `--no-pr`, and a `gh` call that
   * failed after the push went through, both leave a deviation on the record
   * and no pull request URL. Reading the URL alone asked the reader to accept
   * a commit that was already on the remote — and accepting it would then be
   * refused, since the branch is no longer where the review left it.
   */
  it('a pushed fix with no pull request asks for no review', () => {
    render(
      <FixPanel fix={{ ...COMMITTED, pushed: true }} pending={false} error="" onAccept={() => {}} />,
    )

    expect(screen.queryByRole('button', { name: 'Accept and publish' })).toBeNull()
    expect(screen.getByText(/is on the remote, with no pull request/)).toBeInTheDocument()
    // The deviation is still on the record, said in the past tense.
    expect(screen.getByText(/the generator template was changed instead/)).toBeInTheDocument()
  })

  /*
   * A run recorded before the push was written into state.json has a URL and
   * no `pushed`. It is plainly published, and must not read as waiting.
   */
  it('a pull request URL alone still counts as published', () => {
    render(
      <FixPanel
        fix={{ ...COMMITTED, prUrl: 'https://github.com/acme/api/pull/42' }}
        pending={false}
        error=""
        onAccept={() => {}}
      />,
    )
    expect(screen.queryByRole('button', { name: 'Accept and publish' })).toBeNull()
  })

  it('a fix that raised no deviation shows its facts and no review', () => {
    render(
      <FixPanel
        fix={{ branch: 'sirdar/OMNI-1', commit: 'abc1234567', prUrl: 'https://x/pull/1' }}
        pending={false}
        error=""
        onAccept={() => {}}
      />,
    )
    expect(screen.queryByText(/proposed fix/)).toBeNull()
    expect(screen.queryByRole('button')).toBeNull()
  })

  it('the button says what it is doing while the rerun is in flight, and shows a failure', () => {
    const { rerender } = render(
      <FixPanel fix={COMMITTED} pending error="" onAccept={() => {}} />,
    )
    expect(screen.getByRole('button', { name: 'Publishing…' })).toBeDisabled()

    rerender(<FixPanel fix={COMMITTED} pending={false} error="push rejected" onAccept={() => {}} />)
    expect(screen.getByText('push rejected')).toBeInTheDocument()
  })
})
