import type { RunDetail, RunDiff, RunEvent } from '../api/types'

/**
 * Three runs for the session screen's tests and the gallery, shaped on the
 * SBX-1 sandbox runs the 2026-09-16 mocks were drawn from: a completed
 * triage with a steer, two denied calls, a search, a listing, a `git log
 * --stat`, two structured answers and a filed note; a completed fix with
 * an edit, a failing test, the fix, a passing check, a report and a review
 * drop; and the same fix run paused before its first test. Every event is
 * in the thin payload plus the Claude raw line `internal/run` writes, so
 * `lib/events` reads them the way it reads a real log.
 */

export interface SessionFixture {
  detail: RunDetail
  events: RunEvent[]
  note: string
  prompt: string
  diff: RunDiff | null
}

const APP = '/repos/sirdar-sandbox/app'
const BUNDLE = `${APP}/.sirdar/runs/SBX-1/20260915T121105Z-076d/bundle`
const WORKTREE = `${APP}/.sirdar/worktrees/20260915T121451Z-bf19`

let ids = 0
function nextId(): string {
  ids += 1
  return `toolu_${String(ids).padStart(4, '0')}`
}

/** Seconds after a start stamp, as an RFC 3339 string. */
export function at(start: string, seconds: number): string {
  return new Date(Date.parse(start) + seconds * 1000).toISOString()
}

/** A Claude `tool_use` block, as `tool_started` carries it. Returns the event and the id its result names. */
export function claudeToolUse(
  tool: string,
  input: Record<string, unknown>,
  t: string,
  id = nextId(),
): { event: RunEvent; id: string } {
  return {
    id,
    event: {
      t,
      kind: 'tool_started',
      payload: {
        tool,
        raw: {
          type: 'assistant',
          message: { model: 'claude-opus-5', content: [{ type: 'tool_use', id, name: tool, input }] },
        },
      },
    },
  }
}

/** The `tool_result` for a call, paired by id; `isError` is what a denied or failed call carries. */
export function claudeToolResult(id: string, text: string, t: string, isError?: boolean): RunEvent {
  return {
    t,
    kind: 'tool_finished',
    payload: {
      text,
      raw: {
        type: 'user',
        message: {
          content: [{ tool_use_id: id, type: 'tool_result', content: text, ...(isError !== undefined ? { is_error: isError } : {}) }],
        },
      },
    },
  }
}

/** The policy's word on a call, as a Claude `control_request` with Sirdar's decision on it. */
export function claudePermission(
  tool: string,
  decision: 'allow' | 'deny' | 'ask',
  input: Record<string, unknown>,
  t: string,
  id: string,
  opts: { text?: string; rules?: string[]; reason?: string } = {},
): RunEvent {
  return {
    t,
    kind: 'permission',
    payload: {
      tool,
      decision,
      ...(opts.text ? { text: opts.text } : {}),
      raw: {
        type: 'control_request',
        request: {
          subtype: 'can_use_tool',
          tool_name: tool,
          input,
          description: typeof input.description === 'string' ? input.description : undefined,
          permission_suggestions: opts.rules
            ? [{ type: 'addRules', rules: opts.rules.map((r) => ({ toolName: tool, ruleContent: r })), behavior: 'allow' }]
            : undefined,
          decision_reason: opts.reason,
          tool_use_id: id,
        },
      },
    },
  }
}

export function usageEvent(turns: number, costUsd: number | undefined, t: string): RunEvent {
  return { t, kind: 'usage', payload: costUsd === undefined ? { turns } : { turns, costUsd } }
}

/** Claude's rate-limit warning, which `internal/run` files as a system line. */
export function rateLimitEvent(utilization: number, t: string, window = 'seven_day'): RunEvent {
  const pct = Math.round(utilization * 100)
  return {
    t,
    kind: 'system',
    payload: {
      text: `rate limit ${window} at ${pct}% of the window`,
      raw: { type: 'rate_limit_event', rate_limit_info: { rateLimitType: window, utilization, status: 'allowed_warning' } },
    },
  }
}

/** A raw stream delta: the noise Show everything reveals. */
export function streamEvent(t: string, text = ''): RunEvent {
  return { t, kind: 'system', payload: { text, raw: { type: 'stream_event', event: { type: 'content_block_delta' } } } }
}

export function steerEvent(text: string, t: string): RunEvent {
  return { t, kind: 'steer', payload: { text, continuation: 'resume' } }
}

export function finalEvent(answer: unknown, turns: number, costUsd: number, t: string): RunEvent {
  return {
    t,
    kind: 'final',
    payload: {
      text: JSON.stringify(answer),
      turns,
      costUsd,
      raw: { type: 'result', subtype: 'success', structured_output: answer, num_turns: turns, total_cost_usd: costUsd },
    },
  }
}

export function reviewEvent(path: string, hunk: number, t: string): RunEvent {
  return { t, kind: 'review', payload: { action: 'drop', path, hunk } }
}

// ------------------------------------------------------------- the triage

export const TRIAGE_START = '2026-09-15T12:11:05Z'
export const TRIAGE_RUN_ID = '20260915T121105Z-076d'

