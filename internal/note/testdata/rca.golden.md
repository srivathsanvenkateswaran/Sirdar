---
tags: [support-duty, rca]
tracker_key: OMNI-1
tracker_url: https://tracker.example/OMNI-1
helpdesk_id: 12345
helpdesk_url: https://helpdesk.example/12345
customer: Example Corp
customer_id: cust-1
date_reported: 2026-09-01
date_rca: 2026-09-10
service: omni
classification: code
severity: medium
confidence: high
origin: omni
triage_verdict: confirmed
related:
  - "[[OMNI-1 export-fails]]"
  - "[[OMNI-1 export-fails-resolution]]"
run: run-1
provider: claude-code
---

# Export job times out on large orders

## Summary

The CSV export buffered the entire result set in memory before writing it, so orders over 500 line items exceeded the request timeout. This has been happening since the export feature shipped, but only became visible once customers started placing bulk orders. The fix streams rows to the response instead of buffering them.

## Impact

- Customers affected: 1 confirmed, likely more with large orders
- Records affected: n/a
- Financial impact: none reported
- First occurrence: 2026-06-01
- Detection: customer report
- Time to detect: unknown, likely months

## Timeline

- **2026-09-01T10:00:00Z** Customer reported export failing. (Zoho ticket #12345)
- **2026-09-02T09:00:00Z** Reproduced locally with a 600-line order. (local repro log)

## Root Cause

The export handler buffers every row into a slice before encoding it as CSV, so large orders exceed the request timeout while still building the slice.

Mechanism: Building the full slice before writing means nothing is flushed to the client until encoding finishes, so the request timeout fires first on large orders.

Offending code:

```
rows := make([][]string, 0)
for _, item := range order.Items { rows = append(rows, itemRow(item)) }
writeCSV(w, rows)
```

Code references: internal/export/csv.go:42

## Contributing Factors

- No test exercised an order above 100 line items.

## Evidence

Database:

- `select count(*) from order_items where order_id = 4821` → 612 rows

Logs:

- `service:export level:error` → 12 timeouts in the last 30 days

APM:


Code:

- internal/export/csv.go:42: Buffers all rows before writing.

Attachments:


## Blast Radius

Query: `select count(distinct order_id) from order_items group by order_id having count(*) > 500`
Count: 37 orders in the last 90 days
Scope: systemic
Reasoning: Any order above the row threshold hits the same code path.

## Why Not Caught Earlier

Load testing used small fixture orders, so the buffering cost never showed up.

## Prevention

- [test] Add a load test with a 1000-line order. (owner: platform, ticket: OMNI-2)

## Open Questions


## Triage Review

Verdict: confirmed

Got right: Identified the timeout and the affected file.
Missed: Did not identify the exact buffering mechanism.
Why missed: Triage did not have profiling data available.

## Lessons

- Load test with realistic data sizes, not fixtures.
