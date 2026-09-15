import { useState } from 'react'
import Badge from '../ui/badge'
import Banner from '../ui/banner'
import Button from '../ui/button'
import Card from '../ui/card'
import DataTable, { type DataColumn } from '../ui/data-table'
import Dialog from '../ui/dialog'
import EventRow, { EVENT_GLYPHS, type EventVariant } from '../ui/event-row'
import GroupLabel from '../ui/group-label'
import Heatmap from '../ui/heatmap'
import HeroBand from '../ui/hero-band'
import ItemRow from '../ui/item-row'
import KanbanColumn, { type LaneId } from '../ui/kanban-column'
import KindChip from '../ui/kind-chip'
import ModalSheet from '../ui/modal-sheet'
import ModelPicker, { type ModelChoicePair } from '../ui/model-picker'
import { Marquee, MarqueeItem, RingText } from '../ui/ambient'
import NotePane from '../ui/note-pane'
import PageHead from '../ui/page-head'
import PillNav from '../ui/pill-nav'
import ProviderMark from '../ui/provider-mark'
import QuotaChip from '../ui/quota-chip'
import RunCard from '../ui/run-card'
import SearchBar from '../ui/search-bar'
import SegmentedControl from '../ui/segmented-control'
import SettingRow, { SettingCard } from '../ui/setting-row'
import SidebarFooterCard from '../ui/sidebar-footer-card'
import SidebarNavItem from '../ui/sidebar-nav-item'
import StatCard from '../ui/stat-card'
import StateGlyph, { type GlyphState } from '../ui/state-glyph'
import StatusBadge, { PriorityBadge, STATE_WORDS, type SdStatus } from '../ui/status-badge'
import Toasts from '../ui/toast'
import Toggle from '../ui/toggle'
import './library.css'

/**
 * The asset library, at `#/library`.
 *
 * Every component in `src/ui/`, in every state it has, with a light and dark
 * switch and a direction switch that both apply to the specimens rather than
 * to the page around them — so the two themes and the two directions can be
 * compared without reloading and without changing what the rest of the window
 * looks like.
 *
 * Every component that carries text carries a real Arabic string here, taken
 * from the kind of ticket Sirdar is for. Arabic is not a localisation exercise
 * in this product: the customer's complaint and the reply draft are usually
 * Arabic, so a component that has never been seen with Arabic in it has not
 * been seen.
 */

const ARABIC_TITLE = 'العميل لا يستطيع تصدير كشف الحساب منذ التحديث الأخير'
const ARABIC_REASON = 'طلب الوكيل تحديد رقم الحساب قبل المتابعة'
const ARABIC_BODY =
  'يشكو العميل من أن تصدير كشف الحساب يتوقف بعد دقيقتين دون رسالة خطأ واضحة. تكرر ذلك ثلاث مرات أمس.'

const EVERY_STATUS: SdStatus[] = [
  'queued',
  'preparing',
  'running',
  'blocked',
  'completed',
  'failed',
  'over_budget',
]

const LANES: LaneId[] = ['queue', 'gathering', 'blocked', 'triaged', 'done', 'failed']

/** The providers a session can name, in the order the Providers page lists them. */
const PROVIDERS_SHOWN = [
  'claude',
  'codex',
  'openai',
  'copilot',
  'agy',
  'gemini',
  'qwen',
  'cursor',
  'opencode',
  'kimi',
  'acp',
]

/** lucide `inbox` for the item-row specimens. */
function InboxIcon(): JSX.Element {
  return (
    <svg
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.6"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
    >
      <path d="M3 9V7a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2v2a2 2 0 0 0 0 6v2a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-2a2 2 0 0 0 0-6z" />
      <path d="M13 5v14" />
    </svg>
  )
}

interface TableRow {
  key: string
  ticket: string
  verdict: string
  cost: string
}

const TABLE_ROWS: TableRow[] = [
  { key: 'OMNI-2510', ticket: 'Statement export times out', verdict: 'agreed', cost: '$0.42' },
  { key: 'OMNI-2511', ticket: ARABIC_TITLE, verdict: 'needs work', cost: '$1.08' },
  { key: 'OMNI-2514', ticket: 'Webhook delivery is filtered out', verdict: 'agreed', cost: '$0.19' },
]

const TABLE_COLUMNS: DataColumn<TableRow>[] = [
  { id: 'key', header: 'Key', cell: (row) => row.key, sortable: true },
  { id: 'ticket', header: 'Ticket', cell: (row) => <span dir="auto">{row.ticket}</span> },
  { id: 'verdict', header: 'Verdict', cell: (row) => row.verdict },
  { id: 'cost', header: 'Cost', cell: (row) => row.cost, numeric: true, sortable: true },
]