const LEDGER_GO = [
  'package ledger',
  '',
  '// Ledger tracks current stock per product.',
  'type Ledger struct {',
  '\tStock   map[string]int',
  '\tHistory []Movement',
  '}',
  '',
  'func (l *Ledger) ApplyMovement(m Movement) error {',
  '\tswitch m.Type {',
  '\tcase Purchase:',
  '\t\tl.Stock[m.ProductID] += m.Quantity',
  '\tcase Sale:',
  '\t\tl.Stock[m.ProductID] -= m.Quantity',
  '\tcase Return:',
  '\t\t// A returned item goes back on the shelf. restock used to be the',
  '\t\t// only place that happened, wired up from a separate "item',
  '\t\t// scanned back in" event; now that ApplyMovement records the',
  '\t\t// return movement itself too, the quantity is added here once',
  '\t\t// directly and once again through restock below.',
  '\t\tl.Stock[m.ProductID] += m.Quantity',
  '\t\tl.restock(m.ProductID, m.Quantity)',
  '\tcase Adjustment:',
  '\t\tl.Stock[m.ProductID] += m.Quantity',
  '\t}',
  '\treturn nil',
  '}',
  '',
  '// restock puts returned stock back on the shelf.',
  'func (l *Ledger) restock(productID string, qty int) {',
  '\tl.Stock[productID] += qty',
  '}',
]

/** Claude's Read output: `N\tline`, from line 1 or a given start. */
export function numbered(lines: string[], from = 1): string {
  return lines.map((l, i) => `${from + i}\t${l}`).join('\n')
}

const LS_APP = [
  'total 48',
  'drwxr-xr-x@ 10 sri  staff   320 15 Sep 14:07 .',
  'drwxr-xr-x@ 20 sri  staff   640 15 Sep 17:40 ..',
  'drwxr-xr-x@ 13 sri  staff   416 15 Sep 17:40 .git',
  'drwxr-xr-x@  7 sri  staff   224 15 Sep 14:11 .sirdar',
  '-rw-r--r--@  1 sri  staff    39 15 Sep 14:06 go.mod',
  '-rw-r--r--@  1 sri  staff  1987 15 Sep 14:06 ledger.go',
  '-rw-r--r--@  1 sri  staff  2920 15 Sep 14:06 ledger_test.go',
  '-rw-r--r--@  1 sri  staff  1860 15 Sep 14:06 movement.go',
  '-rw-r--r--@  1 sri  staff   567 15 Sep 14:06 product.go',
  '-rw-r--r--@  1 sri  staff  1003 15 Sep 14:06 reconcile.go',
].join('\n')

const GIT_STAT = [
  'a9b28cd 2026-09-15 14:06:20 +0530 Add sandbox/ledger: inventory stock tracked from movement history',
  '',
  ' go.mod         |  3 +',
  ' ledger.go      | 65 ++++++++++++',
  ' ledger_test.go | 94 ++++++++++++++++',
  ' movement.go    | 63 ++++++++++++',
  ' product.go     | 24 +++++',
  ' reconcile.go   | 24 +++++',
  ' 6 files changed, 273 insertions(+)',
].join('\n')

const RG_RETURN = [
  'ledger.go:27:\tcase Return:',
  'ledger.go:28:\t\t// A returned item goes back on the shelf. restock used to be the',
  'ledger.go:32:\t\t// directly and once again through restock below.',
  'ledger.go:33:\t\tl.Stock[m.ProductID] += m.Quantity',
  'ledger.go:34:\t\tl.restock(m.ProductID, m.Quantity)',
  'ledger.go:43:// restock puts returned stock back on the shelf.',
  'ledger.go:44:func (l *Ledger) restock(productID string, qty int) {',
  'movement.go:14:\tReturn',
  'movement.go:53:\tcase Purchase, Return:',
  'reconcile.go:9:\t// A return counts once, as +Quantity.',
].join('\n')

const RG_PARTIAL = [
  'ledger_test.go:9:\treturn Movement{ProductID: productID, Type: typ, Quantity: qty, At: time.Now()}',
  'ledger_test.go:42:\tif err := l.ApplyMovement(Movement{ProductID: "", Type: Purchase, Quantity: 1}); err == nil {',
  'ledger.go:24:\t\tl.Stock[m.ProductID] += m.Quantity',
  'ledger.go:26:\t\tl.Stock[m.ProductID] -= m.Quantity',
  'ledger.go:33:\t\tl.Stock[m.ProductID] += m.Quantity',
  'ledger.go:34:\t\tl.restock(m.ProductID, m.Quantity)',
  'ledger.go:36:\t\tl.Stock[m.ProductID] += m.Quantity',
  'movement.go:26:\tQuantity  int // always positive; the sign is implied by Type',
  'movement.go:36:\tif m.Quantity <= 0 {',
  'movement.go:54:\t\treturn m.Quantity',
  'movement.go:56:\t\treturn -m.Quantity',
].join('\n')

const DENY_PREFIX =
  'Sirdar policy: not permitted by permissions.bash; allowed here: git log*, git show*, git grep*, rg *, ls *, cat *, head *, tail *, ... (see .sirdar/config.yaml). '

