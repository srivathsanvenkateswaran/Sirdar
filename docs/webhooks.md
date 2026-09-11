# Inbound webhook triggers

A ticket lands on you; `sirdar serve` triages it before you have read it. That is the whole of
this feature: a tracker or helpdesk POSTs to Sirdar when something is assigned, and Sirdar runs
the same triage `sirdar triage KEY` would have run.

Nothing here trusts the webhook body beyond the ticket key it names. The key goes to the normal
triage path, which reads the ticket through the configured adapter — so a forged payload with a
plausible signature can, at worst, make Sirdar triage a ticket that already exists.

## Before you start: the endpoints are not reachable by default

`sirdar serve` binds `127.0.0.1:7777`. A hosted tracker cannot reach that, and this is
deliberate: the JSON API on the same listener has no authentication, so anybody who can reach the
port can start runs and read your notes.

Two ways to let a tracker in, both of which need a decision from you:

- **A TLS reverse proxy.** `sirdar serve --allow-remote --addr 0.0.0.0:7777`, with nginx, Caddy or
  a cloud load balancer terminating TLS in front of it. Restrict the proxy to `/hooks/` unless you
  also want the UI exposed — which you almost certainly do not.
- **A tunnel.** `cloudflared`, `ngrok`, a Tailscale funnel. The listener stays on loopback and the
  tunnel does the exposure. This is the one to start with.

Either way, **use TLS**. Five of the nine sources authenticate with a shared secret in a plain
header; on an unencrypted connection anyone on the path can read it and replay the delivery.

Sirdar refuses to register the hook routes at all unless `webhooks.enabled: true`. With them off,
every path under `/hooks/` is a 404.

## Configuration

```yaml
webhooks:
  enabled: true
  cooldown: 10m            # a key triaged this recently is skipped; 0s disables the cooldown
  match:
    assignee: me           # "me" = the account email on sources.tracker, else sources.helpdesk
    statuses: [Open, "In Progress"]
    labels: [support]
  sources:
    jira:
      secret: keychain:jira-hook-secret
    linear:
      secret: keychain:linear-hook-secret
    azdo:
      username: sirdar
      password: keychain:azdo-hook-password
```

`secret` and `password` are credential references — `env:NAME` or `keychain:SERVICE` — never the
secret itself, like every other credential in the file. They are resolved once at startup: a
reference that cannot be resolved stops `sirdar serve` rather than leaving an endpoint up that
rejects everything.

### `match`

A tracker fires on every change to every ticket it is configured for. `match` is what turns that
firehose into "something landed on my plate".

- `assignee` — the one that matters. Matched case-insensitively against whoever the payload says
  the ticket now belongs to. **A payload that carries no assignee is rejected when this is set**:
  "only what is assigned to me" cannot be satisfied by a body that does not say. If your source
  sends a body without an assignee (a minimal Azure DevOps subscription, a Zendesk template you
  wrote without one), either add the field to the body or leave `assignee` unset and filter in the
  tracker's own rule instead.
