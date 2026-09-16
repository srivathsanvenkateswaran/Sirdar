import type { RunDetail, RunEvent } from '../../../api/types'
import type { IndexedEvent } from '../../../lib/events'

/**
 * Two runs shaped the way the sandbox's SBX-1 runs are on disk — the
 * Claude provider's raw lines under a thin payload — cut down to what the
 * Workbench's six scenes need: a completed triage with a denied call, a
 * steer and two structured answers, and a fix with an Edit, a failing test,
 * a passing suite and a hunk dropped in review. Test data for this layout
 * and its model; nothing here is fetched.
 */

const T0 = Date.parse('2026-09-15T12:11:05.184Z')

function at(seconds: number): string {
  return new Date(T0 + Math.round(seconds * 1000)).toISOString()
}

let ids = 0

interface Call {
  tool: string
  input: Record<string, unknown>
  output?: string
  isError?: boolean
  /** The policy's word, when it spoke. */
  decision?: 'allow' | 'deny'
  reason?: string
  start: number
  /** How long the call took, in seconds. */
  took: number
}

function assistantLine(content: unknown[], model = 'claude-opus-5'): Record<string, unknown> {
  return {
    type: 'assistant',
    message: { model, id: `msg_${ids}`, type: 'message', role: 'assistant', content },
    session_id: 'sess-1',
  }
}

/** A tool call as the log writes it: the start, the policy's answer if any, and the result. */
function call(c: Call): RunEvent[] {
  const id = `toolu_${String(++ids).padStart(3, '0')}`
  const out: RunEvent[] = [
    {
      t: at(c.start),
      kind: 'tool_started',
      payload: {
        tool: c.tool,
        raw: assistantLine([{ type: 'tool_use', id, name: c.tool, input: c.input }]),
      },
    },
  ]
  if (c.decision) {
    out.push({
      t: at(c.start + 0.002),
      kind: 'permission',
      payload: {
        tool: c.tool,
        decision: c.decision,
        text: c.reason ?? '',
        raw: {
          type: 'control_request',
          request: { subtype: 'can_use_tool', tool_name: c.tool, input: c.input, tool_use_id: id },
        },
      },
    })
  }
  if (c.output !== undefined) {
    out.push({
      t: at(c.start + c.took),
      kind: 'tool_finished',
      payload: {
        text: c.output,
        raw: {
          type: 'user',
          message: {
            role: 'user',
            content: [{ tool_use_id: id, type: 'tool_result', content: c.output, is_error: c.isError ?? false }],
          },
        },
      },
    })
  }
  return out
}

function usage(seconds: number, turns: number | undefined, thinking = false): RunEvent {
  const content = thinking ? [{ type: 'thinking', thinking: '', signature: 'sig' }] : []
  const payload: RunEvent['payload'] = { raw: assistantLine(content) }
  if (turns !== undefined) payload.turns = turns
  return { t: at(seconds), kind: 'usage', payload }
}

function system(seconds: number, subtype: string, extra: Record<string, unknown> = {}): RunEvent {
  return { t: at(seconds), kind: 'system', payload: { text: subtype, raw: { type: 'system', subtype, ...extra } } }
}

function hooks(seconds: number, name: string, n: number): RunEvent[] {
  const out: RunEvent[] = []
  for (let i = 0; i < n; i += 1) out.push(system(seconds, 'hook_started', { hook_name: name }))
  return out
}

export const LEDGER_GO = [
  'package ledger',
  '',
  '// Ledger tracks current stock per product, built up by applying a sequence',
  '// of movements.',
  'type Ledger struct {',
  '\tStock map[string]int',
  '}',
  '',
  'func (l *Ledger) ApplyMovement(m Movement) error {',
  '\tswitch m.Type {',
  '\tcase Purchase:',
  '\t\tl.Stock[m.ProductID] += m.Quantity',
  '\tcase Sale:',
  '\t\tl.Stock[m.ProductID] -= m.Quantity',
  '\tcase Return:',
  '\t\tl.Stock[m.ProductID] += m.Quantity',
  '\t\tl.restock(m.ProductID, m.Quantity)',
  '\t}',
  '\treturn nil',
  '}',
]
  .map((line, i) => `${String(i + 1).padStart(6)}\t${line}`)
  .join('\n')

