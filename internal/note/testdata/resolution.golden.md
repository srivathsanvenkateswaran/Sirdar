---
tags: ["support-duty", "resolution"]
tracker_key: "OMNI-1"
tracker_url: "https://tracker.example/OMNI-1"
helpdesk_id: "12345"
helpdesk_url: "https://helpdesk.example/12345"
customer: "Example Corp"
customer_id: "cust-1"
service: "omni"
resolution_type: "data-fix"
status: "proposed"
pr: "<fill: pr>"
pr_status: "<fill: pr_status>"
approved_by: "<fill: approved_by>"
applied_by: "<fill: applied_by>"
applied_at: "<fill: applied_at>"
verified_at: "<fill: verified_at>"
related:
  - "[[OMNI-1 export-fails]]"
  - "[[OMNI-1 export-fails-rca]]"
run: "run-1"
---

# Backfill the missing export flag for large orders

## What Was Wrong

612 orders had export_enabled left false by a bad migration.

## What We Changed

Ran a one-off update to set export_enabled=true for the affected orders.

### Data change

Authorised by: j.manager at 2026-09-08T12:00:00Z
Executed by: j.engineer at 2026-09-08T13:00:00Z

Pre-verification:

```sql
select count(*) from orders where export_enabled = false and line_items > 500;
```

Pre output: 612

Change:

```sql
update orders set export_enabled = true where export_enabled = false and line_items > 500;
```

Rows affected: 612

Post-verification:

```sql
select count(*) from orders where export_enabled = false and line_items > 500;
```

Post output: 0

Rollback plan: update orders set export_enabled = false where id in (<snapshot ids>);
Side effects: None observed.


## Verification

| Check | Environment | Result | Date | By |
| --- | --- | --- | --- | --- |
| Export a 600-line order | staging | completes in 4s | 2026-09-08 | j.reviewer |

## Customer Outcome

- Told: Explained the fix and asked them to retry.
- Confirmed fixed: yes
- Helpdesk closed: yes
- Tracker status: done

## Residual Risk / Follow-ups

- Very large orders (10k+ lines) are untested.

## Lessons

Add a load test for large orders as a matter of course for export features.
