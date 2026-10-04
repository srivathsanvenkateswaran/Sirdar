import { describe, expect, it } from 'vitest'
import {
  helpdeskIdInURL,
  intentChips,
  intentKind,
  intentRef,
  isSlackLink,
  keyInURL,
  parseIntent,
  type Intent,
} from './composeIntent'

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
    name: 'a mode word sharing the line with other words is prose, not a command',
    text: 'OMNI-3233 fix the tax rounding on invoice lines',
    want: {
      key: 'OMNI-3233',
      mode: '',
      instruction: 'fix the tax rounding on invoice lines',
      ambiguity: '',
    },
  },
  {
    name: 'implement is fix',
    text: 'implement OMNI-1',
    want: { key: 'OMNI-1', mode: 'fix', instruction: '', ambiguity: '' },
  },
  {
    name: 'root cause for a ticket, with words around it, is prose and sets no mode',
    text: 'root cause for OMNI-9 please',
    want: { key: 'OMNI-9', mode: '', instruction: 'root cause for please', ambiguity: '' },
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
    name: 'a short hash is not a helpdesk number, and the line is a session with no ticket',
    text: '#123 is not a ticket',
    want: { key: '', helpdesk: '', ambiguity: '' },
  },
  {
    name: 'prose with no ticket anywhere is a session, not an ambiguous line',
    text: 'the export is empty again',
    want: { key: '', helpdesk: '', mode: '', ambiguity: '' },
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
    name: 'two mode words sharing the line with other words set no mode at all',
    text: 'triage OMNI-1 then fix it',
    want: { key: 'OMNI-1', mode: '', instruction: 'triage then fix it', ambiguity: '' },
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
  {
    name: 'a Zoho agent link is a helpdesk link, not prose',
    text: 'https://desk.zoho.com/agent/acme/support/tickets/details/123400000456789 the export is empty',
    want: {
      key: '',
      helpdeskUrl: 'https://desk.zoho.com/agent/acme/support/tickets/details/123400000456789',
      instruction: 'the export is empty',
      ambiguity: '',
    },
  },
  {
    name: 'a classic Zoho link reads its id out of the fragment',
    text: 'https://desk.zoho.eu/support/acme/ShowHomePage.do#Cases/dv/123400000456789',
    want: { helpdeskUrl: 'https://desk.zoho.eu/support/acme/ShowHomePage.do#Cases/dv/123400000456789', ambiguity: '' },
  },
  {
    name: 'a Slack link with Arabic around it',
    text: 'شوف https://acme.slack.com/archives/C0123ABCD/p1712345678901234?thread_ts=1712345600.000100 لو سمحت',
    want: {
      key: '',
      slack: 'https://acme.slack.com/archives/C0123ABCD/p1712345678901234?thread_ts=1712345600.000100',
      instruction: 'شوف لو سمحت',
      ambiguity: '',
    },
  },
  {
    name: 'a look-alike Slack host is just a URL, and the line is a session with no ticket',
    text: 'https://acme.slack.com.example.org/archives/C0123ABCD/p1712345678901234',
    want: { slack: '', ambiguity: '' },
  },
  {
    name: 'a key with Arabic around it',
    text: 'العميل يقول SBX-1 لا يعمل',
    want: { key: 'SBX-1', instruction: 'العميل يقول لا يعمل', ambiguity: '' },
  },
  {
    name: 'a GitHub link stays in the instruction and sets no mode',
    text: 'SBX-1 broke after https://github.com/acme/web/pull/828-fix',
    want: { key: 'SBX-1', mode: '', instruction: 'broke after https://github.com/acme/web/pull/828-fix', ambiguity: '' },
  },
  {
    name: 'a GitHub reference is not a helpdesk number',
    text: 'SBX-1 since acme/Acme.Web#12345',
    want: { key: 'SBX-1', helpdesk: '', instruction: 'since acme/Acme.Web#12345', ambiguity: '' },
  },
]