export const RG_OUT = [
  'ledger_test.go:9:\treturn Movement{ProductID: productID, Type: typ, Quantity: qty, At: time.Now()}',
  'ledger.go:24:\t\tl.Stock[m.ProductID] += m.Quantity',
  'ledger.go:26:\t\tl.Stock[m.ProductID] -= m.Quantity',
  'ledger.go:33:\t\tl.Stock[m.ProductID] += m.Quantity',
  'ledger.go:34:\t\tl.restock(m.ProductID, m.Quantity)',
  'movement.go:26:\tQuantity  int // always positive; the sign is implied by Type',
  'movement.go:54:\t\treturn m.Quantity',
].join('\n')

export const DENY_REASON =
  'Sirdar policy: not permitted by permissions.bash; allowed here: git log*, git show*, git grep*, rg *, ls *, cat *, head *, tail *, ... (see .sirdar/config.yaml). "git -C /repo log --stat" passes git "-C" before the subcommand, which moves where git reads its configuration or runs code from for everything that follows; no allow-list pattern approves it'

/** The triage answer, in the schema `internal/triage` validates. */
export const TRIAGE_ANSWER = {
  ticket: {
    key: 'SBX-1',
    title: 'Product 00219 stock shows 1 more than the movement report',
    trackerUrl: 'https://sandbox.local/tracker/SBX-1',
    helpdeskId: '88341',
    helpdeskUrl: 'https://sandbox.local/desk/88341',
    priority: 'normal',
    service: 'sandbox/ledger',
    customer: 'متجر الفهد للأدوات المنزلية',
    customerId: 'SBX-CUST-1',
    customerIds: null,
  },
  title: 'Recording a customer return adds its quantity to stock twice, so on-hand stock drifts above the movement report',
  complaint:
    '(2026-09-12 09:14 +03:00) "Peace be upon you and God\'s mercy. I have a problem with the inventory: for product number 00219, the quantity showing in the system is one more than what\'s in the report."\n\n(2026-09-12 10:41 +03:00) "The return was last Tuesday. Before I returned it, the quantity in the system matched the report."\n\nTone: polite and calm, not escalated.',
  complaintOriginal:
    '[2026-09-12T09:14:00+03:00]\nالسلام عليكم ورحمة الله\nعندي مشكلة بالمخزون، المنتج رقم 00219 الكمية الظاهرة عندي بالنظام أكبر بواحد عن اللي بالتقرير.',
  customerReplyDraft: {
    language: 'ar',
    text: 'وعليكم السلام أستاذ أحمد،\nشكراً على التفاصيل اللي أرسلتها بخصوص المنتج 00219. نراجع الموضوع الآن ونرجع لك.',
  },
  timeline: [
    { at: '2026-09-08 (Tuesday, inferred)', role: 'customer', summary: 'Customer records a return movement for product 00219.' },
    { at: '2026-09-12T09:14:00+03:00', role: 'customer', summary: 'Reports the on-hand quantity is 1 higher than the report.' },
    { at: '2026-09-12T10:05:00+03:00', role: 'agent', summary: 'L1 asks for the exact return date.' },
  ],
  reproSteps: ['Create a product 00219 with a Purchase of 10.', 'Record a Return of 1.', 'Compare CurrentStock (12) with SumMovements (11).'],
  rootCause: {
    hypothesis:
      'In Ledger.ApplyMovement, the Return case adds the return quantity to the running stock twice: once directly at ledger.go:33 and again through l.restock at ledger.go:34, which does its own += at ledger.go:45.',
    confidence: 'high',
    evidence: [
      { source: 'code', query: 'Read ledger.go', finding: 'ledger.go:27-34: `case Return:` runs the increment and then restock. Net effect on Stock is +2×Quantity.' },
      { source: 'code', query: 'rg -n "Return|restock" --type go', finding: 'restock is defined at ledger.go:44 and its only caller is ledger.go:34.' },
      { source: 'git', query: "git log --stat --format='%h %ad %s' --date=iso", finding: 'The repository has one commit: a9b28cd.' },
    ],
    codeRefs: ['ledger.go:27-34', 'ledger.go:44-46', 'movement.go:51-62', 'reconcile.go:19-23'],
  },
  blastRadius: 'Every product, for every customer using this ledger, that has at least one Return movement.',
  classification: 'code',
  proposedFix: {
    description:
      "Apply a return's quantity once in ApplyMovement. The smallest change is to delete one of the two increments in the Return case. Add a regression test in ledger_test.go: Purchase 10, Return 1, then assert CurrentStock == 11 and Reconcile ok.",
    files: ['ledger.go', 'ledger_test.go'],
    remediationSql: 'None provided. No database source is configured for this run.',
    risks: 'If the separate "item scanned back in" event mentioned at ledger.go:28-31 still exists in some other service, returns may be counted more than twice in production.',
  },
  openQuestions: ['Does any other service still emit the "item scanned back in" event?', 'How many past returns need their stock rebuilt?'],
}

