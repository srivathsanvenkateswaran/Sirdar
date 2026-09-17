import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import RunCard from './index'

const BASE = {
  runKey: 'OMNI-2510',
  kind: 'triage',
  provider: 'claude',
  onOpen: () => {},
} as const

describe('RunCard', () => {
  it('names the run, its ticket and its state in one accessible label', () => {
    render(<RunCard {...BASE} status="completed" title="Statement export times out" />)
    expect(
      screen.getByRole('button', { name: 'OMNI-2510: Statement export times out, completed' }),
    ).toBeInTheDocument()
  })

  it('says the state in words in the label for every state', () => {
    const { rerender } = render(<RunCard {...BASE} status="blocked" title="Login loop" />)
    expect(screen.getByRole('button', { name: 'OMNI-2510: Login loop, blocked' })).toBeInTheDocument()
    rerender(<RunCard {...BASE} status="over_budget" title="Login loop" />)
    expect(screen.getByRole('button', { name: 'OMNI-2510: Login loop, over budget' })).toBeInTheDocument()
  })

  it('shows the title first and the key at the foot, with no key in the head', () => {
    const { container } = render(
      <RunCard {...BASE} status="completed" title="Statement export times out" />,
    )
    const card = container.querySelector('.sd-run-card') as HTMLElement
    expect(card.firstElementChild).toHaveClass('sd-run-card__title')
    expect(card.querySelector('.sd-run-card__foot .sd-run-card__key')).toHaveTextContent(
      'OMNI-2510',
    )
  })

  it('carries the other number as the tooltip on the one shown', () => {
    const { container, rerender } = render(
      <RunCard {...BASE} runKey="#25312" keyTitle="Jira OMNI-2510" status="completed" title="Login loop" />,
    )
    expect(container.querySelector('.sd-run-card__key')).toHaveAttribute('title', 'Jira OMNI-2510')
    expect(container.querySelector('.sd-run-card__title')).not.toHaveAttribute('title')
    // With no title the number is the title, and the tooltip moves with it.
    rerender(<RunCard {...BASE} runKey="#25312" keyTitle="Jira OMNI-2510" status="queued" />)
    expect(container.querySelector('.sd-run-card__title')).toHaveAttribute('title', 'Jira OMNI-2510')
    expect(screen.getByRole('button', { name: '#25312, queued' })).toBeInTheDocument()
  })

  it('shows the key once, as the title, when the tracker has no title', () => {
    const { container } = render(<RunCard {...BASE} status="queued" />)
    expect(screen.getByRole('button', { name: 'OMNI-2510, queued' })).toBeInTheDocument()
    expect(container.querySelector('.sd-run-card__title')).toHaveTextContent('OMNI-2510')
    expect(container.querySelector('.sd-run-card__key')).toBeNull()
    expect(screen.getAllByText('OMNI-2510')).toHaveLength(1)
  })

  it('is a link to the tracker when it has somewhere to go but no session to open', () => {
    render(
      <RunCard
        {...BASE}
        onOpen={undefined}
        status="queued"
        title="Login loop"
        href="https://acme.atlassian.net/browse/OMNI-2510"
      />,
    )
    const link = screen.getByRole('link', { name: 'OMNI-2510: Login loop, queued' })
    expect(link).toHaveAttribute('href', 'https://acme.atlassian.net/browse/OMNI-2510')
    expect(link).toHaveAttribute('target', '_blank')
    expect(link).toHaveAttribute('rel', 'noreferrer noopener')
    expect(screen.queryByRole('button')).toBeNull()
  })

  it('is a plain box, not a control, when it neither opens nor goes anywhere', () => {
    const { container } = render(<RunCard {...BASE} onOpen={undefined} status="queued" title="Login loop" />)
    expect(screen.queryByRole('button')).toBeNull()
    expect(screen.queryByRole('link')).toBeNull()
    expect(container.querySelector('div.sd-run-card')).toHaveAttribute('aria-label', 'OMNI-2510: Login loop, queued')
  })

  it('opens the run on click and from the keyboard', () => {
    const onOpen = vi.fn()
    render(<RunCard {...BASE} status="running" onOpen={onOpen} />)
    const card = screen.getByRole('button')
    card.focus()
    expect(card).toHaveFocus()
    fireEvent.click(card)
    expect(onOpen).toHaveBeenCalledOnce()
  })

  it.each(['preparing', 'running'] as const)('marks a %s run as the live one', (status) => {
    const { container } = render(<RunCard {...BASE} status={status} />)
    expect(container.querySelector('.sd-run-card')).toHaveAttribute('data-live', 'true')
  })

  it('leaves a finished run flat', () => {
    const { container } = render(<RunCard {...BASE} status="completed" />)
    expect(container.querySelector('.sd-run-card')).not.toHaveAttribute('data-live')
  })

  it('carries the kind as a chip and the state as a glyph with its word', () => {
    render(<RunCard {...BASE} kind="fix" status="blocked" />)
    expect(screen.getByText('fix')).toHaveClass('sd-kind')
    expect(screen.getByText('blocked')).toBeInTheDocument()
  })

  it('puts a detail after the state word, and nothing at all without one', () => {
    const { rerender } = render(<RunCard {...BASE} status="blocked" detail="model limit" />)
    expect(screen.getByText('blocked · model limit')).toBeInTheDocument()
    rerender(<RunCard {...BASE} status="blocked" />)
    expect(screen.getByText('blocked')).toBeInTheDocument()
  })

  it('shows the clock only while the run is live or waiting', () => {
    const { rerender } = render(<RunCard {...BASE} status="running" clock="1:47" />)
    expect(screen.getByText('1:47')).toBeInTheDocument()
    rerender(<RunCard {...BASE} status="blocked" clock="4:12" />)
    expect(screen.getByText('4:12')).toBeInTheDocument()
    rerender(<RunCard {...BASE} status="completed" clock="9:02" />)
    expect(screen.queryByText('9:02')).toBeNull()
  })

  it('draws the provider mark as the avatar, named by the vendor', () => {
    render(<RunCard {...BASE} status="done" provider="agy" />)
    expect(screen.getByRole('img', { name: 'Antigravity' })).toBeInTheDocument()
  })

  it('carries neither a reason nor a cost', () => {
    const { container } = render(<RunCard {...BASE} status="failed" />)
    expect(container.querySelector('.sd-run-card__reason')).toBeNull()
    expect(container.querySelector('.sd-run-card__cost')).toBeNull()
  })

  it('keeps the key and the clock left to right inside an Arabic card', () => {
    render(
      <div dir="rtl">
        <RunCard
          {...BASE}
          status="blocked"
          title="العميل لا يستطيع تصدير كشف الحساب"
          clock="4:12"
        />
      </div>,
    )
    expect(screen.getByText('OMNI-2510')).toHaveAttribute('dir', 'ltr')
    expect(screen.getByText('4:12')).toHaveAttribute('dir', 'ltr')
    expect(screen.getByText('العميل لا يستطيع تصدير كشف الحساب')).toHaveAttribute('dir', 'auto')
  })
})