const TICKET = {
  key: 'SBX-1',
  title: 'Product 00219 stock shows 1 more than the movement report',
  trackerUrl: 'https://sandbox.local/tracker/SBX-1',
  helpdeskId: '88341',
  helpdeskUrl: 'https://sandbox.local/desk/88341',
  priority: 'normal',
  service: 'sandbox/ledger',
  customer: 'متجر الفهد للأدوات المنزلية',
  customerId: 'SBX-CUST-1',
}

const COMPLAINT_AR =
  '[2026-09-12T09:14:00+03:00]\nالسلام عليكم ورحمة الله\nعندي مشكلة بالمخزون، المنتج رقم 00219 الكمية الظاهرة عندي بالنظام أكبر بواحد عن اللي بالتقرير. رجعت منتج مرتجع الأسبوع اللي فات وبعدها صار الفرق. ممكن تشوفولي الموضوع؟ الله يعطيكم العافية\n\n[2026-09-12T10:41:00+03:00]\nالمرتجع كان يوم الثلاثاء الماضي، رقم الطلب مو متذكره بالضبط بس المنتج نفس الكود 00219. قبل ما أرجعه كانت الكمية بالنظام مطابقة للتقرير، وبعد ما سجلت المرتجع صارت الكمية بالنظام زايدة وحدة عن التقرير.'

const COMPLAINT_EN =
  '(2026-09-12 09:14 +03:00) "Peace be upon you and God\'s mercy. I have a problem with the inventory: for product number 00219, the quantity showing in the system is one more than what\'s in the report. I processed a returned product last week and the difference appeared after that. Could you look into it for me? May God give you strength."\n\n(2026-09-12 10:41 +03:00, answering L1\'s request for the exact return date) "The return was last Tuesday. I don\'t remember the order number exactly, but it\'s the same product code, 00219. Before I returned it, the quantity in the system matched the report, and after I recorded the return the quantity in the system became one more than the report."'

const REPLY_AR =
  'وعليكم السلام أستاذ أحمد،\nشكراً على التفاصيل اللي أرسلتها بخصوص المنتج 00219 والفرق بين الكمية بالنظام والتقرير بعد تسجيل المرتجع. الموضوع حالياً عند الفريق الفني وقاعدين نراجعه، وراح نتواصل معك أول ما يكون عندنا تحديث.\nالله يعطيك العافية.'

const HYPOTHESIS =
  'In Ledger.ApplyMovement, the Return case adds the return quantity to the running stock twice: once directly at ledger.go:33 and again through l.restock at ledger.go:34, which does its own `+=` at ledger.go:45. The movement report (SumMovements/Movement.Delta) counts a return once, as +Quantity. After any return, on-hand stock therefore sits above the report by that return\'s quantity. A 1-unit return gives the +1 gap the customer sees. Purchase, Sale and Adjustment each apply their quantity once, so Return is the only case that disagrees.'

const EVIDENCE = [
  {
    source: 'code',
    query: 'Read ledger.go',
    finding:
      'ledger.go:27-34: `case Return:` runs `l.Stock[m.ProductID] += m.Quantity` (line 33) and then `l.restock(m.ProductID, m.Quantity)` (line 34). ledger.go:44-46: restock does `l.Stock[productID] += qty`. Net effect on Stock is +2×Quantity.',
  },
  {
    source: 'code',
    query: 'Read movement.go',
    finding: 'movement.go:51-62: Delta returns +Quantity for Purchase and Return, −Quantity for Sale and +Quantity for Adjustment. A return adds +1×Quantity to the report.',
  },
  {
    source: 'code',
    query: 'Read reconcile.go',
    finding: 'reconcile.go:7-13: SumMovements sums m.Delta() over the history. reconcile.go:19-23: Reconcile compares CurrentStock with that sum. The drift is in the Stock fold.',
  },
  {
    source: 'code',
    query: 'rg -n "Return|restock" --type go',
    finding: 'restock is defined at ledger.go:44 and its only caller is ledger.go:34, so no other code path in this module reaches it. No test in ledger_test.go refers to Return.',
  },
  {
    source: 'code',
    query: 'rg -n -i "partial|Quantity" --type go',
    finding: 'No match for "partial" in the module. The Movement struct (movement.go:23-29) has no field linking a return to its original sale, so a partial return is just a Return movement with a smaller Quantity. It goes through the same double increment at ledger.go:33-34.',
  },
  {
    source: 'code',
    query: 'Read ledger_test.go',
    finding: 'Tests cover Purchase (line 12), Sale (line 22), Adjustment (line 31) and Reconcile with Purchase, Sale and Adjustment only (lines 65-86). No test applies a Return movement.',
  },
  {
    source: 'git',
    query: "git log --stat --format='%h %ad %s' --date=iso",
    finding: 'The repository has one commit: a9b28cd at 2026-09-15 14:06:20 +0530, "Add sandbox/ledger: inventory stock tracked from movement history". Git history can\'t show when the direct increment was added next to restock.',
  },
  {
    source: 'helpdesk thread',
    query: 'ticket.json Thread[0], Thread[2]',
    finding: 'The customer says stock matched the report before the return and was 1 higher right after recording it. This matches a defect that only fires on Return movements, provided the return quantity was 1, which the customer has not stated.',
  },
]

