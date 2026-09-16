import { describe, expect, it } from 'vitest'
import type { RunEvent } from '../api/types'
import {
  askedQuestion,
  callDuration,
  callId,
  conversation,
  DEFAULT_FILTER,
  detectTable,
  fieldLabel,
  filterTurns,
  FOLD_MIN,
  foldSystem,
  groupTurns,
  inputSummary,
  isBlank,
  lastCallIndex,
  notePathFor,
  offsetLabel,
  outputFailed,
  outputText,
  parseAnswer,
  promptAttachments,
  splitFrontmatter,
  toolLabel,
  type ConversationItem,
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

describe('foldSystem', () => {
  const delta = (text: string) => ev('stream_event', { text })

  it('collapses a run of stream events into one row', () => {
    const rows = foldSystem(
      indexed([
        ev('assistant_text', { text: 'looking' }),
        delta('a'),
        delta('b'),
        delta('c'),
        ev('tool_started', { tool: 'Bash' }),
      ]),
    )

    expect(rows.map((r) => r.kind)).toEqual(['event', 'fold', 'event'])
    const fold = rows[1]
    if (fold.kind !== 'fold') throw new Error('expected a fold')
    expect(fold.items).toHaveLength(3)
    // The fold is keyed and timed by the first line it stands for.
    expect(fold.index).toBe(1)
  })

  it('leaves a run too short to be worth hiding as its own rows', () => {
    const rows = foldSystem(indexed([delta('a'), delta('b'), ev('usage', { turns: 1 })]))
    expect(rows.map((r) => r.kind)).toEqual(['event', 'event', 'event'])
    expect(FOLD_MIN).toBeGreaterThan(2)
  })

  it('folds each run separately, so a turn keeps its shape', () => {
    const rows = foldSystem(
      indexed([
        delta('a'),
        delta('b'),
        delta('c'),
        ev('tool_finished', { tool: 'Bash' }),
        delta('d'),
        delta('e'),
        delta('f'),
      ]),
    )
    expect(rows.map((r) => r.kind)).toEqual(['fold', 'event', 'fold'])
  })

  it('folds nothing the agent actually did', () => {
    const rows = foldSystem(
      indexed([
        ev('tool_started', { tool: 'Read' }),
        ev('permission', { decision: 'deny' }),
        ev('assistant_text', { text: 'hm' }),
        ev('error', { text: 'boom' }),
        ev('final', {}),
      ]),
    )
    expect(rows.every((r) => r.kind === 'event')).toBe(true)
  })

  it('keeps every line it hides, in order', () => {
    const events = indexed([delta('a'), delta('b'), delta('c'), delta('d')])
    const rows = foldSystem(events)
    expect(rows).toHaveLength(1)
    const fold = rows[0]
    if (fold.kind !== 'fold') throw new Error('expected a fold')
    expect(fold.items).toEqual(events)
  })

  it('is empty for an empty turn', () => {
    expect(foldSystem([])).toEqual([])
  })
})

describe('DEFAULT_FILTER', () => {
  // "All" is the raw file; a provider that streams deltas makes it unreadable.
  it('opens the stream on the tool calls', () => {
    expect(DEFAULT_FILTER).toBe('tools')
  })
})

// ---------------------------------------------------------- conversation

/** A Claude tool_use with the id the result and the permission will name. */
function claudeCall(id: string, name: string, input: unknown, t = ''): RunEvent {
  return ev(
    'tool_started',
    { tool: name, raw: { type: 'assistant', message: { content: [{ type: 'tool_use', id, name, input }] } } },
    t,
  )
}

function claudeResult(id: string, text: string, t = '', isError?: boolean): RunEvent {
  return ev(
    'tool_finished',
    {
      text,
      raw: {
        type: 'user',
        message: { content: [{ type: 'tool_result', tool_use_id: id, content: text, is_error: isError }] },
      },
    },
    t,
  )
}

function claudePermission(id: string, name: string, decision: string, text = ''): RunEvent {
  return ev('permission', {
    tool: name,
    decision,
    text,
    raw: { type: 'control_request', request: { tool_name: name, input: {}, tool_use_id: id } },
  })
}

describe('callId', () => {
  it('reads the tool_use id off a Claude call, its result and its permission', () => {
    expect(callId(claudeCall('tu1', 'Read', { file_path: 'a.go' }))).toBe('tu1')
    expect(callId(claudeResult('tu1', 'ok'))).toBe('tu1')
    expect(callId(claudePermission('tu1', 'Read', 'allow'))).toBe('tu1')
  })

  it('reads the item id off a Codex item and the approval that names it', () => {
    const item = { type: 'commandExecution', id: 'exec-1', command: 'ls' }
    expect(callId(ev('tool_started', { tool: 'commandExecution', raw: { method: 'item/started', params: { item } } }))).toBe('exec-1')
    expect(callId(ev('permission', { tool: 'commandExecution', raw: { method: 'item/commandExecution/requestApproval', params: { itemId: 'exec-1' } } }))).toBe('exec-1')
  })

  it('is empty when the line names nothing', () => {
    expect(callId(ev('tool_finished', { text: 'ok' }))).toBe('')
  })
})

describe('conversation', () => {
  it('pairs a call with its result and its permission by id', () => {
    const items = conversation(
      indexed([
        claudeCall('tu1', 'Edit', { file_path: 'a.go' }),
        claudePermission('tu1', 'Edit', 'allow'),
        claudeResult('tu1', 'The file a.go has been updated.'),
      ]),
    )
    expect(items).toHaveLength(1)
    const item = items[0]
    expect(item.kind).toBe('call')
    if (item.kind !== 'call') return
    expect(item.call.permission?.event.payload.decision).toBe('allow')
    expect(item.call.finished?.event.payload.text).toBe('The file a.go has been updated.')
  })

  it('pairs two parallel calls to their own results whichever order they land in', () => {
    const items = conversation(
      indexed([
        claudeCall('tu1', 'Read', { file_path: 'a.go' }),
        claudeCall('tu2', 'Read', { file_path: 'b.go' }),
        claudeResult('tu2', 'contents of b'),
        claudeResult('tu1', 'contents of a'),
      ]),
    )
    expect(items.map((i) => i.kind)).toEqual(['call', 'call'])
    const [a, b] = items as Extract<ConversationItem, { kind: 'call' }>[]
    expect(a.call.finished?.event.payload.text).toBe('contents of a')
    expect(b.call.finished?.event.payload.text).toBe('contents of b')
  })

  it('falls back to the oldest waiting call when the result carries no id', () => {
    const items = conversation(
      indexed([
        ev('tool_started', { tool: 'shell', raw: { params: { command: 'ls' } } }),
        ev('tool_finished', { text: 'a.go\nb.go' }),
      ]),
    )
    expect(items).toHaveLength(1)
    const item = items[0] as Extract<ConversationItem, { kind: 'call' }>
    expect(item.call.finished?.event.payload.text).toBe('a.go\nb.go')
  })

  it('keeps a result or a permission that pairs with nothing as its own row', () => {
    const items = conversation(indexed([claudeResult('tu9', 'orphan'), claudePermission('tu8', 'Bash', 'deny')]))
    expect(items.map((i) => i.kind)).toEqual(['event', 'event'])
  })

  it('grows one message from streamed deltas and starts a paragraph for a whole message', () => {
    const delta = (text: string) => ev('assistant_text', { text, raw: { type: 'stream_event' } })
    const items = conversation(
      indexed([
        ev('assistant_text', { text: 'Looking at', raw: { type: 'assistant' } }),
        delta(' the handler'),
        delta('.'),
        ev('assistant_text', { text: 'It nils the tenant.', raw: { type: 'assistant' } }),
      ]),
    )
    expect(items).toHaveLength(1)
    const item = items[0] as Extract<ConversationItem, { kind: 'message' }>
    expect(item.text).toBe('Looking at the handler.\n\nIt nils the tenant.')
    expect(item.parts).toHaveLength(4)
  })

  it('breaks a message at a tool call', () => {
    const items = conversation(
      indexed([
        ev('assistant_text', { text: 'one' }),
        claudeCall('tu1', 'Read', { file_path: 'a.go' }),
        ev('assistant_text', { text: 'two' }),
      ]),
    )
    expect(items.map((i) => i.kind)).toEqual(['message', 'call', 'message'])
  })

  it('folds a run of stream lines and leaves the rest as rows', () => {
    const items = conversation(
      indexed([
        ev('system', { text: 'd1' }),
        ev('system', { text: 'd2' }),
        ev('system', { text: 'd3' }),
        ev('final', { text: '{}' }),
        ev('error', { text: 'boom' }),
      ]),
      true,
    )
    expect(items.map((i) => i.kind)).toEqual(['fold', 'event', 'event'])
  })
})

describe('lastCallIndex', () => {
  it('names the last tool call, or -1 without one', () => {
    const events = indexed([ev('assistant_text', { text: 'hi' }), claudeCall('a', 'Read', {}), claudeResult('a', 'x'), claudeCall('b', 'Bash', {})])
    expect(lastCallIndex(events)).toBe(3)
    expect(lastCallIndex(indexed([ev('assistant_text', { text: 'hi' })]))).toBe(-1)
  })
})

describe('callDuration', () => {
  const at = (s: number) => `2026-09-10T10:00:${String(s).padStart(2, '0')}Z`
  const call = (a: string, b?: string) => ({
    started: { index: 0, event: claudeCall('x', 'Bash', {}, a) },
    finished: b === undefined ? undefined : { index: 1, event: claudeResult('x', 'ok', b) },
  })

  it('reads in tenths under ten seconds, whole seconds under a minute, then the clock', () => {
    expect(callDuration(call(at(0), '2026-09-10T10:00:00.400Z'))).toBe('0.4s')
    expect(callDuration(call(at(0), at(12)))).toBe('12s')
    expect(callDuration(call(at(0), '2026-09-10T10:01:04Z'))).toBe('1:04')
  })

  it('is empty until the result lands', () => {
    expect(callDuration(call(at(0)))).toBe('')
  })
})

describe('outputText', () => {
  it('prefers the thin payload text', () => {
    expect(outputText(claudeResult('a', 'hello'))).toBe('hello')
  })

  it('reads a Codex item\'s aggregated output', () => {
    const raw = { method: 'item/completed', params: { item: { type: 'commandExecution', id: 'e1', aggregatedOutput: 'a.go\nb.go\n' } } }
    expect(outputText(ev('tool_finished', { tool: 'commandExecution', raw }))).toBe('a.go\nb.go\n')
  })

  it('reads a Cursor call\'s result and an ACP update\'s content blocks', () => {
    const cursor = { type: 'tool_call', subtype: 'completed', tool_call: { readToolCall: { args: {}, result: { success: { content: 'x' } } } } }
    expect(outputText(ev('tool_finished', { tool: 'Read', raw: cursor }))).toContain('"content": "x"')
    const acp = { method: 'session/update', params: { update: { sessionUpdate: 'tool_call_update', status: 'completed', content: [{ type: 'content', content: { type: 'text', text: 'done' } }] } } }
    expect(outputText(ev('tool_finished', { tool: 'read', raw: acp }))).toBe('done')
  })

  it('is empty for nothing', () => {
    expect(outputText(undefined)).toBe('')
    expect(outputText(ev('tool_finished', {}))).toBe('')
  })
})

describe('outputFailed', () => {
  it('reads Claude\'s is_error and a Codex exit code', () => {
    expect(outputFailed(claudeResult('a', 'denied', '', true))).toBe(true)
    expect(outputFailed(claudeResult('a', 'ok'))).toBe(false)
    const raw = { params: { item: { type: 'commandExecution', id: 'e1', exitCode: 2, status: 'completed' } } }
    expect(outputFailed(ev('tool_finished', { raw }))).toBe(true)
  })
})

describe('detectTable', () => {
  it('reads a pipe table, dropping the markdown rule row', () => {
    const table = detectTable('| OrderCode | Channel | Qty |\n|---|---|---|\n| A1 | web | 3 |\n| A2 | pos | 1 |')
    expect(table?.head).toEqual(['OrderCode', 'Channel', 'Qty'])
    expect(table?.rows).toEqual([['A1', 'web', '3'], ['A2', 'pos', '1']])
  })

  it('reads a tab table of three or more columns', () => {
    const table = detectTable('id\tname\tstock\n1\tbolt\t40\n2\tnut\t12\n')
    expect(table?.head).toEqual(['id', 'name', 'stock'])
    expect(table?.rows).toHaveLength(2)
  })

  it('does not mistake a numbered file listing for a table', () => {
    expect(detectTable('1\tpackage ledger\n2\t\n3\t// Ledger tracks stock')).toBeUndefined()
  })

  it('does not mistake prose or a single line for a table', () => {
    expect(detectTable('ok  \tsandbox/ledger\t0.654s')).toBeUndefined()
    expect(detectTable('a | b\nplain line\nanother plain line\nand one more')).toBeUndefined()
  })

  it('pads a short row rather than dropping the table', () => {
    const table = detectTable('a | b | c\n1 | 2 | 3\n4 | 5 | 6\n7 | 8 | 9\n10 | 11')
    expect(table?.rows[3]).toEqual(['10', '11', ''])
  })
})

describe('parseAnswer', () => {
  it('reads the JSON answer and leaves prose alone', () => {
    expect(parseAnswer('{"title":"x","confidence":"high"}')).toEqual({ title: 'x', confidence: 'high' })
    expect(parseAnswer('Committed the fix.')).toBeUndefined()
    expect(parseAnswer('{not json')).toBeUndefined()
    expect(parseAnswer(undefined)).toBeUndefined()
  })
})

describe('fieldLabel', () => {
  it('spells a key as a sentence-case label', () => {
    expect(fieldLabel('rootCause')).toBe('Root cause')
    expect(fieldLabel('customer_reply_draft')).toBe('Customer reply draft')
    expect(fieldLabel('title')).toBe('Title')
    expect(fieldLabel('prURLs')).toBe('Pr urls')
  })
})

describe('isBlank', () => {
  it('is true for nothing and false for a value', () => {
    expect(isBlank(null)).toBe(true)
    expect(isBlank('  ')).toBe(true)
    expect(isBlank([])).toBe(true)
    expect(isBlank({})).toBe(true)
    expect(isBlank(0)).toBe(false)
    expect(isBlank(false)).toBe(false)
    expect(isBlank('x')).toBe(false)
  })
})

describe('notePathFor', () => {
  const notes = [
    '/w/.sirdar/runs/K/r1/note.md',
    '/vault/Triage/K.md',
    '/w/.sirdar/runs/K/r1/note-resolution.md',
    '/vault/Resolutions/K.md',
  ]

  it('opens the filed copy of each kind', () => {
    expect(notePathFor('rca', notes)).toBe('/vault/Triage/K.md')
    expect(notePathFor('resolution', notes)).toBe('/vault/Resolutions/K.md')
    expect(notePathFor('triage', ['/w/.sirdar/runs/K/r1/note.md'])).toBe('/w/.sirdar/runs/K/r1/note.md')
  })

  it('is empty when the run recorded no such note', () => {
    expect(notePathFor('resolution', ['/w/.sirdar/runs/K/r1/note.md'])).toBe('')
    expect(notePathFor('triage', [])).toBe('')
    expect(notePathFor('triage', undefined)).toBe('')
  })
})
