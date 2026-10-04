import type { ConfigSummary, RepoSummary } from '../../api/types'
import SettingRow, { SettingCard } from '../../ui/setting-row'
import type { Loaded } from './shared'

/** The level a clone's state is drawn at: a missing clone is worth a look. */
function levelOf(r: RepoSummary): 'ok' | 'warn' {
  return r.facts.exists ? 'ok' : 'warn'
}

/**
 * Repositories: the workspace's own and every companion from `repos:`, with
 * what a read-only look at each clone says — the branch, how far behind its
 * last fetch left it, and when that was. Read-only, like the rest of the
 * summary; the path and the origin are on the name's tooltip rather than in
 * the row.
 */
export default function ReposCard({ summary }: { summary: Loaded<ConfigSummary> }): JSX.Element | null {
  if (summary.status !== 'done') return null
  const repos = summary.data.repos ?? []
  if (repos.length === 0) return null
  const companions = repos.filter((r) => !r.workspace).length

  return (
    <SettingCard heading="Repositories">
      {repos.map((r) => (
        <SettingRow
          key={r.name}
          label={
            <span title={[r.path, r.origin].filter(Boolean).join('\n')}>
              <bdi dir="ltr">{r.name}</bdi>
              {r.workspace ? <span className="settings-hue"> · this workspace</span> : null}
            </span>
          }
          value={r.workspace ? 'Where a fix is made' : r.about || undefined}
          help={
            <span className="settings-status" data-level={levelOf(r)} data-testid={`repo-state-${r.name}`}>
              <bdi dir="ltr">{r.state}</bdi>
            </span>
          }
        />
      ))}
      <p className="settings-note">
        {companions === 0
          ? 'A session reads this repository only. Name others under repos: in config.yaml to let it read them too.'
          : 'A session may read every repository here; a fix is made only in this workspace. Nothing here fetches.'}
      </p>
    </SettingCard>
  )
}
