import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type KeyboardEvent,
  type SVGProps,
} from 'react'
import type { RunSummary, Ticket, Transport, Workspace } from '../api/types'
import ProviderFields from '../components/run/ProviderFields'
import { useProvidePrimaryAction } from '../components/shell/primaryAction'
import { parseTime, relativeTime } from '../lib/format'
import { getRunJob, subscribeRunJobs } from '../lib/jobs'
import { isQueueUnsupported } from '../store/appStore'
import Button from '../ui/button'
import GroupLabel from '../ui/group-label'
import ItemRow, { type ItemTone } from '../ui/item-row'
import ProviderMark from '../ui/provider-mark'
import SearchBar from '../ui/search-bar'
import SegmentedControl from '../ui/segmented-control'
import './new-session.css'

/** What a session does to a ticket, in the order the control lists them. */
export type SessionMode = 'triage' | 'rca' | 'fix'

/** The one-off overrides a start accepts; empty means the workspace's own. */
export interface StartOverrides {
  provider?: string
  model?: string
  dryRun?: boolean
}

const MODES: { id: SessionMode; label: string }[] = [
  { id: 'triage', label: 'Triage' },
  { id: 'rca', label: 'RCA' },
  { id: 'fix', label: 'Fix' },
]

/** How many of the tickets that landed are listed. */
export const LANDED_LIMIT = 5

/** A ticket key as the trackers write one: letters, a dash, a number. */
const KEY = /^[A-Z]+-\d+$/i

/**
 * The key in what was typed: the key itself, or the last path segment of a
 * tracker URL that is one (`…/browse/OMNI-2510`, `…/issue/OMNI-2510`). Case
 * is forgiven and the answer is upper-case, because that is how the store
 * files the run. Null when there is no key in it.
 */
export function extractKey(input: string): string | null {
  const text = input.trim()
  if (!text) return null
  if (KEY.test(text)) return text.toUpperCase()
  let url: URL
  try {
    url = new URL(text)
  } catch {
    return null
  }
  const segments = url.pathname.split('/').filter((s) => s !== '')
  const last = segments[segments.length - 1] ?? ''
  return KEY.test(last) ? last.toUpperCase() : null
}

/** True when the key has a completed triage behind it, which is what RCA and Fix start from. */
export function hasTriageNote(runs: RunSummary[], key: string): boolean {
  return runs.some((r) => r.key === key && r.kind === 'triage' && r.status === 'completed')
}

function stamp(value: string | undefined): number {
  const ms = parseTime(value)
  return Number.isNaN(ms) ? 0 : ms
}

/** The newest `limit` tickets by their last change. */
export function newestFirst(tickets: Ticket[], limit = LANDED_LIMIT): Ticket[] {
  return tickets
    .slice()
    .sort((a, b) => stamp(b.updatedAt) - stamp(a.updatedAt))
    .slice(0, limit)
}

/** The run that changed last, or undefined when the workspace has none. */
export function newestRun(runs: RunSummary[]): RunSummary | undefined {
  return runs
    .slice()
    .sort((a, b) => stamp(b.updatedAt || b.startedAt) - stamp(a.updatedAt || a.startedAt))[0]
}

const HELPDESKS = ['zendesk', 'zoho', 'freshdesk', 'helpscout', 'intercom', 'hubspot', 'frontapp', 'gorgias']

/**
 * Where a ticket came from, read off its URL. The queue row does not carry
 * the adapter's name, and the host is the next best thing: `atlassian.net`
 * or a `/browse/` path is Jira, `linear.app` is Linear, and so on down the
 * thirteen adapters. Anything else is named by its host's own label, so a
 * self-hosted tracker is still called something.
 */
export function ticketSource(ticket: Ticket): string {
  let host = ''
  try {
    host = new URL(ticket.url).hostname.toLowerCase()
  } catch {
    return ticket.helpdeskRef ? 'helpdesk' : 'tracker'
  }
  if (host.endsWith('atlassian.net') || ticket.url.includes('/browse/')) return 'jira'
  if (host.endsWith('linear.app')) return 'linear'
  if (host.endsWith('dev.azure.com') || host.endsWith('visualstudio.com')) return 'azure'
  if (host.includes('rallydev')) return 'rally'
  if (host.includes('service-now')) return 'servicenow'
  for (const name of HELPDESKS) {
    if (host.includes(name)) return name === 'frontapp' ? 'front' : name
  }
  const labels = host.split('.')
  return labels.length >= 2 ? labels[labels.length - 2] : host || 'tracker'
}

