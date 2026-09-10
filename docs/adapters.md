# Writing a source adapter

Sirdar reads tickets from a tracker (e.g. Jira) and a helpdesk (e.g. Zoho
Desk) through *adapters*: separate processes that speak a small
line-delimited JSON protocol over stdin/stdout. Sirdar spawns the adapter,
sends it requests, and reads its responses; the adapter can be written in
any language and can hold whatever credentials or vendor-specific logic it
needs, none of which touches Sirdar's process or its Go types.

This keeps vendor integrations, and any credentials they need, out of
Sirdar's core and out of this repository. An adapter for a proprietary
helpdesk can live in a private repo and never be upstreamed.

`internal/source/plugin` implements the Sirdar-side client (`Start`,
`Client`) that speaks this protocol against a spawned adapter process.
`examples/adapters/file` is a reference adapter, used by the client's own
tests and by `sirdar --dry-run`, that serves the protocol from a static
JSON fixture instead of a real API.

## Transport

The adapter is launched as a subprocess. Its stdin carries one JSON
**request** object per line; its stdout carries one JSON **response**
object per line, in reply to each request, matched by `id`. Anything the
adapter writes to stderr is passed through to Sirdar's own stderr for
diagnostics and is never parsed — an adapter can log freely there.

A request:

```json
{"id": 1, "method": "tracker.get", "params": {"key": "OMNI-1"}}
```

A response, on success:

```json
{"id": 1, "result": {"Key": "OMNI-1", "Title": "Export fails", ...}}
```

or, on failure:

```json
{"id": 1, "error": {"code": "not_found", "message": "tracker ticket not found: OMNI-1"}}
```

`result` and `error` are mutually exclusive. `id` echoes the request's
`id`; adapter output that doesn't parse as JSON, or whose `id` doesn't
match the request just sent, is skipped rather than treated as an error —
this lets an adapter emit occasional stray lines (e.g. a startup banner)
without breaking the exchange, though adapters should prefer stderr for
anything that isn't a response.

Requests are sent one at a time and each is answered before the next is
sent, so an adapter does not need to handle concurrent requests or
out-of-order responses.

## Methods

| method                 | params                          | result                              |
|-------------------------|----------------------------------|--------------------------------------|
| `describe`               | none                              | `{"name","roles","version"}`         |
| `tracker.get`             | `{"key"}`                          | a `ticket.TrackerTicket`               |
| `tracker.list`            | `{"assignee","status","parent","limit"}` (any may be absent) | `[]ticket.TrackerTicket`     |
| `helpdesk.get`            | `{"id"}`                           | a `ticket.HelpdeskTicket`              |
| `helpdesk.threads`        | `{"id"}`                           | a `ticket.Thread` (`[]ticket.Message`) |
| `helpdesk.attachments`    | `{"id","dir"}`                     | `[]ticket.Attachment`                  |
| `shutdown`                | none                              | `null`                               |

Ticket types are defined in `internal/ticket`; their JSON field names are
the Go field names verbatim (`Key`, `Title`, `HelpdeskRef`, and so on —
capitalized, no renaming). An adapter that fulfils only one role need not
implement the other role's methods at all; Sirdar checks `describe`'s
`roles` before calling them and never sends a method the adapter didn't
advertise.

### `describe`

Returns the adapter's identity and capabilities. `roles` is a subset of
`["tracker", "helpdesk"]`; an adapter may support one or both. Sirdar
calls `describe` once, lazily, on first use, and caches the result — an
adapter is not asked to `describe` itself more than once per process.

```json
{"id": 1, "result": {"name": "file", "roles": ["tracker", "helpdesk"], "version": "1"}}
```

### `tracker.get` / `tracker.list`

`tracker.get` fetches one ticket by key; `tracker.list` fetches many,
filtered by `assignee`, `status`, `parent` (all optional, all substring-or-
equality is the adapter's choice) and capped at `limit` (0 or absent =
adapter's own default/no cap). An adapter may choose to ignore filters it
doesn't support rather than error — the reference file adapter ignores
all of them and always returns its full fixture.

### `helpdesk.get` / `helpdesk.threads`

Fetch the helpdesk ticket record and its conversation thread,
respectively, both keyed by the helpdesk ticket's own id (not the tracker
key — Sirdar resolves `TrackerTicket.HelpdeskRef`, or falls back to the
tracker key itself, before calling these).

### `helpdesk.attachments`

Downloads every attachment referenced by the ticket's thread into `dir`
(an absolute path; the adapter must create it if missing) and returns
their metadata. Each file is written as `<n>-<name>`, where `n` is the
attachment's 1-based position, to avoid collisions between attachments
that share a filename. The returned `Attachment.Path` is relative to the
*bundle* directory — `dir`'s parent, i.e. the ticket's working directory —
not to `dir` itself:

```json
{"id": "555", "dir": "/abs/bundle/attachments"}
```

