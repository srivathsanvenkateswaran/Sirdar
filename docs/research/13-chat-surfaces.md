# Chat surfaces: a Sirdar agent in Slack, and the Buzz alternative (2026-09-15)

Research only. Nothing here is built, and the sequencing assumption is that the desktop app
(HANDOFF "Next steps", items 1-2) lands first — a chat surface is a third front-end over the same
core, and building it before the second one is finished buys nothing.

External facts are dated and sourced at the end. Sirdar facts are read off this tree today.

## 1. What a Sirdar Slack agent would do

Everything below already exists as a CLI verb or an HTTP route. The Slack app adds no capability;
it adds a place to type from and a place to watch.

| In Slack | What it calls today | Notes |
|---|---|---|
| `/sirdar triage OMNI-3217` | `StartTriage` (`POST …/triage`) | Posts one parent message; the run's thread hangs off it |
| `/sirdar rca OMNI-3217` | `StartRCA` | Same shape |
| `/sirdar queue`, `/sirdar runs [KEY]` | `Queue`, `Runs` | Ephemeral reply, visible to the caller alone |
| progress in the thread | `Subscribe()` → `run.event` / `run.updated` | One message, edited in place, not a message per tool call |
| the agent's question, in the thread | the `question` run event (`provider.EvQuestion`) | Off by default — see below |
| a thread reply while the run is `blocked` | `Resume(ws, runID, answer)` (`POST …/resume` `{"answer":…}`) | The existing answer path, unchanged |
| a thread reply while the run is `completed` | `Steer(ws, runID, text)` (`POST …/steer` `{"text":…}`) | The existing steer path, with its own refusals |
| a Cancel button on the parent message | `Cancel(jobID)` | The 2 s mis-pairing caveat in HANDOFF applies here too |
| the completion digest | `internal/notify` | Unchanged, including `includeTitle` |

The blocked-question turn is the one that makes the app worth building. Today a question parks a
run until the operator is back at the laptop; `notify` deliberately posts `reason: agent asked a
question` and never the question, because the text can quote the ticket. In Slack the thread is
the natural place to answer it, so the question has to be posted — which is a real widening of an
existing rule, not an incidental detail. It gets its own key (`chat.slack.includeQuestion`,
default `false`, same posture as `includeTitle`), and with it off the thread says a question is
waiting and the answer still works: the operator replies, the reply goes to `Resume`, and the
question itself never left the machine.

The digest already posts to Slack through an incoming webhook. With a bot token the connector can
post it into the run's own thread instead of a channel's flat history, and the "one workspace,
one webhook, one channel" limit in `docs/notifications.md` goes away — a bot token can post to
any channel it is in, so `channel:` becomes a real setting for the first time.

### What stays out

- **No writes to a helpdesk or a tracker, ever.** Slack does not become the exception to the
  standing constraint. No status transitions, no comments, no assignment.
- **No note body in a message.** The thread carries the note's path — relative to the workspace
  root, as `notify` already renders it — and reading the finding means opening the note. Same rule
  for the complaint, the root-cause hypothesis, and any log line the agent quoted.
- **Ticket titles only under `includeTitle`.** The existing key governs the chat surface too, and
  keeps its default of off.
- **No `sirdar fix` from Slack.** The precedent is already in the tree: `POST …/fix` answers 403
  on a non-loopback listener because a fix writes code and opens a PR under the operator's GitHub
  login. A message typed from a phone is further from the keyboard than that listener is, so fix
  stays a laptop verb. Reviewing a fix (`runs diff`, per-hunk drop) stays out for the same reason.
- **No secrets, no absolute paths.** Workspace-relative paths only, no `/Users/<name>/…`, and the
  existing rule that an error never quotes a credential or a receiver's body carries over.
- **No free-text prompt injection into a run's permissions.** A steer reaches the agent as prompt
  text and nothing else (`docs/steer.md`); the Slack path adds no way to widen a policy.

## 2. Architecture for a laptop daemon

### Socket Mode, because there is no inbound URL

Slack's Socket Mode replaces the public Request URL with an outbound WebSocket the app opens
itself: no reverse proxy, no tunnel, no TLS termination, and no request-signing secret to verify,
since nothing arrives unsolicited. Events, slash commands, interactivity, modals and shortcuts all
ride the same socket. This is the opposite posture from `/hooks/`, which needs the operator to
expose a port and warns about it at length in `docs/webhooks.md`; a Socket Mode connector needs
neither `--allow-remote` nor a tunnel, and a laptop that sleeps simply drops the socket.

The cost is distribution: Socket Mode apps are not allowed in the public Slack Marketplace. For
Sirdar that is the right trade anyway — the app is per-operator, holds no server-side state, and
would be installed as an internal (customer-built) app from a manifest checked into this repo.
That also sidesteps the 2025-26 rate-limit squeeze on non-Marketplace apps, which caps
`conversations.history` / `conversations.replies` at 15 messages a minute for existing installs
from 3 March 2026 but excludes internal customer-built apps — and the connector reads no history
regardless, since it works off events it was pushed.

