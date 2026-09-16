import { useEffect, useId, useMemo, useState, useSyncExternalStore } from 'react'
import type { HookOutcome, RunSummary, SourcesSummary, Ticket, Transport } from '../api/types'
import Age from '../components/Age'
import RunCard from '../components/cards/RunCard'
import { reasonOf, relativeTime } from '../lib/format'
import { FILTER_DEBOUNCE_MS, useDebounced } from '../lib/useDebounced'
import {
  sessionsShow,
  shownNumber,
  subscribeSessionsShow,
  type SessionsShow,
} from '../lib/sessionsShow'
import type { InboundDelivery } from '../store/appStore'
import Button from '../ui/button'
import GroupLabel from '../ui/group-label'
import ItemRow, { type ItemTone } from '../ui/item-row'
import KanbanColumn from '../ui/kanban-column'
import PageHead from '../ui/page-head'
import SdRunCard from '../ui/run-card'
import SearchBar from '../ui/search-bar'
import SegmentedControl from '../ui/segmented-control'
import './board.css'

/**
 * One card in a lane: either an untouched ticket or a run. `title` is the
 * ticket's, or empty when nothing names one; the card then shows the key
 * once, as its title.
 */
export type BoardCard =
  | { kind: 'ticket'; key: string; title: string; ticket: Ticket }
  | { kind: 'run'; key: string; title: string; run: RunSummary; priority: string }

export type ColumnId = 'queue' | 'gathering' | 'blocked' | 'triaged' | 'done' | 'failed'

export interface BoardColumn {
  id: ColumnId
  name: string
  /** Shown in place of cards. One sentence, and it says what to do next. */
  empty: string
  cards: BoardCard[]
}

const COLUMNS: { id: ColumnId; name: string; empty: string }[] = [
  { id: 'queue', name: 'Queue', empty: 'Nothing in the tracker is assigned to you and untouched.' },
  { id: 'gathering', name: 'Gathering', empty: 'No run is gathering evidence right now.' },
  { id: 'blocked', name: 'Blocked', empty: 'No run is waiting on an answer.' },
  { id: 'triaged', name: 'Triaged', empty: 'No triage note is waiting to be read.' },
  { id: 'done', name: 'Done', empty: 'No root cause has been recorded yet.' },
  { id: 'failed', name: 'Failed', empty: 'Nothing has failed.' },
]

const LIVE = new Set<RunSummary['status']>(['preparing', 'running'])

/** Who a card belongs to, and what it is. The two quick filters. */
type Owner = 'all' | 'mine'
type KindFilter = 'all' | 'triage' | 'rca' | 'fix'

const OWNER_OPTIONS = [
  { id: 'mine', label: 'Mine' },
  { id: 'all', label: 'All' },
]

const KIND_OPTIONS = [
  { id: 'all', label: 'All' },
  { id: 'triage', label: 'Triage' },
  { id: 'rca', label: 'RCA' },
  { id: 'fix', label: 'Fix' },
]

function stamp(run: RunSummary): number {
  const updated = Date.parse(run.updatedAt ?? '')
  if (!Number.isNaN(updated)) return updated
  const started = Date.parse(run.startedAt ?? '')
  return Number.isNaN(started) ? 0 : started
}

/**
 * Splits the workspace's queue and runs into the six lanes.
 *
 * A key moves right as work happens to it: a queued ticket nobody has run sits
 * in Queue; a run in flight is Gathering; a triage note with no root cause
 * behind it is Triaged; a completed RCA sends the key to Done. Runs are ordered
 * by their last change so the lane's top card is the one that just moved.
 *
 * `queued` is the Queue lane's whole source — the tracker's answer to "what is
 * assigned to me", so the lane is a list of work rather than a list of every
 * ticket in the project. `tickets` is only looked in, for the title and the
 * priority a run's own record may not carry; it defaults to the queue for a
 * caller that has nothing else.
 */
