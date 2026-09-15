import { describe, expect, it } from 'vitest'
import {
  CLI_DEFAULT,
  describeModel,
  hintFor,
  modelLabel,
  modelsFor,
  PICKABLE_PROVIDERS,
} from './models'

describe('the model lists', () => {
  it('offer the seven providers minus agy', () => {
    expect(PICKABLE_PROVIDERS).toEqual(['claude', 'codex', 'openai', 'acp', 'qwen', 'cursor'])
  })

  it('open every list with the CLI default, which is the empty id', () => {
    for (const provider of PICKABLE_PROVIDERS) {
      expect(modelsFor(provider)[0]).toBe(CLI_DEFAULT)
    }
    expect(CLI_DEFAULT.id).toBe('')
  })

  it('name the four Claude models by id, unchanged', () => {
    expect(modelsFor('claude').map((m) => m.id)).toEqual([
      '',
      'claude-fable-5-1',
      'claude-opus-5',
      'claude-sonnet-5',
      'claude-haiku-4-5-20251001',
    ])
  })

  it('list for codex only the name the probe observed', () => {
    expect(modelsFor('codex').map((m) => m.id)).toEqual(['', 'gpt-5.6-luna'])
  })

  it('offer free text with a hint where no name was observed', () => {
    for (const provider of ['openai', 'acp', 'qwen', 'cursor']) {
      expect(modelsFor(provider)).toEqual([CLI_DEFAULT])
      expect(hintFor(provider)).not.toBe('')
    }
    expect(modelsFor('nothing')).toEqual([CLI_DEFAULT])
    expect(hintFor('nothing')).toBe('')
  })
})

describe('modelLabel', () => {
  it('reads a curated id by its label and any other id as itself', () => {
    expect(modelLabel('claude', 'claude-sonnet-5')).toBe('Sonnet 5')
    expect(modelLabel('claude', 'sonnet')).toBe('sonnet')
    expect(modelLabel('claude', '')).toBe('CLI default')
  })
})

describe('describeModel', () => {
  it('says the provider and the model', () => {
    expect(describeModel('claude', 'claude-sonnet-5')).toBe('claude · Sonnet 5')
  })

  it('says CLI default when nothing names a model', () => {
    expect(describeModel('claude', '')).toBe('claude · CLI default')
  })

  it('appends what the newest run used only when nothing names a model', () => {
    expect(describeModel('claude', '', 'claude-sonnet-5')).toBe(
      'claude · CLI default · last used claude-sonnet-5',
    )
    expect(describeModel('claude', 'claude-opus-5', 'claude-sonnet-5')).toBe('claude · Opus 5')
  })

  it('never shows a bare word when there is no provider', () => {
    expect(describeModel('', '')).toBe('not set')
  })
})