Go has no official Bolt. `github.com/slack-go/slack` is the community library, it carries
`socketmode` with a `SocketmodeHandler` that routes by event type, and it already has the agent
surfaces (`AssistantThreadStarted`, `SetAssistantThreadsStatus`). That is the dependency; the
alternative is hand-rolling `apps.connections.open` plus a WebSocket loop, which is a day of work
and a permanent maintenance tax.

### Where the code sits

`internal/chat/slack`, a second front-end over `app.Service`, exactly as `internal/httpapi` is the
first. The service interface already has everything the connector needs — `StartTriage`,
`StartRCA`, `Resume`, `Steer`, `Cancel`, `Runs`, `Queue`, and `Subscribe()` for the fan-out — so
nothing in `internal/run` or `internal/app` changes. `sirdar serve` starts the connector when the
config block is present; a `--no-chat` flag silences it the way `SIRDAR_NO_NOTIFY` silences a post.

### Who may command it

An allow-list of Slack **user ids** (`U…`), matched against the `user_id` Slack puts on every
slash command and event. Not emails, not display names, not "anyone in the channel": a workspace
admin can change a display name, and an email match would quietly grant a guest account whatever
the operator's laptop can do. Anything from an id not on the list gets an ephemeral refusal and a
line on the run log. A channel allow-list on top of it, so a run's thread cannot be started in a
channel the operator did not nominate.

```yaml
chat:
  slack:
    botToken: keychain:sirdar-slack-bot      # xoxb-…
    appToken: keychain:sirdar-slack-app      # xapp-…, connections:write
    allow: [U024BE7LH]                       # Slack user ids, never emails
    channels: [C07SUPPORT]
    includeTitle: false
    includeQuestion: false
```

Both tokens are credential references, resolved once at startup, rejected at config load if
written literally — the rule `notify` already enforces for a webhook URL, and for the same reason:
an `xoxb-` token is a bearer credential. Both are stripped from the agent session's environment
alongside the adapters' credentials, so a session that can run shell commands cannot post to the
channel as Sirdar. A token that will not resolve stops `sirdar serve` rather than leaving a
connector up that fails every command.

### A run is a thread

| Thing | Slack thing |
|---|---|
| the run | one parent message in the nominated channel, edited in place as status changes |
| the run's progress | `thread_ts` of that message; one edited progress message inside it |
| the agent's question | a thread reply |
| the operator's answer / steer | a thread reply, routed by the run's status at the moment it arrives |
| the digest | a thread reply, plus the channel-level post if `notify.slack` is still configured |

The mapping lives in `.sirdar/chat/threads.jsonl` — `runId ↔ (channel, thread_ts)`, both
directions — so a reconnect after a laptop wakes finds the thread again. It is written where every
other per-run artefact is written, and it is not a secret.

Two Slack limits shape the posting: roughly one message per second per channel, and 50 Block Kit
blocks per message. So progress is one message edited via `chat.update` on a timer (a second or
two), never a message per tool call — which is also what keeps a 40-turn run from filling a
channel. In the assistant/agent surface, `assistant.threads.setStatus` gives the "thinking" line
for free, and it clears itself when a message is sent.

### What the socket cannot do

Slack does not replay events an app missed. A command typed while the laptop is asleep is lost —
Slack shows the slash command failing, which is honest, and the operator retries. A run that was
live when the socket dropped keeps running locally and its thread catches up on reconnect from
`Runs`/`Run`, since the state is on disk. Nothing queues.

## 3. Buzz

**Which Buzz.** In 2026 the unambiguous reading is Block's **Buzz**, launched 21 July 2026: an
Apache-2.0 group-chat workspace where AI agents are members with their own identity, with Git
hosting in the same window, built on Nostr (`github.com/block/buzz`). Older or lesser claimants —
Google Buzz, retired in 2011, and "buzz" as a generic feed feature in various suites — are not
platforms anyone would ask this question about. This section takes the Block reading.

