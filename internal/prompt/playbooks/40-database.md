Direct database access lets you confirm what state the system was actually in, rather than
what the application logic implies it should have been.

## How to use it

- Access is read-only. Never run a statement that could change state, even to "check" something.
- State the timezone the stored timestamps are in; databases frequently store UTC while the
  application displays local time.
- Resolve the customer or record identifier from the ticket to a concrete row before filtering
  further; do not assume a name or email in the ticket maps to a unique account without checking.
- Cite the exact query and row count for any claim; a query that returns zero rows is only
  evidence of absence once you've confirmed it isn't a typo or a scoping mistake.

## Workspace gotchas

<!-- add the mistakes this workspace has already made once -->
