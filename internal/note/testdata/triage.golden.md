---
tags: ["support-duty", "triage"]
tracker_key: "OMNI-1"
tracker_url: "https://tracker.example/OMNI-1"
helpdesk_id: "12345"
helpdesk_url: "https://helpdesk.example/12345"
customer: "Example Corp"
customer_id: "cust-1"
date: "2026-09-10"
priority: "high"
service: "omni"
status: "triaged"
run: "run-1"
provider: "claude-code"
---

# Sample issue for template checks

Register: [[_Issue Register]] · RCA: <fill: RCA> · Resolution: <fill: Resolution>

## Customer Complaint (translated)

Customer reports the export fails for orders over 500 lines.

## Conversation Summary

- **2026-09-01T10:00:00Z** (customer): Reported the export failing.
- **2026-09-01T10:15:00Z** (L1): Confirmed the failure and escalated.

## Repro Steps

1. Create an order with 500+ line items.
1. Request a CSV export.
1. Observe the export job fail.

## Root Cause Hypothesis

**Classification:** code
**Confidence:** medium

The export job times out serializing more than 500 rows.

Evidence:

- logs (`service:export level:error`): Timeout after 30s on large exports.

Code references: internal/export/csv.go:42

Blast radius: Any customer exporting an order with more than 500 line items.

## Proposed Fix

Stream the export instead of buffering it in memory.

Files: internal/export/csv.go

Remediation SQL:

```sql

```

Risks: Streaming changes the export's error-handling path.

## Open Questions

- Is 500 lines the exact threshold, or does it vary by column count?