```json
{"id": 1, "result": [
  {"ID": "a1", "Name": "shot.png", "MIME": "image/png", "Path": "attachments/1-shot.png"}
]}
```

An adapter that fails to fetch one attachment should still return the
others where possible; Sirdar treats an `Attachments` error as a warning
on the bundle rather than a fatal one, but a partial return only helps if
the adapter makes it.

### `shutdown`

Sent when Sirdar is done with the adapter. The adapter should reply
`{"id": N, "result": null}` and then exit; Sirdar closes the adapter's
stdin right after sending this, so an adapter that instead reads its next
request in a loop will see EOF and can treat that the same way — stdin
EOF and an explicit `shutdown` request are equivalent shutdown signals,
and an adapter should handle whichever arrives.

Sirdar does not wait for the `{"result": null}` reply before proceeding:
it closes stdin and then waits up to 5 seconds for the process to exit on
its own, killing it if it hasn't. An adapter with cleanup to do (flushing
a cache, closing a connection) has that window to do it in before exit;
an adapter that hangs indefinitely is killed, not waited on.

## Error codes

| code           | meaning                                                        |
|----------------|-----------------------------------------------------------------|
| `not_found`      | the requested key/id doesn't exist                                |
| `auth`           | the adapter's credentials are missing, expired, or rejected        |
| `unsupported`    | the method isn't implemented by this adapter, or the role wasn't advertised in `describe` |
| `rate_limited`    | the upstream API is throttling; Sirdar may back off and retry later |
| `internal`        | anything else — a parse failure, a network error, a bug            |

An unrecognized method should return `unsupported`:

```json
{"id": 4, "error": {"code": "unsupported", "message": "unknown method foo.bar"}}
```

## The file adapter

`examples/adapters/file` is a complete, minimal adapter: it reads a JSON
fixture (via `-file path/to/tickets.json` or `SIRDAR_FILE_ADAPTER`) shaped
like

```json
{
  "tracker": {"OMNI-1": {"Key": "OMNI-1", "Title": "Export fails", "...": "..."}},
  "helpdesk": {
    "555": {
      "ticket": {"ID": "555", "Subject": "...", "...": "..."},
      "thread": [{"At": "...", "Author": "...", "Role": "customer", "Text": "...", "AttachmentIDs": ["a1"]}],
      "attachments": [{"id": "a1", "name": "shot.png", "mime": "image/png", "content_base64": "..."}]
    }
  }
}
```

and serves it: `describe` reports both roles; `tracker.list` ignores its
filter and returns every tracker ticket in the fixture; `helpdesk.attachments`
decodes each fixture attachment's `content_base64` and writes it into the
requested directory. It's what `internal/source/plugin`'s tests build and
drive, and it's the adapter CI's `--dry-run` mode runs Sirdar against, so
a full triage pass can be exercised in CI without any real credentials.

Run it standalone to see the protocol in action:

```sh
go run ./examples/adapters/file -file internal/source/plugin/testdata/tickets.json
```

then type a request line and press enter:

```
{"id": 1, "method": "describe"}
{"id": 2, "method": "tracker.get", "params": {"key": "OMNI-1"}}
```

## Writing a private adapter

An adapter is any executable that:

1. Reads JSON request objects from stdin, one per line.
2. Writes one JSON response object per line to stdout, matching each
   request's `id`, before reading the next request (or as soon as it's
   ready — nothing stops an adapter from answering out of the order
   requests were meant to be handled in, but since Sirdar only ever has
   one request in flight, in practice this just means: answer promptly).
3. Treats EOF on stdin the same as an explicit `shutdown` request, and
   exits soon after either.
4. Sends anything that isn't a protocol response — logs, warnings, stack
   traces — to stderr, never stdout.

Point Sirdar's config at it as a shell command; Sirdar will start it,
speak the protocol above, and stop it when the run ends. Because the
protocol is language-agnostic, a private adapter for an internal or
proprietary system (say, an in-house ticketing tool) can be a short Python
or Node script living entirely outside this repository — it only needs to
implement whichever of `tracker.*` or `helpdesk.*` it advertises in
`describe`, and can return `unsupported` for methods outside its role.

A minimal sketch in Python:

```python
import json, sys

def handle(req):
    if req["method"] == "describe":
        return {"name": "example", "roles": ["tracker"], "version": "1"}
    if req["method"] == "tracker.get":
        key = req["params"]["key"]
        ticket = fetch(key)  # however this adapter talks to its backend
        if ticket is None:
            raise LookupError(key)
        return ticket
    raise NotImplementedError(req["method"])

for line in sys.stdin:
    req = json.loads(line)
    try:
        result = handle(req)
        resp = {"id": req["id"], "result": result}
    except LookupError as e:
        resp = {"id": req["id"], "error": {"code": "not_found", "message": str(e)}}
    except NotImplementedError as e:
        resp = {"id": req["id"], "error": {"code": "unsupported", "message": f"unknown method {e}"}}
    print(json.dumps(resp), flush=True)
    if req["method"] == "shutdown":
        break
```
