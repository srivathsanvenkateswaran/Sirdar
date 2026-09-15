import { fireEvent, render, screen } from '@testing-library/react'
import { useState } from 'react'
import { describe, expect, it, vi } from 'vitest'
import SegmentedControl from './index'

const OPTIONS = [
  { id: 'all', label: 'All' },
  { id: 'mine', label: 'Mine' },
  { id: 'blocked', label: 'Blocked' },
]

/** The control is controlled, so the cases that move it hold its value. */
function Harness({ dir = 'ltr' as 'ltr' | 'rtl', start = 'all' }) {
  const [value, setValue] = useState(start)
  return (
    <div dir={dir}>
      <SegmentedControl options={OPTIONS} value={value} onChange={setValue} label="Filter" />
      <p data-testid="value">{value}</p>
    </div>
  )
}

describe('SegmentedControl', () => {
  it('is a named radio group with one checked option', () => {
    render(<SegmentedControl options={OPTIONS} value="mine" onChange={() => {}} label="Filter" />)
    expect(screen.getByRole('radiogroup', { name: 'Filter' })).toBeInTheDocument()
    expect(screen.getByRole('radio', { checked: true })).toHaveTextContent('Mine')
  })

  it('reports the option that was clicked', () => {
    const onChange = vi.fn()
    render(<SegmentedControl options={OPTIONS} value="all" onChange={onChange} label="Filter" />)
    fireEvent.click(screen.getByRole('radio', { name: 'Blocked' }))
    expect(onChange).toHaveBeenCalledWith('blocked')
  })

  it('keeps one tab stop and moves the rest with the arrows', () => {
    render(<Harness />)
    const radios = screen.getAllByRole('radio')
    expect(radios.map((r) => r.getAttribute('tabindex'))).toEqual(['0', '-1', '-1'])
    fireEvent.keyDown(screen.getByRole('radiogroup'), { key: 'ArrowRight' })
    expect(screen.getByTestId('value')).toHaveTextContent('mine')
    expect(screen.getByRole('radio', { name: 'Mine' })).toHaveFocus()
  })

  it('wraps at both ends and jumps with Home and End', () => {
    render(<Harness />)
    const group = screen.getByRole('radiogroup')
    fireEvent.keyDown(group, { key: 'ArrowLeft' })
    expect(screen.getByTestId('value')).toHaveTextContent('blocked')
    fireEvent.keyDown(group, { key: 'Home' })
    expect(screen.getByTestId('value')).toHaveTextContent('all')
    fireEvent.keyDown(group, { key: 'End' })
    expect(screen.getByTestId('value')).toHaveTextContent('blocked')
  })

  it('reads the arrows in the reader own direction under RTL', () => {
    render(<Harness dir="rtl" />)
    const group = screen.getByRole('radiogroup')
    // In an Arabic pane the next option is to the left of the current one, so
    // ArrowLeft advances and ArrowRight goes back.
    fireEvent.keyDown(group, { key: 'ArrowLeft' })
    expect(screen.getByTestId('value')).toHaveTextContent('mine')
    fireEvent.keyDown(group, { key: 'ArrowRight' })
    expect(screen.getByTestId('value')).toHaveTextContent('all')
  })

  it('leaves the vertical arrows direction-blind', () => {
    render(<Harness dir="rtl" />)
    fireEvent.keyDown(screen.getByRole('radiogroup'), { key: 'ArrowDown' })
    expect(screen.getByTestId('value')).toHaveTextContent('mine')
  })

  it('refuses every move when disabled', () => {
    const onChange = vi.fn()
    render(
      <SegmentedControl
        options={OPTIONS}
        value="all"
        onChange={onChange}
        label="Filter"
        disabled
      />,
    )
    fireEvent.keyDown(screen.getByRole('radiogroup'), { key: 'ArrowRight' })
    fireEvent.click(screen.getByRole('radio', { name: 'Mine' }))
    expect(onChange).not.toHaveBeenCalled()
    expect(screen.getByRole('radiogroup')).toHaveAttribute('aria-disabled', 'true')
  })

  it('turns one option off with its reason, and the arrows step over it', () => {
    const onChange = vi.fn()
    function Off(): JSX.Element {
      const [value, setValue] = useState('all')
      return (
        <SegmentedControl
          options={OPTIONS}
          value={value}
          onChange={(id) => {
            onChange(id)
            setValue(id)
          }}
          label="Mode"
          disabledOptions={{ mine: 'Needs a triage note first' }}
        />
      )
    }
    render(<Off />)
    const mine = screen.getByRole('radio', { name: 'Mine' })
    expect(mine).toBeDisabled()
    expect(mine).toHaveAttribute('title', 'Needs a triage note first')
    expect(screen.getByRole('radiogroup')).not.toHaveAttribute('aria-disabled')

    fireEvent.click(mine)
    expect(onChange).not.toHaveBeenCalled()

    // The arrow walks past the option that is off to the next one that is on.
    fireEvent.keyDown(screen.getByRole('radiogroup'), { key: 'ArrowRight' })
    expect(onChange).toHaveBeenLastCalledWith('blocked')
    fireEvent.keyDown(screen.getByRole('radiogroup'), { key: 'ArrowLeft' })
    expect(onChange).toHaveBeenLastCalledWith('all')
  })

  it('carries Arabic labels', () => {
    render(
      <div dir="rtl">
        <SegmentedControl
          options={[
            { id: 'all', label: 'الكل' },
            { id: 'mine', label: 'المسندة إليّ' },
          ]}
          value="mine"
          onChange={() => {}}
          label="التصفية"
        />
      </div>,
    )
    expect(screen.getByRole('radio', { checked: true })).toHaveTextContent('المسندة إليّ')
  })
})