export function buildColumns(
  queued: Ticket[],
  runs: RunSummary[],
  tickets: Ticket[] = queued,
): BoardColumn[] {
  const ticketByKey = new Map(tickets.map((t) => [t.key, t]))
  const keysWithRuns = new Set(runs.map((r) => r.key))
  const rcaDone = new Set(
    runs.filter((r) => r.kind === 'rca' && r.status === 'completed').map((r) => r.key),
  )

  const lanes: Record<ColumnId, BoardCard[]> = {
    queue: [],
    gathering: [],
    blocked: [],
    triaged: [],
    done: [],
    failed: [],
  }

  for (const ticket of queued) {
    if (keysWithRuns.has(ticket.key)) continue
    lanes.queue.push({ kind: 'ticket', key: ticket.key, title: ticket.title, ticket })
  }

  for (const run of [...runs].sort((a, b) => stamp(b) - stamp(a))) {
    const ticket = ticketByKey.get(run.key)
    // The run's own title is what its bundle recorded; the tracker's is the
    // fallback for a run that predates the field.
    const card: BoardCard = {
      kind: 'run',
      key: run.key,
      title: run.title || ticket?.title || '',
      run,
      priority: ticket?.priority ?? '',
    }
    switch (run.status) {
      case 'preparing':
      case 'running':
        lanes.gathering.push(card)
        break
      case 'blocked':
        lanes.blocked.push(card)
        break
      case 'failed':
      case 'over_budget':
        lanes.failed.push(card)
        break
      case 'completed':
        if (run.kind === 'rca') lanes.done.push(card)
        else if (!rcaDone.has(run.key)) lanes.triaged.push(card)
        break
    }
  }

  return COLUMNS.map((c) => ({ ...c, cards: lanes[c.id] }))
}

/** The search well: a key, a title, or a provider. */
function matches(card: BoardCard, needle: string): boolean {
  if (!needle) return true
  const q = needle.toLowerCase()
  if (card.key.toLowerCase().includes(q) || card.title.toLowerCase().includes(q)) return true
  return card.kind === 'run' && card.run.provider.toLowerCase().includes(q)
}

/** A ticket nobody has run is what a triage would be, so it counts as one. */
function ofKind(card: BoardCard, kind: KindFilter): boolean {
  if (kind === 'all') return true
  return card.kind === 'ticket' ? kind === 'triage' : card.run.kind === kind
}

/**
 * The status line's clock: seconds while it is under a minute — the line
 * says "updated 12s ago" because the board is about now — and the shared
 * relative time past that. Empty when nothing has a stamp.
 */
export function updatedAgo(latest: number, now: number): string {
  if (!Number.isFinite(latest) || latest <= 0) return ''
  const seconds = Math.max(0, Math.round((now - latest) / 1000))
  if (seconds < 60) return `${seconds}s ago`
  return relativeTime(new Date(latest).toISOString(), now)
}

/** What each delivery outcome came of, in the row's meta line. */
const REASON: Record<HookOutcome, string> = {
  started: '',
  skipped: 'that key is busy or in its cooldown',
  filtered: "did not match this workspace's filter",
  ignored: 'named no ticket',
  rejected: 'the delivery did not verify',
}

const TONE: Record<HookOutcome, ItemTone> = {
  started: 'live',
  skipped: 'blocked',
  filtered: 'plain',
  ignored: 'plain',
  rejected: 'failed',
}

/* Inline stroke icons on the 24 grid, as the mock draws them. */

function FiltersIcon(): JSX.Element {
  return (
    <svg
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.5"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
      focusable="false"
    >
      <path d="M4 6h10M18 6h2M4 12h4M12 12h8M4 18h12M20 18h0" />
      <circle cx="16" cy="6" r="2" />
      <circle cx="10" cy="12" r="2" />
      <circle cx="18" cy="18" r="2" />
    </svg>
  )
}

function ChevronIcon({ open }: { open: boolean }): JSX.Element {
  return (
    <svg
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.5"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
      focusable="false"
      data-open={open ? 'true' : undefined}
    >
      <path d="m6 9 6 6 6-6" />
    </svg>
  )
}