export const PARTIAL_ANSWER = {
  ...TRIAGE_ANSWER,
  title:
    'Partial returns are affected too: this module has no separate partial-return path, so returning fewer units than were sold is an ordinary Return movement with a smaller Quantity.',
}

function finalEvent(seconds: number, answer: unknown, turns: number, costUsd: number): RunEvent {
  return {
    t: at(seconds),
    kind: 'final',
    payload: {
      text: JSON.stringify(answer),
      turns,
      costUsd,
      raw: {
        type: 'result',
        duration_api_ms: 101_700,
        total_cost_usd: costUsd,
        usage: { cache_creation_input_tokens: 44_160, cache_read_input_tokens: 157_945, output_tokens: 7966 },
        structured_output: answer,
      },
    },
  }
}

/** The completed triage: `20260915T121105Z-076d`, cut to its shape. */
export function triageEvents(): RunEvent[] {
  ids = 0
  return [
    ...hooks(1.0, 'SessionStart:startup', 5),
    system(1.05, 'init', { cwd: '/Users/me/Documents/Personal/sirdar-sandbox/app' }),
    usage(3.3, undefined, true),
    usage(4.7, 1),
    ...call({ tool: 'Bash', input: { command: 'ls -la /repo', description: 'List repository root files' }, output: 'total 48\n-rw-r--r-- 1 me staff 1801 ledger.go\n-rw-r--r-- 1 me staff 2647 ledger_test.go', start: 4.7, took: 0.068 }),
    usage(7.4, 2),
    ...call({ tool: 'Read', input: { file_path: '/repo/ledger.go' }, output: LEDGER_GO, start: 7.4, took: 0.009 }),
    usage(8.1, 3),
    ...call({ tool: 'Read', input: { file_path: '/repo/movement.go' }, output: LEDGER_GO, start: 8.1, took: 0.004 }),
    usage(18.8, 3, true),
    usage(20.5, 4),
    ...call({
      tool: 'Bash',
      input: { command: "git -C /repo log --stat --format='%h %ad %s' --date=iso", description: 'Show commit history with changed files' },
      decision: 'deny',
      reason: DENY_REASON,
      output: DENY_REASON,
      isError: true,
      start: 20.5,
      took: 0.01,
    }),
    usage(23.5, 5),
    ...call({
      tool: 'Bash',
      input: { command: "git log --stat --format='%h %ad %s' --date=iso", description: 'Show commit history with changed files' },
      output: "a9b28cd 2026-09-15 14:06:20 +0530 Add sandbox/ledger: inventory stock tracked from movement history\n\n ledger.go | 46 ++++\n ledger_test.go | 94 ++++++++\n 5 files changed, 260 insertions(+)",
      start: 23.5,
      took: 0.051,
    }),
    usage(24.4, 6),
    ...call({
      tool: 'Bash',
      input: { command: 'rg -n "Return|restock" --type go', description: 'Search Go code for return and restock usages' },
      output: RG_OUT,
      start: 24.4,
      took: 0.03,
    }),
    usage(42.7, 6, true),
    usage(102.8, 7),
    ...call({ tool: 'StructuredOutput', input: TRIAGE_ANSWER, output: 'Structured output provided successfully', start: 102.8, took: 0.024 }),
    { t: at(102.9), kind: 'usage', payload: { turns: 8, costUsd: 0.72, raw: { type: 'result' } } },
    finalEvent(102.95, TRIAGE_ANSWER, 8, 0.72),
    { t: at(123.1), kind: 'steer', payload: { text: 'Re-check whether the partial-return path is also affected, and say so in one sentence.', continuation: 'resume' } },
    ...hooks(124.3, 'SessionStart:resume', 4),
    usage(126.0, undefined, true),
    usage(127.3, 1),
    ...call({
      tool: 'Bash',
      input: { command: 'rg -n -i "partial|Quantity" --type go', description: 'Search Go code for partial-return handling' },
      output: RG_OUT,
      start: 127.3,
      took: 0.082,
    }),
    usage(131.8, 1, true),
    usage(194.5, 2),
    ...call({ tool: 'StructuredOutput', input: PARTIAL_ANSWER, output: 'Structured output provided successfully', start: 194.5, took: 0.029 }),
    { t: at(194.6), kind: 'usage', payload: { turns: 3, costUsd: 0.3, raw: { type: 'result' } } },
    finalEvent(194.7, PARTIAL_ANSWER, 3, 0.3),
  ]
}