/** Whether a source is a helpdesk, so the row's icon says which kind of record it is. */
function isHelpdesk(source: string): boolean {
  return source === 'helpdesk' || HELPDESKS.some((h) => source === (h === 'frontapp' ? 'front' : h))
}

/** The tone rail and its word, from the newest run the queue row carries. */
function toneOf(run: RunSummary | undefined): { tone: ItemTone; word: string } {
  switch (run?.status) {
    case 'preparing':
    case 'running':
      return { tone: 'live', word: 'running' }
    case 'blocked':
      return { tone: 'blocked', word: 'blocked' }
    case 'completed':
      return { tone: 'done', word: run.kind === 'rca' ? 'done' : 'completed' }
    case 'failed':
    case 'over_budget':
      return { tone: 'failed', word: 'failed' }
    default:
      return { tone: 'plain', word: '' }
  }
}

function Icon(props: SVGProps<SVGSVGElement>): JSX.Element {
  return (
    <svg
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.6"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
      focusable="false"
      {...props}
    />
  )
}

/** lucide `ticket` — a tracker record. */
function TicketIcon(): JSX.Element {
  return (
    <Icon>
      <path d="M3 9V7a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2v2a2 2 0 0 0 0 6v2a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-2a2 2 0 0 0 0-6z" />
      <path d="M13 5v14" />
    </Icon>
  )
}

/** lucide `life-buoy` — a helpdesk record. */
function HelpdeskIcon(): JSX.Element {
  return (
    <Icon>
      <circle cx="12" cy="12" r="10" />
      <circle cx="12" cy="12" r="4" />
      <path d="m4.93 4.93 4.24 4.24M14.83 9.17l4.24-4.24M14.83 14.83l4.24 4.24M9.17 14.83l-4.24 4.24" />
    </Icon>
  )
}

/**
 * Where every piece of work starts: a ticket key or URL, a mode, and Start.
 *
 * The bar takes a key or a tracker URL and the segmented control says what
 * to do with it. Triage always can; RCA and Fix start from a triage note, so
 * they are off — with the reason under the controls — until the key has
 * one. Start calls the mode's start through the store, then waits for the
 * run the job produces and opens it: the store pairs the first `run.updated`
 * for the key with the job id in `lib/jobs`, and this screen watches that
 * pairing rather than guessing from the key alone, which would open an
 * older run for the same ticket.
 *
 * "Landed today" is the tracker's queue for the reader — `queue()` with
 * `assignee: me`, newest first, five at most — each row with a Triage button
 * that starts a triage the same way. The chips say which playbook and which
 * model the session will get; neither is a control, and the one-off
 * overrides the old dialog had live under More options.
 */
