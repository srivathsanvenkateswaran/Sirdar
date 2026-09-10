# Sirdar

An open-source harness for engineering-level support tickets. A ticket comes in from a helpdesk
or tracker, a coding agent you already pay for (Claude Code, Codex) — or any OpenAI-compatible
model — gathers evidence through your own read-only MCP servers, translates the conversation, and writes a root-cause note for you to
review. Once a human has made and merged the fix, Sirdar writes the RCA and Resolution notes that
record it — it never opens a PR itself. Runs on your machine with your logins.

On a Himalayan expedition the sirdar is the lead Sherpa: the one who assigns the team's work,
decides who goes up and when, and answers to the client for the outcome. The name is a tribute
to the Sherpa people, whose work on the mountain makes every ascent possible and is rarely the
part that gets photographed.

Status: v0: command-line triage core; the board is next.

## Install

```
go install github.com/srivathsanvenkateswaran/sirdar/cmd/sirdar@latest
```

Or build from source:

```
git clone https://github.com/srivathsanvenkateswaran/sirdar
cd sirdar
make build
```

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
| `sirdar triage KEY [KEY...]` | `--provider claude\|codex\|openai`, `--model NAME`, `--concurrency N`, `--dry-run` | Runs triage for one or more keys and prints a digest; `--dry-run` writes the bundle and prompt without starting the agent |
| `sirdar rca KEY` | `--pr URL`, `--resolution TEXT\|@FILE`, `--provider claude\|codex\|openai`, `--model NAME` | Produces the RCA note and the Resolution draft for a resolved ticket |
| `sirdar resume RUN_ID` | none | Continues a blocked or interrupted run |
| `sirdar runs [KEY]` | `--json` | Lists runs and their states, optionally filtered to one key |
| `sirdar register` | `--markdown` print rows in the vault's issue-register table shape | Prints one row per ticket: triage date, confidence, classification, RCA date, verdict, severity, resolution, and which notes exist |
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

The agent session runs with permission to read the workspace and to run the `Bash` commands
listed under `permissions.bash` in config; `Edit`, `Write`, `MultiEdit`, and `NotebookEdit` are
always denied. Sirdar never writes to the tracker or helpdesk and never opens a PR itself: the
RCA and Resolution notes record a fix a human already made.

## Models

Sirdar drives a run in one of two ways. `provider: claude` and `provider: codex` spawn the
Claude Code or Codex CLI you already have installed and signed in, so the work counts against
the plan you already pay for. `provider: openai` spawns nothing: Sirdar runs the agent loop
itself against any OpenAI-compatible Chat Completions endpoint — OpenRouter, Groq, Together,
DeepSeek, Moonshot, Zhipu, or Ollama, vLLM and llama.cpp on your own machine — with its own
read-only tool set and your workspace's MCP servers, and a per-million-token price you set in
config for the USD budget. See `docs/config.md` for the `openai:` block, and
`docs/superpowers/plans/2026-09-10-provider-roadmap.md` for what comes after it.

## Bring your own agent login

Sirdar spawns the `claude` or `codex` binary already installed on your machine and signed in
with your own account; it never stores or proxies your credentials. Usage counts against your
existing Claude or ChatGPT plan the same way an interactive session would. If you'd rather pay
per token instead, set `billing: api` in config and put an API key in the provider's environment.
See `docs/research/03-licensing-byo-subscription.md` for the licensing research behind this.

## Adapters

Sirdar talks to trackers and helpdesks through adapters: small processes speaking a
line-delimited JSON protocol over stdin/stdout, so a vendor integration and its credentials
never touch Sirdar's core or this repository. Zoho Desk ships built in; anything else, such as
Jira or Janus or an internal tracker, is a separate executable named in config. See
`docs/adapters.md`.

## Configuration

See `docs/config.md` for every `.sirdar/config.yaml` key, its default, and what it means,
including credential references, the `permissions.bash` glob syntax, and template overrides.

## Development

```
make build   # ./sirdar
make test    # go test ./...
make vet     # go vet ./...
```

Provider tests never touch the real CLI: the test binary replays a canned stream-json script and
is handed to the provider as `SessionSpec.Binary`, so nothing is looked up on `PATH`. The same
override is available to you in config as `providers.claude.path` and `providers.codex.path`.
The tests therefore run offline and deterministically.

Release builds via `.goreleaser.yaml` stamp the version with `-X main.version=...`.

## License

Apache License 2.0. See `LICENSE`.
