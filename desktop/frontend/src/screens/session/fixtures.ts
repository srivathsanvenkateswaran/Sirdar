import type { RunDetail, RunEvent } from '../../api/types'

/*
 * The SBX-1 runs, in the shape Claude writes them, for the layout's tests:
 * the triage run 20260915T121105Z-076d (fifteen calls, two denials, a steer,
 * two structured answers, a note) and the fix run 20260915T121451Z-bf19 (two
 * edits, a failing test, the fix, a report on a branch). Every string is
 * from the real logs and the note they filed; the token deltas and hooks
 * the provider writes by the hundred are left out, since the layout reads
 * past them.
 *
 * The events carry the fields the readers in `lib/events` look for and no
 * more: `tool_use` blocks with ids, `tool_result` blocks paired by id,
 * `control_request` permissions, `system/init`, `system/thinking_tokens`,
 * and a `result` line with `structured_output`.
 */

let ids = 0

function at(offset: string, base = '2026-09-15T12:11:05Z'): string {
  const [m, s] = offset.split(':').map(Number)
  return new Date(Date.parse(base) + (m * 60 + s) * 1000).toISOString()
}

export function init(t: string, cwd: string, model = 'claude-opus-5[1m]'): RunEvent {
  return { t, kind: 'system', payload: { text: 'init', raw: { type: 'system', subtype: 'init', cwd, model } } }
}

export function thinking(t: string, tokens: number): RunEvent {
  return {
    t,
    kind: 'system',
    payload: { text: 'thinking_tokens', raw: { type: 'system', subtype: 'thinking_tokens', estimated_tokens: tokens } },
  }
}

export function stream(t: string): RunEvent {
  return { t, kind: 'system', payload: { text: 'stream_event', raw: { type: 'stream_event', event: { type: 'content_block_delta' } } } }
}

export function usage(t: string, turns: number, costUsd: number): RunEvent {
  return { t, kind: 'usage', payload: { turns, costUsd, raw: { type: 'assistant' } } }
}

/** A tool call and its result, paired by the tool_use id. */
export function call(
  t: string,
  tool: string,
  input: Record<string, unknown>,
  result?: { t: string; text: string; error?: boolean },
): RunEvent[] {
  const id = `toolu_${String(++ids).padStart(3, '0')}`
  const started: RunEvent = {
    t,
    kind: 'tool_started',
    payload: {
      tool,
      raw: { type: 'assistant', message: { content: [{ type: 'tool_use', id, name: tool, input }] } },
    },
  }
  if (!result) return [started]
  const finished: RunEvent = {
    t: result.t,
    kind: 'tool_finished',
    payload: {
      text: result.text,
      raw: {
        type: 'user',
        message: {
          content: [{ type: 'tool_result', tool_use_id: id, content: result.text, ...(result.error ? { is_error: true } : {}) }],
        },
      },
    },
  }
  return [started, finished]
}

/** A call the policy ruled on: start, the ruling, then the result, in the order the log writes them. */
export function ruled(
  t: string,
  tool: string,
  input: Record<string, unknown>,
  decision: 'allow' | 'deny',
  text: string,
  extra: Record<string, unknown>,
  result?: { t: string; text: string; error?: boolean },
): RunEvent[] {
  const [started, finished] = call(t, tool, input, result)
  const ruling = permission(t, tool, decision, input, text, extra)
  return finished ? [started, ruling, finished] : [started, ruling]
}

/** The policy's word on the call before it in the list. */
export function permission(
  t: string,
  tool: string,
  decision: 'allow' | 'deny',
  input: Record<string, unknown>,
  text = '',
  extra: Record<string, unknown> = {},
): RunEvent {
  return {
    t,
    kind: 'permission',
    payload: {
      tool,
      decision,
      ...(text ? { text } : {}),
      raw: {
        type: 'control_request',
        request: { subtype: 'can_use_tool', tool_name: tool, input, tool_use_id: `toolu_${String(ids).padStart(3, '0')}`, ...extra },
      },
    },
  }
}

export const ROOT = '/Users/srivathsanv/Documents/Personal/sirdar-sandbox/app'
const RUN_DIR = `${ROOT}/.sirdar/runs/SBX-1/20260915T121105Z-076d`