export default function NewSession(props: {
  transport: Transport
  workspaceId: string
  /** The current workspace, for the model chip and the provider default. */
  workspace?: Workspace
  /** The workspace's runs, live from the store: what RCA and Fix are gated on. */
  runs: RunSummary[]
  /**
   * Starts a session and answers with the job id, or '' when nothing was
   * started. The store's own `startTriage` / `startRCA` / `startFix`.
   */
  onStart: (mode: SessionMode, key: string, overrides: StartOverrides) => Promise<string>
  onOpenRun: (runId: string) => void
}): JSX.Element {
  const { transport, workspaceId, workspace, runs, onStart, onOpenRun } = props
  const [text, setText] = useState('')
  const [mode, setMode] = useState<SessionMode>('triage')
  const [provider, setProvider] = useState('')
  const [model, setModel] = useState('')
  const [dryRun, setDryRun] = useState(false)
  /** The key a start is in flight for, and the job it became once answered. */
  const [starting, setStarting] = useState('')
  const [awaiting, setAwaiting] = useState('')
  const [error, setError] = useState('')
  // The job id as the event handler reads it: set the moment the start
  // answers, so a `job.finished` that lands before the next render is not
  // missed. The state copy is what the screen draws from.
  const awaitingRef = useRef('')
  const openRun = useRef(onOpenRun)
  openRun.current = onOpenRun

  const [landed, setLanded] = useState<Ticket[] | null>(null)
  const [landedError, setLandedError] = useState('')
  const [noTracker, setNoTracker] = useState(false)

  const key = useMemo(() => extractKey(text), [text])
  const triaged = key ? hasTriageNote(runs, key) : true
  const busy = starting !== '' || awaiting !== ''
  const newest = useMemo(() => newestRun(runs), [runs])

  // --- what landed --------------------------------------------------------

  const load = useCallback(async () => {
    if (!workspaceId) return
    setLandedError('')
    try {
      const tickets = await transport.queue(workspaceId, { assignee: 'me', limit: LANDED_LIMIT })
      setLanded(newestFirst(tickets))
      setNoTracker(false)
    } catch (err) {
      setLanded([])
      if (isQueueUnsupported(err)) {
        setNoTracker(true)
        return
      }
      setLandedError(err instanceof Error ? err.message : String(err))
    }
  }, [transport, workspaceId])

  useEffect(() => {
    void load()
  }, [load])

  // One subscription for the two events the screen answers. A delivery means
  // the tracker changed something, so the list is read again rather than
  // left to say what landed before it did. A job that ends before any run
  // was reported — a key the tracker does not know, a provider that refused
  // — still names its runs in the outcomes, so the session opens if there is
  // one and the wait ends either way.
  useEffect(() => {
    return transport.subscribe((e) => {
      if (e.kind === 'hook.received') {
        void load()
        return
      }
      if (e.kind !== 'job.finished' || !awaitingRef.current || e.jobId !== awaitingRef.current) {
        return
      }
      const runId = e.outcomes?.find((o) => o.runId)?.runId
      awaitingRef.current = ''
      setAwaiting('')
      if (runId) openRun.current(runId)
      else setError('The job ended before a session started.')
    })
  }, [transport, load])

  // --- starting -----------------------------------------------------------

  const begin = useCallback(
    async (what: SessionMode, forKey: string) => {
      if (busy) return
      setError('')
      setStarting(forKey)
      try {
        const jobId = await onStart(what, forKey, {
          provider: provider || undefined,
          model: model.trim() || undefined,
          dryRun: what !== 'rca' && dryRun ? true : undefined,
        })
        if (jobId) {
          awaitingRef.current = jobId
          setAwaiting(jobId)
        }
      } catch (err) {
        setError(err instanceof Error ? err.message : String(err))
      } finally {
        setStarting('')
      }
    },
    [busy, onStart, provider, model, dryRun],
  )

  // The run the job produced: the store pairs it with the job id as its first
  // `run.updated` arrives, and the pairing is read here off the runs the
  // store already holds. Both the pairing and the run list can be the one
  // that lands second, so the check runs on either changing.
  useEffect(() => {
    if (!awaiting) return
    function check(): void {
      const found = runs.find((r) => getRunJob(r.runId) === awaiting)
      if (!found) return
      awaitingRef.current = ''
      setAwaiting('')
      onOpenRun(found.runId)
    }
    check()
    return subscribeRunJobs(check)
  }, [awaiting, runs, onOpenRun])

  // --- what Start can do ------------------------------------------------

  const needsNote = key !== null && !triaged
  const noteReason = key ? `Needs a triage note for ${key} first` : ''
  const disabledModes = needsNote ? { rca: noteReason, fix: noteReason } : undefined
  const canStart = key !== null && !(needsNote && mode !== 'triage') && !busy

  const reason = error
    ? error
    : text.trim() && key === null
      ? 'Enter a ticket key like OMNI-2510, or a tracker URL that ends in one.'
      : needsNote
        ? `RCA and Fix need a triage note for ${key} first. Start a triage.`
        : ''

  const start = useCallback(() => {
    if (!canStart || !key) return
    void begin(mode, key)
  }, [canStart, key, mode, begin])

  useProvidePrimaryAction({
    label: busy ? 'Starting…' : 'Start',
    onRun: start,
    disabled: !canStart,
    busy,
    shortcut: '⌘↵',
    placement: 'screen',
  })

  function onKeyDown(e: KeyboardEvent<HTMLElement>): void {
    if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) {
      e.preventDefault()
      start()
    }
  }

  const chipProvider = provider || workspace?.provider || ''
  const chipModel = provider ? model.trim() : model.trim() || workspace?.model || ''

  return (
    <section className="new-session" aria-label="New session" onKeyDown={onKeyDown}>
      <div className="new-session__col">
        <h1 className="new-session__title">Start with a ticket</h1>

        <div className="new-session__start">
          <SearchBar
            label="Ticket key or URL"
            value={text}
            placeholder="Paste a ticket key or URL"
            autoFocus
            onChange={setText}
            onSubmit={start}
            aside={
              newest ? (
                <button
                  type="button"
                  className="new-session__recent"
                  onClick={() => onOpenRun(newest.runId)}
                >
                  Recent sessions
                </button>
              ) : undefined
            }
          />

          <div className="new-session__controls">
            <SegmentedControl
              label="Mode"
              options={MODES}
              value={mode}
              onChange={(id) => setMode(id as SessionMode)}
              disabledOptions={disabledModes}
            />
            <span className="new-session__chip">
              Playbook <span className="mono">auto</span>
            </span>
            {chipProvider && (
              <span className="new-session__chip">
                Model <ProviderMark provider={chipProvider} size="sm" />
                <span className="mono" dir="ltr">
                  {chipModel ? `${chipProvider} · ${chipModel}` : chipProvider}
                </span>
              </span>
            )}
            {/* The screen's one filled button: the sidebar's New session
                steps down while this is up. */}
            <Button
              variant="primary"
              shortcut="⌘↵"
              busy={busy}
              disabled={!canStart && !busy}
              onClick={start}
            >
              {busy ? 'Starting…' : 'Start'}
            </Button>
          </div>

          {reason && (
            <p className="new-session__reason" data-tone={error ? 'error' : undefined} role="status">
              {reason}
            </p>
          )}

          <details className="new-session__more">
            <summary>More options</summary>
            <div className="new-session__more-body">
              <ProviderFields
                idPrefix="new"
                provider={provider}
                model={model}
                defaultProvider={workspace?.provider}
                disabled={busy}
                onProvider={setProvider}
                onModel={setModel}
              />
              <label className="checkbox">
                <input
                  type="checkbox"
                  checked={dryRun}
                  disabled={busy || mode === 'rca'}
                  onChange={(e) => setDryRun(e.target.checked)}
                />
                Dry run — build the prompt and bundle, call no provider
              </label>
            </div>
          </details>
        </div>

        <div className="new-session__landed">
          <GroupLabel as="h2">Landed today</GroupLabel>
          {landed === null ? (
            <p className="new-session__empty">Loading what landed…</p>
          ) : noTracker ? (
            <p className="new-session__empty">
              This workspace has no tracker; start by key.
            </p>
          ) : landedError ? (
            <p className="new-session__empty">Could not read the queue. {landedError}</p>
          ) : landed.length === 0 ? (
            <p className="new-session__empty">Nothing landed today</p>
          ) : (
            landed.map((ticket) => {
              const source = ticketSource(ticket)
              const { tone, word } = toneOf(ticket.latestRun)
              const age = relativeTime(ticket.updatedAt)
              const facts = [ticket.key, source, age, word].filter(Boolean).join(' · ')
              const latest = ticket.latestRun
              return (
                <ItemRow
                  key={ticket.key}
                  icon={isHelpdesk(source) ? <HelpdeskIcon /> : <TicketIcon />}
                  title={ticket.title || ticket.key}
                  dir="auto"
                  tone={tone}
                  meta={
                    <span className="new-session__meta" dir="ltr">
                      {facts}
                    </span>
                  }
                  onOpen={latest ? () => onOpenRun(latest.runId) : undefined}
                  openLabel={latest ? `Open ${ticket.key}, ${ticket.title || ticket.key}` : undefined}
                  action={
                    <Button
                      variant="pale"
                      size="sm"
                      busy={starting === ticket.key}
                      disabled={busy && starting !== ticket.key}
                      aria-label={`Triage ${ticket.key}`}
                      onClick={() => void begin('triage', ticket.key)}
                    >
                      Triage
                    </Button>
                  }
                />
              )
            })
          )}
        </div>
      </div>
    </section>
  )
}