export const TRIAGE_RUN: RunDetail = {
  runId: '20260915T121105Z-076d',
  key: 'SBX-1',
  helpdeskKey: '88341',
  title: 'Product 00219 stock shows 1 more than the movement report',
  kind: 'triage',
  status: 'completed',
  provider: 'claude',
  model: 'claude-opus-5',
  startedAt: at(0),
  updatedAt: at(195.1),
  reason: '',
  assignee: 'ops@sandbox.local',
  mine: false,
  usage: { turns: 17, inputTokens: 837_662, outputTokens: 14_823, costUsd: 1.024 },
  notes: [
    '/repo/.sirdar/runs/SBX-1/20260915T121105Z-076d/note.md',
    '/Users/me/Documents/Personal/sirdar-sandbox/notes/SBX-1 recording-a-customer-return-adds-its-quantity-to-stock-twice.md',
  ],
  promptPath: '/repo/.sirdar/runs/SBX-1/20260915T121105Z-076d/prompt.md',
  bundleDir: '/repo/.sirdar/runs/SBX-1/20260915T121105Z-076d/bundle',
  warnings: [],
  handle: 'sess-1',
  budget: { maxTurns: 60, maxMinutes: 20, maxUsd: 5 },
}

export const TRIAGE_NOTE = `---
tags: ["support-duty", "triage"]
tracker_key: "SBX-1"
helpdesk_id: "88341"
customer: "متجر الفهد للأدوات المنزلية"
date: "2026-09-15"
priority: "normal"
service: "sandbox/ledger"
status: "triaged"
run: "20260915T121105Z-076d"
provider: "claude"
---

# Recording a customer return adds its quantity to stock twice, so on-hand stock drifts above the movement report

## Customer Complaint (translated)

(2026-09-12 09:14 +03:00) "Peace be upon you and God's mercy. I have a problem with the inventory."

## Root Cause Hypothesis

In \`Ledger.ApplyMovement\`, the Return case adds the return quantity twice: at ledger.go:33 and through restock at ledger.go:34.

## Proposed Fix

Apply a return's quantity once in ApplyMovement.

## Customer Reply Draft

<div dir="rtl">

وعليكم السلام أستاذ أحمد، نراجع الموضوع الآن.

</div>
`

export const TRIAGE_PROMPT = `# Language

Write the note in English.

# Playbooks

## 00-environment

# Environment

The ledger service.

## How to use it

Read the code.

## 10-helpdesk

## How to use it

Read the thread.

## 50-code

## Workspace gotchas

None yet.

# Ticket

Key: SBX-1
Title: Product 00219 stock shows 1 more than the movement report
Priority: normal
Tracker URL: https://sandbox.local/tracker/SBX-1
Helpdesk URL: https://sandbox.local/desk/88341
Customer: متجر الفهد للأدوات المنزلية
Customer ID: SBX-CUST-1
Bundle directory: /repo/.sirdar/runs/SBX-1/20260915T121105Z-076d/bundle

Files:
(none)

## Conversation

\`\`\`
# Conversation (original language)

## 2026-09-12T09:14:00+03:00 · customer · أحمد الفهد

السلام عليكم ورحمة الله
عندي مشكلة بالمخزون، المنتج رقم 00219 الكمية الظاهرة عندي بالنظام أكبر بواحد عن اللي بالتقرير.

## 2026-09-12T10:05:00+03:00 · agent · Layla (L1)

وعليكم السلام، شكراً تواصلكم معنا. ممكن تأكدلنا تاريخ المرتجع بالضبط حتى نراجع الحركة؟

## 2026-09-12T10:41:00+03:00 · customer · أحمد الفهد

المرتجع كان يوم الثلاثاء الماضي، رقم الطلب مو متذكره بالضبط بس المنتج نفس الكود 00219.

## 2026-09-13T11:02:00+03:00 · agent · Layla (L1)

تمام، وصلتنا التفاصيل. راح نراجع سجل حركة المنتج 00219 ونرجعلك بالنتيجة.
\`\`\`

# Output

- ticket identifies the record.
`

// ---------------------------------------------------------------------- fix

