import { PROVIDERS, type Check, type CheckLevel, type ConfigSummary, type Provider, type Transport, type Workspace } from '../../api/types'
import Badge from '../../ui/badge'
import ProviderMark, { providerName } from '../../ui/provider-mark'
import SettingRow, { SettingCard } from '../../ui/setting-row'
import { levelOf, OpenConfig, type DoctorState, type Loaded } from './shared'

/**
 * What each provider is driven by, whether it may run a fix, and what its
 * read-only guarantee rests on. These are facts about the adapters, from
 * HANDOFF.md's Providers table, not about this machine; the sign-in column
 * is the part doctor answers.
 */
interface ProviderFacts {
  drivenBy: string
  fix: 'yes' | 'refused'
  guard: string
}

export const PROVIDER_FACTS: Record<Provider, ProviderFacts> = {
  claude: {
    drivenBy: 'Claude Code CLI · stream-json',
    fix: 'yes',
    guard: 'every call mediated',
  },
  codex: {
    drivenBy: 'app-server JSON-RPC',
    fix: 'yes',
    guard: 'every call mediated',
  },
  openai: {
    drivenBy: "Sirdar's own loop · any OpenAI-compatible endpoint",
    fix: 'yes',
    guard: 'every call mediated',
  },
  acp: {
    drivenBy: 'Agent Client Protocol · Copilot CLI, OpenCode, Kimi',
    fix: 'yes',
    guard: 'every call mediated',
  },
  qwen: {
    drivenBy: 'Qwen Code CLI',
    fix: 'yes',
    guard: 'hook + tool exclusion',
  },
  cursor: {
    drivenBy: 'cursor-agent · ask mode',
    fix: 'refused',
    guard: 'completed write ends the run',
  },
  // Off: Google's Antigravity terms do not allow a program to drive the CLI.
  // The adapter is still in the tree behind agy.acknowledgeTerms, and doctor
  // says "disabled (Antigravity terms)" for a workspace that still names it.
  agy: {
    drivenBy: 'agy · plan mode',
    fix: 'refused',
    guard: 'disabled (Antigravity terms)',
  },
}

/** What doctor prints for the disabled provider, and what its row says without doctor. */
const AGY_DISABLED = 'disabled (Antigravity terms)'

/** The doctor rows that belong to a provider, by the name each adapter prints. */
function rowsFor(provider: Provider, configured: string, checks: Check[]): Check[] {
  const prefix = provider === 'cursor' ? 'cursor' : provider
  return checks.filter((c) => {
    if (c.name === 'provider') return provider === configured
    if (provider === 'claude' && c.name === 'claude environment') return true
    return c.name.startsWith(`${prefix} `) || c.name === prefix
  })
}

/** Whether a doctor row is the one that says the CLI is logged in. */
function isSignIn(c: Check): boolean {
  return /\b(auth|login|status|models|agent|endpoint)\b/.test(c.name)
}

export interface SignIn {
  level: CheckLevel | 'none'
  word: string
  detail?: string
}

/**
 * One provider's sign-in cell, read off its doctor rows: the failing row
 * first, then a warning, then the row that proves the login. A provider the
 * doctor did not look at — every one but the workspace's own — is "not
 * checked", not "signed out".
 */
export function signInOf(provider: Provider, configured: string, doctor: DoctorState): SignIn {
  const rows = doctor.status === 'done' ? rowsFor(provider, configured, doctor.checks) : []
  // The disabled provider reads as disabled whether or not doctor ran: its
  // one row says so, and without that row nothing else would either.
  const off = rows.find((c) => c.name === 'agy')
  if (off) return { level: 'fail', word: 'disabled', detail: off.detail }
  if (provider === 'agy' && rows.length === 0) {
    return { level: 'fail', word: 'disabled', detail: AGY_DISABLED }
  }
  if (rows.length === 0) return { level: 'none', word: 'not checked' }
  const failed = rows.find((c) => levelOf(c) === 'fail')
  if (failed) return { level: 'fail', word: 'failed', detail: `${failed.name}: ${failed.detail}` }
  const warned = rows.find((c) => levelOf(c) === 'warn')
  if (warned) return { level: 'warn', word: 'warning', detail: `${warned.name}: ${warned.detail}` }
  const login = rows.find(isSignIn) ?? rows[0]
  return { level: 'ok', word: login && isSignIn(login) ? 'signed in' : 'ready', detail: login?.detail }
}