function triageAnswer(withPartial: boolean): Record<string, unknown> {
  return {
    ticket: TICKET,
    title: 'Recording a customer return adds its quantity to stock twice, so on-hand stock drifts above the movement report',
    complaint: COMPLAINT_EN,
    complaintOriginal: COMPLAINT_AR,
    customerReplyDraft: { language: 'ar', text: REPLY_AR },
    timeline: [
      { at: '2026-09-12T09:14:00+03:00', role: 'customer', summary: 'أحمد الفهد reports product 00219\'s on-hand quantity is 1 higher than the report, since a return last week.' },
      { at: '2026-09-12T10:05:00+03:00', role: 'agent (Layla, L1)', summary: 'Asks the customer to confirm the exact date of the return. Reasonable; no incorrect guidance.' },
      { at: '2026-09-12T10:41:00+03:00', role: 'customer', summary: 'The return was last Tuesday; doesn\'t remember the order number. Stock matched before, 1 higher after.' },
    ],
    reproSteps: [
      'l := NewLedger()',
      'l.ApplyMovement(Movement{ProductID: "00219", Type: Purchase, Quantity: 10}) → CurrentStock("00219") = 10.',
      'l.ApplyMovement(Movement{ProductID: "00219", Type: Return, Quantity: 1})',
      'Expected: CurrentStock("00219") = 11. Actual: ledger.go:33 adds 1 and ledger.go:34 → restock (ledger.go:45) adds another 1, so CurrentStock = 12.',
    ],
    rootCause: {
      hypothesis: withPartial
        ? `${HYPOTHESIS}\n\nPartial returns are affected too. This module has no separate partial-return path, so returning fewer units than were sold is an ordinary Return movement with a smaller Quantity and is double-counted by that amount.`
        : HYPOTHESIS,
      confidence: 'high',
      evidence: withPartial ? EVIDENCE : EVIDENCE.filter((e) => !/partial/.test(e.query)),
      codeRefs: ['ledger.go:27-34', 'ledger.go:44-46', 'movement.go:23-29', 'movement.go:51-62', 'reconcile.go:7-13'],
    },
    blastRadius:
      'From the code: every product, for every customer using this ledger, that has at least one Return movement has on-hand stock overstated by the total quantity of all its returns. Purchases, sales and adjustments are not affected. I could not count affected customers because this run has no database, log or APM source.',
    classification: 'code',
    proposedFix: {
      description:
        'Apply a return\'s quantity once in ApplyMovement. The smallest change is to delete one of the two increments in the Return case: either remove line 33 and keep restock, or remove the restock call at line 34 along with the now-unused restock helper at ledger.go:43-46. Add a regression test in ledger_test.go: Purchase 10, Return 1, then assert CurrentStock == 11 and Reconcile ok.',
      files: ['ledger.go', 'ledger_test.go'],
      remediationSql: 'None provided. No database source is configured for this run and this module keeps Stock as an in-memory running map (ledger.go:5-8).',
      risks:
        'If the separate "item scanned back in" event mentioned at ledger.go:28-31 still exists in some other service and adds stock on its own, returns may be counted more than twice in production.',
    },
    openQuestions: [
      'I couldn\'t check the customer\'s actual movement history for product 00219: no database, log or APM source is configured for this run.',
      'Which date is the return? "Last Tuesday", written on Saturday 2026-09-12, most likely means 2026-09-08 but could mean 2026-09-01.',
      'Is the separate "item scanned back in" event that used to drive restock (ledger.go:28-31) still wired up outside this module?',
    ],
  }
}

/** The filed note, as `internal/note` writes it from the answer above. */
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

${COMPLAINT_EN}

Tone: polite and calm, not escalated. The customer is asking for a check, not demanding anything urgently.

## Customer Complaint (original)

<div dir="rtl">

${COMPLAINT_AR}

</div>

## Conversation Summary

- **2026-09-12T09:14:00+03:00** (customer): أحمد الفهد reports product 00219's on-hand quantity is 1 higher than the report, since a return last week.
- **2026-09-12T10:05:00+03:00** (agent (Layla, L1)): Asks the customer to confirm the exact date of the return. Reasonable; no incorrect guidance.
- **2026-09-12T10:41:00+03:00** (customer): The return was last Tuesday; doesn't remember the order number. Stock matched before, 1 higher after.

## Repro Steps

1. \`l := NewLedger()\`
2. \`l.ApplyMovement(Movement{ProductID: "00219", Type: Purchase, Quantity: 10})\` → CurrentStock("00219") = 10.
3. \`l.ApplyMovement(Movement{ProductID: "00219", Type: Return, Quantity: 1})\`
4. Expected: CurrentStock("00219") = 11. Actual: ledger.go:33 adds 1 and ledger.go:34 → restock (ledger.go:45) adds another 1, so CurrentStock = 12.

## Root Cause Hypothesis

**Classification:** code
**Confidence:** high

${HYPOTHESIS}

Partial returns are affected too. This module has no separate partial-return path, so returning fewer units than were sold is an ordinary Return movement with a smaller Quantity and is double-counted by that amount.

Evidence:

${EVIDENCE.map((e) => `- ${e.source} (\`${e.query}\`): ${e.finding}`).join('\n')}

Code references: ledger.go:27-34, ledger.go:44-46, movement.go:23-29, movement.go:51-62, reconcile.go:7-13