export const FIX_REPORT = {
  summary:
    "Apply a return's quantity to stock once in ApplyMovement\n\nIn the Return case, ApplyMovement added the quantity directly and then again through restock. This change removes the restock call and the restock helper.",
  filesChanged: ['ledger.go', 'ledger_test.go'],
  testsRun: [
    { command: 'go test ./... (after adding the regression test, before the fix)', result: 'FAIL: TestApplyMovementReturnAddsQuantityOnce, ledger_test.go:82: CurrentStock = 12, want 11' },
    { command: 'go build ./...', result: 'succeeded, no output' },
    { command: 'go vet ./...', result: 'succeeded, no output' },
    { command: 'go test ./...', result: 'ok  sandbox/ledger 2.070s' },
  ],
  risks:
    'This fixes new returns only. Stock that past returns already inflated stays too high until it is rebuilt from movement history.',
  deviationFromNote: '',
}

const FAIL_OUT = [
  'Exit code 1',
  '--- FAIL: TestApplyMovementReturnAddsQuantityOnce (0.00s)',
  '    ledger_test.go:82: CurrentStock = 12, want 11',
  'FAIL',
  'FAIL\tsandbox/ledger\t0.214s',
  'FAIL',
].join('\n')

const F0 = 0

/** The fix run's events up to the moment it would block on a question (S2), or the whole run. */
export function fixEvents(opts: { untilBlocked?: boolean } = {}): RunEvent[] {
  ids = 100
  const head: RunEvent[] = [
    ...hooks(F0 + 1.0, 'SessionStart:startup', 5),
    system(F0 + 1.05, 'init', { cwd: '/repo/.sirdar/worktrees/20260915T121451Z-bf19' }),
    usage(F0 + 4.4, 1),
    ...call({ tool: 'Read', input: { file_path: '/repo/.sirdar/worktrees/bf19/ledger.go' }, output: LEDGER_GO, start: F0 + 4.4, took: 0.028 }),
    usage(F0 + 4.9, 2),
    ...call({ tool: 'Read', input: { file_path: '/repo/.sirdar/worktrees/bf19/ledger_test.go' }, output: LEDGER_GO, start: F0 + 4.9, took: 0.007 }),
    usage(F0 + 11.3, 3),
    ...call({
      tool: 'Edit',
      input: {
        file_path: '/repo/.sirdar/worktrees/bf19/ledger_test.go',
        old_string: 'func TestSumMovementsNetsPurchasesAndSales(t *testing.T) {',
        new_string: 'func TestApplyMovementReturnAddsQuantityOnce(t *testing.T) {\n\tl := NewLedger()\n}\n\nfunc TestSumMovementsNetsPurchasesAndSales(t *testing.T) {',
      },
      decision: 'allow',
      output: 'The file /repo/.sirdar/worktrees/bf19/ledger_test.go has been updated.',
      start: F0 + 11.3,
      took: 0.013,
    }),
    usage(F0 + 12.4, 4),
    ...call({
      tool: 'Bash',
      input: { command: 'go test ./...', description: 'Run tests before the fix' },
      decision: 'allow',
      output: FAIL_OUT,
      isError: true,
      start: F0 + 12.4,
      took: 1.995,
    }),
  ]
  if (opts.untilBlocked) return head
  return [
    ...head,
    usage(F0 + 19.9, 5),
    ...call({
      tool: 'Edit',
      input: {
        file_path: '/repo/.sirdar/worktrees/bf19/ledger.go',
        old_string: '\t\tl.restock(m.ProductID, m.Quantity)\n',
        new_string: '',
      },
      decision: 'allow',
      output: 'The file /repo/.sirdar/worktrees/bf19/ledger.go has been updated.',
      start: F0 + 19.9,
      took: 0.015,
    }),
    usage(F0 + 21.0, 6),
    ...call({
      tool: 'Bash',
      input: { command: 'go build ./... && go vet ./... && go test ./...', description: 'Build, vet and test after the fix' },
      decision: 'allow',
      output: 'ok  \tsandbox/ledger\t2.070s',
      start: F0 + 21.0,
      took: 2.625,
    }),
    usage(F0 + 31.8, 7),
    ...call({ tool: 'StructuredOutput', input: FIX_REPORT, output: 'Structured output provided successfully', start: F0 + 31.8, took: 0.002 }),
    { t: at(F0 + 31.9), kind: 'usage', payload: { turns: 8, costUsd: 0.5629, raw: { type: 'result' } } },
    finalEvent(F0 + 31.95, FIX_REPORT, 8, 0.5629),
    { t: at(F0 + 50.3), kind: 'review', payload: { action: 'drop', path: 'ledger_test.go', hunk: 0 } },
  ]
}