/** The one sentence billing gets. */
function billingLine(billing: string): string {
  return billing === 'api'
    ? "API: billed to the key the workspace names, and budget.maxUsd is read off the provider's usage line"
    : "Subscription: the API key is stripped from the agent's environment"
}

/**
 * Providers: the default for new sessions, and every provider Sirdar can
 * drive with what doctor says about it. Doctor only reaches the workspace's
 * own provider — `sirdar doctor` has no per-provider mode — so Check on any
 * row re-runs the whole report and the other rows say they were not looked
 * at rather than guessing.
 */
export default function ProvidersPage({
  transport,
  workspace,
  currentWorkspaceId,
  summary,
  doctor,
  onRunDoctor,
}: {
  transport: Transport
  workspace?: Workspace
  currentWorkspaceId?: string
  summary: Loaded<ConfigSummary>
  doctor: DoctorState
  onRunDoctor: () => void
}): JSX.Element {
  const general = summary.status === 'done' ? summary.data.general : null
  const provider = general?.provider ?? workspace?.provider ?? ''
  const model = general?.model ?? workspace?.model ?? ''
  const billing = general?.billing ?? workspace?.billing ?? ''
  const checking = doctor.status === 'loading'

  return (
    <>
      <SettingCard heading="Default for new sessions">
        {!currentWorkspaceId ? (
          <p className="empty-state">Choose a workspace from the switcher to see its provider.</p>
        ) : (
          <>
            <SettingRow
              label="Provider"
              value={
                <span className="settings-provider">
                  <ProviderMark provider={provider} size="sm" />
                  {/* Only the parts the config names: a workspace on the provider's default model has no model to print. */}
                  <span>{[provider, model, billing].filter(Boolean).join(' · ')}</span>
                </span>
              }
              control={
                <OpenConfig
                  transport={transport}
                  workspaceId={currentWorkspaceId}
                  path={general?.configPath}
                  setting="Provider"
                />
              }
            />
            <SettingRow
              label={
                <>
                  Billing {billing && <Badge title="Billing mode">{billing}</Badge>}
                </>
              }
              value={billing ? billingLine(billing) : undefined}
            />
          </>
        )}
      </SettingCard>

      <SettingCard heading="Installed">
        {doctor.status === 'error' && <p className="form-error">{doctor.message}</p>}
        <div className="settings-table" role="table" aria-label="Providers">
          <div className="settings-table__row settings-table__head" role="row">
            <span role="columnheader">Provider</span>
            <span role="columnheader">Driven by</span>
            <span role="columnheader">Sign-in</span>
            <span role="columnheader">Fix</span>
            <span role="columnheader">Read-only guard</span>
            <span role="columnheader">
              <span className="visually-hidden">Check</span>
            </span>
          </div>
          {PROVIDERS.map((id) => {
            const facts = PROVIDER_FACTS[id]
            const signIn = signInOf(id, provider, doctor)
            return (
              <div className="settings-table__row" role="row" key={id}>
                <span role="cell" className="settings-provider">
                  <ProviderMark provider={id} size="sm" label={providerName(id)} />
                  <span className="settings-provider__name">{id}</span>
                </span>
                <span role="cell">{facts.drivenBy}</span>
                <span role="cell" className="settings-status" data-level={signIn.level} title={signIn.detail}>
                  {checking ? 'checking…' : signIn.word}
                </span>
                <span role="cell" className={facts.fix === 'refused' ? 'settings-hue' : undefined}>
                  {facts.fix}
                </span>
                <span role="cell">{facts.guard}</span>
                <span role="cell">
                  <button
                    type="button"
                    className="sd-setting-button settings-table__button"
                    aria-label={`Check ${id}`}
                    disabled={checking || !currentWorkspaceId}
                    onClick={onRunDoctor}
                  >
                    Check
                  </button>
                </span>
              </div>
            )
          })}
        </div>
        <p className="settings-note">
          Doctor checks the workspace's own provider; the other rows say what each one can do.
          Check re-runs the report.
        </p>
      </SettingCard>
    </>
  )
}