/** One component's section: a heading, a sentence, and its specimens. */
function Section({
  id,
  name,
  note,
  children,
}: {
  id: string
  name: string
  note: string
  children: React.ReactNode
}): JSX.Element {
  return (
    <section className="lib-section" id={`lib-${id}`} aria-labelledby={`lib-h-${id}`}>
      <div className="lib-section__head">
        <h2 className="lib-section__name" id={`lib-h-${id}`}>
          {name}
        </h2>
        <p className="lib-section__note">{note}</p>
      </div>
      <div className="lib-section__body">{children}</div>
    </section>
  )
}

/** A labelled specimen. The label says which state is on show. */
function State({ label, children }: { label: string; children: React.ReactNode }): JSX.Element {
  return (
    <div className="lib-state">
      <span className="lib-state__label">{label}</span>
      <div className="lib-state__stage">{children}</div>
    </div>
  )
}

export default function Library(): JSX.Element {
  const [theme, setTheme] = useState<'light' | 'dark'>('light')
  const [dir, setDir] = useState<'ltr' | 'rtl'>('ltr')
  const [segment, setSegment] = useState('all')
  const [dialogOpen, setDialogOpen] = useState(false)
  const [modalOpen, setModalOpen] = useState(false)
  const [modalPage, setModalPage] = useState('general')
  const [search, setSearch] = useState('')
  const [well, setWell] = useState('')
  const [toggled, setToggled] = useState(true)
  const [choice, setChoice] = useState<ModelChoicePair>({ provider: '', model: '' })
  const [sort, setSort] = useState<{ columnId: string; direction: 'asc' | 'desc' }>({
    columnId: 'key',
    direction: 'asc',
  })
  return (
    <div className="lib">
      {/*
       * The bar is outside the frame on purpose: the switches paint the
       * specimens, not the page around them. It is read-only chrome — the
       * primary buttons below are specimens, so nothing here publishes a
       * primary action to the sidebar.
       */}
      <header className="lib__bar">
        <PageHead
          title="Library"
          lede="Every component the app ships, at the size it ships at."
          actions={
            <>
              <SegmentedControl
                label="Theme"
                options={[
                  { id: 'light', label: 'Light' },
                  { id: 'dark', label: 'Dark' },
                ]}
                value={theme}
                onChange={(id) => setTheme(id as 'light' | 'dark')}
              />
              <SegmentedControl
                label="Direction"
                options={[
                  { id: 'ltr', label: 'LTR' },
                  { id: 'rtl', label: 'RTL' },
                ]}
                value={dir}
                onChange={(id) => setDir(id as 'ltr' | 'rtl')}
              />
            </>
          }
        />
      </header>

      <div className="lib__frame" data-theme={theme} dir={dir} data-testid="library-frame">
        <Section
          id="button"
          name="Button"
          note="One filled button per screen, and it is that screen's commit action."
        >
          <div className="lib-row">
            <State label="Primary">
              <Button variant="primary">Start triage</Button>
            </State>
            <State label="Primary, pressed and busy">
              <Button variant="primary" busy>
                Starting
              </Button>
            </State>
            <State label="Secondary">
              <Button>Open note</Button>
            </State>
            <State label="Ghost">
              <Button variant="ghost">Dismiss</Button>
            </State>
            <State label="Disabled">
              <Button variant="primary" disabled>
                Apply fix
              </Button>
            </State>
            <State label="With a shortcut">
              <Button variant="primary" shortcut="n">
                New triage
              </Button>
            </State>
            <State label="Arabic">
              <Button variant="primary">ابدأ الفرز</Button>
            </State>
          </div>
        </Section>

        <Section id="pill-nav" name="Pill nav" note="The current item is a fill, never a border.">
          <div className="lib-col">
            <State label="In the app header">
              <PillNav
                label="Screens"
                current="board"
                items={[
                  { id: 'board', label: 'Board' },
                  { id: 'register', label: 'Register', count: 24 },
                  { id: 'eval', label: 'Eval' },
                  { id: 'settings', label: 'Settings' },
                ]}
              />
            </State>
            <State label="Floating, over a band">
              <div className="lib-band-stage">
                <PillNav
                  floating
                  label="Site"
                  current="docs"
                  items={[
                    { id: 'home', label: 'Home', href: '#/library' },
                    { id: 'docs', label: 'Docs', href: '#/library' },
                  ]}
                />
              </div>
            </State>
            <State label="Arabic">
              <PillNav
                label="الشاشات"
                current="board"
                items={[
                  { id: 'board', label: 'اللوحة' },
                  { id: 'register', label: 'السجل', count: 24 },
                  { id: 'settings', label: 'الإعدادات' },
                ]}
              />
            </State>
          </div>
        </Section>

        <Section
          id="segmented-control"
          name="Segmented control"
          note="One track, one thumb; the arrows read in the reader's own direction."
        >
          <div className="lib-row">
            <State label="Three options">
              <SegmentedControl
                label="Filter"
                options={[
                  { id: 'all', label: 'All' },
                  { id: 'mine', label: 'Mine' },
                  { id: 'blocked', label: 'Blocked' },
                ]}
                value={segment}
                onChange={setSegment}
              />
            </State>
            <State label="Four options, the most it takes">
              <SegmentedControl
                label="Kind"
                options={[
                  { id: 'triage', label: 'Triage' },
                  { id: 'rca', label: 'RCA' },
                  { id: 'fix', label: 'Fix' },
                  { id: 'eval', label: 'Eval' },
                ]}
                value="fix"
                onChange={() => {}}
              />
            </State>
            <State label="Disabled">
              <SegmentedControl
                label="Provider"
                disabled
                options={[
                  { id: 'claude', label: 'Claude' },
                  { id: 'codex', label: 'Codex' },
                ]}
                value="claude"
                onChange={() => {}}
              />
            </State>
            <State label="Arabic">
              <SegmentedControl
                label="التصفية"
                options={[
                  { id: 'all', label: 'الكل' },
                  { id: 'mine', label: 'المسندة إليّ' },
                ]}
                value="mine"
                onChange={() => {}}
              />
            </State>
          </div>
        </Section>

        <Section id="card" name="Card" note="The base box, with no shadow ever.">
          <div className="lib-row">
            <State label="Plain">
              <Card title="Doctor" meta="3 checks">
                Two checks passed. The tracker credential is missing.
              </Card>
            </State>
            <State label="Interactive">
              <Card title="OMNI-2514" meta="webhook" openLabel="Open OMNI-2514" onOpen={() => {}}>
                Delivery filtered out by the queue rule.
              </Card>
            </State>
            <State label="Live edge">
              <Card title="OMNI-2510" meta="triage" tone="live">
                Gathering evidence from the helpdesk thread.
              </Card>
            </State>
            <State label="Failed edge">
              <Card title="OMNI-2512" meta="fix" tone="failed">
                The provider CLI exited with status 1.
              </Card>
            </State>
            <State label="Arabic">
              <Card title={ARABIC_TITLE} meta="OMNI-2511" dir="auto" tone="blocked">
                {ARABIC_REASON}
              </Card>
            </State>
          </div>
        </Section>

        <Section
          id="status-badge"
          name="Status badge"
          note="Every hue is paired with its word; the accent means a live run."
        >
          <div className="lib-row lib-row--tight">
            {EVERY_STATUS.map((status) => (
              <State key={status} label={STATE_WORDS[status]}>
                <StatusBadge status={status} />
              </State>
            ))}
            <State label="Priority, verbatim">
              <PriorityBadge priority="P1" />
            </State>
            <State label="Priority, low">
              <PriorityBadge priority="Minor" />
            </State>
            <State label="Arabic">
              <StatusBadge status="blocked">بانتظار ردّك</StatusBadge>
            </State>
          </div>
        </Section>

        <Section
          id="kanban-column"
          name="Kanban column"
          note="The rail is the board's legend; an empty column says what that means."
        >
          <div className="lib-lanes">
            {LANES.map((lane) => (
              <KanbanColumn
                key={lane}
                lane={lane}
                title={lane}
                count={lane === 'gathering' ? 1 : 0}
                empty={
                  lane === 'queue'
                    ? 'No ticket is waiting. New ones arrive from the tracker.'
                    : 'Nothing here yet.'
                }
              >
                {lane === 'gathering' ? (
                  <RunCard
                    runKey="OMNI-2510"
                    kind="triage"
                    status="running"
                    title="Statement export times out"
                    provider="claude"
                    clock="4:12"
                    onOpen={() => {}}
                  />
                ) : null}
              </KanbanColumn>
            ))}
          </div>
        </Section>

        <Section
          id="run-card"
          name="Run card"
          note="Title up to two lines, the kind chip, then a footer with the state glyph and its word, a mono clock only while running or blocked, and the mono key with the provider's mark at the other end."
        >
          <div className="lib-row">
            <State label="Queued">
              <RunCard
                runKey="SBX-8"
                kind="triage"
                status="queued"
                title="Supplier price import rounds to the nearest riyal"
                provider="cursor"
                onOpen={() => {}}
              />
            </State>
            <State label="Preparing, with the live edge">
              <RunCard
                runKey="SBX-9"
                kind="rca"
                status="preparing"
                title="Refund on a split payment posts to the first card only"
                provider="openai"
                clock="0:08"
                clockTitle="Started 8 seconds ago"
                onOpen={() => {}}
              />
            </State>
            <State label="Running, with the clock">
              <RunCard
                runKey="SBX-4"
                kind="triage"
                status="running"
                title="Reorder reminder fires twice for the same product"
                provider="claude"
                clock="1:47"
                onOpen={() => {}}
              />
            </State>
            <State label="Blocked, with the clock">
              <RunCard
                runKey="SBX-1"
                kind="fix"
                status="blocked"
                title="Product 00219 stock shows 1 more than the movement report"
                provider="claude"
                clock="4:12"
                onOpen={() => {}}
              />
            </State>
            <State label="Completed RCA">
              <RunCard
                runKey="SBX-3"
                kind="rca"
                status="completed"
                title="Stock count export skips products with a zero price"
                provider="copilot"
                onOpen={() => {}}
              />
            </State>
            <State label="Done">
              <RunCard
                runKey="SBX-5"
                kind="fix"
                status="done"
                title="Credit note lands on the wrong customer account"
                provider="claude"
                onOpen={() => {}}
              />
            </State>
            <State label="Failed">
              <RunCard
                runKey="SBX-6"
                kind="triage"
                status="failed"
                title="Barcode lookup returns the discontinued variant"
                provider="qwen"
                onOpen={() => {}}
              />
            </State>
            <State label="Over budget, no clock however long it ran">
              <RunCard
                runKey="SBX-10"
                kind="fix"
                status="over_budget"
                title="Nightly reconciliation double-counts voided receipts"
                provider="codex"
                clock="58:12"
                onOpen={() => {}}
              />
            </State>
            <State label="No title from the tracker">
              <RunCard runKey="OMNI-2513" kind="eval" status="queued" provider="acp" onOpen={() => {}} />
            </State>
            <State label="Arabic">
              <RunCard
                runKey="OMNI-2511"
                kind="triage"
                status="blocked"
                title={ARABIC_TITLE}
                provider="codex"
                clock="2:04"
                onOpen={() => {}}
              />
            </State>
          </div>
        </Section>

        <Section
          id="event-row"
          name="Event row"
          note="Clock, verdict mark, then the tool and its one-line summary, with colour on the rail only."
        >
          <div className="lib-stream">
            {(Object.keys(EVENT_GLYPHS) as EventVariant[]).map((variant, i) => (
              <EventRow key={variant} at={`${i}m 0${i}s`} variant={variant}>
                {variant === 'tool'
                  ? 'Read(/srv/api/statements/export.go)'
                  : variant === 'deny'
                    ? 'Denied: Bash(rm -rf /)'
                    : variant === 'usage'
                      ? '41,204 tokens · $0.42'
                      : variant === 'error'
                        ? 'provider exited with status 1'
                        : variant === 'final'
                          ? 'Wrote notes/OMNI-2510-triage.md'
                          : `a ${variant} event`}
              </EventRow>
            ))}
            <EventRow at="9m 40s" variant="text">
              {ARABIC_BODY}
            </EventRow>
          </div>
        </Section>

        <Section
          id="note-pane"
          name="Note pane"
          note="The app's one serif moment, and the only place the marker sweep runs."
        >
          <div className="lib-row">
            <State label="Bilingual, direction per block">
              <NotePane dir="auto" title="OMNI-2510 triage" source="notes/OMNI-2510-triage.md">
                <h2>Root cause</h2>
                <p>
                  The export job holds one database connection per page and the pool runs dry at
                  the fourth page. See <a href="#/library">the register entry</a>.
                </p>
                <p>{ARABIC_BODY}</p>
                <blockquote>{ARABIC_REASON}</blockquote>
                <pre>
                  <code>SELECT * FROM statements WHERE account_id = $1</code>
                </pre>
              </NotePane>
            </State>
            <State label="Laid out right to left">
              <NotePane dir="rtl" title="فرز التذكرة" source="notes/OMNI-2511-triage.md">
                <h2>السبب الجذري</h2>
                <p>{ARABIC_BODY}</p>
                <p>
                  The reply draft stays in English where the engineer wrote it:{' '}
                  <a href="#/library">open the thread</a>.
                </p>
              </NotePane>
            </State>
          </div>
        </Section>

        <Section
          id="data-table"
          name="Data table"
          note="Tabular figures, a sticky header, and its own sideways scroll."
        >
          <div className="lib-col">
            <State label="Sorted by key">
              <DataTable
                caption="Register"
                columns={TABLE_COLUMNS}
                rows={TABLE_ROWS}
                rowKey={(row) => row.key}
                sort={sort}
                onSort={(columnId) =>
                  setSort((now) => ({
                    columnId,
                    direction:
                      now.columnId === columnId && now.direction === 'asc' ? 'desc' : 'asc',
                  }))
                }
                empty="No run has been recorded in this workspace yet."
              />
            </State>
            <State label="Empty">
              <DataTable
                caption="Register"
                columns={TABLE_COLUMNS}
                rows={[]}
                rowKey={(row) => row.key}
                empty="No run has been recorded in this workspace yet. Start one from the board."
              />
            </State>
          </div>
        </Section>

        <Section id="toast" name="Toast" note="Polite, dismissible, and out of the board's way.">
          <div className="lib-row lib-toast-stage">
            <State label="Info and error">
              <Toasts
                dismissAfterMs={2_147_483_000}
                onDismiss={() => {}}
                toasts={[
                  { id: 1, tone: 'info', text: 'Triage started for OMNI-2510.' },
                  { id: 2, tone: 'error', text: 'Add a workspace before starting a run.' },
                  { id: 3, tone: 'error', text: 'تعذّر بدء التشغيل: لا توجد مساحة عمل.' },
                ]}
              />
            </State>
          </div>
        </Section>

        <Section id="dialog" name="Dialog" note="Focus goes in, stays in, and comes back out.">
          <div className="lib-row">
            <State label="Closed, with its opener">
              <Button variant="primary" onClick={() => setDialogOpen(true)}>
                Open the dialog
              </Button>
            </State>
          </div>
          <Dialog
            open={dialogOpen}
            title="Start a triage"
            onClose={() => setDialogOpen(false)}
            actions={
              <>
                <Button onClick={() => setDialogOpen(false)}>Cancel</Button>
                <Button variant="primary" onClick={() => setDialogOpen(false)}>
                  Start triage
                </Button>
              </>
            }
          >
            <p>One ticket key per line. Each one gets its own run.</p>
            <p dir="auto">{ARABIC_REASON}</p>
          </Dialog>
        </Section>

        <Section
          id="quota-chip"
          name="Quota chip"
          note="Over budget says so in words, not only in red."
        >
          <div className="lib-row lib-row--tight">
            <State label="Fine">
              <QuotaChip provider="claude" window="5h" percent={42} resetsIn="2h 14m" />
            </State>
            <State label="Getting close">
              <QuotaChip provider="claude" window="7d" percent={88} resetsIn="3d 4h" />
            </State>
            <State label="Over budget">
              <QuotaChip provider="codex" window="used" percent={100} />
            </State>
          </div>
        </Section>

        <Section
          id="hero-band"
          name="Hero band"
          note="One roman clause, one italic clause, and one action."
        >
          <div className="lib-col">
            <State label="Deep band, with ring text">
              <HeroBand
                headline="A ticket arrives."
                headlineTail="You read the note, not the logs."
                deck="Sirdar runs the agent you already pay for, inside your own repository, and writes the root cause down."
                action={<Button variant="primary">Read the quick start</Button>}
                aside={<RingText text="evidence first · read-only by default · " />}
              />
            </State>
            <State label="Ink band, Arabic">
              <HeroBand
                tone="ink"
                headline="تصل التذكرة."
                headlineTail="تقرأ الملاحظة، لا السجلات."
                deck="يشغّل سِردار الوكيل الذي تدفع مقابله أصلًا داخل مستودعك، ثم يكتب السبب الجذري."
                action={<Button variant="primary">ابدأ من هنا</Button>}
              />
            </State>
          </div>
        </Section>

        <Section
          id="ambient"
          name="Ring text and marquee"
          note="Both decorative, both reversed under RTL, both stoppable."
        >
          <div className="lib-row">
            <State label="Ring, 48s">
              <RingText text="triage · evidence · note · fix · " />
            </State>
            <State label="Marquee, 34s">
              <Marquee label="What Sirdar reads">
                <MarqueeItem>Claude Code</MarqueeItem>
                <MarqueeItem>Codex</MarqueeItem>
                <MarqueeItem>Zoho Desk</MarqueeItem>
                <MarqueeItem>Jira</MarqueeItem>
                <MarqueeItem>مكتب زوهو</MarqueeItem>
              </Marquee>
            </State>
          </div>
        </Section>

        <Section
          id="sidebar-nav-item"
          name="Sidebar nav item"
          note="The current row is neutral: in Sirdar the accent means a live run."
        >
          <div className="lib-col lib-col--sidebar">
            <State label="Rest, current, and a count">
              <div className="sd-sidebar">
                <nav className="sd-sidebar__nav" aria-label="Screens specimen">
                  <SidebarNavItem label="Board" current count={3} onSelect={() => {}} />
                  <SidebarNavItem label="Register" onSelect={() => {}} />
                  <SidebarNavItem label="Eval" onSelect={() => {}} />
                </nav>
              </div>
            </State>
            <State label="Arabic labels">
              <div className="sd-sidebar">
                <nav className="sd-sidebar__nav" aria-label="الشاشات">
                  <SidebarNavItem label="اللوحة" current count={2} onSelect={() => {}} />
                  <SidebarNavItem label="السجل" onSelect={() => {}} />
                </nav>
              </div>
            </State>
          </div>
        </Section>

        <Section
          id="sidebar-footer-card"
          name="Sidebar footer card"
          note="The two facts a person needs pinned, and the one action this screen commits."
        >
          <div className="lib-col lib-col--sidebar">
            <State label="Switcher, quotas, primary action">
              <SidebarFooterCard
                switcher={<Button variant="ghost">acme-support</Button>}
                quotas={
                  <>
                    <QuotaChip provider="claude" window="5h" percent={62} />
                    <QuotaChip provider="openai" window="used" percent={9} />
                  </>
                }
                action={<Button variant="primary">New triage</Button>}
              />
            </State>
            <State label="No budget, no action">
              <SidebarFooterCard switcher={<Button variant="ghost">ledger</Button>} />
            </State>
          </div>
        </Section>

        <Section
          id="modal-sheet"
          name="Modal sheet with secondary nav"
          note="Settings is a place you leave; the board stays painted behind the scrim."
        >
          <div className="lib-row">
            <State label="Closed, with its opener">
              <Button variant="primary" onClick={() => setModalOpen(true)}>
                Open the settings modal
              </Button>
            </State>
          </div>
          <ModalSheet
            open={modalOpen}
            title={modalPage === 'general' ? 'General' : 'الهوية'}
            groups={[
              { label: 'Workspace', items: [{ id: 'general', label: 'General' }] },
              { label: 'Account', items: [{ id: 'identity', label: 'الهوية' }] },
            ]}
            current={modalPage}
            onSelect={setModalPage}
            onClose={() => setModalOpen(false)}
            navFooter={<>Sirdar desktop v0.0.0</>}
            footer={<Button onClick={() => setModalOpen(false)}>Close</Button>}
          >
            <SettingCard heading="Workspace">
              <SettingRow
                label="Repository"
                value="/repos/omni"
                control={
                  <button type="button" className="sd-setting-button">
                    Change
                  </button>
                }
              />
              <SettingRow label="Provider" value="claude / sonnet" />
            </SettingCard>
          </ModalSheet>
        </Section>

        <Section
          id="setting-row"
          name="Setting row"
          note="One setting and one control per row; a setting that needs two is two rows."
        >
          <div className="lib-col">
            <State label="A card of rows, with the pale control">
              <SettingCard heading="Workspace">
                <SettingRow
                  label="Repository"
                  value="/repos/omni"
                  control={
                    <button type="button" className="sd-setting-button">
                      Change workspace
                    </button>
                  }
                />
                <SettingRow label="Provider" value="claude / sonnet" />
                <SettingRow
                  label="مجلد الملاحظات"
                  value="ملاحظات/الدعم"
                  help="يُكتب كل تقرير جذري هنا، داخل المستودع نفسه."
                  control={
                    <button type="button" className="sd-setting-button" disabled>
                      تغيير
                    </button>
                  }
                />
              </SettingCard>
            </State>
          </div>
        </Section>

        <Section
          id="heatmap"
          name="Heatmap"
          note="Runs per day, with no streak and no flame: a good week is a quiet week."
        >
          <div className="lib-col">
            <State label="Twelve weeks, with the bucket boundaries">
              <Heatmap
                weeks={12}
                endDate="2026-09-15"
                days={[
                  { date: '2026-09-15', count: 12 },
                  { date: '2026-09-14', count: 6 },
                  { date: '2026-09-11', count: 3 },
                  { date: '2026-09-09', count: 1 },
                  { date: '2026-08-28', count: 8 },
                  { date: '2026-08-12', count: 2 },
                ]}
              />
            </State>
            <State label="A workspace that has run nothing">
              <Heatmap weeks={6} endDate="2026-09-15" days={[]} />
            </State>
          </div>
        </Section>

        <Section
          id="badge"
          name="Badge"
          note="A fact about the account; run state is the status badge, with its word."
        >
          <div className="lib-row lib-row--tight">
            <State label="Plan">
              <Badge>pro</Badge>
            </State>
            <State label="Billing mode">
              <Badge title="Billing mode">api key</Badge>
            </State>
            <State label="Arabic">
              <Badge>اشتراك</Badge>
            </State>
          </div>
        </Section>

        <Section
          id="provider-mark"
          name="Provider mark"
          note="The vendors' own marks, white on the brand colour or on the ink; a provider with no mark gets its initials."
        >
          <div className="lib-row lib-row--tight">
            {PROVIDERS_SHOWN.map((provider) => (
              <State key={provider} label={provider}>
                <ProviderMark provider={provider} />
              </State>
            ))}
            <State label="Small, in a card footer">
              <ProviderMark provider="claude" size="sm" />
            </State>
            <State label="Large, on the Providers page">
              <ProviderMark provider="qwen" size="lg" />
            </State>
          </div>
        </Section>

        <Section
          id="banner"
          name="Banner"
          note="What just finished, in the hue it finished in, one per transcript."
        >
          <div className="lib-col">
            <State label="Tests passed">
              <Banner title="Tests passed">go test ./... in 41s, 212 tests</Banner>
            </State>
            <State label="Note filed">
              <Banner tone="done" title="Note filed">
                notes/SBX-1-triage.md, 1.4k words
              </Banner>
            </State>
            <State label="Live, while a step is still running">
              <Banner tone="live" title="Running the suite">
                go test ./... started 12s ago
              </Banner>
            </State>
            <State label="The lead alone, no separator">
              <Banner tone="done" title="Branch created" />
            </State>
            <State label="The agent asked, with an answer button">
              <Banner
                tone="blocked"
                title="The agent asked"
                action={
                  <Button size="sm" variant="pale">
                    Answer
                  </Button>
                }
              >
                Which account should the credit note land on?
              </Banner>
            </State>
            <State label="Failed">
              <Banner tone="failed" title="Build failed">
                exit status 1 in internal/export
              </Banner>
            </State>
            <State label="Arabic">
              <Banner tone="blocked" title="طلب الوكيل" dir="auto">
                {ARABIC_REASON}
              </Banner>
            </State>
          </div>
        </Section>

        <Section
          id="group-label"
          name="Group label"
          note="Tracked small capitals and a dashed rule: it names a group rather than starting a section."
        >
          <div className="lib-col">
            <State label="With the rule">
              <GroupLabel>Landed today</GroupLabel>
            </State>
            <State label="Without, inside a nav column">
              <div style={{ inlineSize: 200 }}>
                <GroupLabel rule={false}>Workspace</GroupLabel>
              </div>
            </State>
            <State label="Arabic">
              <GroupLabel>وصل اليوم</GroupLabel>
            </State>
          </div>
        </Section>

        <Section
          id="item-row"
          name="Item row"
          note="A tone rail, an icon in a box, a title over its facts, and one control."
        >
          <div className="lib-col">
            <State label="Started, with a Triage button">
              <ItemRow
                tone="live"
                icon={<InboxIcon />}
                title="Reorder reminder fires twice for the same product"
                meta="SBX-4 · zendesk · started · 2 min ago"
                action={<Button size="sm">Triage</Button>}
              />
            </State>
            <State label="Skipped, opens the ticket">
              <ItemRow
                tone="blocked"
                icon={<InboxIcon />}
                title="Monthly statement shows a paid invoice as overdue"
                meta="SBX-7 · jira · skipped · assignee is not you · 7 min ago"
                openLabel="Open SBX-7"
                onOpen={() => {}}
              />
            </State>
            <State label="Rejected">
              <ItemRow
                tone="failed"
                icon={<InboxIcon />}
                title="Barcode lookup returns the discontinued variant"
                meta="SBX-6 · jira · rejected · cooldown 10m · 14 min ago"
              />
            </State>
            <State label="Done, opens the session and keeps its own control">
              <ItemRow
                tone="done"
                icon={<InboxIcon />}
                title="Credit note lands on the wrong customer account"
                meta="SBX-5 · zoho · done · note filed · 1 h ago"
                openLabel="Open SBX-5"
                onOpen={() => {}}
                action={<Button size="sm">Open note</Button>}
              />
            </State>
            <State label="Plain, no icon and no control">
              <ItemRow title="Supplier price import rounds to the nearest riyal" meta="SBX-8 · jira" />
            </State>
            <State label="Arabic, two lines">
              <ItemRow
                icon={<InboxIcon />}
                title={ARABIC_TITLE}
                meta="SBX-2 · zoho · started"
                dir="auto"
                wrap
                action={<Button size="sm">Triage</Button>}
              />
            </State>
          </div>
        </Section>

        <Section
          id="search-bar"
          name="Search bar"
          note="The bar a session starts from, and the well the board filters with."
        >
          <div className="lib-col">
            <State label="The bar">
              <SearchBar
                label="Ticket key or URL"
                value={search}
                onChange={setSearch}
                placeholder="A ticket key, or paste its URL"
                aside="Enter to start"
              />
            </State>
            <State label="The bar with a key typed, ready to start">
              <SearchBar
                label="Ticket key or URL"
                value="OMNI-2510"
                onChange={() => {}}
                onSubmit={() => {}}
                aside="Enter to start"
              />
            </State>
            <State label="The well">
              <SearchBar
                variant="well"
                label="Filter the board"
                value={well}
                onChange={setWell}
                placeholder="Filter"
              />
            </State>
            <State label="Disabled, native and not styled apart">
              <SearchBar
                variant="well"
                label="Filter the board"
                value=""
                onChange={() => {}}
                placeholder="Filter"
                disabled
              />
            </State>
            <State label="Arabic">
              <SearchBar
                label="مفتاح التذكرة"
                value=""
                onChange={() => {}}
                placeholder="مفتاح التذكرة أو رابطها"
              />
            </State>
          </div>
        </Section>

        <Section
          id="stat-card"
          name="Stat card"
          note="A big figure, a grey label over it, one line under it, and no arithmetic of its own."
        >
          <div className="lib-row">
            <State label="Runs this week">
              <StatCard label="Runs this week" value="38" detail="12 more than last week" />
            </State>
            <State label="Nothing to count yet">
              <StatCard label="Runs this week" value="—" detail="no run recorded" />
            </State>
            <State label="Spent">
              <StatCard label="Spent" value="$12.40" valueTitle="$12.4031" detail="this week" />
            </State>
            <State label="Confirmed">
              <StatCard label="Confirmed" value="71%" detail="of 24 triaged" />
            </State>
            <State label="Arabic">
              <StatCard label="المصروف" value="$12.40" detail="هذا الأسبوع" />
            </State>
          </div>
        </Section>

        <Section
          id="toggle"
          name="Toggle"
          note="On or off, with the knob travelling the logical axis."
        >
          <div className="lib-row lib-row--tight">
            <State label="Drive it">
              <Toggle label="Include the ticket title" checked={toggled} onChange={setToggled} />
            </State>
            <State label="Off">
              <Toggle label="Notify on failure" checked={false} onChange={() => {}} />
            </State>
            <State label="Disabled, on">
              <Toggle label="Webhooks" checked disabled onChange={() => {}} />
            </State>
            <State label="Disabled, off">
              <Toggle label="Slack" checked={false} disabled onChange={() => {}} />
            </State>
          </div>
        </Section>

        <Section
          id="page-head"
          name="Page head"
          note="The screen's name, a lede and its actions, with the serif on the settings heading only."
        >
          {/*
           * Every specimen is an h2: the gallery's own head is the page's one
           * h1, and the component draws the two levels the same.
           */}
          <div className="lib-col">
            <State label="A screen, with its one filled action">
              <PageHead
                title="Register"
                lede="Every run, newest first."
                level={2}
                actions={
                  <>
                    <Button variant="pale">Filters</Button>
                    <Button variant="primary">Export CSV</Button>
                  </>
                }
              />
            </State>
            <State label="The title alone">
              <PageHead title="Providers" level={2} />
            </State>
            <State label="The settings heading">
              <PageHead title="MCP servers" level={2} serif />
            </State>
            <State label="Arabic">
              <PageHead title="اللوحة" lede="كل تذكرة في مسارها." level={2} />
            </State>
          </div>
        </Section>

        <Section
          id="kind-chip"
          name="Kind chip"
          note="What a run is: three fills, and the word is always there."
        >
          <div className="lib-row lib-row--tight">
            <State label="Triage">
              <KindChip kind="triage" />
            </State>
            <State label="RCA">
              <KindChip kind="rca" />
            </State>
            <State label="Fix">
              <KindChip kind="fix" />
            </State>
            <State label="Unknown kind, verbatim in the triage fill">
              <KindChip kind="eval" />
            </State>
          </div>
        </Section>

        <Section
          id="state-glyph"
          name="State glyph"
          note="Five drawings for eight states, each with its word; the clock only where it means something."
        >
          <div className="lib-row lib-row--tight">
            {(Object.keys(STATE_WORDS) as GlyphState[]).map((state) => (
              <State key={state} label={STATE_WORDS[state]}>
                <StateGlyph
                  state={state}
                  clock={state === 'running' ? '01:47' : state === 'blocked' ? '04:12' : undefined}
                />
              </State>
            ))}
            <State label="Arabic">
              <StateGlyph state="blocked" word="بانتظار ردّك" clock="04:12" />
            </State>
          </div>
        </Section>

        <Section
          id="model-picker"
          name="Model picker"
          note="The chip says which pair will run; the popover under it is where it changes."
        >
          <div className="lib-row">
            <State label="Nothing chosen, with what the last run used">
              <ModelPicker
                provider={choice.provider}
                model={choice.model}
                defaultProvider="claude"
                lastUsed="claude-sonnet-5"
                onChange={setChoice}
              />
            </State>
            <State label="A model chosen">
              <ModelPicker provider="" model="claude-sonnet-5" defaultProvider="claude" />
            </State>
            <State label="Free text">
              <ModelPicker provider="qwen" model="qwen3-coder" defaultProvider="claude" />
            </State>
            <State label="Read-only, in a session">
              <ModelPicker
                provider="claude"
                model="claude-haiku-4-5-20251001"
                readOnly="A steer resumes the same session, so the model cannot change"
              />
            </State>
            <State label="Change, on a setting row">
              <ModelPicker provider="" model="" defaultProvider="claude" trigger="change" />
            </State>
            <State label="Arabic">
              <ModelPicker
                provider="claude"
                model="claude-sonnet-5"
                readOnly="التوجيه يستأنف الجلسة نفسها، فلا يتغيّر النموذج"
              />
            </State>
          </div>
        </Section>
      </div>
    </div>
  )
}