Blast radius: From the code: every product, for every customer using this ledger, that has at least one Return movement has on-hand stock overstated by the total quantity of all its returns. Purchases, sales and adjustments are not affected. I could not count affected customers because this run has no database, log or APM source.

## Proposed Fix

Apply a return's quantity once in ApplyMovement. The smallest change is to delete one of the two increments in the Return case: either remove line 33 and keep restock, or remove the restock call at line 34 along with the now-unused restock helper at ledger.go:43-46. Add a regression test in ledger_test.go: Purchase 10, Return 1, then assert CurrentStock == 11 and Reconcile ok.

Files: ledger.go, ledger_test.go

Remediation SQL:

\`\`\`sql
None provided. No database source is configured for this run and this module keeps Stock as an in-memory running map (ledger.go:5-8).
\`\`\`

Risks: If the separate "item scanned back in" event mentioned at ledger.go:28-31 still exists in some other service and adds stock on its own, returns may be counted more than twice in production.

## Open Questions

- I couldn't check the customer's actual movement history for product 00219: no database, log or APM source is configured for this run.
- Which date is the return? "Last Tuesday", written on Saturday 2026-09-12, most likely means 2026-09-08 but could mean 2026-09-01.
- Is the separate "item scanned back in" event that used to drive restock (ledger.go:28-31) still wired up outside this module?

## Customer reply draft

Language: ar. A draft, not a sent reply: read it before you send it. It promises nothing the ticket does not already record.

<div dir="rtl">

${REPLY_AR}

</div>
`

export const TRIAGE_PROMPT = `You are a support engineer on duty.

# Language

- Write the note in en (language.notes: en).

# Playbooks

## 00-environment

# Environment

- Build: \`go build ./...\`
- Test: \`go test ./...\`

## 10-helpdesk

Read the whole thread, not just the last message.

## 50-code

Cite every code claim as file:line.

# Ticket

Key: SBX-1
Title: Product 00219 stock shows 1 more than the movement report
Priority: normal
Tracker URL: https://sandbox.local/tracker/SBX-1
Helpdesk URL: https://sandbox.local/desk/88341
Customer: متجر الفهد للأدوات المنزلية
Customer ID: SBX-CUST-1
Bundle directory: ${BUNDLE}

Files:
(none)

## Conversation

\`\`\`
# Conversation (original language)

## 2026-09-12T09:14:00+03:00 · customer · أحمد الفهد

السلام عليكم ورحمة الله
عندي مشكلة بالمخزون، المنتج رقم 00219 الكمية الظاهرة عندي بالنظام أكبر بواحد عن اللي بالتقرير. رجعت منتج مرتجع الأسبوع اللي فات وبعدها صار الفرق. ممكن تشوفولي الموضوع؟ الله يعطيكم العافية

## 2026-09-12T10:05:00+03:00 · agent · Layla (L1)

وعليكم السلام، شكراً تواصلكم معنا. ممكن تأكدلنا تاريخ المرتجع بالضبط حتى نراجع الحركة؟

## 2026-09-12T10:41:00+03:00 · customer · أحمد الفهد

المرتجع كان يوم الثلاثاء الماضي، رقم الطلب مو متذكره بالضبط بس المنتج نفس الكود 00219. قبل ما أرجعه كانت الكمية بالنظام مطابقة للتقرير، وبعد ما سجلت المرتجع صارت الكمية بالنظام زايدة وحدة عن التقرير.

## 2026-09-13T11:02:00+03:00 · agent · Layla (L1)

تمام، وصلتنا التفاصيل. راح نراجع سجل حركة المنتج 00219 ونرجعلك بالنتيجة.
\`\`\`

# Output

- ticket identifies the record.
`

export function triageDetail(over: Partial<RunDetail> = {}): RunDetail {
  return {
    runId: TRIAGE_RUN_ID,
    key: 'SBX-1',
    helpdeskKey: '88341',
    title: 'Product 00219 stock shows 1 more than the movement report',
    kind: 'triage',
    status: 'completed',
    provider: 'claude',
    model: 'opus',
    startedAt: TRIAGE_START,
    updatedAt: at(TRIAGE_START, 195),
    reason: '',
    assignee: 'ops@sandbox.local',
    mine: false,
    usage: { turns: 17, inputTokens: 837662, outputTokens: 14823, costUsd: 1.024 },
    notes: [
      `${APP}/.sirdar/runs/SBX-1/${TRIAGE_RUN_ID}/note.md`,
      '/repos/sirdar-sandbox/notes/SBX-1 recording-a-customer-return-adds-its-quantity-to-stock-twice.md',
    ],
    promptPath: `${APP}/.sirdar/runs/SBX-1/${TRIAGE_RUN_ID}/prompt.md`,
    bundleDir: BUNDLE,
    warnings: [],
    handle: '1e0a60c8-0362-43b9-85a5-cc45202df02d',
    budget: { maxTurns: 60, maxMinutes: 20, maxUsd: 5 },
    ...over,
  }
}

