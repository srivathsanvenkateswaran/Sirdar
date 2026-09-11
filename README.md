# Sirdar

[![ci](https://github.com/srivathsanvenkateswaran/Sirdar/actions/workflows/ci.yml/badge.svg)](https://github.com/srivathsanvenkateswaran/Sirdar/actions/workflows/ci.yml)

An open-source harness for engineering-level support tickets. A ticket comes in from a helpdesk
or tracker, a coding agent you already pay for (Claude Code, Codex) — or any OpenAI-compatible
model — gathers evidence through the MCP servers the workspace grants it, translates the
conversation, and writes a root-cause note for you to review. If you agree with the note, one
command implements its proposed fix on a branch and opens the pull request; once the fix is
merged, Sirdar writes the RCA and Resolution notes that record it. Runs on your machine with
your logins.

On a Himalayan expedition the sirdar is the lead Sherpa: the one who assigns the team's work,
decides who goes up and when, and answers to the client for the outcome. The name is a tribute
to the Sherpa people, whose work on the mountain makes every ascent possible and is rarely the
part that gets photographed.

Status: v0: command-line triage core; the board is next.

## Install

**Homebrew** (macOS/Linux, once the tap and a release exist — see `docs/release.md`):

```
brew tap srivathsanvenkateswaran/sirdar
brew install sirdar
```

**`go install`** (any commit, released or not):

```
go install github.com/srivathsanvenkateswaran/sirdar/cmd/sirdar@latest
```

**Release archive**: download `sirdar_<version>_<os>_<arch>.tar.gz` (`.zip` on Windows) from
[Releases](https://github.com/srivathsanvenkateswaran/Sirdar/releases), check it against that
release's `checksums.txt`, and put `sirdar` on your `PATH`. deb/rpm packages for Linux are
attached to each release too.

**Desktop app**: download `sirdar-desktop_<tag>_<os>_<arch>.zip` for your platform from the same
Releases page. It's unsigned — see `docs/release.md` for the Gatekeeper/SmartScreen workaround.

Or build from source:

```
git clone https://github.com/srivathsanvenkateswaran/sirdar
cd sirdar
make build
```

See `docs/release.md` for how releases are cut.

## Quick start

```
cd <your codebase>
sirdar init
```

Edit `.sirdar/config.yaml` for the workspace: which tracker and helpdesk to read from, where
notes go, and how much budget a run gets. See `docs/config.md` for every key.

```
sirdar doctor
```

Checks that the provider CLI is installed and logged in, that the configured sources answer,
and that the notes directory and templates are usable.

```
sirdar triage KEY
```

Fetches the ticket, runs one agent session inside the workspace, and writes a Triage Note.
Review it before acting on it. The agent's root-cause hypothesis is a starting point, not a
verdict.

If you agree with the note's Proposed Fix:

```
sirdar fix KEY
```

Cuts a branch from a fresh default branch, implements that fix and nothing else, commits, pushes,
and opens the pull request. Running the command is the approval — read the note first.

Later, once the fix is merged:

```
sirdar rca KEY --pr URL --resolution @notes.txt
```

Writes the RCA Note (why it happened) and a Resolution Note draft (what changed), and marks the
triage note resolved.

## Commands

| Command | Flags | What it does |
|---|---|---|
| `sirdar init` | `--templates` write the default note templates to `.sirdar/templates`; `--force` overwrite an existing `.sirdar/config.yaml` | Scaffolds `.sirdar/config.yaml`, `.sirdar/playbooks/`, and git excludes for `.sirdar/runs/` and the register |
| `sirdar doctor` | none | Checks the provider CLI, each configured source, the notes directory, and the active templates; exits 1 if any check fails |
| `sirdar triage KEY [KEY...]` | `--provider claude\|codex\|openai\|acp\|qwen`, `--model NAME`, `--concurrency N`, `--dry-run`, `--no-notify` | Runs triage for one or more keys and prints a digest; `--dry-run` writes the bundle and prompt without starting the agent |
| `sirdar rca KEY` | `--pr URL`, `--resolution TEXT\|@FILE`, `--provider claude\|codex\|openai\|acp\|qwen`, `--model NAME`, `--no-notify` | Produces the RCA note and the Resolution draft for a resolved ticket |
| `sirdar fix KEY` | `--dry-run`, `--no-pr`, `--base BRANCH`, `--accept-deviation`, `--provider`, `--model` | Implements an approved triage note's Proposed Fix on a branch, commits, pushes, and opens a pull request. See [Fix flow](#fix-flow) |
| `sirdar eval [KEY...]` | `--golden DIR`, `--provider`, `--model`, `--concurrency N` | Replays the golden bundles through real triage runs and scores the notes; exits 1 if any note fails its assertions. See `docs/eval.md` |
| `sirdar golden add KEY` | `--from RUN_ID`, `--golden DIR`, `--force` | Copies a completed run's bundle into the golden set and writes an `expected.json` skeleton; refuses a golden set inside a git work tree unless forced |
| `sirdar resume RUN_ID` | none | Continues a blocked or interrupted run |
| `sirdar runs [KEY]` | `--json` | Lists runs and their states, optionally filtered to one key |
| `sirdar register` | `--markdown` print rows in the vault's issue-register table shape | Prints one row per ticket: triage date, confidence, classification, fix date, RCA date, verdict, severity, resolution, and which notes exist |
| `sirdar version` | none | Prints the binary version |

Exit code is non-zero if any run ended in `failed` or `over_budget`.

## Note types

| Note | Answers | Written when |
|---|---|---|
| Triage | What we knew on day one: complaint, repro steps, root-cause hypothesis with confidence, proposed fix, open questions | At ticket intake, by `sirdar triage`. Never rewritten once its status leaves `triaged` |
| RCA | Why it happened: confirmed cause, evidence, blast radius, prevention, and a review of whether the triage hypothesis held | After resolution, by `sirdar rca`, alongside the Resolution note |
| Resolution | What changed: PR, files, exact production statements, approvals, verification | Same `sirdar rca` run, drafted from the PR and the `--resolution` text; anything the agent cannot source is left as a `<fill: ...>` marker for a human |

## How a run works

A run moves through `preparing → running → completed`, or off to `failed`, `blocked`, or
`over_budget`. Preparing fetches the ticket and writes the bundle; running streams the agent
session and watches the turn, time, and USD budgets; a blocked run (the agent asked a question,
or hit a rate limit) is continued with `sirdar resume RUN_ID`.

Each run gets its own directory, `.sirdar/runs/<KEY>/<run-id>/`:

```
bundle/ticket.json        normalised ticket fields
bundle/thread.md          the conversation, original language, roles, timestamps
bundle/attachments/       downloaded files
prompt.md                 exactly what was sent to the agent
events.jsonl              raw provider events, one per line
result.json               validated note JSON (absent on failure)
note.md                   rendered note, also copied to the notes directory
state.json                run kind, state, provider, budgets, timings
```

An `rca` run's bundle also carries the triage note being reviewed, the `--resolution` text, and,
when `--pr` is given and `gh` is available, the PR's title, body, and diff.

A triage or rca session runs with permission to read the workspace and to run the `Bash` commands
listed under `permissions.bash` in config; `Edit`, `Write`, `MultiEdit`, and `NotebookEdit` are
always denied. Every segment of a compound command has to match a pattern, command substitution
and redirection are refused outright — bar `2>&1` and `2>/dev/null`, which write nothing — and
so is an argument that points outside the workspace root — a guard rail rather than a sandbox, described in `docs/config.md`. Sirdar never writes to
the tracker or the helpdesk, in any mode.

`sirdar fix` is the one session that may change files, and only after a human has read the note
(see below). It swaps `permissions.bash` for `permissions.fixBash` and adds `Edit`, `Write` and
`MultiEdit`; everything else is refused exactly as before. Being allowed to edit is not being
allowed to edit anything: every write's path is resolved through symlinks and refused unless it
lands inside the workspace root, and refused again under any `.git/` directory, the workspace's
own `.sirdar/`, or the directory this repository sets `core.hooksPath` to — husky, lefthook or
a checked-in `.githooks/` — which Sirdar reads once when the session starts. Those comparisons
fold case, so `.GIT/hooks/pre-commit` is refused on macOS's case-insensitive filesystem as
surely as `.git/hooks/pre-commit` is on Linux. Sirdar's own commit and push both pass
`--no-verify`, so a hook written during the session is not executed by either.

How much of that a given provider enforces depends on the provider, so the third layer does not
depend on any of them:

| Provider | What confines the session |
| --- | --- |
| `claude`, `openai` | The permission policy judges every editing call, the tools re-check the path themselves, and the snapshot check runs afterwards |
| `codex` | Codex's own `workspace-write` sandbox confines the session; Sirdar's policy is not consulted, and the snapshot check runs afterwards |
| `acp` | Whatever the agent implements, plus the snapshot check |

**The snapshot check** is that third layer. Before the session starts, `sirdar fix` takes a
sha256 of every file under `.sirdar/` (bar its own run records) and under the directory git runs
this repository's hooks from. The moment the session ends — before the first git command — it
takes them again. Any difference fails the run and says which files changed: nothing is
restored, nothing is committed, and nothing is pushed, which is the half that matters, because
a hook only becomes code the machine runs at the next commit or push.

## Fix flow

`sirdar fix KEY` is the only part of Sirdar that writes anything, and it is gated on a person.

**The gate is the triage note.** The command refuses to start unless the note's frontmatter
`status` is `triaged` or `fix-approved`. There is no separate approval record: running the
command *is* the approval, so read the note before you run it. If you have changed your mind
about a note, change its status and the fix will not run.

What happens, in order:

1. **Preflight.** The working tree must be clean, or the run stops before anything else — a fix
   commits everything in the tree, and it must not sweep up your uncommitted work. Then
   `git fetch origin`, and the default branch is read from `origin/HEAD`.
2. **Branch.** `git checkout -B fix-<key>-<slug> origin/<default>`. The default branch is never
   committed to and never force-pushed; neither is anything else.
3. **One session**, with write permission, told to implement the note's Proposed Fix and nothing
   else, to run the workspace's build and tests, and to make no commits of its own. It answers
   with JSON: summary, files changed, tests run, risks, and `deviationFromNote`.
4. **Commit.** Everything but `.sirdar/` is staged and committed as `fix: <summary>`, with the
   note's root cause in the body and **no AI attribution trailer of any kind**.
5. **Push and PR.** `git push -u origin <branch>`, then `gh pr create` with the title
   `[KEY] fix: <summary>` and a body carrying the symptom, the root cause, the fix, and the
   tracker and helpdesk links from the note. Without `gh`, the compare URL and the ready-to-paste
   title and body are printed instead.
6. **Record.** The triage note's frontmatter becomes `status: fix-pushed` with `pr:` and
   `commit:`, in both the run copy and the filed copy, and a `kind: fix` row goes into the
   register.

**The deviation rule.** If the agent's `deviationFromNote` is non-empty — it found the cause
elsewhere, the code had moved, the note's fix would have broken something — the commit is made
and **nothing is pushed**. The deviation is printed prominently, the branch stays local, and the
command exits 1. Read the diff; if you accept it, rerun with `--accept-deviation`, or push the
branch yourself. This is the point of the field: a fix that quietly became a different change is
the failure worth catching, so the agent is asked to declare it and the harness stops on it.

The rerun pushes **the commit you read**. `--accept-deviation` on a key whose fix branch still
points at the commit the blocked run recorded skips the agent entirely: it pushes that commit
and opens the pull request for it. If the branch has moved on or is gone, the ordinary flow
runs from scratch.

`--dry-run` makes the branch and writes the prompt without starting an agent, which is how to
read exactly what would be sent. `--no-pr` pushes and stops. `--base BRANCH` overrides the
branch to cut from and target.

## Models

`provider: claude`, `provider: codex` and `provider: qwen` spawn the Claude Code, Codex or Qwen
Code CLI you already have installed and signed in, so the work counts against the plan you
already pay for. `provider: openai` spawns nothing: Sirdar runs the agent loop itself against
any OpenAI-compatible Chat Completions endpoint — OpenRouter, Groq, Together, DeepSeek,
Moonshot, Zhipu, or Ollama, vLLM and llama.cpp on your own machine — with its own read-only tool
set and your workspace's MCP servers, and a per-million-token price you set in config for the
USD budget.

`provider: qwen` sits between the two. Qwen Code is a full agent harness like Claude Code —
its own tools, its own compaction, its own MCP client — and despite the name it points at any
OpenAI-compatible endpoint, so one `qwen:` block gets you a vendor model, an aggregator, or a
server on your own machine without Sirdar owning the loop. What you give up against Claude Code
is the cost signal: Qwen Code reports no spend, so `budget.maxUsd` never bites and a run is
bounded by turns and wall-clock time instead.

`provider: acp` reaches the widest: one Agent Client Protocol client that drives
any agent speaking it — Gemini CLI, Goose, OpenCode, Qwen Code, Kimi CLI, Crush and about forty
more, plus Claude Code and Codex through the ACP adapters. Name the agent's launch command in
the `acp:` block and Sirdar spawns it, hands it the workspace's MCP servers and answers its
permission requests from the same policy every other provider uses. ACP reports no cost and
counts a whole prompt turn as one turn, so `budget.maxMinutes` is what actually bounds those
runs; it also has no schema field, so the note comes back as JSON in the agent's own message
rather than as structured output. And because an ACP agent is a whole CLI with its own tools and
its own MCP configuration, the permission policy covers what the agent chooses to ask about —
`docs/config.md` says where that reaches and where it does not.

`provider: claude` also works against an Anthropic-compatible endpoint — Ollama, llama.cpp,
DeepSeek, GLM, Kimi, OpenRouter — by setting `billing: api` and pointing `ANTHROPIC_BASE_URL` at
it in the environment; Anthropic documents the gateway variables that make this work but does not
support routing non-Claude models through them, and reported cost is unreliable there, so see
`docs/research/providers/spike-anthropic-compatible.md` before relying on `budget.maxUsd`.

See `docs/config.md` for the `openai:`, `qwen:` and `acp:` blocks,
`docs/research/09-qwen-wire-formats.md` for the Qwen Code capture the adapter is built on,
`docs/research/providers/acp-agents.md` for the ACP agents and their launch commands, and
`docs/superpowers/plans/2026-09-10-provider-roadmap.md` for what comes after them.

## Bring your own agent login

Sirdar spawns the `claude`, `codex` or `qwen` binary already installed on your machine and
signed in with your own account; it never stores or proxies your credentials. Usage counts against your
existing Claude or ChatGPT plan the same way an interactive session would. If you'd rather pay
per token instead, set `billing: api` in config and put an API key in the provider's environment.
See `docs/research/03-licensing-byo-subscription.md` for the licensing research behind this.

## Adapters

Sirdar talks to trackers and helpdesks through adapters. Several ship built into the binary:

| Kind | Supported |
|---|---|
| Trackers | Jira Cloud, Jira Data Center, Linear, Azure DevOps, Rally |
| Helpdesks | Zoho Desk, Zendesk, Freshdesk, Help Scout, Intercom, HubSpot Service Hub (more planned, see `docs/research/adapters/helpdesks.md`) |

Anything else — Janus-style trackers, an internal tracker, a different helpdesk — is a separate
executable speaking a small line-delimited JSON protocol over stdin/stdout, named in config, so
a vendor integration and its credentials never touch Sirdar's core or this repository. See
`docs/adapters.md`.

## Inbound triggers

`sirdar serve` can take a webhook from your tracker or helpdesk and start the triage itself, so a
ticket assigned to you is already triaged by the time you open it. Nine sources are verified —
Jira (via an Automation rule), Linear, Azure DevOps service hooks, Rally, Zendesk, Freshdesk,
Intercom, HubSpot, and a generic endpoint you can curl — each with its own signature or shared
secret, a five-minute replay window where the vendor signs a timestamp, a `match` filter so only
what is assigned to you starts a run, and a cooldown so an afternoon of editing one ticket does
not start twelve.

They are off until `webhooks.enabled: true`, and `serve` binds loopback, so exposing them is a
decision: `--allow-remote` behind a TLS reverse proxy, or a tunnel. `docs/webhooks.md` has the
per-source setup steps, the URL shape, and the warnings.

## Languages

Arabic is the default customer language in the workspace this was built for; Sirdar keeps the
original text, writes the engineer's note in English, and drafts customer-facing text in the
customer's language. A triage note carries the complaint translated and again verbatim, plus a
short reply draft the engineer can send; an RCA carries a customer summary for the support
agent to relay. Neither draft may promise a fix, a cause or a date, and Sirdar sends nothing
itself. Set `language.notes` and `language.customer` in `.sirdar/config.yaml` for a workspace
that reads a different pair.

## Configuration

See `docs/config.md` for every `.sirdar/config.yaml` key, its default, and what it means,
including credential references, the `permissions.bash` and `permissions.fixBash` glob syntax,
the `language` block, and template overrides. `docs/eval.md` covers the golden set and how
`sirdar eval` scores it; `docs/webhooks.md` covers the `webhooks` block.

A `notify:` block posts a one-message digest of every finished run — key, state, confidence,
cost and the note's path, never the note's text — to Slack, Microsoft Teams or your own webhook;
see `docs/notifications.md`.

## Development

```
make build   # ./sirdar
make test    # go test ./...
make vet     # go vet ./...
```

Provider tests never touch the real CLI: the test binary replays a canned stream-json script and
is handed to the provider as `SessionSpec.Binary`, so nothing is looked up on `PATH`. The same
override is available to you in config as `providers.claude.path`, `providers.codex.path`
and `qwen.path`.
The tests therefore run offline and deterministically.

Release builds via `.goreleaser.yaml` stamp the version, commit, and date with
`-X main.version=... -X main.commit=... -X main.date=...`; see `docs/release.md`.

## License

Apache License 2.0. See `LICENSE`.