export const LEDGER_GO = [
  '1\tpackage ledger',
  '2\t',
  '3\t// Ledger tracks current stock per product, built up by applying a stream',
  '4\t// of movements.',
  '5\ttype Ledger struct {',
  '6\t\tStock map[string]int',
  '7\t}',
].join('\n')

export const RG_PARTIAL = [
  'ledger_test.go:9:\treturn Movement{ProductID: productID, Type: typ, Quantity: qty, At: time.Now()}',
  'ledger_test.go:42:\tif err := l.ApplyMovement(Movement{ProductID: "", Type: Purchase, Quantity: 1}); err == nil {',
  'ledger_test.go:46:\t\tt.Fatal("expected an error for a non-positive quantity")',
  'ledger.go:24:\t\tl.Stock[m.ProductID] += m.Quantity',
  'ledger.go:26:\t\tl.Stock[m.ProductID] -= m.Quantity',
  'ledger.go:31:\t\t// return movement itself too, the quantity is added here once',
  'ledger.go:33:\t\tl.Stock[m.ProductID] += m.Quantity',
  'ledger.go:34:\t\tl.restock(m.ProductID, m.Quantity)',
  'ledger.go:36:\t\tl.Stock[m.ProductID] += m.Quantity',
  'movement.go:26:\tQuantity  int // always positive; the sign is implied by Type',
  'movement.go:36:\tif m.Quantity <= 0 {',
].join('\n')

export const DENY_GIT_C =
  'Sirdar policy: not permitted by permissions.bash; allowed here: git log*, git show*, git grep*, rg *, ls *, cat *, head *, tail *, ... (see .sirdar/config.yaml). "git -C /Users/srivathsanv/Documents/Personal/sirdar-sandbox/app log --stat --format=\'%h %ad %s\' --date=iso" passes git "-C" before the subcommand, which moves where git reads its configuration or runs code from for everything that follows; no allow-list pattern approves it'

export const DENY_DATE =
  'Sirdar policy: not permitted by permissions.bash; allowed here: git log*, git show*, git grep*, rg *, ls *, cat *, head *, tail *, ... (see .sirdar/config.yaml). "date -j -f %Y-%m-%d 2026-09-12 +%A" is not in the allow-list; every segment of a pipeline or compound command has to match'