/** lucide `headset`: a delivery that started a run. */
function HeadsetIcon(): JSX.Element {
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
    >
      <path d="M4 14v-3a8 8 0 0 1 16 0v3" />
      <rect x="3" y="13" width="4" height="6" rx="1.5" />
      <rect x="17" y="13" width="4" height="6" rx="1.5" />
      <path d="M19 19a3 3 0 0 1-3 3h-3" />
    </svg>
  )
}

/** lucide `ticket`: a delivery that went no further than the ticket it named. */
function TicketIcon(): JSX.Element {
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
    >
      <path d="M3 9V7a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2v2a2 2 0 0 0 0 6v2a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-2a2 2 0 0 0 0-6z" />
      <path d="M13 5v14" />
    </svg>
  )
}

export interface BoardProps {
  transport: Transport
  workspaceId: string
  /**
   * The workspace's provider: the mark a queued ticket wears, since that is
   * where a triage of it would run.
   */
  provider?: string
  /**
   * The workspace's tickets as the store holds them, looked in for the title
   * and priority a run's own record may not carry. The Queue lane is not built
   * from these: the screen asks the tracker for the reader's own keys itself.
   */
  tickets: Ticket[]
  runs: RunSummary[]
  /** The workspace's tracker and helpdesk, named in each card's number tooltip. */
  sources?: SourcesSummary
  queueUnsupported: boolean
  loading: boolean
  inbound?: InboundDelivery[]
  filterRef?: React.RefObject<HTMLInputElement | null>
  onOpenRun: (runId: string) => void
  onTriage: (keys: string[]) => void
}

/**
 * The board, at `#/`: every run as a card in the lane its state puts it in,
 * and under the lanes the deliveries the webhooks brought today.
 *
 * The lanes read the store's runs; the one thing the screen asks the transport
 * for itself is the Queue lane, which is the tracker's answer to "what is
 * assigned to me" rather than every ticket in the project. Everything past
 * Queue is a run, and a run already says whose ticket it is, so the Mine quick
 * filter costs nothing.
 */
