import { describe, expect, it } from 'vitest'
import type { RunEvent } from '../api/types'
import {
  askedQuestion,
  filterTurns,
  groupTurns,
  inputSummary,
  offsetLabel,
  promptAttachments,
  splitFrontmatter,
  toolLabel,
} from './events'

function ev(kind: string, payload: RunEvent['payload'] = {}, t = ''): RunEvent {
  return { t, kind, payload }
}

function indexed(events: RunEvent[]) {
  return events.map((event, index) => ({ index, event }))
}

/** A Claude `assistant` line carrying one tool_use block. */
function claudeTool(name: string, input: unknown): RunEvent {
  return ev('tool_started', {
    tool: name,
    raw: { type: 'assistant', message: { content: [{ type: 'tool_use', name, input }] } },
  })
}

describe('groupTurns', () => {
  it('closes a turn on each usage event', () => {
    const turns = groupTurns(
      indexed([
        ev('system', { text: 'init' }),
        claudeTool('Bash', { command: 'git log -1' }),
        ev('usage', { turns: 1, costUsd: 0.01 }),
        ev('assistant_text', { text: 'looking' }),
        ev('usage', { turns: 2, costUsd: 0.03 }),
      ]),
    )

    expect(turns.map((t) => t.n)).toEqual([1, 2])
    expect(turns[0].events).toHaveLength(3)
    expect(turns[0].costUsd).toBe(0.01)
    expect(turns[1].turns).toBe(2)
  })

  it('starts a turn at assistant text that follows a tool result', () => {
    const turns = groupTurns(
      indexed([
        claudeTool('Read', { file_path: '/a.go' }),
        ev('tool_finished', { tool: 'Read', text: 'contents' }),
        ev('assistant_text', { text: 'the handler is missing a nil check' }),
        ev('final', {}),
      ]),
    )

    expect(turns).toHaveLength(2)
    expect(turns[0].events.map((e) => e.event.kind)).toEqual(['tool_started', 'tool_finished'])
    expect(turns[1].events.map((e) => e.event.kind)).toEqual(['assistant_text', 'final'])
  })

  it('leaves assistant text mid-turn alone when no tool ran before it', () => {
    const turns = groupTurns(
      indexed([ev('assistant_text', { text: 'a' }), ev('assistant_text', { text: 'b' })]),
    )
    expect(turns).toHaveLength(1)
    expect(turns[0].events).toHaveLength(2)
  })

  it('returns nothing for an empty log', () => {
    expect(groupTurns([])).toEqual([])
  })
})

describe('filterTurns', () => {
  const turns = groupTurns(
    indexed([
      claudeTool('Bash', { command: 'ls' }),
      ev('permission', { tool: 'Write', decision: 'deny', text: 'Sirdar policy: read-only' }),
      ev('usage', { turns: 1 }),
      ev('assistant_text', { text: 'done' }),
    ]),
  )

  it('keeps only tool and permission rows for Tools', () => {
    const kinds = filterTurns(turns, 'tools').flatMap((t) => t.events.map((e) => e.event.kind))
    expect(kinds).toEqual(['tool_started', 'permission'])
  })

  it('keeps only denials for Denials', () => {
    const shown = filterTurns(turns, 'denials')
    expect(shown).toHaveLength(1)
    expect(shown[0].events[0].event.payload?.decision).toBe('deny')
  })

  it('keeps text for Text', () => {
    const kinds = filterTurns(turns, 'text').flatMap((t) => t.events.map((e) => e.event.kind))
    expect(kinds).toEqual(['assistant_text'])
  })

  it('returns the turns untouched for All', () => {
    expect(filterTurns(turns, 'all')).toBe(turns)
  })
})