/** The triage answer, as the run's StructuredOutput carried it (trimmed to what the card draws). */
export function triageAnswer(revised = false): Record<string, unknown> {
  return {
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
    },
    title: 'Recording a customer return adds its quantity to stock twice, so on-hand stock drifts above the movement report',
    complaint: '(2026-09-12 09:14 +03:00) "Peace be upon you and God\'s mercy. I have a problem with the inventory."',
    complaintOriginal: '[2026-09-12T09:14:00+03:00]\nالسلام عليكم ورحمة الله\nعندي مشكلة بالمخزون',
    customerReplyDraft: {
      language: 'ar',
      text: 'وعليكم السلام أستاذ أحمد،\nشكراً على التفاصيل اللي أرسلتها بخصوص المنتج 00219 والفرق بين الكمية بالنظام والتقرير بعد تسجيل المرتجع. الموضوع حالياً عند الفريق الفني وقاعدين نراجعه، وراح نتواصل معك أول ما يكون عندنا تحديث.\nالله يعطيك العافية.',
    },
    classification: 'code',
    rootCause: {
      hypothesis:
        'In Ledger.ApplyMovement, the Return case adds the return quantity to the running stock twice: once directly at ledger.go:33 and again through l.restock at ledger.go:34, which does its own `+=` at ledger.go:45. The movement report (SumMovements/Movement.Delta) counts a return once, as +Quantity. After any return, on-hand stock therefore sits above the report by that return\'s quantity. A 1-unit return gives the +1 gap the customer sees.',
      confidence: 'high',
      evidence: [
        {
          source: 'code',
          query: 'Read ledger.go',
          finding:
            'ledger.go:27-34: `case Return:` runs `l.Stock[m.ProductID] += m.Quantity` (line 33) and then `l.restock(m.ProductID, m.Quantity)` (line 34). ledger.go:44-46: restock does `l.Stock[productID] += qty`. Net effect on Stock is +2×Quantity.',
        },
        {
          source: 'code',
          query: 'Read movement.go',
          finding: 'movement.go:51-62: Delta returns +Quantity for Purchase and Return, −Quantity for Sale and +Quantity for Adjustment.',
        },
        {
          source: 'code',
          query: 'Read reconcile.go',
          finding: 'reconcile.go:19-23: Reconcile compares CurrentStock (the Stock map) with that sum. The drift is in the Stock fold.',
        },
        {
          source: 'code',
          query: 'rg "Return|restock"',
          finding: 'ledger.go:44: restock is defined at ledger.go:44 and its only caller is ledger.go:34.',
        },
        ...(revised
          ? [
              {
                source: 'code',
                query: 'rg -i "partial|Quantity"',
                finding:
                  'movement.go:23-29: No match for "partial" in the module. A partial return is just a Return movement with a smaller Quantity.',
              },
            ]
          : []),
        { source: 'code', query: 'Read ledger_test.go', finding: 'ledger_test.go:65-86: no test refers to Return.' },
        { source: 'helpdesk', query: 'thread', finding: 'The customer reports a gap of exactly 1 after one return.' },
      ],
      codeRefs: [
        'ledger.go:27-34',
        'ledger.go:44-46',
        'movement.go:23-29',
        'movement.go:51-62',
        'reconcile.go:7-13',
        'reconcile.go:19-23',
        'ledger_test.go:65-86',
      ],
    },
    blastRadius:
      'Every product, for every customer using this ledger, that has at least one Return movement has on-hand stock overstated by the total quantity of all its returns. Partial returns are affected too.',
    proposedFix: {
      description:
        'Apply a return\'s quantity once in ApplyMovement. The smallest change is to delete one of the two increments in the Return case. Add a regression test in ledger_test.go: Purchase 10, Return 1, then assert CurrentStock == 11 and Reconcile ok.',
      files: ['ledger.go', 'ledger_test.go'],
      risks: 'Stock already inflated by past returns stays too high until it is rebuilt from movement history.',
    },
    openQuestions: [
      'I couldn\'t check the customer\'s actual movement history for product 00219: no database, log or APM source is configured.',
      'Which order carried the return?',
    ],
  }
}

export function final(t: string, answer: Record<string, unknown>, turns: number, costUsd: number): RunEvent {
  const text = JSON.stringify(answer)
  return {
    t,
    kind: 'final',
    payload: {
      text,
      turns,
      costUsd,
      raw: { type: 'result', subtype: 'success', result: text, structured_output: answer, num_turns: turns, total_cost_usd: costUsd },
    },
  }
}

export const TRIAGE_NOTE = `---
tags: ["support-duty", "triage"]
tracker_key: "SBX-1"
tracker_url: "https://sandbox.local/tracker/SBX-1"
helpdesk_id: "88341"
helpdesk_url: "https://sandbox.local/desk/88341"
customer: "متجر الفهد للأدوات المنزلية"
customer_id: "SBX-CUST-1"
date: "2026-09-15"
priority: "normal"
service: "sandbox/ledger"
status: "triaged"
run: "20260915T121105Z-076d"
provider: "claude"
---

# Recording a customer return adds its quantity to stock twice, so on-hand stock drifts above the movement report

Register: [[_Issue Register]] · RCA: SBX-1 RCA recording-a-customer-return-adds-its-quantity-to-stock-twice

## Customer Complaint (translated)

(2026-09-12 09:14 +03:00) "Peace be upon you and God's mercy. I have a problem with the inventory: for product number 00219, the quantity showing in the system is one more than what's in the report."

Tone: polite and calm, not escalated.

## Customer Complaint (original)

<div dir="rtl">

[2026-09-12T09:14:00+03:00]
السلام عليكم ورحمة الله
عندي مشكلة بالمخزون، المنتج رقم 00219 الكمية الظاهرة عندي بالنظام أكبر بواحد عن اللي بالتقرير.

</div>

## Repro Steps

1. Traced through the code only. I did not run it.
1. l := NewLedger()
1. Actual: ledger.go:33 adds 1 and ledger.go:34 → restock (ledger.go:45) adds another 1, so CurrentStock = 12.

## Root Cause Hypothesis

**Classification:** code
**Confidence:** high

In Ledger.ApplyMovement, the Return case adds the return quantity to the running stock twice: once directly at ledger.go:33 and again through l.restock at ledger.go:34, which does its own \`+=\` at ledger.go:45.

Evidence:

- code (\`Read ledger.go\`): ledger.go:27-34: \`case Return:\` runs \`l.Stock[m.ProductID] += m.Quantity\` (line 33) and then \`l.restock(m.ProductID, m.Quantity)\` (line 34).
- code (\`Read movement.go\`): movement.go:51-62: Delta returns +Quantity for Purchase and Return.

## Blast Radius

Every product with at least one Return movement has on-hand stock overstated by the total quantity of all its returns.
`

