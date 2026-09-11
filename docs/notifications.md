# Run-completion notifications

When a triage or RCA run reaches a terminal state, Sirdar can post a short digest of it to a
Slack channel, a Microsoft Teams channel, or any HTTP receiver you run yourself. The message
says which ticket, how the run ended, what the agent concluded, what it cost, and where the note
is. It never says what the note says.

Nothing is posted until you add a `notify:` block to `.sirdar/config.yaml`.

## What goes over the wire

| Field | Example |
|---|---|
| `kind` | `triage`, `rca` |
| `key` | `OMNI-3217` |
| `status` | `completed`, `failed`, `over_budget`, `blocked` |
| `confidence`, `classification`, `service` | `medium`, `code`, `omni` |
| `runId`, `notePath` | `20260911T064500Z-9f2a`, `/w/notes/OMNI-3217 export-times-out.md` |
| `trackerUrl`, `helpdeskUrl` | links to the two tickets |
| `turns`, `costUsd`, `minutes` | `41`, `0.82`, `8.6` |
| `reason` | why a failed, blocked or over-budget run ended that way |
| `workspace` | the `workspace:` label, for a channel that hears from several |
| `title` | the ticket title — **only** with `includeTitle: true` |

No part of a note's body is ever sent: not the complaint, not the root-cause hypothesis, not a
log line the agent quoted. What a channel gets is metadata and a path, and reading the finding
means opening the note.

`includeTitle` is off by default for the same reason. A support ticket's subject line routinely
carries a customer's name, an order number, or a phrase from an angry email, and a chat channel
is a wider and longer-lived audience than a notes directory. Turn it on when your channel is
already the place those tickets are discussed.

`notify.on` decides which runs are worth a message. It defaults to all four terminal states;
a team that only wants to hear about trouble sets `on: [failed, over_budget, blocked]`.

## A failed post never fails a run

By the time a notification goes out the note is written, the register row is appended, and the
run's state is on disk. A webhook that is down, slow, rate limiting, or misconfigured therefore
cannot change what the run was worth. The failure is recorded where a run's other degradations
are recorded — `state.json`'s `warnings`, and a `[KEY] notify: …` line on the progress stream —
and the run keeps the status it earned.

Each post gets 10 seconds. A `429` or a `5xx` answer is retried once, after the delay the
receiver asked for in `Retry-After`, as long as that delay is 30 seconds or less. Destinations
are posted to side by side, so one dead webhook does not cost the others their message.

`SIRDAR_NO_NOTIFY=1` silences one invocation — useful when re-running a batch the channel has
already heard about. `sirdar triage --no-notify` and `sirdar rca --no-notify` do the same for one
command.

## Slack

1. Create a Slack app at <https://api.slack.com/apps> (**From scratch**), pick your workspace.
2. **Incoming Webhooks** → toggle **Activate Incoming Webhooks** on.
3. **Add New Webhook to Workspace**, choose the channel, **Allow**.
4. Copy the `https://hooks.slack.com/services/T…/B…/…` URL. That URL *is* the credential: anyone
   holding it can post to the channel as your app.
5. Put it in a credential store rather than the config file:

   ```
   security add-generic-password -s sirdar-slack-webhook -a "$USER" -w   # macOS keychain
   ```

   or export `SLACK_WEBHOOK` from your shell profile.

```yaml
notify:
  on: [completed, failed, over_budget, blocked]
  slack:
    webhookUrl: keychain:sirdar-slack-webhook    # or env:SLACK_WEBHOOK
```

The message is Block Kit: a header reading `[OMNI-3217] completed`, a context line with the run
kind, workspace and run id, a field block with confidence, classification, service, cost,
duration and turns, a `Tracker · Helpdesk` link line, and the note's path in a trailing context
block.

