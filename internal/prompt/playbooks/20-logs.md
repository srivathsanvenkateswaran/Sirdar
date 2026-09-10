Application and infrastructure logs are the source for what actually ran, in what order, and
with what errors.

## How to use it

- Check the query syntax the datasource uses before trusting a result; a query that silently
  matches nothing looks identical to a query that correctly found no events.
- Before trusting a zero-result query as an absence finding, verify with a control query that
  the same source and filters do return rows for a known event in the same window.
- Narrow by the same identifiers the ticket references (request ID, user ID, trace ID) rather
  than free-text search, which can match unrelated log lines.
- State the timezone the log timestamps are shown in; it is not always the timezone the
  customer reported the issue in.

## Workspace gotchas

<!-- add the mistakes this workspace has already made once -->