describe('inputSummary', () => {
  it('shows the command for Bash', () => {
    expect(inputSummary(claudeTool('Bash', { command: 'git log -1 --stat' }))).toBe(
      'git log -1 --stat',
    )
  })

  it('shows the path for Read', () => {
    expect(inputSummary(claudeTool('Read', { file_path: '/work/handler.go' }))).toBe(
      '/work/handler.go',
    )
  })

  it('shows the pattern and its path for Grep', () => {
    expect(inputSummary(claudeTool('Grep', { pattern: 'nil deref', path: 'internal/' }))).toBe(
      'nil deref in internal/',
    )
  })

  it('shows server and tool for a Claude MCP tool', () => {
    expect(inputSummary(claudeTool('mcp__janus__get_ticket', { key: 'OMNI-2510' }))).toBe(
      'janus/get_ticket',
    )
  })

  it('shows the command for a Codex commandExecution item', () => {
    const codex = ev('tool_started', {
      tool: 'commandExecution',
      raw: {
        method: 'item/started',
        params: { item: { type: 'commandExecution', command: 'rg -n panic' } },
      },
    })
    expect(inputSummary(codex)).toBe('rg -n panic')
  })

  it('shows server and tool for a Codex MCP call', () => {
    const codex = ev('tool_started', {
      tool: 'codex_apps/search',
      raw: {
        method: 'item/started',
        params: { item: { type: 'mcpToolCall', server: 'codex_apps', tool: 'search' } },
      },
    })
    expect(inputSummary(codex)).toBe('codex_apps/search')
  })

  it('reads the input of a Claude permission request', () => {
    const perm = ev('permission', {
      tool: 'Write',
      decision: 'deny',
      raw: {
        type: 'control_request',
        request: { tool_name: 'Write', input: { file_path: '/work/probe.txt' } },
      },
    })
    expect(inputSummary(perm)).toBe('/work/probe.txt')
  })

  it('says nothing when the raw payload carries no input', () => {
    expect(inputSummary(ev('tool_started', { tool: 'Bash' }))).toBe('')
  })
})

describe('toolLabel', () => {
  it('collapses a Claude MCP name', () => {
    expect(toolLabel('mcp__janus__get_ticket')).toBe('janus/get_ticket')
  })

  it('leaves a plain tool name alone', () => {
    expect(toolLabel('Bash')).toBe('Bash')
  })
})

describe('offsetLabel', () => {
  it('counts seconds from the run start', () => {
    expect(offsetLabel('2026-09-10T10:01:07Z', '2026-09-10T10:00:00Z')).toBe('+1:07')
  })

  it('is empty without both stamps', () => {
    expect(offsetLabel(undefined, '2026-09-10T10:00:00Z')).toBe('')
  })
})

describe('splitFrontmatter', () => {
  it('lifts scalars out of the YAML head', () => {
    const { fields, body } = splitFrontmatter(
      '---\nkey: "OMNI-2510"\nservice: payments-api\n---\n\n## Summary\n\nIt fails.\n',
    )
    expect(fields).toEqual([
      { key: 'key', value: 'OMNI-2510' },
      { key: 'service', value: 'payments-api' },
    ])
    expect(body).toBe('## Summary\n\nIt fails.\n')
  })

  it('leaves a note without frontmatter untouched', () => {
    expect(splitFrontmatter('# Title\n').fields).toEqual([])
  })
})

describe('promptAttachments', () => {
  it('reads the Files block', () => {
    const prompt = '# Ticket\n\nKey: OMNI-1\n\nFiles:\n- bundle/a.png\n- bundle/b.log\n\n# Task\n'
    expect(promptAttachments(prompt)).toEqual(['bundle/a.png', 'bundle/b.log'])
  })

  it('is empty when the ticket had none', () => {
    expect(promptAttachments('Files:\n(none)\n')).toEqual([])
  })
})

describe('askedQuestion', () => {
  it('strips the prefix the runner writes', () => {
    expect(askedQuestion('agent asked: which database holds the ledger?')).toBe(
      'which database holds the ledger?',
    )
  })

  it('is empty for any other reason', () => {
    expect(askedQuestion('budget exhausted')).toBe('')
  })
})
