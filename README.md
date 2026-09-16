<img src="docs/design/2026-09-16-logo/final/sirdar-mark-light.svg" alt="" width="88" height="88">

# Sirdar

[![ci](https://github.com/srivathsanvenkateswaran/Sirdar/actions/workflows/ci.yml/badge.svg)](https://github.com/srivathsanvenkateswaran/Sirdar/actions/workflows/ci.yml)

Sirdar is a read-only harness that turns an engineering-level support ticket into triage, RCA and
resolution notes, and — once you have read the triage note and agree with it — into one confined
fix on a branch. It does that by driving a coding-agent CLI you already pay for (Claude Code,
Codex, Cursor Agent, Qwen Code, GitHub Copilot CLI, and others) inside the codebase the ticket
belongs to. It runs on your laptop under your own logins, and it never writes to the helpdesk or
the tracker, in any mode. On a Himalayan expedition the sirdar is the lead Sherpa: the one who
assigns the team's work and answers to the client for the outcome.

## How it works

1. `sirdar init` in the repository the ticket is about. `.sirdar/config.yaml` names the tracker
   and the helpdesk to read from, where notes are filed, and what a run may do.
2. `sirdar triage KEY` fetches the ticket and its whole customer conversation, downloads the
   attachments, and writes them to disk as a bundle under `.sirdar/runs/`.
3. One agent session runs inside the workspace with that bundle, read-only, with whatever MCP
   servers the workspace grants it. It answers with a triage note: the complaint translated and
   verbatim, repro steps, a root-cause hypothesis with cited evidence and a confidence level, a
   proposed fix, open questions, and a reply draft in the customer's language.
4. You read the note. If you agree with its proposed fix, `sirdar fix KEY` runs a second session
   in a linked git worktree, allowed to edit only inside it — implement that fix and nothing else,
   run the build and tests — then commits, pushes, and opens the pull request.
5. After the fix is merged, `sirdar rca KEY --pr URL --resolution ...` writes the RCA note (why it
   happened, and whether the triage hypothesis held) and a Resolution note draft (what changed),
   leaving anything it cannot source as a visible `<fill: ...>` marker for you.

The hypothesis in a triage note is a starting point for your own investigation, not a verdict.
Running `sirdar fix` is the approval — there is no separate approval record, which is why the
command refuses to start unless the note's status is `triaged` or `fix-approved`. If the agent's
fix departs from the note, the commit is made and nothing is pushed until you accept it.

## What you get

The desktop app is a native window (Wails) over the same core the CLI uses. `sirdar serve --open`
runs the identical frontend in your browser over a loopback HTTP API instead. Both give you:

- **Session** — the run's transcript as it happens: tool calls with their results, the agent's
  prose, permission decisions, the question a blocked run is waiting on, and a composer that
  answers it or steers a finished run. Three layouts draw the same run, switched from the session
  header or Settings › General: Conversation (the transcript as a chat with an inspector beside
  it), Document (the note or the change as the window), Workbench (documents in tabs over a
  console of every call).
- **Board** — every run as a card in the lane its state puts it in, with the tracker queue and the
  day's inbound webhook deliveries beside them.
- **Register** — one row per ticket: triage date, confidence, classification, fix and RCA dates,
  verdict, severity, resolution, and which notes exist. **Eval** holds the golden set and its
  reports.
- **Settings** — a modal over whatever you were on: the workspace's `config.yaml` read back and
  never written, providers with what `doctor` says about each, MCP servers with Test, and Try a
  tool, which runs one MCP tool by hand under the workspace's own permissions.

The CLI covers the same ground: `init`, `doctor`, `triage`, `rca`, `fix`, `resume`, `steer`,
`runs` (including `runs diff --drop`), `register`, `eval`, `golden add`, `mcp`, `serve`.

## Providers

Every provider signs in as itself. Sirdar spawns a CLI you have already installed and logged in
to, so a run counts against the plan you already pay for, and it stores and proxies no credentials
of yours; where a provider needs an API key instead, the key stays a reference in config
(`env:`, `keychain:`, `file:`, `cmd:`), never a literal.

| Provider | How it signs in | `sirdar fix` | What makes a run read-only |
|---|---|---|---|
| `claude` | The Claude Code CLI login you already hold; `billing: api` leaves an API key in the environment instead | yes | Every tool call the CLI is not already allowed to make arrives as a permission request and Sirdar's policy answers it; the write tools are refused outright |
| `codex` | The Codex CLI login you already hold | yes | Codex's own `sandbox: read-only` with `approvalPolicy: untrusted`, so shell, MCP and file-change calls are asked about |
| `openai` | No CLI at all: an OpenAI-compatible Chat Completions endpoint (OpenRouter, Groq, Together, DeepSeek, Moonshot, Zhipu, or Ollama, vLLM and llama.cpp on your own machine) plus a key reference | yes | Sirdar runs the agent loop itself, so the policy decides before every call and each tool re-checks the same rule inside itself |
| `acp` | The agent's own CLI login — GitHub Copilot CLI, OpenCode, Moonshot's Kimi CLI, Gemini CLI, Goose, and the rest of the Agent Client Protocol ecosystem | yes | The policy answers every `session/request_permission` and `fs/read_text_file`, and the adapter selects the agent's read-only session mode first. A write or command that completes having asked nobody fails the run |
| `qwen` | Qwen Code's own OAuth login, or a base URL and key you name for any OpenAI-compatible endpoint | yes | `--exclude-tools` for every non-read tool, plus an authenticated, fail-closed loopback `PreToolUse` hook |
| `cursor` | The login `cursor-agent` already holds | refused | Not mediated: `cursor-agent -p` approves its own tool calls. What holds is Cursor's `--mode ask`/`plan`, an excluded-tool header and `--sandbox enabled`. A completed edit or shell call fails the run |
| `agy` | Disabled — see below | refused | — |

`provider: agy` drives Google's Antigravity CLI and is **disabled**: Google's Antigravity terms do
not allow driving the CLI from another program, and an account that does it can be banned. Config
load and `--provider agy` both refuse it, `sirdar doctor` prints `agy — disabled (Antigravity
terms)`, and the adapter stays in the tree for reference only.

`permissions.bash`, `permissions.mcp`, `permissions.fetch` and the read scope reach the first five
rows. On `cursor` there is no call for Sirdar to judge, so `sirdar doctor` carries a warning row
and every session records the same on its own event log. `docs/config.md` has a section per
provider; `docs/fix.md` covers the fix flow in full.

## Sources

Thirteen adapters ship in the binary, all on shared HTTP helpers that pin host trust, restrict
redirects, honour `Retry-After` and cap reads:

| Role | Adapters |
|---|---|
| Tracker | Jira (Cloud and Data Center), Linear, Azure DevOps, Rally, ServiceNow |
| Helpdesk | Zoho Desk, Zendesk, Freshdesk, Help Scout, Intercom, HubSpot Service Hub, Front, Gorgias, ServiceNow |

ServiceNow appears on both rows because one incident is both records. Anything else — an internal
tracker, a helpdesk nobody else uses — is `adapter: exec`: a separate executable speaking a small
line-delimited JSON protocol over stdin and stdout, so a vendor integration and its credentials
never touch this repository (`docs/adapters.md`).

## Install

Nothing has been tagged or released yet, so there is no download and the Homebrew tap
(`srivathsanvenkateswaran/homebrew-sirdar`) does not exist. Until the first tag:

```
go install github.com/srivathsanvenkateswaran/sirdar/cmd/sirdar@latest   # the CLI

git clone https://github.com/srivathsanvenkateswaran/sirdar && cd sirdar
make build      # ./sirdar
make install    # macOS: builds the desktop app into /Applications/Sirdar.app
```

`make install` needs the Wails CLI (`go install github.com/wailsapp/wails/v2/cmd/wails@v2.15.0`);
on Linux the desktop app also needs WebKitGTK 4.1 (`libwebkit2gtk-4.1-dev`, `-tags webkit2_41`).
Once a release is cut, `.goreleaser.yaml` attaches CLI archives for darwin, linux and windows on
amd64 and arm64, deb and rpm packages, `checksums.txt`, and a desktop zip per platform, and the
Homebrew formula pushes to the tap once that repo and its token exist (`docs/release.md`).

The desktop builds are unsigned — no Apple notarization, no Windows code-signing certificate. On
macOS, right-click the app and choose Open, or run
`xattr -d com.apple.quarantine /path/to/Sirdar.app`. On Windows, unzip
`sirdar-desktop_<tag>_windows_amd64.zip` and run `Sirdar.exe`: SmartScreen shows "Windows protected
your PC" the first time, so choose More info → Run anyway, and a file downloaded through a browser
may also need Properties → Unblock. The app is a window around Microsoft's
[WebView2 runtime](https://developer.microsoft.com/microsoft-edge/webview2/), preinstalled on
Windows 11 and current Windows 10; on an image without it the window opens and draws nothing until
you install the Evergreen Bootstrapper.

## Quick start

```
cd <your codebase>
sirdar init          # writes .sirdar/config.yaml, playbooks, and git excludes
```

Edit `.sirdar/config.yaml` for the workspace — which tracker and helpdesk to read, where notes go,
what a run may run and fetch, how much budget it gets (`docs/config.md` documents every key) — then:

```
sirdar doctor        # provider CLI, each source, the notes directory, the templates
sirdar triage SBX-1  # one run; --dry-run writes the bundle and prompt without spending a session
sirdar serve --open  # the same screens as the app, in your browser on loopback
```

`docs/getting-started.md` walks the same path with a worked config.

## Safety model

- **Read-only by construction.** Sirdar never writes to a helpdesk or a tracker in any mode.
  `sirdar fix` is the one session allowed to change files, and it writes only to git and GitHub,
  behind a gate a human passes by reading the note.
- **Permission mediation is per provider, not by convention.** No two providers rest the guarantee
  on the same mechanism; the table above says what each one's rests on, and `sirdar doctor` warns
  about `cursor`, the one Sirdar cannot mediate at all.
- **Allow-lists decide, and they start empty or narrow.** `permissions.bash` (and `fixBash` for a
  fix session) match every segment of a shell command, refusing command substitution and
  redirection; `permissions.mcp` matches MCP tool names over a write-verb heuristic;
  `permissions.fetch` lists the hosts a session may fetch from and is empty by default, so a
  workspace that has not said otherwise fetches nowhere — which matters because a triage reads
  attacker-supplied text all day.
- **Reads are judged on the path, not the tool name.** A session may open the workspace, its own
  run directory and the bundle staged inside it, symlinks resolved first; anything else is refused
  with `read outside the workspace: <path>`. `permissions.readAlso` widens it to a runbook or
  skills directory you name.
- **No secret is ever written to disk by Sirdar.** Every credential in config is a reference —
  `env:NAME`, `keychain:SERVICE`, `file:PATH`, `cmd:COMMAND` — resolved at use, and the MCP
  listings print the names of the env vars and headers a server carries, never their values.
- **One login, yours.** There is no Sirdar account and no proxy: it spawns the agent CLI already
  signed in on your machine, and stores no credential of yours anywhere.

A fix session gets a third layer on top of those: Sirdar takes a sha256 of everything under
`.sirdar/` and under the directory git runs this repository's hooks from before the session starts
and again the moment it ends, before the first git command. Any difference fails the run and names
the files — nothing is committed and nothing is pushed.

## Design

The [design language](docs/design/00-design-language.md) and the
[tokens](docs/design/01-tokens.md) it derives; the [screen mocks](docs/design/2026-09-15-screens)
the UI was built from and the three [Session directions](docs/design/2026-09-16-session) (open
either `index.html`); the [logo round](docs/design/2026-09-16-logo) and its shipped files. Ticket
SBX-1 in the mocks is a sandbox ticket, and every name, key and server in them is fabricated.

## Status

Pre-release. Everything described above exists and is covered by tests, but how much of it has
been watched work against a real service varies.

Run live: `provider: claude`, through repeated real triage and fix runs, plus `sirdar steer`,
`sirdar runs diff --drop` and `sirdar mcp list/tools/call` against a real MCP server.
`provider: acp` on three agents — GitHub Copilot CLI passed triage, rca and fix; OpenCode passed
triage and fix; Kimi CLI got no model turn at all, its free tier's quota having been spent before
the first prompt. `provider: qwen` ran against a real OAuth login, and the two bugs those runs
found are fixed but not yet watched succeed. `provider: agy` ran twice before it was disabled.

Fixtures only, never a real model or a real destination: `provider: openai`, `notify`, and the
inbound `webhooks`. `provider: cursor` is built from a capture of seven small turns, so a refused
or rate-limited turn is inferred rather than observed. `permissions.fetch` has been exercised
against fake CLIs, not a live agent's own fetch request shape. Codex's fix-mode file-change
approval has not been watched fire; the snapshot check is the backstop if it does not. And
`sirdar serve`'s same-origin guard is tested with hand-built requests, not a real cross-site page.

Nothing on Windows has been run on Windows. The cross-build produces a real `Sirdar.exe` and the
`windows` CI job builds and tests on `windows-latest`, but whether WebView2 draws the frontend,
whether the Credential Manager reader finds a stored secret, and whether `taskkill /T` reaps a
wrapper's grandchildren stay open until someone sits at a Windows desktop. Shortcut hints are also
drawn with ⌘ on every platform, though every handler accepts Ctrl. `HANDOFF.md` keeps the long
version of this list.

## Contributing

[`CONTRIBUTING.md`](CONTRIBUTING.md) covers the development loop (`make build`, `test`, `vet`,
`ui`, `desktop`), the frontend's own checks, the commit conventions, and what a pull request
needs; [`docs/architecture.md`](docs/architecture.md) maps the packages; and
[`CODE_OF_CONDUCT.md`](CODE_OF_CONDUCT.md) applies to everyone taking part. Report a security issue
the way [`SECURITY.md`](SECURITY.md) describes, not through a public issue.

## Licence

Apache License 2.0. See [`LICENSE`](LICENSE).
