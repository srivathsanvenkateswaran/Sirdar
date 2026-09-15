import type { ReactNode } from 'react'
import type { ConfigSummary, Transport } from '../../api/types'
import SettingRow, { SettingCard } from '../../ui/setting-row'
import { Chips, OpenConfig, type Loaded } from './shared'

/**
 * The pages that are a config block read back: Budgets, Permissions, Notes,
 * Notifications and Webhooks. None of them has a control the app applies;
 * every row that has a value in config.yaml offers to open the file.
 */

interface PageProps {
  transport: Transport
  currentWorkspaceId?: string
  summary: Loaded<ConfigSummary>
}

/**
 * The three states every config page shares before it has anything to
 * show: no workspace, the read failing, and the read in flight.
 */
function Gate({
  currentWorkspaceId,
  summary,
  children,
}: PageProps & { children: (summary: ConfigSummary) => ReactNode }): JSX.Element {
  if (!currentWorkspaceId) {
    return <p className="empty-state">Choose a workspace from the switcher to read its config.</p>
  }
  if (summary.status === 'error') return <p className="form-error">{summary.message}</p>
  if (summary.status !== 'done') return <p className="empty-state">Reading config.yaml…</p>
  return <>{children(summary.data)}</>
}

function opener(props: PageProps, summary: ConfigSummary) {
  return (setting: string) => (
    <OpenConfig
      transport={props.transport}
      workspaceId={props.currentWorkspaceId}
      path={summary.general.configPath}
      setting={setting}
    />
  )
}

/** "20 turns", "2.00 USD"; 0 reads as the cap being off. */
function cap(n: number, unit: string, off = 'No cap'): string {
  if (!n) return off
  return `${n} ${unit}`
}

export function BudgetsPage(props: PageProps): JSX.Element {
  return (
    <Gate {...props}>
      {(summary) => {
        const open = opener(props, summary)
        const { budget } = summary
        return (
          <SettingCard heading="Per run">
            <SettingRow
              label="Turns"
              value={cap(budget.maxTurns, 'turns')}
              help="A run past this many agent turns is stopped as over budget."
              control={open('Turns')}
            />
            <SettingRow
              label="Minutes"
              value={cap(budget.maxMinutes, 'minutes')}
              help="Wall-clock time from the first prompt."
              control={open('Minutes')}
            />
            <SettingRow
              label="Cost"
              value={budget.maxUsd ? `${budget.maxUsd.toFixed(2)} USD` : 'No cap'}
              help="Read off the provider's own usage line, where it prints one."
              control={open('Cost')}
            />
            <SettingRow
              label="Stall"
              value={
                budget.stallMinutes
                  ? `${budget.stallMinutes} minutes of silence`
                  : 'Off: a silent provider is waited for'
              }
              help="How long a run waits for the provider to say anything before it is cancelled as stalled."
              control={open('Stall')}
            />
          </SettingCard>
        )
      }}
    </Gate>
  )
}

export function PermissionsPage(props: PageProps): JSX.Element {
  return (
    <Gate {...props}>
      {(summary) => {
        const open = opener(props, summary)
        const { permissions } = summary
        return (
          <>
            <SettingCard heading="Shell">
              <SettingRow
                label="Bash"
                value={<Chips items={permissions.bash} empty="Nothing: every command is refused" />}
                help="What a triage or RCA session may run. Anything not matched is refused before the shell sees it."
                control={open('Bash')}
              />
              <SettingRow
                label="Fix bash"
                value={
                  <Chips items={permissions.fixBash} empty="Nothing: a fix session runs no commands" />
                }
                help="A fix session's own list, so widening it does not widen a read-only run."
                control={open('Fix bash')}
              />
            </SettingCard>
            <SettingCard heading="Beyond the workspace">
              <SettingRow
                label="Fetch"
                value={
                  <Chips
                    items={permissions.fetch}
                    empty="Nothing: every web fetch is denied, on every provider"
                  />
                }
                help="Hosts a session may fetch a URL from."
                control={open('Fetch')}
              />
              <SettingRow
                label="Read also"
                value={
                  <Chips
                    items={permissions.readAlso}
                    empty="Nothing: reads stay inside the workspace, the run directory and its bundle"
                  />
                }
                help="Globs that widen where a read-class tool may look."
                control={open('Read also')}
              />
            </SettingCard>
          </>
        )
      }}
    </Gate>
  )
}

