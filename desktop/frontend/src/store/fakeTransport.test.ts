import { describe, expect, it } from 'vitest'
import { createFakeTransport, suggestion } from './fakeTransport'

describe('fake transport suggestions', () => {
  it('gives a remembered message an id no pending suggestion has, after a dismiss', async () => {
    const t = createFakeTransport({ suggestions: [suggestion({ id: 's1' }), suggestion({ id: 's2' })] })
    await t.dismissSuggestion('ws1', 's1')
    const made = await t.remember('ws1', 'r1', 3)
    const ids = (await t.suggestions('ws1')).map((s) => s.id)
    expect(ids).toEqual(['s2', made.id])
    expect(new Set(ids).size).toBe(2)
  })
})