**What it is, technically.** A relay you run (Rust/Axum, Postgres, Redis, MinIO, Docker Compose in
`deploy/compose`, or Block's managed deployment), speaking Nostr NIP-01 with NIP-42 Schnorr
authentication and NIP-34 for git events, with channel/DM/media/workflow/git REST endpoints and a
WebSocket event stream beside the signed log. Every participant, human or agent, holds a keypair
and every message is signed — so an agent's actions are attributable in a way a Slack bot's are
not. The agent surface is `buzz-cli` (JSON in, JSON out, built for tool calls), `buzz-acp` (an ACP
harness driving goose, Codex and Claude Code, with ACP↔MCP translation), `buzz-agent`, and
`buzz-dev-mcp`. The README says plainly that it is not finished.

**What a Sirdar agent would do there.** The same list as section 1, unchanged, and the exclusions
are the same. Two differences are worth naming. First, identity: Sirdar would hold its own keypair
and its posts would be signed as itself, which fits a tool whose whole pitch is an auditable
read-only trail — the run's event log and the chat log would be two signed records of the same
work. Second, the allow-list is cleaner: a Nostr pubkey is a cryptographic identity, not a name an
admin can reassign, so `allow: [npub…]` means what it says.

**Architecture.** The same seam — `internal/chat/buzz` over `app.Service` — and the same
outbound-only posture: the daemon opens a WebSocket to the relay and authenticates with NIP-42, so
the laptop still exposes nothing. The difference is that Slack is a service that exists whether or
not the operator does anything, while Buzz is a relay somebody has to run. For a solo operator
that means standing up Postgres, Redis and MinIO — or trusting a managed relay — before a single
message is exchanged. There is no Go SDK; the client would be hand-written against the REST and
NIP-01 endpoints plus a Schnorr signer (`secp256k1`, which Go has good libraries for).

**The other door.** Buzz drives agents over ACP. Sirdar is an ACP *client* (`internal/provider/acp`
speaks the client half to Copilot CLI, OpenCode, Kimi). Implementing the *agent* half — an ACP
facade that answers `session/new` and `session/prompt` by starting a Sirdar run — would make
Sirdar joinable by any ACP host, Buzz included, with no Buzz-specific code. That is speculative:
whether `buzz-acp` would accept an arbitrary ACP agent binary as a channel member, and what
`buzz-agent` does that a plain ACP agent cannot, is not established from the README and would need
an hour against a running relay. If it holds, it is the cheapest route into Buzz and into whatever
else speaks ACP next year.

## 4. Effort, risks, recommendation

| Work | Slack | Buzz |
|---|---|---|
| connector skeleton + transport | 1 d (`slack-go` socketmode) | 3 d (hand-written client, NIP-42 signer) |
| commands, allow-list, config block, credential refs | 1.5 d | 1.5 d |
| run → thread mapping, progress throttling, message rendering | 1.5 d | 1.5 d |
| question → answer → `Resume`, reply → `Steer` | 1 d | 1 d |
| app manifest / bot identity, install docs, `doctor` row | 1 d | 1.5 d (plus relay setup docs) |
| tests against a fake transport, one live end-to-end run | 1.5 d | 2 d |
| **first cut** | **6-8 days** | **10-13 days**, plus a relay to run |

Risks, both:

- The question text is the privacy decision, not a detail. Default it off, and make the "a
  question is waiting" message useful enough that most operators never turn it on.
- A chat surface is a remote control for a laptop. Keeping `fix` and diff review out is what
  bounds the blast radius of a stolen token or a compromised Slack account.
- Two bearer tokens now sit on the laptop and in a channel's history of what Sirdar can do.
- Rendering is where leaks happen. One function builds every outbound message, it takes a typed
  struct rather than strings, and the note body is not reachable from it.

Slack-specific: `slack-go` is community-maintained, and Slack is moving the status scope from
`assistant:write` to `chat:write` (changelog, March 2026), so the app definition will drift.
Socket Mode forecloses the Marketplace, which is fine for an internal app and means every OSS user
creates their own app from the manifest.

Buzz-specific: the platform is two months old and says it is unfinished, the wire surface will
move, and the population of workspaces that run a Buzz relay today is close to nobody — including
this operator. Anything built now is built against a moving target for an audience of zero.

**Recommendation: Slack, after the desktop UI, as an internal Socket Mode app with the manifest in
this repo.** It costs about a working week, needs no inbound URL, no tunnel and no new
authentication story, and reuses `app.Service` whole. Ship it with `includeQuestion: false`,
`includeTitle: false`, a user-id allow-list, and no fix route. Leave Buzz alone for now, and
revisit it when the README stops saying "not finished" — and when revisiting, price the ACP agent
facade first, since it may reach Buzz and every other ACP host for less than a Buzz connector
costs on its own.

## Sources

Slack: [Socket Mode](https://docs.slack.dev/apis/events-api/using-socket-mode/) ·
[rate limits](https://docs.slack.dev/apis/web-api/rate-limits/) ·
[non-Marketplace rate-limit changes](https://api.slack.com/changelog/2025-05-terms-rate-limit-update-and-faq) ·
[assistant.threads.setStatus](https://docs.slack.dev/reference/methods/assistant.threads.setStatus/) ·
[setSuggestedPrompts](https://docs.slack.dev/reference/methods/assistant.threads.setSuggestedPrompts/) ·
[set-status scope change, 2026-03-05](https://docs.slack.dev/changelog/2026/03/05/set-status-scope-update/) ·
[Bolt (JS, Python, Java only)](https://api.slack.com/bolt) ·
[slack-go/slack](https://github.com/slack-go/slack) ·
[Add to Slack, 2026](https://slack.com/blog/news/add-to-slack) ·
[Slack Code channels](https://slack.com/blog/news/slack-code-channels-for-agents).

Buzz: [github.com/block/buzz](https://github.com/block/buzz) ·
[TechCrunch, 2026-07-21](https://techcrunch.com/2026/07/21/jack-dorsey-is-taking-on-slack-with-buzz-a-group-chat-platform-for-teams-and-their-ai-agents/) ·
[Open Source For You](https://www.opensourceforu.com/2026/07/block-unveils-buzz-an-open-source-workspace-built-for-human-agent-parity/).
