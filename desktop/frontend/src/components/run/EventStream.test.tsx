import { fireEvent, render, screen, within } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import type { RunEvent } from '../../api/types'
import type { IndexedEvent } from '../../lib/events'
import EventStream from './EventStream'
import { LINE_CAP } from './CappedBlock'

const START = '2026-09-10T10:00:00Z'

function at(seconds: number): string {
  const d = new Date(Date.parse(START) + seconds * 1000)
  return d.toISOString()
}

function indexed(events: RunEvent[]): IndexedEvent[] {
  return events.map((event, index) => ({ index, event }))
}

/** A Claude tool_use line: the call, with the id its result and permission name. */
function call(id: string, name: string, input: unknown, t: string): RunEvent {
  return {
    t,
    kind: 'tool_started',
    payload: {
      tool: name,
      raw: { type: 'assistant', message: { content: [{ type: 'tool_use', id, name, input }] } },
    },
  }
}

function result(id: string, text: string, t: string, isError = false): RunEvent {
  return {
    t,
    kind: 'tool_finished',
    payload: {
      text,
      raw: {
        type: 'user',
        message: { content: [{ type: 'tool_result', tool_use_id: id, content: text, is_error: isError }] },
      },
    },
  }
}

function permission(id: string, name: string, decision: 'allow' | 'deny', text = ''): RunEvent {
  return {
    t: at(1),
    kind: 'permission',
    payload: { tool: name, decision, text, raw: { type: 'control_request', request: { tool_name: name, input: {}, tool_use_id: id } } },
  }
}

function said(text: string, t = at(2), delta = false): RunEvent {
  return { t, kind: 'assistant_text', payload: { text, raw: { type: delta ? 'stream_event' : 'assistant' } } }
}

function stream(events: RunEvent[], live = false) {
  return render(<EventStream events={indexed(events)} startedAt={START} live={live} provider="claude" />)
}

/** The rows a screen reader would list: every tool call is a button that opens. */
function callRow(name: RegExp): HTMLElement {
  return screen.getByRole('button', { name })
}

