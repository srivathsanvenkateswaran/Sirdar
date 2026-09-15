import { PROVIDERS, type Provider } from '../api/types'

/**
 * The models a reader can pick per provider, and the words the Model chip
 * uses to say which one is in use.
 *
 * Every list opens with "CLI default", which is the empty id: the start
 * carries no model and the provider's own CLI picks, as it does when
 * `model: ""` is in the config. The names that follow are the ones the
 * research observed on the wire (`docs/research/06-wire-formats.md` and
 * `docs/research/providers/*.md`); nothing here is guessed, and a provider
 * whose names were never seen offers free text with a hint instead of a
 * list. Ids pass through unchanged — the Claude CLI also takes the aliases
 * `opus`, `sonnet` and `haiku`, and a reader who types one gets that.
 */

export interface ModelChoice {
  /** What the start carries. Empty is the CLI's own default. */
  id: string
  /** What the chip and the list say. */
  label: string
  /** One line under the label, when the name needs one. */
  note?: string
}

export interface ProviderModels {
  id: Provider
  /** The curated list, "CLI default" first. Never empty. */
  models: ModelChoice[]
  /** Under the free-text row: where a name for this provider was observed. */
  hint: string
}

/** The one every list opens with. */
export const CLI_DEFAULT: ModelChoice = {
  id: '',
  label: 'CLI default',
  note: 'Whatever the provider’s CLI is set to',
}

/**
 * The providers the picker offers: the seven the app knows, minus `agy`,
 * which is disabled — Google’s terms do not allow driving Antigravity from
 * another program.
 */
export const PICKABLE_PROVIDERS: Provider[] = PROVIDERS.filter((p) => p !== 'agy')

const CLAUDE: ModelChoice[] = [
  CLI_DEFAULT,
  { id: 'claude-fable-5-1', label: 'Fable 5.1' },
  { id: 'claude-opus-5', label: 'Opus 5' },
  { id: 'claude-sonnet-5', label: 'Sonnet 5' },
  { id: 'claude-haiku-4-5-20251001', label: 'Haiku 4.5' },
]

/** `gpt-5.6-luna` is the one name the app-server probe reported back. */
const CODEX: ModelChoice[] = [
  CLI_DEFAULT,
  { id: 'gpt-5.6-luna', label: 'gpt-5.6-luna', note: 'Reported by the app-server probe' },
]

export const MODELS: Record<Provider, ProviderModels> = {
  claude: {
    id: 'claude',
    models: CLAUDE,
    hint: 'Any Claude model id; the CLI also takes opus, sonnet and haiku.',
  },
  codex: {
    id: 'codex',
    models: CODEX,
    hint: 'Any name the Codex app-server accepts on thread/start.',
  },
  openai: {
    id: 'openai',
    models: [CLI_DEFAULT],
    hint: 'Whatever the endpoint serves, e.g. gpt-oss-120b or qwen3-coder.',
  },
  acp: {
    id: 'acp',
    models: [CLI_DEFAULT],
    hint: 'ACP carries no model field; most agents choose their own.',
  },
  qwen: {
    id: 'qwen',
    models: [CLI_DEFAULT],
    hint: 'The name OPENAI_MODEL or QWEN_MODEL carries, e.g. qwen3-coder.',
  },
  cursor: {
    id: 'cursor',
    models: [CLI_DEFAULT],
    hint: 'auto on a free plan; a paid one takes ids like composer-2.5 or gpt-5.4-nano-low.',
  },
  agy: {
    id: 'agy',
    models: [CLI_DEFAULT],
    hint: 'Antigravity is disabled.',
  },
}

/** The curated list for a provider, or just "CLI default" for one the app does not know. */
export function modelsFor(provider: string): ModelChoice[] {
  return MODELS[provider as Provider]?.models ?? [CLI_DEFAULT]
}

/** The free-text hint for a provider; empty for one the app does not know. */
export function hintFor(provider: string): string {
  return MODELS[provider as Provider]?.hint ?? ''
}

/**
 * How a model id reads on a chip: its curated label when the list has it,
 * otherwise the id itself, which is what the CLI will be told.
 */
export function modelLabel(provider: string, model: string): string {
  if (!model) return CLI_DEFAULT.label
  return modelsFor(provider).find((m) => m.id === model)?.label ?? model
}

/**
 * The chip's value: "claude · Sonnet 5", "claude · CLI default", or, when
 * nothing names a model and a run on that provider reported one, "claude ·
 * CLI default · last used claude-sonnet-5". A missing provider reads as
 * "not set", so the chip never shows a bare word. `unknownAs` is what an
 * empty model reads as where "CLI default" would be a claim — a run that
 * has not reported its model yet is "model unknown", not the default.
 */
export function describeModel(
  provider: string,
  model: string,
  lastUsed = '',
  unknownAs = CLI_DEFAULT.label,
): string {
  if (!provider) return 'not set'
  const parts = [provider, model ? modelLabel(provider, model) : unknownAs]
  if (!model && lastUsed) parts.push(`last used ${lastUsed}`)
  return parts.join(' · ')
}
