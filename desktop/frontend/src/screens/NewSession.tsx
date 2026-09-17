import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  useSyncExternalStore,
  type SVGProps,
} from 'react'
import type { ComposedIntent, HelpdeskLink, RunSummary, Ticket, Transport, Workspace } from '../api/types'
import ChipMenu, { type ChipMenuItem } from '../components/composer/ChipMenu'
import ComposerCard from '../components/composer/ComposerCard'
import { ACCESS, MODES, accessOf, type SessionMode } from '../components/composer/modes'
import { useProvidePrimaryAction } from '../components/shell/primaryAction'
import WorkspaceSwitcher from '../components/shell/WorkspaceSwitcher'
import {
  COMPOSER_PLACEHOLDER,
  NO_KEY_REASON,
  intentChips,
  parseIntent,
} from '../lib/composeIntent'
import {
  FIX_THEN_NOTE,
  FIX_THEN_OPTIONS,
  composerPrefs,
  setFixThen,
  subscribeComposerPrefs,
  type FixThen,
} from '../lib/composerPrefs'
import { parseTime, reasonOf, relativeTime } from '../lib/format'
import { getRunJob, subscribeRunJobs } from '../lib/jobs'
import { useDebounced } from '../lib/useDebounced'
import { isQueueUnsupported } from '../store/appStore'
import Button from '../ui/button'
import GroupLabel from '../ui/group-label'
import ItemRow, { type ItemTone } from '../ui/item-row'
import ModelPicker from '../ui/model-picker'
import SegmentedControl from '../ui/segmented-control'
import './new-session.css'

export type { SessionMode } from '../components/composer/modes'

/**
 * Everything one start carries besides the mode and the key: the one-off
 * provider and model, the dry run, what the operator asked for in their own
 * words, and the two modes' own inputs — what a fix does with its commit,
 * and the pull request and resolution an RCA reads.
 */
export interface StartOverrides {
  provider?: string
  model?: string
  dryRun?: boolean
  /** What the operator typed around the ticket key; the session reads it as its Operator's request. */
  instruction?: string
  /** Fix only: push the branch and open no pull request. */
  noPr?: boolean
  /** Fix only: stop at the commit — nothing pushed, the change read in Change review. */
  local?: boolean
  /** RCA only: the merged pull request to read. */
  prUrl?: string
  /** RCA only: the engineer's own account of what was done. */
  resolution?: string
}

/** How many of the tickets that landed are listed. */
export const LANDED_LIMIT = 5

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

/**
 * The model the newest run on `provider` reported, or '' when no run on that
 * provider has said. This is what "CLI default" turned out to be last time:
 * with `model: ""` in the config nobody knows whether the CLI ran Fable,
 * Opus or Sonnet until a run says so, and `RunSummary.model` carries what
 * the run reported.
 */