export const FIX_RUN: RunDetail = {
  ...TRIAGE_RUN,
  runId: '20260915T121451Z-bf19',
  kind: 'fix',
  status: 'completed',
  startedAt: at(F0),
  updatedAt: at(F0 + 33.5),
  usage: { turns: 8, inputTokens: 339_799, outputTokens: 2205, costUsd: 0.5629 },
  notes: [],
  promptPath: '/repo/.sirdar/runs/SBX-1/20260915T121451Z-bf19/prompt.md',
  bundleDir: '/repo/.sirdar/runs/SBX-1/20260915T121451Z-bf19/bundle',
  warnings: ['fix branch: fix-sbx-1-recording-a-customer-return-adds-its-qua'],
  fix: {
    branch: 'fix-sbx-1-recording-a-customer-return-adds-its-qua',
    base: 'main',
    commit: 'f1449369417839f2c5b4c0658d0523b70092f298',
    pushed: false,
  },
}

/** The fix run paused on a question after the pre-fix test failed. */
export const BLOCKED_FIX_RUN: RunDetail = {
  ...FIX_RUN,
  status: 'blocked',
  updatedAt: at(F0 + 14.5),
  usage: { turns: 4, inputTokens: 120_000, outputTokens: 900, costUsd: 0 },
  reason:
    'agent asked: The smallest change is to delete one of the two increments in the Return case. Which should I keep?\n1. remove line 33 and keep restock\n2. remove the restock call at line 34 and the restock helper\n3. fold every movement type with l.Stock[m.ProductID] += m.Delta()',
  fix: {},
}

export const FIX_PATCH = `diff --git a/ledger.go b/ledger.go
--- a/ledger.go
+++ b/ledger.go
@@ -25,13 +25,8 @@ func (l *Ledger) ApplyMovement(m Movement) error {
 	case Sale:
 		l.Stock[m.ProductID] -= m.Quantity
 	case Return:
-		// A returned item goes back on the shelf. restock used to be the
-		// only place that happened.
+		// A returned item goes back on the shelf, once.
 		l.Stock[m.ProductID] += m.Quantity
-		l.restock(m.ProductID, m.Quantity)
 	case Adjustment:
 		l.Stock[m.ProductID] += m.Quantity
 	}
@@ -40,11 +35,6 @@ func (l *Ledger) ApplyMovement(m Movement) error {
 	return nil
 }

-// restock puts returned stock back on the shelf.
-func (l *Ledger) restock(productID string, qty int) {
-	l.Stock[productID] += qty
-}
-
 // CurrentStock returns a product's current stock level.
diff --git a/ledger_test.go b/ledger_test.go
--- a/ledger_test.go
+++ b/ledger_test.go
@@ -74,6 +74,20 @@ func TestReconcileAgreesAfterPurchasesAndSales(t *testing.T) {
 	}
 }

+func TestApplyMovementReturnAddsQuantityOnce(t *testing.T) {
+	l := NewLedger()
+	must(t, l.ApplyMovement(mv("00219", Purchase, 10)))
+	must(t, l.ApplyMovement(mv("00219", Return, 1)))
+	if got := l.CurrentStock("00219"); got != 11 {
+		t.Fatalf("CurrentStock = %d, want 11", got)
+	}
+}
+
 func TestSumMovementsNetsPurchasesAndSales(t *testing.T) {
`

export function fixDiff() {
  return {
    base: 'main',
    head: 'fix-sbx-1-recording-a-customer-return-adds-its-qua',
    branch: 'fix-sbx-1-recording-a-customer-return-adds-its-qua',
    worktree: '/repo/.sirdar/worktrees/20260915T121451Z-bf19',
    worktreePresent: true,
    pushed: false,
    files: [
      { path: 'ledger.go', status: 'modified' as const, additions: 1, deletions: 11 },
      { path: 'ledger_test.go', status: 'modified' as const, additions: 14, deletions: 0 },
    ],
    patch: FIX_PATCH,
    etag: 'etag-fix-1',
  }
}

/** Numbers a list of events from 1, the way the feed does. */
export function indexed(events: RunEvent[]): IndexedEvent[] {
  return events.map((event, i) => ({ index: i + 1, event }))
}
