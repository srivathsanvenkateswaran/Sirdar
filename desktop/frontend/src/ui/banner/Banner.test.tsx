import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import Banner from './index'

describe('Banner', () => {
  it('is a status line with a bold lead and the rest after it', () => {
    render(<Banner title="Tests passed">go test ./... in 41s</Banner>)
    const banner = screen.getByRole('status')
    expect(banner).toHaveAttribute('data-tone', 'ok')
    expect(banner.querySelector('b')).toHaveTextContent('Tests passed')
    expect(banner).toHaveTextContent('go test ./... in 41s')
  })

  it.each(['ok', 'done', 'blocked', 'failed', 'live'] as const)('wears the %s tone', (tone) => {
    render(<Banner tone={tone} title="Step" />)
    expect(screen.getByRole('status')).toHaveAttribute('data-tone', tone)
  })

  it('draws no separator when there is only a lead', () => {
    const { container } = render(<Banner title="Note filed" />)
    expect(container.querySelector('.sd-banner__sep')).toBeNull()
  })

  it('takes one action at the end and an Arabic direction', () => {
    render(
      <Banner tone="blocked" title="طلب الوكيل" dir="auto" action={<button type="button">Answer</button>}>
        أي رقم عميل تقصد؟
      </Banner>,
    )
    expect(screen.getByRole('button', { name: 'Answer' })).toBeInTheDocument()
    expect(screen.getByRole('status')).toHaveAttribute('dir', 'auto')
  })
})