/** The triage run's log: fifteen calls in seventeen turns, a steer at 02:02. */
export function triageEvents(): RunEvent[] {
  const S = TRIAGE_START
  const out: RunEvent[] = []
  const call = (
    tool: string,
    input: Record<string, unknown>,
    sec: number,
    result: string,
    opts: { deny?: string; ms?: number; error?: boolean } = {},
  ) => {
    const { event, id } = claudeToolUse(tool, input, at(S, sec))
    out.push(event)
    if (opts.deny) {
      out.push(claudePermission(tool, 'deny', input, at(S, sec), id, { text: DENY_PREFIX + opts.deny }))
      out.push(claudeToolResult(id, DENY_PREFIX + opts.deny, at(S, sec + 0.05), true))
      return
    }
    const ms = opts.ms ?? 30
    out.push(
      claudeToolResult(id, result, at(S, sec + ms / 1000), tool === 'Bash' ? Boolean(opts.error) : undefined),
    )
  }
  const usage = (turns: number, sec: number) => out.push(usageEvent(turns, undefined, at(S, sec)))

  call('Bash', { command: `ls -la ${APP}`, description: 'List repository root files' }, 3, LS_APP, { ms: 70 })
  usage(1, 3.5)
  out.push(rateLimitEvent(0.9, at(S, 4)))
  call('Bash', { command: `ls -la ${BUNDLE}`, description: 'List ticket bundle directory' }, 4, 'total 16\ndrwxr-xr-x@ 5 sri  staff  160 15 Sep 17:41 .\ndrwxr-xr-x@ 6 sri  staff  192 15 Sep 17:41 ..\ndrwxr-xr-x@ 2 sri  staff   64 15 Sep 17:41 attachments\n-rw-r--r--@ 1 sri  staff 1306 15 Sep 17:41 thread.md\n-rw-r--r--@ 1 sri  staff 2890 15 Sep 17:41 ticket.json')
  usage(2, 4.5)
  call('Read', { file_path: `${APP}/ledger.go` }, 6, numbered(LEDGER_GO), { ms: 10 })
  usage(3, 6.5)
  call('Read', { file_path: `${APP}/movement.go` }, 7, numbered(['package ledger', '', 'type MovementType int', '', 'const (', '\tPurchase MovementType = iota', '\tSale', '\tReturn', '\tAdjustment', ')']), { ms: 8 })
  usage(4, 7.5)
  call('Read', { file_path: `${APP}/reconcile.go` }, 7.6, numbered(['package ledger', '', '// SumMovements returns the net stock change.', 'func SumMovements(ms []Movement) int {', '\tsum := 0', '\treturn sum', '}']), { ms: 8 })
  usage(5, 8)
  call('Read', { file_path: `${APP}/ledger_test.go` }, 8.2, numbered(['package ledger', '', 'import "testing"', '', 'func TestPurchase(t *testing.T) {}']), { ms: 9 })
  usage(6, 8.6)
  call('Read', { file_path: `${APP}/product.go` }, 9, numbered(['package ledger', '', 'type Product struct{ ID string }']), { ms: 8 })
  usage(7, 9.5)
  call('Read', { file_path: `${BUNDLE}/ticket.json` }, 10, numbered(['{', '  "Tracker": {', '    "Key": "SBX-1"', '  }', '}']), { ms: 9 })
  usage(8, 10.5)
  call(
    'Bash',
    { command: `git -C ${APP} log --stat --format='%h %ad %s' --date=iso`, description: 'Show commit history with changed files' },
    19,
    '',
    {
      deny:
        `"git -C ${APP} log --stat --format='%h %ad %s' --date=iso" passes git "-C" before the subcommand, which moves where git reads its configuration or runs code from for everything that follows; no allow-list pattern approves it`,
    },
  )
  usage(9, 19.5)
  call(
    'Bash',
    { command: 'date -j -f %Y-%m-%d 2026-09-12 +%A', description: 'Check weekday of the first customer message date' },
    20,
    '',
    { deny: '"date -j -f %Y-%m-%d 2026-09-12 +%A" is not in the allow-list; every segment of a pipeline or compound command has to match' },
  )
  usage(10, 20.5)
  call('Bash', { command: "git log --stat --format='%h %ad %s' --date=iso", description: 'Show commit history with changed files' }, 22, GIT_STAT, { ms: 40 })
  usage(11, 22.5)
  call('Bash', { command: 'rg -n "Return|restock" --type go', description: 'Search Go code for return and restock usages' }, 23, RG_RETURN, { ms: 60 })
  usage(12, 23.5)
  call('StructuredOutput', triageAnswer(false), 101, 'Structured output provided successfully', { ms: 5 })
  usage(13, 101.5)
  out.push(finalEvent(triageAnswer(false), 14, 0.72, at(S, 102)))
  out.push(steerEvent('Re-check whether the partial-return path is also affected, and say so in one sentence.', at(S, 122)))
  out.push(rateLimitEvent(0.91, at(S, 126)))
  call('Bash', { command: 'rg -n -i "partial|Quantity" --type go', description: 'Search Go code for partial-return handling' }, 126, RG_PARTIAL, { ms: 80 })
  usage(15, 126.5)
  call('StructuredOutput', triageAnswer(true), 193, 'Structured output provided successfully', { ms: 5 })
  usage(17, 193.5)
  out.push(finalEvent(triageAnswer(true), 3, 0.3, at(S, 194)))
  return out
}