- `statuses` and `labels` — best-effort, and looser on purpose. Sirdar looks through the payload
  for anything status-shaped (`status`, `ScheduleState`, `System.State`, a plain-string `state`)
  or label-shaped (`labels`, `tags`, `labelIds`, `System.Tags`) and passes the delivery if any of
  them matches. **A payload carrying none of those fields passes rather than being dropped** —
  several of these sources can be configured to send nothing but an id. Two known gaps: Linear's
  workflow state arrives as an object at `data.state`, which is not harvested (it collides with
  Rally's whole-snapshot `message.state`), so filter Linear on labels; and HubSpot sends neither.

`assignee: me` resolves to the account email on `sources.tracker`, falling back to
`sources.helpdesk`. A Linear API key or an Azure DevOps PAT names nobody, so a workspace using one
has to write the address out. For HubSpot, write the **numeric owner id** — `hubspot_owner_id`
carries an id, not an address.

### `cooldown`

A key that was triaged inside the cooldown is skipped, as is one with a run already `preparing` or
`running`. Without it, editing a ticket by hand generates a delivery every few seconds and each
one would be a fresh agent session. A *failed* triage holds the key too: a workspace whose adapter
is down should not answer every delivery with another run.

## The URL

```
POST http://<host>:<port>/hooks/<workspace-id>/<source>
```

`<source>` is the key under `webhooks.sources`: `jira`, `linear`, `azdo`, `rally`, `zendesk`,
`freshdesk`, `intercom`, `hubspot`, `generic`. `<workspace-id>` is printed by
`GET /api/workspaces` — or read it off the UI. The receiver is built from the workspace
`sirdar serve` was started in, so that is the id to use.

### What comes back

| Status | Body | Meaning |
|---|---|---|
| 202 | `{"jobId":"job-3","keys":["OMNI-2510"]}` | A triage started. `jobIds` is added when one delivery named several tickets, since each gets its own job. |
| 202 | `{"skipped":"OMNI-2510: was triaged 2m ago and the cooldown is 10m"}` | Verified, and deliberately did nothing — already running, on cooldown, or filtered out. |
| 400 | `{"error":{"code":"bad_request",…}}` | Verified, but the body is not JSON or names no ticket. |
| 401 | `{"error":{"code":"unauthorized",…}}` | The signature or secret did not check out. |
| 404 | `{"error":{"code":"not_found",…}}` | No such source — which is also the answer for a source that exists but is not enabled, and for a workspace id nobody knows. |
| 413 | `{"error":{"code":"too_large",…}}` | Over 1 MiB. The body is size-checked before it is verified. |

A skip is a 2xx on purpose: a tracker told its delivery failed will send it again.

Every delivery also publishes a `hook.received` event on `/api/events` —
`{source, key, outcome}`, where the outcome is `started`, `skipped`, `filtered`, `ignored` or
`rejected`. It is how you tell a hook that never arrives from one that arrives and is filtered.

## Setting each source up

### Jira — Automation rule (`jira`)

Jira Cloud's own webhooks are unsigned, so use Automation, where you control the headers.

1. Project settings → **Automation** → **Create rule**.
2. Trigger: **Issue assigned** (or **Field value changed** on Assignee).
3. Condition, if you want one: `Assignee = <you>`. Doing it here rather than in `match` saves the
   round trip.
4. Action: **Send web request**.
   - URL: `https://<your-host>/hooks/<workspace-id>/jira`
   - Method POST, **Webhook body: Issue data (automation format)** — or a custom body, see below.
   - Headers: `X-Sirdar-Secret: <the secret you put in the config>`.
5. Put the same value in `webhooks.sources.jira.secret` as a credential ref.

Sirdar reads `issue.key`, `webhookEvent`, and the assignee from
`issue.fields.assignee.emailAddress`, falling back to `accountId` then `displayName`. If your rule
sends a custom body, the flat shape works too:

```json
{"key": "{{issue.key}}", "assignee": "{{issue.assignee.emailAddress}}", "status": "{{issue.status.name}}"}
```

A platform webhook registered through `POST /rest/api/3/webhook` also works, provided something in
front of it adds the `X-Sirdar-Secret` header — Jira will not. Assignment is not an event of its
own there: it arrives as `jira:issue_updated` with an `assignee` changelog entry, so scope the
subscription with `fieldIdsFilter` or lean on `match.assignee`.

### Linear (`linear`)

1. Settings → **API** → **Webhooks** → **New webhook**.
2. URL `https://<your-host>/hooks/<workspace-id>/linear`; data change events, **Issues**.
3. Copy the **signing secret** into `webhooks.sources.linear.secret`.

Linear signs the raw body with HMAC-SHA256 and sends the hex digest as `Linear-Signature`. Sirdar
also checks `webhookTimestamp` is within 60 seconds, which is Linear's own recommendation.

A `create` always triggers. An `update` triggers only when `updatedFrom` shows the assignee or the
labels changed — an edited description is not a reason to start an agent session. The key is
`data.identifier` (`ENG-431`), and the assignee is read from `data.assignee.email` where the
payload embeds the user, `data.assigneeId` otherwise.

### Azure DevOps — service hook (`azdo`)

1. Project settings → **Service hooks** → **+**  → **Web Hooks**.
2. Trigger: **Work item updated** (and a second subscription for **Work item created**). Filter by
   area path, work item type, or changed field — server-side filtering beats filtering here.
3. Action → URL `https://<your-host>/hooks/<workspace-id>/azdo`, and set **Basic authentication
   username** and **password**. Those two go in `webhooks.sources.azdo.username` and `.password`.
4. **Resource details to send**: `All` if you want `match` to see the state and tags; `Minimal` is
   enough for the key alone.

The key is `resource.workItemId`, falling back to `resource.id` — the order matters, because on an
update `resource.id` is the revision, not the work item. The assignee comes from
`resource.revision.fields["System.AssignedTo"]`, then the `newValue` of the same field in
`resource.fields`; `Display Name <addr@example.com>` is reduced to the address.

### Rally (`rally`)

Rally has no UI for webhooks; create one through the API
(`POST https://rally1.rallydev.com/apps/pigeon/api/v2/webhook`, authenticated with an API key —
basic auth is not accepted there):

```json
{
  "Name": "Sirdar triage",
  "TargetUrl": "https://<your-host>/hooks/<workspace-id>/rally",
  "ObjectTypes": ["Defect"],
  "Expressions": [{"AttributeName": "Owner", "Operator": "=", "Value": "<your user ref>"}]
}
```

Rally sends no signature, so Sirdar needs `X-Sirdar-Secret`. If the webhook config will not carry
a custom header, put the secret in front of Sirdar instead — a proxy that adds the header on a
path only Rally knows.

Sirdar reads `FormattedID` from `message.state`, falling back to `message.changes.FormattedID`,
and the assignee from `message.state.Owner`. **The delivered payload shape is unverified** — the
Rally research pass could not load the Webhooks Payload page — so check a real delivery before
relying on this one.

### Zendesk — trigger plus webhook (`zendesk`)

1. Admin Center → **Apps and integrations → Webhooks → Create webhook**.
2. Endpoint `https://<your-host>/hooks/<workspace-id>/zendesk`, POST, JSON.
3. Authentication **None**, but turn **signing** on and copy the **signing secret** into
   `webhooks.sources.zendesk.secret`.
4. Admin Center → **Objects and rules → Business rules → Triggers → Add trigger**. Condition:
   *Assignee changed to <you>*. Action: **Notify active webhook**, choosing the one above, with
   this body:

```json
{
  "ticket_id": "{{ticket.id}}",
  "status": "{{ticket.status}}",
  "assignee_email": "{{ticket.assignee.email}}",
  "tags": "{{ticket.tags}}"
}
```

Zendesk signs `timestamp + body` with HMAC-SHA256 and base64s it into
`X-Zendesk-Webhook-Signature`, with the timestamp in `X-Zendesk-Webhook-Signature-Timestamp`.
Sirdar rejects a delivery whose timestamp is more than five minutes from now, in either direction.

The body is yours, so the only fixed requirement is `ticket_id` (`ticket.id` and `id` are accepted
as fallbacks).

### Freshdesk — automation rule (`freshdesk`)

1. Admin → **Workflows → Automations → Ticket updates → New rule**.
2. Condition: *Agent is <you>*. Action: **Trigger webhook**.
3. URL `https://<your-host>/hooks/<workspace-id>/freshdesk`, POST, JSON, **custom headers**
   `X-Sirdar-Secret: <secret>`.
4. Body: `{"ticket": {"id": "{{ticket.id}}", "status": "{{ticket.status}}", "assignee_email": "{{ticket.agent.email}}"}}`

Freshdesk signs nothing; the header is the whole of the proof. Sirdar reads `ticket.id`, with
`freshdesk_webhook.ticket_id` and `ticket_id` as fallbacks.

### Intercom (`intercom`)

1. **Developer Hub** → your app → **Webhooks**.
2. Endpoint `https://<your-host>/hooks/<workspace-id>/intercom`; subscribe to the
   **`conversation.admin.assigned`** topic.
3. Copy the app's **client secret** into `webhooks.sources.intercom.secret` — the signature is
   computed with it, not with an access token.

Intercom sends `X-Hub-Signature: sha1=<hex>`, an HMAC-SHA1 of the body. Sirdar acts only on
`conversation.admin.assigned` — every other topic you subscribe to arrives at the same endpoint
and is accepted and ignored. The key is `data.item.id`; the assignee is
`data.item.assignee.email` where present, else `data.item.admin_assignee_id`.

### HubSpot — private app subscriptions (`hubspot`)

1. Settings → **Integrations → Private apps** → your app → **Webhooks**.
2. Target URL `https://<your-host>/hooks/<workspace-id>/hubspot`.
3. Create subscriptions for **`ticket.creation`** and **`ticket.propertyChange`** (the
   `hubspot_owner_id` property, for assignment).
4. Copy the app's **client secret** into `webhooks.sources.hubspot.secret`.

The v3 signature is base64(HMAC-SHA256(`method + uri + body + timestamp`)) in
`X-HubSpot-Signature-v3`, with `X-HubSpot-Request-Timestamp` in epoch milliseconds and a five
minute window.

**The URI is part of the signed message**, and it is the URL HubSpot called — not the one this
process sees behind a proxy. Your proxy must preserve the `Host` header and set
`X-Forwarded-Proto: https`; without those, Sirdar reconstructs a different URI and every delivery
fails verification.

HubSpot posts a batch: one JSON array of up to a hundred events, of every type you subscribed to.
Sirdar takes the ticket events, deduplicates by `objectId`, and starts one job per ticket.

### Generic (`generic`)

For a tracker with no integration, a script, or a smoke test.

```bash
curl -X POST https://<your-host>/hooks/<workspace-id>/generic \
  -H 'X-Sirdar-Secret: <secret>' \
  -H 'Content-Type: application/json' \
  -d '{"key":"OMNI-2510","assignee":"sri@acme.com"}'
```

`key` is required. `assignee`, `event`, and anything else you add (`status`, `labels`) are read by
the match filter.

## Verification at a glance

| Source | Header | Scheme |
|---|---|---|
| `jira` | `X-Sirdar-Secret` | shared secret, constant-time compare |
| `linear` | `Linear-Signature` | hex HMAC-SHA256 over the raw body, + 60 s timestamp window |
| `azdo` | `Authorization` | HTTP basic, both halves compared constant-time |
| `rally` | `X-Sirdar-Secret` | shared secret |
| `zendesk` | `X-Zendesk-Webhook-Signature` (+ `-Timestamp`) | base64 HMAC-SHA256 over `timestamp + body`, 5 min window |
| `freshdesk` | `X-Sirdar-Secret` | shared secret |
| `intercom` | `X-Hub-Signature` | `sha1=` + hex HMAC-SHA1 over the body |
| `hubspot` | `X-HubSpot-Signature-v3` (+ `X-HubSpot-Request-Timestamp`) | base64 HMAC-SHA256 over `method + uri + body + timestamp`, 5 min window |
| `generic` | `X-Sirdar-Secret` | shared secret |

Bodies are capped at 1 MiB and the cap is enforced before verification. Every comparison is
constant-time. A key that is not one plain path element — anything with a separator, a `..`, or a
glob character — is dropped rather than sanitised.

## When a delivery does nothing

Watch the event stream while you test:

```bash
curl -N http://127.0.0.1:7777/api/events | grep hook.received
```

- `rejected` — the signature failed, or the body would not parse. Check the secret is the same on
  both ends, and for HubSpot check the proxy headers.
- `ignored` — verified, but the payload was not an event Sirdar acts on. A Linear update that
  changed only the title, an Intercom topic other than `conversation.admin.assigned`, a HubSpot
  batch with no ticket events.
- `filtered` — `webhooks.match` rejected it. The response body says which rule and what it saw.
- `skipped` — the key is already running, or inside the cooldown.
- `started` — a run is going; watch `/api/events` or the UI.