export const TRIAGE_PROMPT = `# Language

- Write the note in en (language.notes: en).

# Playbooks

## 00-environment

# Environment

This workspace is a single Go module, \`sandbox/ledger\`, at the repository root. Build \`go build ./...\` · Test \`go test ./...\` · Vet \`go vet ./...\`.

## 10-helpdesk

The helpdesk is the sandbox desk. Read the thread before the code.

## 50-code

Search with rg. Read whole files.

# Ticket

Key: SBX-1
Title: Product 00219 stock shows 1 more than the movement report
Priority: normal
Tracker URL: https://sandbox.local/tracker/SBX-1
Helpdesk URL: https://sandbox.local/desk/88341
Customer: متجر الفهد للأدوات المنزلية
Customer ID: SBX-CUST-1
Bundle directory: ${RUN_DIR}/bundle

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

export const TRIAGE_DETAIL: RunDetail = {
  runId: '20260915T121105Z-076d',
  key: 'SBX-1',
  helpdeskKey: '88341',
  title: 'Product 00219 stock shows 1 more than the movement report',
  kind: 'triage',
  status: 'completed',
  provider: 'claude',
  model: 'claude-opus-5',
  startedAt: '2026-09-15T12:11:05Z',
  updatedAt: '2026-09-15T12:14:20Z',
  reason: '',
  assignee: 'ops@sandbox.local',
  usage: { turns: 17, inputTokens: 837662, outputTokens: 14823, costUsd: 1.024 },
  notes: [`${RUN_DIR}/note.md`, '/Users/srivathsanv/Documents/Personal/sirdar-sandbox/notes/SBX-1 recording-a-customer-return-adds-its-quantity-to-stock-twice.md'],
  promptPath: `${RUN_DIR}/prompt.md`,
  bundleDir: `${RUN_DIR}/bundle`,
  warnings: [],
  handle: '1e0a60c8',
  budget: { maxTurns: 60, maxMinutes: 20, maxUsd: 5 },
}

/**
 * The triage run's log: eight reads, a thought, two denials and two more
 * calls, an answer, the operator's steer, one more search, the revised
 * answer. Offsets are the mock's.
 */
export function triageEvents(): RunEvent[] {
  ids = 0
  const T = (o: string) => at(o)
  return [
    init(T('00:01'), ROOT),
    stream(T('00:02')),
    ...call(T('00:03'), 'Bash', { command: `ls -la ${ROOT}`, description: 'List repository root files' }, {
      t: T('00:03'),
      text: 'total 48\ndrwxr-xr-x@ 10 srivathsanv  staff   320 15 Sep 14:07 .\n-rw-r--r--@  1 srivathsanv  staff  1801 15 Sep 14:05 ledger.go\n-rw-r--r--@  1 srivathsanv  staff  1680 15 Sep 14:05 movement.go',
    }),
    ...call(T('00:04'), 'Bash', { command: `ls -la ${RUN_DIR}/bundle`, description: 'List ticket bundle directory' }, {
      t: T('00:04'),
      text: 'total 16\n-rw-r--r--  1 srivathsanv  staff  2510 15 Sep 17:41 ticket.json\n-rw-r--r--  1 srivathsanv  staff   901 15 Sep 17:41 thread.md',
    }),
    ...call(T('00:06'), 'Read', { file_path: `${ROOT}/ledger.go` }, { t: T('00:06'), text: LEDGER_GO }),
    ...call(T('00:07'), 'Read', { file_path: `${ROOT}/movement.go` }, { t: T('00:07'), text: '1\tpackage ledger\n2\t\n3\ttype Movement struct {' }),
    ...call(T('00:07'), 'Read', { file_path: `${ROOT}/reconcile.go` }, { t: T('00:07'), text: '1\tpackage ledger\n2\t\n3\tfunc SumMovements() int {' }),
    ...call(T('00:08'), 'Read', { file_path: `${ROOT}/ledger_test.go` }, { t: T('00:08'), text: '1\tpackage ledger\n2\t\n3\timport "testing"' }),
    ...call(T('00:09'), 'Read', { file_path: `${ROOT}/product.go` }, { t: T('00:09'), text: '1\tpackage ledger\n2\t\n3\ttype Product struct{}' }),
    ...call(T('00:10'), 'Read', { file_path: `${RUN_DIR}/bundle/ticket.json` }, { t: T('00:10'), text: '1\t{\n2\t  "Tracker": {\n3\t    "Key": "SBX-1"\n4\t  }\n5\t}' }),
    usage(T('00:10'), 3, 0.12),
    thinking(T('00:11'), 50),
    thinking(T('00:12'), 440),
    ...ruled(
      T('00:19'),
      'Bash',
      { command: `git -C ${ROOT} log --stat --format='%h %ad %s' --date=iso`, description: 'Show commit history with changed files' },
      'deny',
      DENY_GIT_C,
      {},
      { t: T('00:19'), text: 'Sirdar policy: not permitted by permissions.bash', error: true },
    ),
    ...ruled(
      T('00:20'),
      'Bash',
      { command: 'date -j -f %Y-%m-%d 2026-09-12 +%A', description: 'Check weekday of the first customer message date' },
      'deny',
      DENY_DATE,
      {},
      { t: T('00:20'), text: 'Sirdar policy: not permitted by permissions.bash', error: true },
    ),
    ...call(T('00:22'), 'Bash', { command: "git log --stat --format='%h %ad %s' --date=iso", description: 'Show commit history with changed files' }, {
      t: T('00:22'),
      text: 'a9b28cd 2026-09-15 14:06:20 +0530 Add sandbox/ledger: inventory stock tracked from movement history\n\n go.mod         |  3 ++\n ledger.go      | 64 ++++\n 6 files changed, 268 insertions(+)',
    }),
    ...call(T('00:23'), 'Bash', { command: 'rg -n "Return|restock" --type go', description: 'Search Go code for return and restock usages' }, {
      t: T('00:23'),
      text: 'ledger.go:27:\tcase Return:\nledger.go:34:\t\tl.restock(m.ProductID, m.Quantity)\nledger.go:44:func (l *Ledger) restock(productID string, qty int) {\nmovement.go:20:\tReturn',
    }),
    usage(T('00:24'), 9, 0.41),
    thinking(T('00:25'), 1100),
    ...call(T('00:41'), 'StructuredOutput', triageAnswer(), { t: T('01:41'), text: 'Structured output provided successfully' }),
    usage(T('01:41'), 14, 0.72),
    final(T('01:41'), triageAnswer(), 14, 0.72),
    { t: T('02:02'), kind: 'steer', payload: { text: 'Re-check whether the partial-return path is also affected, and say so in one sentence.', continuation: 'resume' } },
    ...call(T('02:06'), 'Bash', { command: 'rg -n -i "partial|Quantity" --type go', description: 'Search Go code for partial-return handling' }, {
      t: T('02:06'),
      text: RG_PARTIAL,
    }),
    thinking(T('02:07'), 300),
    thinking(T('02:08'), 300),
    ...call(T('02:10'), 'StructuredOutput', triageAnswer(true), { t: T('03:13'), text: 'Structured output provided successfully' }),
    usage(T('03:13'), 17, 1.024),
    final(T('03:13'), triageAnswer(true), 17, 1.024),
  ]
}

// ------------------------------------------------------------- the fix run

export const WORKTREE = `${ROOT}/.sirdar/worktrees/20260915T121451Z-bf19`
const FIX_START = '2026-09-15T12:14:51Z'

export const FIX_DETAIL: RunDetail = {
  runId: '20260915T121451Z-bf19',
  key: 'SBX-1',
  helpdeskKey: '88341',
  title: 'Product 00219 stock shows 1 more than the movement report',
  kind: 'fix',
  status: 'completed',
  provider: 'claude',
  model: 'claude-opus-5',
  startedAt: FIX_START,
  updatedAt: '2026-09-15T12:15:24Z',
  reason: '',
  usage: { turns: 8, inputTokens: 339799, outputTokens: 2205, costUsd: 0.5629 },
  notes: [],
  promptPath: `${ROOT}/.sirdar/runs/SBX-1/20260915T121451Z-bf19/prompt.md`,
  bundleDir: `${ROOT}/.sirdar/runs/SBX-1/20260915T121451Z-bf19/bundle`,
  warnings: [],
  handle: '6dffe054',
  budget: { maxTurns: 60, maxMinutes: 20, maxUsd: 5 },
  fix: {
    branch: 'fix-sbx-1-recording-a-customer-return-adds-its-qua',
    base: 'main',
    commit: 'f1449369417839f2c5b4c0658d0523b70092f298',
  },
}

export const FIX_REPORT = {
  summary:
    "Apply a return's quantity to stock once in ApplyMovement\n\nIn the Return case, ApplyMovement added the quantity directly and then again through restock, so a return raised on-hand stock by twice its quantity while the movement report (SumMovements/Delta) counted it once. This change removes the restock call and the restock helper, which had no other caller, and keeps the single direct increment. It also adds TestApplyMovementReturnAddsQuantityOnce to ledger_test.go. The test failed before the fix with CurrentStock = 12.",
  filesChanged: ['ledger.go', 'ledger_test.go'],
  testsRun: [
    { command: 'go test ./... (after adding the regression test, before the fix)', result: 'FAIL: TestApplyMovementReturnAddsQuantityOnce, ledger_test.go:82: CurrentStock = 12, want 11' },
    { command: 'go build ./...', result: 'succeeded, no output' },
    { command: 'go vet ./...', result: 'succeeded, no output' },
    { command: 'go test ./...', result: 'ok  sandbox/ledger 2.070s' },
  ],
  risks:
    'This fixes new returns only. Stock that past returns already inflated stays too high until it is rebuilt from movement history (SumMovements over MovementsFor), and that step is not part of this change.',
  deviationFromNote: '',
}

export const FIX_PATCH = `diff --git a/ledger.go b/ledger.go
--- a/ledger.go
+++ b/ledger.go
@@ -25,13 +25,8 @@ func (l *Ledger) ApplyMovement(m Movement) error {
 	case Sale:
 		l.Stock[m.ProductID] -= m.Quantity
 	case Return:
-		// A returned item goes back on the shelf. restock used to be the
-		// only place that happened, wired up from a separate "item
-		// scanned back in" event; now that ApplyMovement records the
-		// return movement itself too, the quantity is added here once
-		// directly and once again through restock below.
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
 // CurrentStock returns a product's current stock level as the ledger has
 // recorded it. A product with no movements has a stock of 0.
 func (l *Ledger) CurrentStock(productID string) int {
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
+
+	current, report, ok := l.Reconcile("00219")
+	if !ok {
+		t.Fatalf("Reconcile: current stock %d != movement report %d", current, report)
+	}
+}
+
 func TestSumMovementsNetsPurchasesAndSales(t *testing.T) {
 	movements := []Movement{
 		mv("00219", Purchase, 10),
`

const NEW_TEST = `func TestApplyMovementReturnAddsQuantityOnce(t *testing.T) {
	l := NewLedger()
	must(t, l.ApplyMovement(mv("00219", Purchase, 10)))
	must(t, l.ApplyMovement(mv("00219", Return, 1)))
	if got := l.CurrentStock("00219"); got != 11 {
		t.Fatalf("CurrentStock = %d, want 11", got)
	}

	current, report, ok := l.Reconcile("00219")
	if !ok {
		t.Fatalf("Reconcile: current stock %d != movement report %d", current, report)
	}
}

func TestSumMovementsNetsPurchasesAndSales(t *testing.T) {`

/**
 * The fix run up to the moment it asked to run the tests. `blocked` stops
 * the log there, with the `go test` call started and unanswered; the full
 * log carries the approval, the failing run, the fix and the report.
 */
export function fixEvents(upTo: 'blocked' | 'done' = 'done'): RunEvent[] {
  ids = 100
  const T = (o: string) => at(o, FIX_START)
  const head: RunEvent[] = [
    init(T('00:01'), WORKTREE),
    ...call(T('00:04'), 'Read', { file_path: `${WORKTREE}/ledger.go` }, { t: T('00:04'), text: LEDGER_GO }),
    ...call(T('00:05'), 'Read', { file_path: `${WORKTREE}/ledger_test.go` }, { t: T('00:05'), text: '1\tpackage ledger\n2\t\n3\timport "testing"' }),
    thinking(T('00:06'), 120),
    thinking(T('00:08'), 240),
    ...ruled(
      T('00:10'),
      'Edit',
      { file_path: `${WORKTREE}/ledger_test.go`, old_string: 'func TestSumMovementsNetsPurchasesAndSales(t *testing.T) {', new_string: NEW_TEST },
      'allow',
      '',
      { permission_suggestions: [{ type: 'setMode', mode: 'acceptEdits', destination: 'session' }] },
      { t: T('00:10'), text: `The file ${WORKTREE}/ledger_test.go has been updated.` },
    ),
    ...call(T('00:11'), 'Bash', { command: 'go test ./...', description: 'Run tests before the fix' }),
  ]
  if (upTo === 'blocked') return head
  return [
    ...head,
    permission(T('00:12'), 'Bash', 'allow', { command: 'go test ./...' }, '', {
      decision_reason: 'This command requires approval',
      permission_suggestions: [{ type: 'addRules', rules: [{ toolName: 'Bash', ruleContent: 'go test *' }], behavior: 'allow' }],
    }),
    {
      t: T('00:14'),
      kind: 'tool_finished',
      payload: {
        text: 'Exit code 1\n--- FAIL: TestApplyMovementReturnAddsQuantityOnce (0.00s)\n    ledger_test.go:82: CurrentStock = 12, want 11\nFAIL\nFAIL\tsandbox/ledger\t1.425s\nFAIL',
        raw: {
          type: 'user',
          message: { content: [{ type: 'tool_result', tool_use_id: 'toolu_104', is_error: true, content: 'Exit code 1' }] },
        },
      },
    },
    thinking(T('00:16'), 800),
    ...ruled(
      T('00:19'),
      'Edit',
      {
        file_path: `${WORKTREE}/ledger.go`,
        old_string: '\t\t// A returned item goes back on the shelf. restock used to be the\n\t\t// only place that happened\n\t\tl.Stock[m.ProductID] += m.Quantity\n\t\tl.restock(m.ProductID, m.Quantity)',
        new_string: '\t\t// A returned item goes back on the shelf, once.\n\t\tl.Stock[m.ProductID] += m.Quantity',
      },
      'allow',
      '',
      { permission_suggestions: [{ type: 'setMode', mode: 'acceptEdits', destination: 'session' }] },
      { t: T('00:19'), text: `The file ${WORKTREE}/ledger.go has been updated.` },
    ),
    ...ruled(
      T('00:21'),
      'Bash',
      { command: 'go build ./... && go vet ./... && go test ./...', description: 'Build, vet and test after the fix' },
      'allow',
      '',
      { decision_reason: 'This command requires approval' },
      { t: T('00:24'), text: 'ok  \tsandbox/ledger\t2.070s' },
    ),
    ...call(T('00:24'), 'StructuredOutput', FIX_REPORT, { t: T('00:30'), text: 'Structured output provided successfully' }),
    usage(T('00:30'), 8, 0.5629),
    final(T('00:30'), FIX_REPORT, 8, 0.5629),
  ]
}