describe('the assignee avatar', () => {
  it('draws the initials before the provider mark and names the person in the label', () => {
    const { container } = render(
      <RunCard
        {...BASE}
        status="running"
        title="Statement export times out"
        assignee="Sri Venkateswaran"
      />,
    )
    const avatar = container.querySelector('.sd-avatar') as HTMLElement
    expect(avatar).toHaveTextContent('SV')
    expect(avatar).toHaveAttribute('title', 'Sri Venkateswaran')
    expect(avatar).toHaveAttribute('aria-hidden', 'true')

    // The foot's trailing side reads key, then who it is, then what ran it.
    const who = [...(container.querySelector('.sd-run-card__who')?.children ?? [])]
    expect(who.map((el) => el.className.split(' ')[0])).toEqual([
      'sd-run-card__key',
      'sd-avatar',
      'sd-mark',
    ])

    expect(
      screen.getByRole('button', {
        name: 'OMNI-2510: Statement export times out, running, assigned to Sri Venkateswaran',
      }),
    ).toBeInTheDocument()
  })

  it('reads an address by its local part', () => {
    const { container, rerender } = render(
      <RunCard {...BASE} status="queued" assignee="sri.venkateswaran@acme.com" />,
    )
    expect(container.querySelector('.sd-avatar')).toHaveTextContent('SV')
    rerender(<RunCard {...BASE} status="queued" assignee="sri@acme.com" />)
    expect(container.querySelector('.sd-avatar')).toHaveTextContent('S')
  })

  it('draws nothing when nobody is assigned', () => {
    const { container, rerender } = render(<RunCard {...BASE} status="queued" title="Login loop" />)
    expect(container.querySelector('.sd-avatar')).toBeNull()
    expect(screen.getByRole('button', { name: 'OMNI-2510: Login loop, queued' })).toBeInTheDocument()

    rerender(<RunCard {...BASE} status="queued" title="Login loop" assignee="   " />)
    expect(container.querySelector('.sd-avatar')).toBeNull()
    expect(screen.getByRole('button', { name: 'OMNI-2510: Login loop, queued' })).toBeInTheDocument()
  })

  it('lets an explicit label stand, avatar and all', () => {
    const { container } = render(
      <RunCard
        runKey="OMNI-2510"
        kind="triage"
        provider="claude"
        status="queued"
        title="Login loop"
        assignee="Sri Venkateswaran"
        label="Start triage of OMNI-2510"
      />,
    )
    expect(screen.getByLabelText('Start triage of OMNI-2510')).toBeInTheDocument()
    expect(container.querySelector('.sd-avatar')).toHaveTextContent('SV')
  })
})
