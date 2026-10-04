import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'
import type { RepoAsk, RepoMention } from '../api/types'
import { askReason, mentionPhrase, repoAsks, repoMentions, slugOf, type Repo } from './repoMentions'

/*
 * The same table internal/repos tests the service's reading against, so the
 * chip and the prompt cannot drift apart.
 */
interface Table {
  repos: Repo[]
  mentions: { text: string; want: RepoMention[] }[]
  asks: { text: string; want: RepoAsk[] }[]
}

const table = JSON.parse(
  readFileSync(resolve(process.cwd(), '..', '..', 'internal', 'repos', 'testdata', 'mentions.json'), 'utf8'),
) as Table

describe('repoMentions', () => {
  for (const c of table.mentions) {
    it(c.text, () => {
      expect(repoMentions(c.text, table.repos)).toEqual(c.want)
    })
  }
})

describe('repoAsks', () => {
  for (const c of table.asks) {
    it(c.text, () => {
      expect(repoAsks(c.text, table.repos)).toEqual(c.want)
    })
  }
})

describe('words', () => {
  it('says what each mention and ask means', () => {
    expect(mentionPhrase({ name: 'Acme.Web', ref: 'x', status: 'companion' })).toBe('mentions Acme.Web (companion repo)')
    expect(mentionPhrase({ name: 'B', ref: 'x', status: 'unknown' })).toBe('mentions B (not configured — add it under repos:)')
    expect(mentionPhrase({ name: 'api', ref: 'x', status: 'workspace' })).toBe('')
    expect(askReason({ phrase: 'Acme.Web', status: 'unknown' })).toBe('Acme.Web is not a configured repository')
    expect(askReason({ phrase: 'POS', status: 'ambiguous', candidates: ['A.POS', 'B.POS'] })).toBe(
      'POS matches more than one repository: A.POS, B.POS',
    )
    expect(askReason({ phrase: 'web', name: 'Acme.Web', status: 'companion' })).toBe('')
  })

  it('reads a remote the way the service does', () => {
    expect(slugOf('git@github.com:Acme/Web.git')).toBe('github.com/acme/web')
    expect(slugOf('https://user@github.com/acme/web.git')).toBe('github.com/acme/web')
    expect(slugOf('C:/repos/web')).toBe('')
    expect(slugOf(undefined)).toBe('')
  })
})