export function NotesPage(props: PageProps): JSX.Element {
  return (
    <Gate {...props}>
      {(summary) => {
        const open = opener(props, summary)
        const { notes, general } = summary
        const names = [notes.filenames.triage, notes.filenames.rca, notes.filenames.resolution].filter(
          Boolean,
        )
        return (
          <SettingCard heading="Notes">
            <SettingRow
              label="Directory"
              value={<code className="settings-mono">{notes.dir}</code>}
              control={open('Directory')}
            />
            <SettingRow
              label="Filenames"
              value={<Chips items={names} empty="The defaults" />}
              help="Triage, RCA and resolution, in that order."
              control={open('Filenames')}
            />
            <SettingRow
              label="Templates"
              value={
                notes.templates ? (
                  <code className="settings-mono">{notes.templates}</code>
                ) : (
                  'Embedded defaults'
                )
              }
              control={open('Templates')}
            />
            <SettingRow
              label="RTL markup"
              value={
                general.rtlMarkup
                  ? 'On: a right-to-left paragraph is wrapped so Obsidian lays it out as written'
                  : 'Off: paragraphs are written as plain markdown'
              }
              help="Applies to the embedded templates only; a workspace with its own templates owns its markup."
              control={open('RTL markup')}
            />
          </SettingCard>
        )
      }}
    </Gate>
  )
}

/** How a credential reference is named once its value is gone. */
function credential(scheme?: string): string {
  return scheme ? `${scheme}: reference` : 'no credential'
}

/**
 * Nothing on these two pages is a secret. Every credential a workspace
 * configures is a reference, and what the API sends is the scheme alone —
 * env, keychain, file, cmd — which says where a secret comes from and
 * names neither it nor the variable holding it.
 */
export function NotificationsPage(props: PageProps): JSX.Element {
  return (
    <Gate {...props}>
      {(summary) => {
        const open = opener(props, summary)
        const { notify } = summary
        if (!notify.enabled) {
          return (
            <SettingCard heading="Notify">
              <SettingRow
                label="When a run finishes"
                value="This workspace posts nothing"
                help="Add a notify block to its config to change that. The note body is never sent either way."
                control={open('When a run finishes')}
              />
            </SettingCard>
          )
        }
        return (
          <>
            <SettingCard heading="Notify">
              <SettingRow
                label="Posts on"
                value={notify.on.join(', ') || 'every terminal state'}
                help="The note body is never sent."
                control={open('Posts on')}
              />
              <SettingRow
                label="Ticket title"
                value={notify.includeTitle ? 'Included in the post' : 'Left out of the post'}
                control={open('Ticket title')}
              />
            </SettingCard>
            <SettingCard heading="Destinations">
              {notify.destinations.map((d, i) => (
                <SettingRow
                  key={`${d.type}-${i}`}
                  label={d.type}
                  value={
                    <>
                      {d.target && <code className="settings-mono">{d.target} · </code>}
                      {credential(d.credential)}
                      {d.headers && d.headers.length > 0 ? `; headers: ${d.headers.join(', ')}` : ''}
                      {d.signed ? '; signed' : ''}
                    </>
                  }
                  control={open(`${d.type} destination`)}
                />
              ))}
              <p className="settings-note">
                Secrets are never shown: a workspace names a reference, and only the scheme of
                that reference reaches this window.
              </p>
            </SettingCard>
          </>
        )
      }}
    </Gate>
  )
}

export function WebhooksPage(props: PageProps): JSX.Element {
  return (
    <Gate {...props}>
      {(summary) => {
        const open = opener(props, summary)
        const { webhooks } = summary
        if (!webhooks.enabled) {
          return (
            <SettingCard heading="Inbound webhooks">
              <SettingRow
                label="Endpoint"
                value="Off: there is no /hooks endpoint to find"
                help="sirdar serve only listens for deliveries when the webhooks block is enabled."
                control={open('Endpoint')}
              />
            </SettingCard>
          )
        }
        const match = [
          webhooks.match.assignee ? `only tickets assigned to ${webhooks.match.assignee}` : '',
          webhooks.match.statuses?.length ? `statuses ${webhooks.match.statuses.join(', ')}` : '',
          webhooks.match.labels?.length ? `labels ${webhooks.match.labels.join(', ')}` : '',
        ].filter(Boolean)
        return (
          <>
            <SettingCard heading="Inbound webhooks">
              <SettingRow
                label="Cooldown"
                value={webhooks.cooldown}
                help="How long a key is left alone after a triage of it."
                control={open('Cooldown')}
              />
              <SettingRow
                label="Match"
                value={match.length > 0 ? match.join('; ') : 'Every verified delivery'}
                control={open('Match')}
              />
            </SettingCard>
            <SettingCard heading="Sources">
              {webhooks.sources.map((src) => (
                <SettingRow
                  key={src.name}
                  label={src.name}
                  value={
                    <>
                      {src.auth === 'basic' ? 'username and password' : 'signed deliveries'} ·{' '}
                      {credential(src.credential)}
                      {src.proxy && (
                        <>
                          {' '}
                          · via <code className="settings-mono">{src.proxy}</code>
                        </>
                      )}
                    </>
                  }
                  control={open(`${src.name} source`)}
                />
              ))}
              <p className="settings-note">
                Secrets are never shown: a workspace names a reference, and only the scheme of
                that reference reaches this window.
              </p>
            </SettingCard>
          </>
        )
      }}
    </Gate>
  )
}
