import { useMemo, useState } from 'react'
import type { RunSummary, Ticket } from '../api/types'
import RunCard from '../components/cards/RunCard'
import TicketCard from '../components/cards/TicketCard'

/** One card in a lane: either an untouched ticket or a run. */
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
  { id: 'queue', name: 'Queue', empty: 'The tracker has nothing assigned and untouched.' },
  { id: 'gathering', name: 'Gathering', empty: 'No run is gathering evidence right now.' },
  { id: 'blocked', name: 'Needs input', empty: 'No run is waiting on an answer.' },
  { id: 'triaged', name: 'Triaged', empty: 'No triage note is waiting to be read.' },
  { id: 'done', name: 'Done', empty: 'No root cause has been recorded yet.' },
  { id: 'failed', name: 'Failed', empty: 'Nothing has failed.' },
]

function stamp(run: RunSummary): number {
  const updated = Date.parse(run.updatedAt ?? '')
  if (!Number.isNaN(updated)) return updated
  const started = Date.parse(run.startedAt ?? '')
  return Number.isNaN(started) ? 0 : started
}

/**
 * Splits the workspace's tickets and runs into the six lanes.
 *
 * A key moves right as work happens to it: a ticket nobody has run sits in
 * Queue; a run in flight is Gathering; a triage note with no root cause behind
 * it is Triaged; a completed RCA sends the key to Done. Runs are ordered by
 * their last change so the lane's top card is the one that just moved.
 */
export function buildColumns(tickets: Ticket[], runs: RunSummary[]): BoardColumn[] {
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

  for (const ticket of tickets) {
    if (keysWithRuns.has(ticket.key)) continue
    lanes.queue.push({ kind: 'ticket', key: ticket.key, title: ticket.title, ticket })
  }

  for (const run of [...runs].sort((a, b) => stamp(b) - stamp(a))) {
    const ticket = ticketByKey.get(run.key)
    const card: BoardCard = {
      kind: 'run',
      key: run.key,
      title: ticket?.title || run.key,
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

function matches(card: BoardCard, needle: string): boolean {
  if (!needle) return true
  const q = needle.toLowerCase()
  return card.key.toLowerCase().includes(q) || card.title.toLowerCase().includes(q)
}

export default function Board(props: {
  tickets: Ticket[]
  runs: RunSummary[]
  queueUnsupported: boolean
  loading: boolean
  filterRef?: React.RefObject<HTMLInputElement | null>
  onOpenRun: (runId: string) => void
  onTriage: (keys: string[]) => void
}): JSX.Element {
  const { tickets, runs, queueUnsupported, loading, filterRef, onOpenRun, onTriage } = props
  const [filter, setFilter] = useState('')

  const columns = useMemo(() => buildColumns(tickets, runs), [tickets, runs])
  const live = columns.find((c) => c.id === 'gathering')?.cards.length ?? 0

  return (
    <section className="board" aria-label="Board">
      <div className="board-bar">
        <label className="filter">
          <span className="visually-hidden">Filter cards</span>
          <input
            ref={filterRef}
            className="filter-input"
            type="search"
            value={filter}
            placeholder="Filter by key or title"
            onChange={(e) => setFilter(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Escape') {
                setFilter('')
                e.currentTarget.blur()
              }
            }}
          />
          <kbd className="kbd">/</kbd>
        </label>
        <span className="board-status">
          {loading
            ? 'Loading runs…'
            : live > 0
              ? `${live} ${live === 1 ? 'run' : 'runs'} in flight`
              : 'Nothing running'}
        </span>
      </div>

      <div className="lanes">
        {columns.map((column) => {
          const cards = column.cards.filter((card) => matches(card, filter))
          const emptyText =
            column.id === 'queue' && queueUnsupported
              ? 'This workspace has no tracker; start triage by key.'
              : filter
                ? `Nothing here matches “${filter}”.`
                : column.empty

          return (
            <section className="lane" key={column.id} data-lane={column.id}>
              <h2 className="lane-head">
                <span className="lane-name">{column.name}</span>
                <span className="lane-count">{cards.length}</span>
              </h2>
              <div className="lane-body">
                {cards.length === 0 ? (
                  <p className="lane-empty">{emptyText}</p>
                ) : (
                  cards.map((card) =>
                    card.kind === 'ticket' ? (
                      <TicketCard
                        key={`t:${card.key}`}
                        ticket={card.ticket}
                        onTriage={(key) => onTriage([key])}
                      />
                    ) : (
                      <RunCard
                        key={card.run.runId}
                        run={card.run}
                        title={card.title}
                        priority={card.priority}
                        onOpen={onOpenRun}
                      />
                    ),
                  )
                )}
              </div>
            </section>
          )
        })}
      </div>
    </section>
  )
}
