import { render, screen } from '@testing-library/react'
import '@testing-library/jest-dom/vitest'
import { describe, expect, it } from 'vitest'
import AskedFor from './AskedFor'

describe('AskedFor', () => {
  it('says what the run was asked for, with the whole of it as the title', () => {
    render(<AskedFor instruction="  check the tax rounding on invoice lines  " />)
    const asked = screen.getByTitle('check the tax rounding on invoice lines')
    expect(asked).toHaveTextContent('Asked check the tax rounding on invoice lines')
  })

  it('draws nothing for a run started without one', () => {
    const { container } = render(<AskedFor instruction="   " />)
    expect(container).toBeEmptyDOMElement()
    const bare = render(<AskedFor />)
    expect(bare.container).toBeEmptyDOMElement()
  })
})
