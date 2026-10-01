import { fireEvent, render, screen, within } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import type { DecisionAsk } from '../../api/types'
import DecisionBar from './DecisionBar'

const ASKS: Record<DecisionAsk['kind'], DecisionAsk> = {
  bash: { kind: 'bash', tool: 'Bash', summary: 'rg -n refund src', patterns: ['rg *'], verdict: 'deny', reason: 'Sirdar policy: not permitted by permissions.bash' },
  mcp: { kind: 'mcp', tool: 'mcp__grafana__create_incident', summary: '{"title":"x"}', patterns: ['mcp__grafana__create_incident'], verdict: 'deny', reason: 'Sirdar policy: MCP tool looks like a write' },
  fetch: { kind: 'fetch', tool: 'WebFetch', summary: 'https://docs.example.com/a', patterns: ['docs.example.com'], verdict: 'deny', reason: 'Sirdar policy: host not in permissions.fetch' },
  tool: { kind: 'tool', tool: 'Read', summary: '/opt/runbooks/refunds.md', patterns: ['/opt/runbooks'], verdict: 'deny', reason: 'Sirdar policy: read outside the workspace' },
}

function bar(ask: DecisionAsk, busy = false) {
  const onDecide = vi.fn()
  const utils = render(
    <>
      <DecisionBar ask={ask} busy={busy} onDecide={onDecide} />
      <textarea aria-label="Answer" />
    </>,
  )
  return { onDecide, ...utils }
}

describe('DecisionBar', () => {
  it.each([
    ['bash', 'Bash', 'rg -n refund src', 'Not on permissions.bash'],
    ['mcp', 'grafana · create_incident', '', 'Not allowed by permissions.mcp'],
    ['fetch', 'WebFetch', 'https://docs.example.com/a', 'Host not on permissions.fetch'],
    ['tool', 'Read', '/opt/runbooks/refunds.md', 'Outside what this run may use'],
  ] as const)('draws a %s question as the tool, the call and why, in one line', (kind, tool, call, why) => {
    bar(ASKS[kind])
    const group = screen.getByRole('group', { name: 'The agent asks to run' })
    expect(group).toHaveAttribute('data-kind', kind)
    const line = group.querySelector('.sn-decide__q') as HTMLElement
    expect(within(line).getByText(tool)).toBeInTheDocument()
    if (call) expect(within(line).getByText(call).tagName).toBe('CODE')
    expect(line).toHaveTextContent(why)
    // The whole call and the policy's own reason are on hover.
    expect(line.title).toContain(ASKS[kind].summary)
    expect(line.title).toContain(ASKS[kind].reason)
  })

  it('answers with the three verdicts, Allow once filled', () => {
    const { onDecide } = bar(ASKS.bash)
    const once = screen.getByRole('button', { name: /Allow once/ })
    expect(once).toHaveAttribute('data-variant', 'primary')
    fireEvent.click(once)
    expect(onDecide).toHaveBeenLastCalledWith('allow')
    const run = screen.getByRole('button', { name: 'Allow for this run' })
    expect(run.title).toContain('rg *')
    fireEvent.click(run)
    expect(onDecide).toHaveBeenLastCalledWith('allow_run')
  })

  it('Deny opens a reason field; the reason goes with the verdict, and Cancel goes back', () => {
    const { onDecide } = bar(ASKS.bash)
    fireEvent.click(screen.getByRole('button', { name: /^Deny/ }))
    const field = screen.getByRole('textbox', { name: 'Why not' })
    expect(field).toHaveFocus()
    expect(screen.queryByRole('button', { name: /Allow once/ })).toBeNull()
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }))
    expect(screen.getByRole('button', { name: /Allow once/ })).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: /^Deny/ }))
    fireEvent.change(screen.getByRole('textbox', { name: 'Why not' }), { target: { value: 'use the index' } })
    fireEvent.submit(screen.getByRole('textbox', { name: 'Why not' }).closest('form') as HTMLFormElement)
    expect(onDecide).toHaveBeenLastCalledWith('deny', 'use the index')
  })

  it('a reason is optional', () => {
    const { onDecide } = bar(ASKS.bash)
    fireEvent.click(screen.getByRole('button', { name: /^Deny/ }))
    fireEvent.click(screen.getByRole('button', { name: /^Deny/ }))
    expect(onDecide).toHaveBeenLastCalledWith('deny', undefined)
  })

  it('⌘⏎ and Ctrl+Enter allow once, ahead of the composer’s own Enter', () => {
    const { onDecide } = bar(ASKS.bash)
    const box = screen.getByRole('textbox', { name: 'Answer' })
    const onBoxKey = vi.fn()
    box.addEventListener('keydown', onBoxKey)
    fireEvent.keyDown(box, { key: 'Enter', metaKey: true })
    expect(onDecide).toHaveBeenLastCalledWith('allow')
    expect(onBoxKey).not.toHaveBeenCalled()
    fireEvent.keyDown(window, { key: 'Enter', ctrlKey: true })
    expect(onDecide).toHaveBeenCalledTimes(2)
    // Enter alone is the composer's.
    fireEvent.keyDown(box, { key: 'Enter' })
    expect(onDecide).toHaveBeenCalledTimes(2)
  })

  it('⌘⌫ opens Deny and a second ⌘⌫ sends it; in a field with text it stays a delete', () => {
    const { onDecide } = bar(ASKS.bash)
    const box = screen.getByRole('textbox', { name: 'Answer' }) as HTMLTextAreaElement
    fireEvent.change(box, { target: { value: 'half a sentence' } })
    fireEvent.keyDown(box, { key: 'Backspace', metaKey: true })
    expect(screen.queryByRole('textbox', { name: 'Why not' })).toBeNull()

    fireEvent.keyDown(window, { key: 'Backspace', metaKey: true })
    expect(screen.getByRole('textbox', { name: 'Why not' })).toBeInTheDocument()
    expect(onDecide).not.toHaveBeenCalled()
    fireEvent.keyDown(screen.getByRole('textbox', { name: 'Why not' }), { key: 'Backspace', ctrlKey: true })
    expect(onDecide).toHaveBeenLastCalledWith('deny', undefined)
  })

  it('does nothing on a shortcut while a resume is in flight', () => {
    const { onDecide } = bar(ASKS.bash, true)
    fireEvent.keyDown(window, { key: 'Enter', metaKey: true })
    fireEvent.keyDown(window, { key: 'Backspace', metaKey: true })
    expect(onDecide).not.toHaveBeenCalled()
  })

  it('lays an Arabic reason out from its own first letter', () => {
    bar(ASKS.bash)
    fireEvent.click(screen.getByRole('button', { name: /^Deny/ }))
    expect(screen.getByRole('textbox', { name: 'Why not' })).toHaveAttribute('dir', 'auto')
  })
})