**There is no `channel:` setting, and there cannot be one.** An incoming webhook created by a
Slack app is bound to the channel it was authorised for; Slack has ignored the legacy `channel`
override on app webhooks since 2018, and sending it changes nothing. To post to a second
channel, add a second webhook to the app for that channel — and, for now, a second Sirdar
workspace or a generic hook that fans out, since one workspace posts to one Slack webhook.

## Microsoft Teams

Microsoft retired Office 365 connectors, so the current route is a Workflows (Power Automate)
webhook:

1. In Teams, open the channel → **⋯** → **Workflows**.
2. Pick the template **Post to a channel when a webhook request is received**.
3. Confirm the team and channel, **Add workflow**, and copy the URL it gives you.

A tenant that still has connectors can use an **Incoming Webhook** connector URL instead; both
accept the same envelope.

```yaml
notify:
  teams:
    webhookUrl: keychain:sirdar-teams-webhook     # or env:TEAMS_WEBHOOK
```

Sirdar posts an Adaptive Card 1.4 inside the `attachments` envelope Teams expects: the headline
as a bold `TextBlock`, a subtle subtitle, a `FactSet` carrying the same fields as the Slack
message, the note path, and `Action.OpenUrl` buttons for the tracker and helpdesk tickets.

If the workflow answers `202` and nothing appears in the channel, the flow run history in Power
Automate is where the reason is: a card rejected by Teams fails inside the flow, after the HTTP
request Sirdar made has already succeeded.

## Generic webhook

A generic hook receives the event itself as JSON — the fields in the table above, in a flat
object, with empty ones omitted. Use it for a Discord or Google Chat relay, an internal service,
a queue, or a script on your own machine.

```yaml
notify:
  generic:
    - url: https://hooks.example.com/sirdar
      headers:
        Authorization: env:SIRDAR_HOOK_TOKEN    # env:/keychain: values are resolved
        X-Env: production                       # anything else is sent as written
      secret: env:SIRDAR_HOOK_SECRET            # optional: signs the body
```

`url` must be `https`, unless the host is loopback — `http://127.0.0.1:9000/hook` is accepted so
you can point Sirdar at something on your own machine. Header values starting with `env:` or
`keychain:` are resolved from your credential store; anything else is sent literally, so put
tokens in a reference and keep routing hints inline.

With `secret` set, every request carries:

```
X-Sirdar-Signature: sha256=<hex HMAC-SHA256 of the exact request body>
```

Verify it against the raw body, before parsing, with a constant-time comparison:

```go
func verify(secret string, body []byte, header string) bool {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	want := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(want), []byte(header))
}
```

```python
import hashlib, hmac

def verify(secret: bytes, body: bytes, header: str) -> bool:
    want = "sha256=" + hmac.new(secret, body, hashlib.sha256).hexdigest()
    return hmac.compare_digest(want, header)
```

A receiver that re-serialises the JSON before hashing will not match: the signature is over the
bytes that arrived.

## Credentials

Every secret in the notify block is a reference — `env:NAME` or `keychain:SERVICE` — never the
value. Load rejects a chat `webhookUrl` or a `secret` that carries one directly, because an
incoming-webhook URL is a bearer credential in its path.

Those environment variables are stripped from the agent session's environment alongside the
adapters' credentials, so a session that can run shell commands cannot read the team's webhook
and post to the channel as Sirdar.

Errors and warnings never quote a webhook URL past its host: a post that fails against
`https://hooks.slack.com/services/T…/B…/xoxb-…` is reported as
`notify: https://hooks.slack.com/… answered 404: no_service`, so `state.json` and the terminal
stay safe to paste.

## Where the notification comes from

The runner posts, at the moment a run writes its terminal state. That is the one path the CLI,
`sirdar serve` and the desktop app all share, so a run started from the UI notifies exactly like
one started from a terminal. The application layer logs `job.finished` and posts nothing, which
is what keeps a desktop session from sending every message twice.

A run that fails before its directory exists — an unusable ticket key, a workspace that cannot
be written to — has no state to notify from, and is reported to whoever ran the command instead.