export function triageFixture(over: Partial<RunDetail> = {}): SessionFixture {
  return { detail: triageDetail(over), events: triageEvents(), note: TRIAGE_NOTE, prompt: TRIAGE_PROMPT, diff: null }
}

// ---------------------------------------------------------------- the fix

export const FIX_START = '2026-09-15T12:14:51Z'
export const FIX_RUN_ID = '20260915T121451Z-bf19'
export const FIX_BRANCH = 'fix-sbx-1-recording-a-customer-return-adds-its-qua'

const TEST_ADDED = [
  'func TestApplyMovementReturnAddsQuantityOnce(t *testing.T) {',
  '\tl := NewLedger()',
  '\tmust(t, l.ApplyMovement(mv("00219", Purchase, 10)))',
  '\tmust(t, l.ApplyMovement(mv("00219", Return, 1)))',
  '\tif got := l.CurrentStock("00219"); got != 11 {',
  '\t\tt.Fatalf("CurrentStock = %d, want 11", got)',
  '\t}',
  '',
  '\tcurrent, report, ok := l.Reconcile("00219")',
  '\tif !ok {',
  '\t\tt.Fatalf("Reconcile: current stock %d != movement report %d", current, report)',
  '\t}',
  '}',
  '',
]

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

${TEST_ADDED.map((l) => `+${l}`).join('\n')}
 func TestSumMovementsNetsPurchasesAndSales(t *testing.T) {
 	movements := []Movement{
`

export function fixDiff(over: Partial<RunDiff> = {}): RunDiff {
  return {
    base: 'main',
    head: FIX_BRANCH,
    branch: FIX_BRANCH,
    worktree: WORKTREE,
    worktreePresent: true,
    pushed: false,
    files: [
      { path: 'ledger.go', status: 'modified', additions: 1, deletions: 11 },
      { path: 'ledger_test.go', status: 'modified', additions: 14, deletions: 0 },
    ],
    patch: FIX_PATCH,
    etag: 'etag-fix-1',
    ...over,
  }
}

export const FIX_REPORT = {
  summary:
    "Apply a return's quantity to stock once in ApplyMovement\n\nIn the Return case, ApplyMovement added the quantity directly and then again through restock, so a return raised on-hand stock by twice its quantity while the movement report (SumMovements/Delta) counted it once. This change removes the restock call and the restock helper, which had no other caller, and keeps the single direct increment. It also adds TestApplyMovementReturnAddsQuantityOnce to ledger_test.go (Purchase 10, Return 1, then assert CurrentStock == 11 and Reconcile ok). The test failed before the fix with CurrentStock = 12.",
  filesChanged: ['ledger.go', 'ledger_test.go'],
  testsRun: [
    { command: 'go test ./... (after adding the regression test, before the fix)', result: 'FAIL: TestApplyMovementReturnAddsQuantityOnce, ledger_test.go:82: CurrentStock = 12, want 11' },
    { command: 'go build ./...', result: 'succeeded, no output' },
    { command: 'go vet ./...', result: 'succeeded, no output' },
    { command: 'go test ./...', result: 'ok  sandbox/ledger 2.070s' },
  ],
  risks:
    'This fixes new returns only. Stock that past returns already inflated stays too high until it is rebuilt from movement history (SumMovements over MovementsFor), and that step is not part of this change. The note also raises another risk this module can\'t answer: if some other integration still sends a separate "item scanned back in" event that adds stock, returns would still be over-counted in production.',
  deviationFromNote: '',
}

const GO_TEST_FAIL = [
  '--- FAIL: TestApplyMovementReturnAddsQuantityOnce (0.00s)',
  '    ledger_test.go:82: CurrentStock = 12, want 11',
  'FAIL',
  'FAIL\tsandbox/ledger\t1.425s',
  'FAIL',
].join('\n')

export function fixDetail(over: Partial<RunDetail> = {}): RunDetail {
  return {
    runId: FIX_RUN_ID,
    key: 'SBX-1',
    helpdeskKey: '88341',
    title: 'Product 00219 stock shows 1 more than the movement report',
    kind: 'fix',
    status: 'completed',
    provider: 'claude',
    model: 'opus',
    startedAt: FIX_START,
    updatedAt: at(FIX_START, 33),
    reason: '',
    assignee: 'ops@sandbox.local',
    mine: false,
    usage: { turns: 8, inputTokens: 339799, outputTokens: 2205, costUsd: 0.563 },
    notes: [],
    promptPath: `${APP}/.sirdar/runs/SBX-1/${FIX_RUN_ID}/prompt.md`,
    bundleDir: `${APP}/.sirdar/runs/SBX-1/${FIX_RUN_ID}/bundle`,
    warnings: [`fix branch: ${FIX_BRANCH}`],
    handle: '6dffe054-23e1-4ee4-a243-6ef96fa00f25',
    budget: { maxTurns: 60, maxMinutes: 20, maxUsd: 5 },
    fix: { branch: FIX_BRANCH, base: 'main', commit: 'f1449369417839f2c5b4c0658d0523b70092f298' },
    ...over,
  }
}

/**
 * The fix run's log. `upTo` cuts it before the first test's result, which
 * is where the blocked fixture stands: the call is asked for and nothing
 * has come back.
 */
export function fixEvents(upTo: 'blocked' | 'end' = 'end'): RunEvent[] {
  const S = FIX_START
  const out: RunEvent[] = []
  const usage = (turns: number, sec: number) => out.push(usageEvent(turns, undefined, at(S, sec)))

  const r1 = claudeToolUse('Read', { file_path: `${WORKTREE}/ledger.go` }, at(S, 3))
  out.push(r1.event, claudeToolResult(r1.id, numbered(LEDGER_GO), at(S, 3.03)))
  usage(1, 3.1)
  const r2 = claudeToolUse('Read', { file_path: `${WORKTREE}/ledger_test.go` }, at(S, 3.5))
  out.push(r2.event, claudeToolResult(r2.id, numbered(['package ledger', '', 'import "testing"']), at(S, 3.53)))
  usage(2, 3.6)

  const editTest = {
    file_path: `${WORKTREE}/ledger_test.go`,
    old_string: 'func TestSumMovementsNetsPurchasesAndSales(t *testing.T) {',
    new_string: `${TEST_ADDED.join('\n')}\nfunc TestSumMovementsNetsPurchasesAndSales(t *testing.T) {`,
    replace_all: false,
  }
  const e1 = claudeToolUse('Edit', editTest, at(S, 10))
  out.push(e1.event, claudePermission('Edit', 'allow', editTest, at(S, 10), e1.id))
  out.push(claudeToolResult(e1.id, `The file ${WORKTREE}/ledger_test.go has been updated.`, at(S, 10.1)))
  usage(3, 10.2)

  const test1 = { command: 'go test ./...', description: 'Run tests before the fix' }
  const t1 = claudeToolUse('Bash', test1, at(S, 11))
  out.push(t1.event)
  if (upTo === 'blocked') {
    out.push(
      claudePermission('Bash', 'ask', test1, at(S, 11), t1.id, {
        text: 'This command requires approval',
        rules: ['go test *'],
        reason: 'This command requires approval',
      }),
    )
    return out
  }
  out.push(claudePermission('Bash', 'allow', test1, at(S, 11), t1.id, { rules: ['go test *'], reason: 'This command requires approval' }))
  out.push(claudeToolResult(t1.id, `Exit code 1\n${GO_TEST_FAIL}`, at(S, 12.99), true))
  usage(4, 13)

  const editLedger = {
    file_path: `${WORKTREE}/ledger.go`,
    old_string: LEDGER_GO.slice(15, 32).join('\n'),
    new_string: ['\t\t// A returned item goes back on the shelf, once.', '\t\tl.Stock[m.ProductID] += m.Quantity', '\tcase Adjustment:', '\t\tl.Stock[m.ProductID] += m.Quantity', '\t}', '\treturn nil', '}'].join('\n'),
    replace_all: false,
  }
  const e2 = claudeToolUse('Edit', editLedger, at(S, 19))
  out.push(e2.event, claudePermission('Edit', 'allow', editLedger, at(S, 19), e2.id))
  out.push(claudeToolResult(e2.id, `The file ${WORKTREE}/ledger.go has been updated.`, at(S, 19.1)))
  usage(5, 19.2)

  const check = { command: 'go build ./... && go vet ./... && go test ./...', description: 'Build, vet and test after the fix' }
  const t2 = claudeToolUse('Bash', check, at(S, 20))
  out.push(t2.event, claudePermission('Bash', 'allow', check, at(S, 20), t2.id, { rules: ['go build *', 'go vet *', 'go test *'] }))
  out.push(claudeToolResult(t2.id, 'ok  \tsandbox/ledger\t2.070s', at(S, 22.07), false))
  usage(6, 22.1)

  const so = claudeToolUse('StructuredOutput', FIX_REPORT, at(S, 30))
  out.push(so.event, claudeToolResult(so.id, 'Structured output provided successfully', at(S, 30.01)))
  usage(8, 30.1)
  out.push(finalEvent(FIX_REPORT, 8, 0.563, at(S, 30.2)))
  out.push(reviewEvent('ledger_test.go', 0, at(S, 49)))
  return out
}

export function fixFixture(over: Partial<RunDetail> = {}): SessionFixture {
  return { detail: fixDetail(over), events: fixEvents('end'), note: '', prompt: TRIAGE_PROMPT, diff: fixDiff() }
}

/** The fix run at 00:11, waiting for the reader to allow `go test ./...`. */
export function blockedFixture(over: Partial<RunDetail> = {}): SessionFixture {
  return {
    detail: fixDetail({
      status: 'blocked',
      reason: 'agent asked: Run `go test ./...` in the worktree?',
      updatedAt: at(FIX_START, 11),
      usage: { turns: 4, inputTokens: 120000, outputTokens: 800, costUsd: 0 },
      fix: { branch: FIX_BRANCH, base: 'main' },
      ...over,
    }),
    events: fixEvents('blocked'),
    note: '',
    prompt: TRIAGE_PROMPT,
    diff: fixDiff({
      files: [{ path: 'ledger_test.go', status: 'modified', additions: 14, deletions: 0 }],
      patch: FIX_PATCH.slice(FIX_PATCH.indexOf('diff --git a/ledger_test.go')),
      etag: 'etag-fix-0',
    }),
  }
}