describe('parseIntent', () => {
  for (const { name, text, want } of TABLE) {
    it(name, () => {
      expect(parseIntent(text)).toMatchObject(want)
    })
  }

  it('leaves a mode word in the instruction when it is not the whole line', () => {
    expect(parseIntent('fix OMNI-1 and leave the schema alone').instruction).toBe(
      'fix and leave the schema alone',
    )
  })

  it('leaves the punctuation at a join tidy', () => {
    expect(parseIntent('OMNI-1, the customer says it started on Tuesday').instruction).toBe(
      'the customer says it started on Tuesday',
    )
  })

  it('reads a leading slash command as the mode, cut from the instruction', () => {
    expect(parseIntent('/rca OMNI-2510')).toMatchObject({
      key: 'OMNI-2510',
      mode: 'rca',
      instruction: '',
      ambiguity: '',
    })
    expect(parseIntent('/fix OMNI-2510 keep it small')).toMatchObject({
      key: 'OMNI-2510',
      mode: 'fix',
      instruction: 'keep it small',
      ambiguity: '',
    })
    expect(parseIntent('/triage')).toMatchObject({
      key: '',
      mode: 'triage',
      instruction: '',
      ambiguity: 'no-key',
    })
  })

  it('reads a bare mode word alone as the mode, with no instruction left', () => {
    expect(parseIntent('triage OMNI-2510')).toMatchObject({
      key: 'OMNI-2510',
      mode: 'triage',
      instruction: '',
    })
    expect(parseIntent('OMNI-2510 root cause')).toMatchObject({
      key: 'OMNI-2510',
      mode: 'rca',
      instruction: '',
    })
  })

  it('keeps a mode word read as prose once anything else is said alongside the key', () => {
    expect(
      parseIntent('Triage OMNI-2510, the customer says it started after the 3.2 release'),
    ).toMatchObject({
      key: 'OMNI-2510',
      mode: '',
      instruction: 'Triage, the customer says it started after the 3.2 release',
    })
  })

  it('still reads two keys as ambiguous whatever word is said alongside them', () => {
    expect(parseIntent('please fix OMNI-1 and OMNI-2')).toMatchObject({ ambiguity: 'two-keys' })
  })
})

describe('intentKind', () => {
  it('is a session for an instruction with no mode word and no ticket', () => {
    const intent = parseIntent('Why is the refund for order 1234 stuck?')
    expect(intentKind(intent)).toBe('session')
    expect(intent.mode).toBe('')
    expect(intent.ambiguity).toBe('')
  })

  it('is a triage for a bare key, with no instruction', () => {
    const intent = parseIntent('OMNI-2510')
    expect(intentKind(intent)).toBe('triage')
    expect(intent.instruction).toBe('')
  })

  it('is a session for a key with something asked about it', () => {
    const intent = parseIntent('OMNI-2510 was it the PR?')
    expect(intentKind(intent)).toBe('session')
    expect(intent.key).toBe('OMNI-2510')
    expect(intent.instruction).toBe('was it the PR?')
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

describe('intentRef', () => {
  it('is the reference the workspace is asked about, in the service order', () => {
    expect(intentRef(parseIntent('triage SBX-1 #28310'))).toBe('SBX-1')
    expect(intentRef(parseIntent('#28310 is broken again'))).toBe('#28310')
    expect(
      intentRef(parseIntent('https://desk.zoho.com/agent/acme/tickets/details/123400000456789 and https://acme.slack.com/archives/C0123ABCD/p1712345678901234')),
    ).toBe('https://desk.zoho.com/agent/acme/tickets/details/123400000456789')
    expect(intentRef(parseIntent('https://acme.slack.com/archives/C0123ABCD/p1712345678901234 please'))).toBe(
      'https://acme.slack.com/archives/C0123ABCD/p1712345678901234',
    )
    expect(intentRef(parseIntent('the export is empty'))).toBe('')
  })

  it('reads a Zoho id only off a Zoho host', () => {
    expect(helpdeskIdInURL('https://desk.zoho.in/agent/acme/tickets/123400000456789')).toBe('123400000456789')
    expect(helpdeskIdInURL('https://desk.zoho.com.evil.example/agent/acme/tickets/details/123400000456789')).toBe('')
    expect(isSlackLink('http://acme.slack.com/archives/C0123ABCD/p1712345678901234')).toBe(false)
  })
})

describe('intentChips', () => {
  it('puts the resolution in the key’s place', () => {
    expect(
      intentChips({ mode: 'triage', key: 'SBX-1', instruction: '', resolution: '#28310 → SBX-1 · matched by title' }),
    ).toEqual(['Triage', '#28310 → SBX-1 · matched by title'])
  })

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

  it('names a session and its access, with no note chip and no key chip when there is none', () => {
    expect(
      intentChips({ mode: 'session', key: '', instruction: 'Why?', access: 'read-only' }),
    ).toEqual(['Session', 'read-only'])
    expect(
      intentChips({ mode: 'session', key: 'OMNI-2510', instruction: 'was it the PR?', access: 'worktree' }),
    ).toEqual(['Session', 'OMNI-2510', 'writes in worktree'])
  })
})
