import { describe, expect, it } from 'vitest'
import { intentChips, keyInURL, parseIntent, type Intent } from './composeIntent'

/** The table: one line in, the whole reading out. */
const TABLE: { name: string; text: string; want: Partial<Intent> }[] = [
  {
    name: 'an empty box says nothing and is not ambiguous',
    text: '',
    want: { key: '', helpdesk: '', mode: '', instruction: '', ambiguity: '' },
  },
  {
    name: 'a key on its own',
    text: 'OMNI-2510',
    want: { key: 'OMNI-2510', mode: '', instruction: '', ambiguity: '' },
  },
  {
    name: 'a key on its own is read in any case',
    text: '  omni-2510 ',
    want: { key: 'OMNI-2510', instruction: '', ambiguity: '' },
  },
  {
    name: 'a mode word before the key',
    text: 'Triage OMNI-3233',
    want: { key: 'OMNI-3233', mode: 'triage', instruction: '', ambiguity: '' },
  },
  {
    name: 'a mode word after the key, with a note',
    text: 'OMNI-3233 fix the tax rounding on invoice lines',
    want: {
      key: 'OMNI-3233',
      mode: 'fix',
      instruction: 'the tax rounding on invoice lines',
      ambiguity: '',
    },
  },
  {
    name: 'implement is fix',
    text: 'implement OMNI-1',
    want: { key: 'OMNI-1', mode: 'fix', instruction: '', ambiguity: '' },
  },
  {
    name: 'root cause is one phrase, not the word cause',
    text: 'root cause for OMNI-9 please',
    want: { key: 'OMNI-9', mode: 'rca', instruction: 'for please', ambiguity: '' },
  },
  {
    name: 'rca is rca',
    text: 'rca OMNI-9',
    want: { key: 'OMNI-9', mode: 'rca', ambiguity: '' },
  },
  {
    name: 'resolution is rca',
    text: 'OMNI-9 resolution',
    want: { key: 'OMNI-9', mode: 'rca', instruction: '', ambiguity: '' },
  },
  {
    name: 'a tracker URL, and the whole URL leaves the instruction',
    text: 'https://acme.atlassian.net/browse/OMNI-2510 check the rounding',
    want: { key: 'OMNI-2510', mode: '', instruction: 'check the rounding', ambiguity: '' },
  },
  {
    name: 'a tracker URL with a query',
    text: 'https://acme.atlassian.net/browse/OMNI-2510?focusedCommentId=1',
    want: { key: 'OMNI-2510', ambiguity: '' },
  },
  {
    name: 'a linear URL',
    text: 'https://linear.app/acme/issue/SBX-7/',
    want: { key: 'SBX-7', ambiguity: '' },
  },
  {
    name: 'a helpdesk number is reported, not resolved',
    text: '#25312 the customer says the total is off',
    want: {
      key: '',
      helpdesk: '25312',
      instruction: 'the customer says the total is off',
      ambiguity: '',
    },
  },
  {
    name: 'a short hash is not a helpdesk number',
    text: '#123 is not a ticket',
    want: { key: '', helpdesk: '', ambiguity: 'no-key' },
  },
  {
    name: 'prose with no ticket anywhere is ambiguous',
    text: 'the export is empty again',
    want: { key: '', helpdesk: '', mode: '', ambiguity: 'no-key' },
  },
  {
    name: 'two keys are ambiguous, and the first is still offered',
    text: 'is OMNI-1 the same bug as OMNI-2',
    want: { key: 'OMNI-1', ambiguity: 'two-keys' },
  },
  {
    name: 'the same key twice is one key',
    text: 'OMNI-1 — see OMNI-1 again',
    want: { key: 'OMNI-1', ambiguity: '' },
  },
  {
    name: 'two mode words are ambiguous, and the first in the line is still offered',
    text: 'triage OMNI-1 then fix it',
    want: { key: 'OMNI-1', mode: 'triage', ambiguity: 'two-modes' },
  },
  {
    name: 'the first key is the first in the line, URL or not',
    text: 'OMNI-5 looks like https://acme.atlassian.net/browse/OMNI-9',
    want: { key: 'OMNI-5', ambiguity: 'two-keys' },
  },
  {
    name: 'a lower-case hyphenated word is not a key',
    text: 'order-3 never shipped for OMNI-4',
    want: { key: 'OMNI-4', ambiguity: '' },
  },
  {
    name: 'a digit-carrying project prefix still reads',
    text: 'triage A1B-22',
    want: { key: 'A1B-22', mode: 'triage', ambiguity: '' },
  },
]

describe('parseIntent', () => {
  for (const { name, text, want } of TABLE) {
    it(name, () => {
      expect(parseIntent(text)).toMatchObject(want)
    })
  }

  it('reads the mode word out of the instruction', () => {
    expect(parseIntent('fix OMNI-1 and leave the schema alone').instruction).toBe(
      'and leave the schema alone',
    )
  })

  it('leaves the punctuation at a join tidy', () => {
    expect(parseIntent('OMNI-1, the customer says it started on Tuesday').instruction).toBe(
      'the customer says it started on Tuesday',
    )
  })
})

describe('keyInURL', () => {
  it('is the last path segment when that is a key', () => {
    expect(keyInURL('https://acme.atlassian.net/browse/OMNI-2510')).toBe('OMNI-2510')
    expect(keyInURL('https://linear.app/acme/issue/sbx-7/')).toBe('SBX-7')
  })

  it('is nothing for a URL that ends in no key, and for prose', () => {
    expect(keyInURL('https://acme.atlassian.net/browse/')).toBe('')
    expect(keyInURL('not a url')).toBe('')
  })
})

describe('intentChips', () => {
  it('names the mode and the ticket, and the note only when there is one', () => {
    expect(intentChips({ mode: 'triage', key: 'OMNI-3233', instruction: '' })).toEqual([
      'Triage',
      'OMNI-3233',
    ])
    expect(
      intentChips({ mode: 'triage', key: 'OMNI-3233', instruction: 'check the rounding' }),
    ).toEqual(['Triage', 'OMNI-3233', 'with your note'])
    expect(intentChips({ mode: 'triage', key: 'OMNI-3233', instruction: '   ' })).toEqual([
      'Triage',
      'OMNI-3233',
    ])
  })
})