export default function Board(props: BoardProps): JSX.Element {
  const {
    transport,
    workspaceId,
    provider = '',
    tickets,
    runs,
    sources,
    queueUnsupported,
    loading,
    inbound,
    filterRef,
    onOpenRun,
    onTriage,
  } = props
  const [filter, setFilter] = useState('')
  // The field shows every keystroke; the lanes narrow once the typist pauses.
  const applied = useDebounced(filter, FILTER_DEBOUNCE_MS)
  const [filtersOpen, setFiltersOpen] = useState(false)
  const [owner, setOwner] = useState<Owner>('all')
  const [kind, setKind] = useState<KindFilter>('all')
  const filtersId = useId()
  const show = useSyncExternalStore(
    subscribeSessionsShow,
    sessionsShow,
    () => 'tracker' as SessionsShow,
  )
  /** A queued ticket's number on the same preference the run cards follow. */
  const queuedNumber = (ticket: Ticket) =>
    shownNumber({ key: ticket.key, helpdeskKey: ticket.helpdeskRef }, show, sources)

  // The Queue lane: the keys the tracker lists for the reader's own account,
  // asked for once per workspace. Null until it answers, which is what tells
  // "still loading" from "nothing is assigned to you".
  const [queued, setQueued] = useState<Ticket[] | null>(null)
  const [queueError, setQueueError] = useState('')
  useEffect(() => {
    if (queueUnsupported) {
      setQueued([])
      setQueueError('')
      return
    }
    let cancelled = false
    setQueued(null)
    setQueueError('')
    transport
      .queue(workspaceId, { assignee: 'me' })
      .then((rows) => {
        if (!cancelled) setQueued(rows)
      })
      .catch((err) => {
        if (!cancelled) setQueueError(reasonOf(err))
      })
    return () => {
      cancelled = true
    }
  }, [queueUnsupported, transport, workspaceId])

  const columns = useMemo(() => buildColumns(queued ?? [], runs, tickets), [queued, runs, tickets])
  const live = useMemo(() => runs.filter((r) => LIVE.has(r.status)).length, [runs])
  const latest = useMemo(() => runs.reduce((max, r) => Math.max(max, stamp(r)), 0), [runs])
  const titles = useMemo(() => new Map(tickets.map((t) => [t.key, t.title])), [tickets])

  // The status line and the landed rows say how long ago. Each is an `Age`
  // that ticks for itself — every second while the newest change is under a
  // minute old, since the line then reads in seconds, and every fifteen once
  // it reads in minutes — so the tick re-draws a few words and not the board.
  const agePeriod = Date.now() - latest < 60_000 ? 1000 : 15_000

  // Mine costs no call: a run carries whether its ticket is the reader's, and
  // every ticket in the Queue lane was asked for by that name. A service that
  // predates the field says nothing about ownership for any run, and then the
  // filter narrows nothing rather than emptying the board.
  const mineKeys = useMemo(() => new Set((queued ?? []).map((t) => t.key)), [queued])
  const ownershipKnown = useMemo(() => runs.some((r) => r.mine !== undefined), [runs])
  const isMine = (card: BoardCard): boolean =>
    card.kind === 'run' ? !ownershipKnown || card.run.mine === true : mineKeys.has(card.key)

  const keep = (card: BoardCard): boolean =>
    matches(card, applied) && ofKind(card, kind) && (owner !== 'mine' || isMine(card))

  const deliveries = inbound ?? []
  const filtering = applied !== '' || kind !== 'all' || owner === 'mine'
  const shownRuns = columns.reduce(
    (total, column) => total + column.cards.filter((c) => c.kind === 'run' && keep(c)).length,
    0,
  )

  return (
    <section className="board" aria-label="Board">
      <PageHead
        title="Board"
        actions={
          <Button
            variant="pale"
            icon={<FiltersIcon />}
            aria-expanded={filtersOpen}
            aria-controls={filtersId}
            onClick={() => setFiltersOpen((open) => !open)}
          >
            Filters
          </Button>
        }
      />

      <div className="board-row">
        <SearchBar
          variant="well"
          label="Filter cards"
          placeholder="Filter by key, title or provider"
          value={filter}
          onChange={setFilter}
          inputRef={filterRef}
          aside={<kbd className="kbd">/</kbd>}
          onKeyDown={(e) => {
            if (e.key === 'Escape') {
              setFilter('')
              e.currentTarget.blur()
            }
          }}
        />
        <button
          type="button"
          className="board-quick"
          aria-expanded={filtersOpen}
          aria-controls={filtersId}
          onClick={() => setFiltersOpen((open) => !open)}
        >
          Quick filters
          <ChevronIcon open={filtersOpen} />
        </button>
        <p className="board-status">
          {loading ? (
            'Loading runs…'
          ) : (
            <>
              {filtering ? (
                <>
                  <b>{shownRuns}</b> of <b>{runs.length}</b>{' '}
                  {runs.length === 1 ? 'run' : 'runs'}
                </>
              ) : (
                <>
                  <b>{runs.length}</b> {runs.length === 1 ? 'run' : 'runs'}
                </>
              )}{' '}
              · <b>{live}</b> live
              {latest > 0 && (
                <>
                  {' · updated '}
                  <Age period={agePeriod} format={(now) => updatedAgo(latest, now)} />
                </>
              )}
            </>
          )}
        </p>
      </div>

      {filtersOpen && (
        <div id={filtersId} className="board-filters">
          <SegmentedControl
            label="Show"
            options={OWNER_OPTIONS}
            value={owner}
            onChange={(id) => setOwner(id as Owner)}
          />
          <SegmentedControl
            label="Kind"
            options={KIND_OPTIONS}
            value={kind}
            onChange={(id) => setKind(id as KindFilter)}
          />
          {!ownershipKnown && runs.length > 0 ? (
            <p className="board-filters__note">
              This workspace's account names nobody, so no run can be called yours and every run is
              shown.
            </p>
          ) : null}
        </div>
      )}

      <div className="board-lanes">
        {columns.map((column) => {
          const cards = column.cards.filter(keep)
          const queue = column.id === 'queue'
          const emptyText =
            queue && queueUnsupported
              ? 'This workspace has no tracker; start triage by key.'
              : queue && queueError
                ? `Could not load your queue. ${queueError}`
                : queue && queued === null
                  ? 'Reading the tickets assigned to you…'
                  : filtering
                    ? applied
                      ? `Nothing here matches “${applied}”.`
                      : 'Nothing here matches the filters.'
                    : column.empty

          return (
            <KanbanColumn
              key={column.id}
              lane={column.id}
              title={column.name}
              count={cards.length}
              note={queue && !queueUnsupported ? 'assigned to you' : undefined}
              empty={emptyText}
            >
              {cards.length === 0
                ? null
                : cards.map((card) =>
                    card.kind === 'ticket' ? (
                      // A queued ticket has no session to open, so its card
                      // goes to the ticket in the tracker, and the one click
                      // that spends the provider is a button of its own.
                      <div key={`t:${card.key}`} className="board-ticket">
                        <SdRunCard
                          runKey={queuedNumber(card.ticket).text}
                          keyTitle={queuedNumber(card.ticket).other || undefined}
                          kind="triage"
                          status="queued"
                          title={card.title || undefined}
                          provider={provider}
                          assignee={card.ticket.assignee || undefined}
                          href={card.ticket.url || undefined}
                        />
                        <Button
                          variant="pale"
                          size="sm"
                          aria-label={`Triage ${card.key}`}
                          title="Start a triage of this ticket"
                          onClick={() => onTriage([card.key])}
                        >
                          Triage
                        </Button>
                      </div>
                    ) : (
                      <RunCard
                        key={card.run.runId}
                        run={card.run}
                        sources={sources}
                        title={card.title || undefined}
                        done={column.id === 'done'}
                        onOpen={onOpenRun}
                      />
                    ),
                  )}
            </KanbanColumn>
          )
        })}
      </div>

      {/* Deliveries, not "landed": New session's "Landed today" is the
          tracker's queue, and this is what the webhooks brought. */}
      <section className="board-landed" aria-labelledby="board-landed-label">
        <GroupLabel as="h2" id="board-landed-label">
          Deliveries today
        </GroupLabel>
        {deliveries.length === 0 ? (
          <p className="board-landed__empty">
            No webhook delivery has arrived in this session. Hooks are off unless the workspace
            enables them.
          </p>
        ) : (
          <div className="board-landed__items">
            {deliveries.map((d) => {
              const title = titles.get(d.key) || d.key || d.source
              const run = d.key ? runs.find((r) => r.key === d.key) : undefined
              const reason = REASON[d.outcome]
              return (
                <ItemRow
                  key={d.id}
                  tone={TONE[d.outcome]}
                  icon={d.outcome === 'started' ? <HeadsetIcon /> : <TicketIcon />}
                  title={title}
                  dir="auto"
                  wrap
                  onOpen={run ? () => onOpenRun(run.runId) : undefined}
                  openLabel={run ? `Open ${d.key}: ${title}` : undefined}
                  meta={
                    <>
                      {d.key && (
                        <>
                          <span className="board-landed__key" dir="ltr">
                            {d.key}
                          </span>
                          {' · '}
                        </>
                      )}
                      {d.source}
                      {' · '}
                      <span className="board-landed__outcome" data-outcome={d.outcome}>
                        {d.outcome}
                      </span>
                      {reason && ` · ${reason}`}
                      {' · '}
                      <Age at={d.at} title={d.at} period={15_000} format={(now) => relativeTime(d.at, now)} />
                    </>
                  }
                />
              )
            })}
          </div>
        )}
      </section>
    </section>
  )
}
