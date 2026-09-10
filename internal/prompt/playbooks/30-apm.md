Application performance monitoring (traces, spans, error rates) shows what a request actually
did once it hit the service, including calls the logs don't capture individually.

## How to use it

- Check the retention window before concluding an event didn't happen; older data may have
  been silently clamped or aggregated away rather than genuinely absent.
- Before citing an absence (a span, an error, a call), run a control query that proves the same
  source captures that kind of event in that window at all.
- A span's timestamp can mark either the start or the end of the operation; check which before
  building a timeline from it.
- Cross-check a suspicious trace against a known-good one from the same endpoint to see what's
  actually different, rather than reasoning from the anomalous trace alone.

## Workspace gotchas

<!-- add the mistakes this workspace has already made once -->