describe('the transcript as a conversation', () => {
  describe('tool calls', () => {
    it('draws one compact row per call: tool, input summary, the decision word and the time it took', () => {
      stream([
        call('a', 'Bash', { command: 'git status' }, at(1)),
        permission('a', 'Bash', 'allow'),
        result('a', 'On branch main', at(6)),
        call('b', 'Read', { file_path: '/srv/app/ledger.go' }, at(7)),
        result('b', '1\tpackage ledger', at(7.4)),
      ])
      const bash = callRow(/Bash/)
      expect(bash).toHaveTextContent('git status')
      expect(bash).toHaveTextContent('allowed')
      expect(bash).toHaveTextContent('5.0s')
      expect(bash).toHaveAttribute('aria-expanded', 'false')
      // No decision recorded: no word. The last call is the one that opens on its own.
      const read = callRow(/Read/)
      expect(read).not.toHaveTextContent('allowed')
      expect(read).toHaveTextContent('0.4s')
      expect(read).toHaveAttribute('aria-expanded', 'true')
    })

    it('opens on click to the input as JSON and the output in full', () => {
      stream([
        call('a', 'Bash', { command: 'git status', description: 'Show status' }, at(1)),
        result('a', 'On branch main\nnothing to commit, working tree clean', at(2)),
        call('b', 'Read', { file_path: 'x' }, at(3)),
        result('b', 'y', at(4)),
      ])
      const bash = callRow(/Bash/)
      expect(screen.queryByText(/nothing to commit/)).toBeNull()

      fireEvent.click(bash)
      expect(bash).toHaveAttribute('aria-expanded', 'true')
      const input = screen.getByLabelText('Bash input')
      expect(input).toHaveTextContent('"command": "git status"')
      expect(input).toHaveTextContent('"description": "Show status"')
      const output = screen.getByLabelText('Bash output')
      expect(output).toHaveTextContent('On branch main')
      expect(output).toHaveTextContent('nothing to commit, working tree clean')
      const row = bash.closest('.sd-event') as HTMLElement
      expect(within(row).getByText('Input')).toBeInTheDocument()
      expect(within(row).getByText('Output')).toBeInTheDocument()

      fireEvent.click(bash)
      expect(bash).toHaveAttribute('aria-expanded', 'false')
      expect(screen.queryByText(/nothing to commit/)).toBeNull()
    })

    it('starts a denied call open, with the reason, and says denied in words', () => {
      const reason = 'Sirdar policy: "go vet ./..." is not in the allow-list'
      stream([
        call('a', 'Bash', { command: 'go vet ./...' }, at(1)),
        permission('a', 'Bash', 'deny', reason),
        result('a', reason, at(2), true),
        call('b', 'Read', { file_path: 'x' }, at(3)),
        result('b', 'y', at(4)),
      ])
      const row = callRow(/go vet/)
      expect(row).toHaveTextContent('denied')
      expect(row).toHaveAttribute('aria-expanded', 'true')
      expect(row.closest('.sd-event')).toHaveAttribute('data-variant', 'deny')
      // The reason leads the open body, and the tool's own echo of it is the output.
      expect(screen.getByText(reason, { selector: '.ev-reason' })).toBeInTheDocument()
    })

    it('says a call failed when the tool itself reported an error', () => {
      stream([
        call('a', 'Bash', { command: 'go test ./...' }, at(1)),
        result('a', 'FAIL\tapp\t0.1s', at(2), true),
      ])
      expect(callRow(/go test/)).toHaveTextContent('failed')
    })

    it('caps a long output at forty lines behind Show all', () => {
      const lines = Array.from({ length: 60 }, (_, i) => `line ${i + 1}`)
      stream([call('a', 'Bash', { command: 'cat big' }, at(1)), result('a', lines.join('\n'), at(2))])
      const output = screen.getByLabelText('Bash output')
      expect(output).toHaveTextContent(`line ${LINE_CAP}`)
      expect(output).not.toHaveTextContent('line 60')
      fireEvent.click(within(output).getByRole('button', { name: 'Show all (60 lines)' }))
      expect(output).toHaveTextContent('line 60')
      expect(within(output).getByRole('button', { name: 'Show less' })).toBeInTheDocument()
    })

    it('draws a table-shaped result as a table', () => {
      const text = '| OrderCode | Channel | Qty |\n|---|---|---|\n| A1 | web | 3 |\n| A2 | pos | 1 |'
      stream([call('a', 'mcp__db__query', { query: 'select' }, at(1)), result('a', text, at(2))])
      const table = screen.getByRole('table')
      expect(within(table).getByRole('columnheader', { name: 'OrderCode' })).toBeInTheDocument()
      expect(within(table).getAllByRole('row')).toHaveLength(3)
      expect(within(table).getByText('pos')).toBeInTheDocument()
    })

    it('reads a Codex result off the item when the payload has no text', () => {
      const item = { type: 'commandExecution', id: 'e1', command: 'rg --files', aggregatedOutput: 'a.go\nb.go\n', exitCode: 0 }
      stream([
        { t: at(1), kind: 'tool_started', payload: { tool: 'commandExecution', raw: { method: 'item/started', params: { item: { ...item, aggregatedOutput: null } } } } },
        { t: at(2), kind: 'tool_finished', payload: { tool: 'commandExecution', raw: { method: 'item/completed', params: { item } } } },
      ])
      expect(screen.getByLabelText('commandExecution output')).toHaveTextContent('a.go b.go')
    })

    it('says a call is still running while the run is live and no result has landed', () => {
      stream([call('a', 'Bash', { command: 'go test ./...' }, at(1))], true)
      expect(callRow(/go test/)).toHaveTextContent('running')
      expect(screen.getByText('Waiting for the result.')).toBeInTheDocument()
    })

    it('hands the open state to the newest call as calls land, unless the reader has chosen', () => {
      const first = [call('a', 'Read', { file_path: 'a.go' }, at(1)), result('a', 'x', at(2))]
      const { rerender } = render(
        <EventStream events={indexed(first)} startedAt={START} live provider="claude" />,
      )
      expect(callRow(/a\.go/)).toHaveAttribute('aria-expanded', 'true')

      const second = [...first, call('b', 'Read', { file_path: 'b.go' }, at(3)), result('b', 'y', at(4))]
      rerender(<EventStream events={indexed(second)} startedAt={START} live provider="claude" />)
      expect(callRow(/a\.go/)).toHaveAttribute('aria-expanded', 'false')
      expect(callRow(/b\.go/)).toHaveAttribute('aria-expanded', 'true')

      // The reader opens the first by hand; the next call does not close it.
      fireEvent.click(callRow(/a\.go/))
      const third = [...second, call('c', 'Read', { file_path: 'c.go' }, at(5)), result('c', 'z', at(6))]
      rerender(<EventStream events={indexed(third)} startedAt={START} live provider="claude" />)
      expect(callRow(/a\.go/)).toHaveAttribute('aria-expanded', 'true')
      expect(callRow(/b\.go/)).toHaveAttribute('aria-expanded', 'false')
      expect(callRow(/c\.go/)).toHaveAttribute('aria-expanded', 'true')
    })
  })

  describe('messages', () => {
    it('renders what the model said as markdown under its provider', () => {
      stream([said('The double count is in **ApplyMovement**:\n\n- line 89\n- line 91')])
      const message = screen.getByTestId('assistant-message')
      expect(within(message).getByText('ApplyMovement').tagName).toBe('STRONG')
      expect(within(message).getAllByRole('listitem')).toHaveLength(2)
      expect(within(message).getByText('claude')).toBeInTheDocument()
      expect(within(message).getByRole('img', { name: 'Claude' })).toBeInTheDocument()
      expect(within(message).getByText('+0:02')).toBeInTheDocument()
    })

    it('grows one block from streamed deltas', () => {
      stream([said('Looking at', at(2)), said(' the handler', at(2), true), said('.', at(2), true)])
      expect(screen.getAllByTestId('assistant-message')).toHaveLength(1)
      expect(screen.getByText('Looking at the handler.')).toBeInTheDocument()
    })

    it('draws a message that is a JSON object as fields, not a wall of braces', () => {
      stream([said('{"classification":"code","confidence":"high"}')])
      const message = screen.getByTestId('assistant-message')
      expect(within(message).getByText('Classification')).toBeInTheDocument()
      expect(within(message).getByText('code')).toBeInTheDocument()
      expect(within(message).queryByText(/\{"classification"/)).toBeNull()
    })
  })

  describe('the answer', () => {
    const answer = {
      title: 'Return counted twice',
      rootCause: { hypothesis: 'ApplyMovement adds a return twice.', confidence: 'high', codeRefs: ['ledger.go:33'] },
      classification: 'code',
      references: ['https://tracker.example/SBX-1'],
      customerReplyDraft: { language: 'en', text: 'Thanks for the report.\n\nWe found the cause.' },
      openQuestions: [],
      blastRadius: '',
    }

    it('heads a card "Answer" with the schema fields as rows and the raw JSON behind a disclosure', () => {
      stream([{ t: at(9), kind: 'final', payload: { text: JSON.stringify(answer), turns: 8, costUsd: 0.56 } }])
      const card = screen.getByTestId('answer-card')
      expect(within(card).getByRole('heading', { name: 'Answer' })).toBeInTheDocument()
      expect(within(card).getByText('8 turns · $0.56')).toBeInTheDocument()
      expect(within(card).getByText('Title')).toBeInTheDocument()
      expect(within(card).getByText('Return counted twice')).toBeInTheDocument()
      expect(within(card).getByText('Root cause')).toBeInTheDocument()
      expect(within(card).getByText('Hypothesis')).toBeInTheDocument()
      expect(within(card).getByText('Code refs')).toBeInTheDocument()
      expect(within(card).getByRole('link', { name: 'https://tracker.example/SBX-1' })).toHaveAttribute('href', 'https://tracker.example/SBX-1')
      // The reply draft is prose: two paragraphs, not one string with \n\n in it.
      expect(within(card).getByText('We found the cause.').tagName).toBe('P')
      // Empty fields are left out.
      expect(within(card).queryByText('Open questions')).toBeNull()
      expect(within(card).queryByText('Blast radius')).toBeNull()
      const raw = within(card).getByText('Raw JSON')
      expect(raw.closest('details')).not.toHaveAttribute('open')
      expect(raw.closest('details')).toHaveTextContent('"title": "Return counted twice"')
      expect(screen.queryByText('note produced')).toBeNull()
    })

    it('draws a prose final line as prose and an empty one as a finished run', () => {
      const { unmount } = stream([{ t: at(9), kind: 'final', payload: { text: 'Committed the fix.' } }])
      expect(within(screen.getByTestId('answer-card')).getByText('Committed the fix.')).toBeInTheDocument()
      expect(screen.queryByText('Raw JSON')).toBeNull()
      unmount()
      stream([{ t: at(9), kind: 'final', payload: {} }])
      expect(screen.getByText('The run finished without a structured answer.')).toBeInTheDocument()
    })
  })

  describe('the rest of the ledger', () => {
    it('keeps the question callout and the failed hue', () => {
      stream([
        { t: at(3), kind: 'question', payload: { text: 'Recount the affected SKUs?' } },
        { t: at(4), kind: 'error', payload: { text: 'invalid_json_schema' } },
      ])
      expect(screen.getByText('The agent is asking')).toBeInTheDocument()
      expect(screen.getByText('Recount the affected SKUs?')).toBeInTheDocument()
      expect(screen.getByText('invalid_json_schema').closest('.sd-event')).toHaveAttribute('data-variant', 'error')
    })

    it('shows a result that paired with no call rather than dropping it', () => {
      stream([result('ghost', 'orphaned output', at(2))])
      expect(screen.getByLabelText('tool result')).toHaveTextContent('orphaned output')
    })
  })
})
