import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import SearchBar from './index'

describe('SearchBar', () => {
  it('is a labelled search field that reports every keystroke', () => {
    const onChange = vi.fn()
    render(<SearchBar label="Ticket key or URL" value="" onChange={onChange} placeholder="SBX-1" />)
    const input = screen.getByRole('searchbox', { name: 'Ticket key or URL' })
    expect(input).toHaveAttribute('placeholder', 'SBX-1')
    fireEvent.change(input, { target: { value: 'SBX-4' } })
    expect(onChange).toHaveBeenCalledWith('SBX-4')
  })

  it('submits on Enter with the current value, and never reloads the page', () => {
    const onSubmit = vi.fn()
    render(<SearchBar label="Find" value="SBX-4" onChange={() => {}} onSubmit={onSubmit} />)
    fireEvent.submit(screen.getByRole('search'))
    expect(onSubmit).toHaveBeenCalledWith('SBX-4')
  })

  it('is the bar by default and the well on request', () => {
    const { rerender } = render(<SearchBar label="Find" value="" onChange={() => {}} />)
    expect(screen.getByRole('search')).toHaveAttribute('data-variant', 'bar')
    rerender(<SearchBar label="Find" value="" onChange={() => {}} variant="well" />)
    expect(screen.getByRole('search')).toHaveAttribute('data-variant', 'well')
  })

  it('shows a hint at the end', () => {
    render(<SearchBar label="Find" value="" onChange={() => {}} aside="or paste a URL" />)
    expect(screen.getByText('or paste a URL')).toBeInTheDocument()
  })

  it('takes an Arabic label and placeholder', () => {
    render(<SearchBar label="مفتاح التذكرة" value="" onChange={() => {}} placeholder="ابحث" />)
    expect(screen.getByRole('searchbox', { name: 'مفتاح التذكرة' })).toHaveAttribute(
      'placeholder',
      'ابحث',
    )
  })
})