export function lastUsedModel(runs: RunSummary[], provider: string): string {
  if (!provider) return ''
  return newestRun(runs.filter((r) => r.provider === provider && r.model))?.model ?? ''
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

/** How long a helpdesk number waits after a keystroke before it is looked up. */
export const HELPDESK_DEBOUNCE_MS = 250

/** Why a line cannot be settled here, in the words the status line uses. */
export const AMBIGUOUS_REASON: Record<string, string> = {
  '': '',
  'no-key': 'No ticket named yet \u2014 press Enter and I will read what you typed',
  'two-keys': 'Two ticket keys in there \u2014 press Enter and I will work out which you meant',
  'two-modes': 'Two things asked for at once \u2014 press Enter and I will work out which you meant',
}

/** What the RCA note carries when the two fields are left empty. */
export const RCA_EMPTY_HELP =
  'Both are optional. Left empty, the note carries `<fill: pull request>` and `<fill: what was done>` for you to complete.'

/**
 * Why the pull request URL will not do, or '' when it will. Empty is fine:
 * the field is optional, and the note says so.
 */
export function prUrlProblem(value: string): string {
  const text = value.trim()
  if (text === '') return ''
  try {
    const url = new URL(text)
    if (url.protocol !== 'http:' && url.protocol !== 'https:') {
      return 'The pull request URL has to be an http or https address.'
    }
  } catch {
    return 'The pull request URL has to be an http or https address.'
  }
  return ''
}

/**
 * Where every piece of work starts: one box you type into, a mode, and the
 * send button.
 *
 * The box is a prompt and not a key field. A person says what they want —
 * "triage OMNI-2510, the customer says it started after the 3.2 release" —
 * and `lib/composeIntent` reads that line on every keystroke: the ticket
 * (a key, a tracker URL, or a helpdesk number this screen then asks the
 * helpdesk about), the mode (a word like triage, fix or root cause), and
 * the instruction, which is everything left once those come out. The status
 * line under the box says what was understood, in chips — "Triage ·
 * OMNI-3233 · with your note" — or why nothing can be started yet, and the
 * send button's title says the same thing.
 *
 * The Mode chip wins over the words. A word sets it live while nobody has
 * touched it; the moment somebody picks a mode, their pick stands whatever
 * they type afterwards, because a control that moved under a person's hand
 * is worse than one that ignored a word.
 *
 * A line the parser cannot settle — words with no ticket in them, two keys,
 * two mode words — is read once by the model instead, behind the
 * `composer.intentAssist` setting, and comes back as the same chips in a
 * Confirm state. Nothing starts on that reading until a second Enter. The
 * call is never made on an empty box or on a line that read cleanly.
 *
 * The instruction travels with the start and reaches the session as an
 * Operator's request section at the top of its prompt. It is not the note:
 * nothing filed carries it.
 *
 * Fix and RCA have inputs of their own under the card — what the fix does
 * once it has committed, and the pull request and resolution an RCA reads —
 * and RCA and Fix are off until the key has a triage note behind it. Send
 * calls the mode's start through the store, then waits for the run the job
 * produces and opens it: the store pairs the first `run.updated` for the
 * key with the job id in `lib/jobs`, and this screen watches that pairing
 * rather than guessing from the key alone, which would open an older run
 * for the same ticket.
 *
 * "Landed today" is the tracker's queue for the reader — `queue()` with
 * `assignee: me`, newest first, five at most — each row with a Triage button
 * that starts a triage the same way. Dry run lives under More options.
 */
export default function NewSession(props: {
  transport: Transport
  workspaceId: string
  /** The current workspace, for the headline, the model chip and the provider default. */
  workspace?: Workspace
  /** Every workspace, for the switcher the headline opens. */
  workspaces?: Workspace[]
  onSelectWorkspace?: (id: string) => void
  onAddWorkspace?: () => void
  /** The workspace's runs, live from the store: what RCA and Fix are gated on. */
  runs: RunSummary[]
  /**
   * Starts a session and answers with the job id, or '' when nothing was
   * started. The store's own `startTriage` / `startRCA` / `startFix`.
   */
  onStart: (mode: SessionMode, key: string, overrides: StartOverrides) => Promise<string>
  onOpenRun: (runId: string) => void
}): JSX.Element {
  const {
    transport,
    workspaceId,
    workspace,
    workspaces = workspace ? [workspace] : [],
    onSelectWorkspace = () => {},
    onAddWorkspace = () => {},
    runs,
    onStart,
    onOpenRun,
  } = props
  const [text, setText] = useState('')
  /**
   * The mode somebody picked, which outranks whatever word the line uses.
   * Null until they pick one, and then it stands.
   */
  const [pinnedMode, setPinnedMode] = useState<SessionMode | null>(null)
  const [provider, setProvider] = useState('')
  const [model, setModel] = useState('')
  const [dryRun, setDryRun] = useState(false)
  /** RCA's own two inputs. */
  const [prUrl, setPrUrl] = useState('')
  const [resolution, setResolution] = useState('')
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

  const prefs = useSyncExternalStore(subscribeComposerPrefs, () => composerPrefs(workspaceId))

  const intent = useMemo(() => parseIntent(text), [text])

  // --- the helpdesk number, resolved --------------------------------------

  /** What the helpdesk said about the number in the box, when there is one. */
  const [link, setLink] = useState<HelpdeskLink | null>(null)
  const [resolving, setResolving] = useState(false)
  const number = useDebounced(intent.helpdesk, HELPDESK_DEBOUNCE_MS)

  useEffect(() => {
    if (!number || !workspaceId) {
      setLink(null)
      setResolving(false)
      return
    }
    let live = true
    setResolving(true)
    void transport
      .resolveHelpdesk(workspaceId, number)
      .then((got) => {
        if (live) setLink(got)
      })
      .catch((err: unknown) => {
        if (live) setLink({ number, key: '', reason: reasonOf(err) })
      })
      .finally(() => {
        if (live) setResolving(false)
      })
    return () => {
      live = false
    }
  }, [transport, workspaceId, number])

  // --- what the model made of an ambiguous line ---------------------------

  /** The reading waiting to be confirmed, and the line it was made of. */
  const [confirming, setConfirming] = useState<{ of: string; read: ComposedIntent } | null>(null)
  const [reading, setReading] = useState(false)
  // A reading is about the line it was made of. Type another character and
  // it is stale, so it goes rather than sitting there confirming something
  // nobody typed.
  useEffect(() => {
    setConfirming((held) => (held && held.of === text ? held : null))
  }, [text])

  const confirmed = confirming?.read
  const key = confirmed?.key || intent.key || (number === intent.helpdesk ? link?.key ?? '' : '')
  const instruction = (confirmed?.instruction ?? intent.instruction).trim()
  const mode: SessionMode = pinnedMode ?? (confirmed?.mode || intent.mode || 'triage')
  const fixThen = prefs.fixThen

  const triaged = key ? hasTriageNote(runs, key) : true
  const busy = starting !== '' || awaiting !== '' || reading

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
      setLandedError(reasonOf(err))
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
    async (what: SessionMode, forKey: string, note: string) => {
      if (busy) return
      setError('')
      setStarting(forKey)
      try {
        const jobId = await onStart(what, forKey, {
          provider: provider || undefined,
          model: model.trim() || undefined,
          dryRun: what !== 'rca' && dryRun ? true : undefined,
          instruction: note || undefined,
          noPr: what === 'fix' && fixThen === 'noPr' ? true : undefined,
          local: what === 'fix' && fixThen === 'local' ? true : undefined,
          prUrl: what === 'rca' ? prUrl.trim() || undefined : undefined,
          resolution: what === 'rca' ? resolution.trim() || undefined : undefined,
        })
        if (jobId) {
          awaitingRef.current = jobId
          setAwaiting(jobId)
        }
      } catch (err) {
        setError(reasonOf(err))
      } finally {
        setStarting('')
      }
    },
    [busy, onStart, provider, model, dryRun, fixThen, prUrl, resolution],
  )

  // The run the job produced: the store pairs it with the job id as its first
  // `run.updated` arrives, and the pairing is read here off the runs the
  // store already holds. The store notifies the pairing before it publishes
  // the run, so the run list is a real dependency: the check runs again when
  // it lands. The open callback is read through its ref, because App hands
  // over a fresh one every render and re-subscribing on each was the cost.
  useEffect(() => {
    if (!awaiting) return
    function check(): void {
      const found = runs.find((r) => getRunJob(r.runId) === awaiting)
      if (!found) return
      awaitingRef.current = ''
      setAwaiting('')
      openRun.current(found.runId)
    }
    check()
    return subscribeRunJobs(check)
  }, [awaiting, runs])

  // --- what the send button can do ---------------------------------------

  const needsNote = key !== '' && !triaged
  const noteReason = key ? `Needs a triage note for ${key} first` : ''
  /**
   * Whether the model should be asked to read the line: it is ambiguous,
   * nothing has been confirmed yet, the setting is on, and the line has not
   * already resolved to a ticket on its own.
   */
  const wantsReading =
    prefs.intentAssist && intent.ambiguity !== '' && !confirmed && key === ''
  const canStart = (key !== '' && !(needsNote && mode !== 'triage') && !busy) || (wantsReading && !busy)

  /** The chips, or the one line saying why there is nothing to start. */
  const chips = key !== '' ? intentChips({ mode, key, instruction }) : []
  /**
   * The one line that stops a start. A key with no triage note behind it
   * stops an RCA or a fix and nothing else, so on a triage it is a note
   * under the chips rather than in place of them: most keys worth typing
   * have no note yet, and telling somebody that instead of what they are
   * about to start would be wrong on nearly every line.
   */
  const blocked = error
    ? error
    : needsNote && mode !== 'triage'
      ? `RCA and Fix need a triage note for ${key} first. Start a triage.`
      : key !== ''
        ? ''
        : resolving
          ? `Looking up helpdesk ${intent.helpdesk}…`
          : link && link.number === intent.helpdesk && link.reason
            ? link.reason
            : wantsReading
              ? AMBIGUOUS_REASON[intent.ambiguity]
              : NO_KEY_REASON

  const start = useCallback(() => {
    if (!canStart) return
    if (wantsReading) {
      setError('')
      setReading(true)
      const of = text
      void transport
        .composeIntent(workspaceId, of)
        .then((read) => setConfirming({ of, read }))
        .catch((err: unknown) => setError(reasonOf(err)))
        .finally(() => setReading(false))
      return
    }
    if (!key) return
    void begin(mode, key, instruction)
  }, [canStart, wantsReading, text, transport, workspaceId, key, mode, instruction, begin])

  useProvidePrimaryAction({
    label: busy ? 'Starting…' : wantsReading ? 'Read this' : 'Start',
    onRun: start,
    disabled: !canStart,
    busy,
    shortcut: '↵',
    placement: 'screen',
  })

  // What "CLI default" was last time, for the chip: the newest run on the
  // provider the session will use, whether that is the override or the
  // workspace's own.
  const chipProvider = provider || workspace?.provider || ''
  const lastUsed = useMemo(() => lastUsedModel(runs, chipProvider), [runs, chipProvider])

  const modeItems: ChipMenuItem[] = MODES.map((m) => ({
    ...m,
    disabled: needsNote && m.id !== 'triage' ? noteReason : undefined,
  }))
  const access = accessOf(mode)

  const sendTitle = canStart
    ? wantsReading
      ? 'Read what this says (↵)'
      : `${chips.join(' · ')} (↵)`
    : busy
      ? undefined
      : needsNote
        ? noteReason
        : blocked

  return (
    <section className="new-session" aria-label="New session">
      <div className="new-session__col">
        <h1 className="new-session__title">
          What should we look at in{' '}
          <WorkspaceSwitcher
            variant="inline"
            workspaces={workspaces}
            currentId={workspace?.id ?? workspaceId}
            onSelect={onSelectWorkspace}
            onAdd={onAddWorkspace}
          />
          ?
        </h1>

        <div className="new-session__start">
          <ComposerCard
            name="Start"
            label="What to look at: a ticket key or URL, and anything you want to say"
            value={text}
            onChange={setText}
            placeholder={COMPOSER_PLACEHOLDER}
            autoFocus
            disabled={busy}
            chips={
              <>
                <ModelPicker
                  provider={provider}
                  model={model}
                  defaultProvider={workspace?.provider}
                  defaultModel={workspace?.model}
                  lastUsed={lastUsed}
                  disabled={busy}
                  onChange={(choice) => {
                    setProvider(choice.provider)
                    setModel(choice.model)
                  }}
                />
                <ChipMenu
                  label="Mode"
                  value={mode}
                  items={modeItems}
                  disabled={busy}
                  onSelect={(id) => setPinnedMode(id as SessionMode)}
                />
                <ChipMenu label="Access" value={access} items={ACCESS} disabled={busy} />
              </>
            }
            send={{
              label: confirming ? 'Start' : wantsReading ? 'Read this' : 'Start',
              busyLabel: reading ? 'Reading…' : 'Starting…',
              busy,
              disabled: !canStart,
              title: sendTitle,
              onClick: start,
            }}
          />

          <p
            className="new-session__reason"
            data-tone={error ? 'error' : undefined}
            data-state={confirming ? 'confirm' : undefined}
            role="status"
          >
            {blocked === '' && chips.length > 0 ? (
              <>
                {confirming ? <span className="new-session__confirm">Confirm</span> : null}
                <span className="new-session__chips">{chips.join(' · ')}</span>
                {confirming ? ' — press Enter again to start' : ''}
              </>
            ) : (
              blocked
            )}
          </p>

          {needsNote && mode === 'triage' ? (
            <p className="new-session__help">
              RCA and Fix need a triage note for {key} first. Start a triage.
            </p>
          ) : null}

          {mode === 'fix' ? (
            <div className="new-session__mode-fields">
              <SegmentedControl
                label="Then"
                options={FIX_THEN_OPTIONS}
                value={fixThen}
                disabled={busy}
                onChange={(id) => setFixThen(workspaceId, id as FixThen)}
              />
              <p className="new-session__help">{FIX_THEN_NOTE[fixThen]}</p>
            </div>
          ) : null}

          {mode === 'rca' ? (
            <div className="new-session__mode-fields">
              <label className="new-session__field">
                <span className="new-session__field-label">Pull request URL</span>
                <input
                  type="url"
                  value={prUrl}
                  disabled={busy}
                  placeholder="https://github.com/acme/api/pull/42"
                  aria-invalid={prUrlProblem(prUrl) ? true : undefined}
                  onChange={(e) => setPrUrl(e.target.value)}
                />
              </label>
              {prUrlProblem(prUrl) ? (
                <p className="new-session__help" data-tone="error">
                  {prUrlProblem(prUrl)}
                </p>
              ) : null}
              <label className="new-session__field">
                <span className="new-session__field-label">What was done</span>
                <textarea
                  rows={3}
                  value={resolution}
                  disabled={busy}
                  placeholder="Redeployed the refund worker and reprocessed the stuck queue."
                  onChange={(e) => setResolution(e.target.value)}
                />
              </label>
              <p className="new-session__help">{RCA_EMPTY_HELP}</p>
            </div>
          ) : null}

          <details className="new-session__more">
            <summary>More options</summary>
            <div className="new-session__more-body">
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
                      onClick={() => void begin('triage', ticket.key, '')}
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
