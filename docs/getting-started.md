# Getting started

This walks through installing Sirdar, wiring up a workspace, and producing your first triage
note. It assumes you already have a coding-agent CLI installed and signed in — Claude Code or
Codex — since Sirdar spawns that binary rather than talking to a model directly.

## Install

The simplest path is `go install`, which pulls the latest tagged release straight from source:

```sh
go install github.com/srivathsanvenkateswaran/sirdar/cmd/sirdar@latest
```

A Homebrew tap is planned but not published yet — it needs a first tagged release to point at.
Until then, `go install` or a source build are the supported paths:

```sh
git clone https://github.com/srivathsanvenkateswaran/sirdar
cd sirdar
make build
```

Once releases start shipping, `.goreleaser.yaml` produces `tar.gz` archives per OS and
architecture (`darwin`/`linux`, `amd64`/`arm64`) attached to each GitHub release, for anyone who
would rather download a binary than build one.

## Scaffold a workspace

Run `sirdar init` from the root of the codebase the ticket work belongs to — the same repo the
coding agent will investigate in. It writes `.sirdar/config.yaml` with placeholders and
comments, plus a `.sirdar/playbooks/` directory of generic evidence-source playbooks, and
excludes `.sirdar/runs/` and the register from git:

```sh
cd <your codebase>
sirdar init
```

Add `--templates` if you also want the three embedded note templates written out to
`.sirdar/templates` as a starting point for customizing them, and `--force` to overwrite an
existing config.

## Edit the config

`.sirdar/config.yaml` tells Sirdar which tracker and helpdesk to read from, where notes go, and
which provider drives runs. A minimal setup reading Zoho Desk as both tracker and helpdesk, with
notes filed into an "exec tracker" style vault, looks like this:

```yaml
workspace: acme-api
provider: claude
sources:
  helpdesk:
    adapter: zohodesk
    orgId: "60044805777"
    baseUrl: https://desk.zoho.com
    auth:
      clientId: keychain:zoho-desk-client-id
      clientSecret: keychain:zoho-desk-client-secret
      refreshToken: keychain:zoho-desk-refresh-token
notes:
  dir: ~/Documents/Support/notes
```

A workspace with a real tracker in front of a separate helpdesk looks different — here Jira
carries the ticket and Zendesk carries the customer conversation:

```yaml
workspace: acme-api
provider: claude
sources:
  tracker:
    adapter: jira
    baseUrl: https://acme.atlassian.net
    email: env:JIRA_EMAIL
    apiToken: env:JIRA_API_TOKEN
    projectKey: OMNI
  helpdesk:
    adapter: zendesk
    subdomain: acme
    email: env:ZENDESK_EMAIL
    apiToken: env:ZENDESK_API_TOKEN
notes:
  dir: ~/Documents/Support/notes
```

Every credential in config is a reference (`env:NAME` or `keychain:SERVICE`), never a literal
secret. See [Configuration](config.md) for every key and [Sources](adapters.md) for the full
list of built-in adapters and how to write your own.

## Check the setup

`sirdar doctor` verifies the provider CLI is installed and signed in, that each configured
source can answer an authenticated call, and that the notes directory and templates are usable
— before you spend an agent session finding out one of them isn't:

```sh
sirdar doctor
```

## Run your first triage

Pass the tracker key (or the helpdesk id, if the workspace has no tracker configured) to
`sirdar triage`. It fetches the ticket, writes a bundle to disk, runs one agent session inside
the workspace, and writes a Triage Note:

```sh
sirdar triage OMNI-1234
```

Add `--dry-run` the first time if you want to see the bundle and the exact prompt Sirdar would
send, without spending an agent session on it.

## Read the note

The triage note lands in `notes.dir` and a copy sits in the run directory at
`.sirdar/runs/OMNI-1234/<run-id>/note.md`. It states the translated complaint, a conversation
summary, repro steps, a root-cause hypothesis with a confidence level and cited evidence, a
proposed fix, and open questions. Treat the hypothesis as a starting point for your own
investigation, not a verdict — review it before acting on anything in it.

## Start the board

`sirdar serve` starts the local web UI and API on loopback, for watching runs and reviewing
notes from a browser instead of the terminal:

```sh
sirdar serve --open
```

It binds to `127.0.0.1` and refuses a non-loopback address unless you pass `--allow-remote`,
since there is no authentication in front of it. The same frontend also ships as a desktop app
(Wails, under `desktop/` in the repo) for running Sirdar without a terminal open at all.

## Close the loop

Once a human has made and merged the fix, `sirdar rca` produces the RCA note (why it happened)
and a Resolution note draft (what changed) from the merged PR and your own account of what was
done, and marks the triage note resolved:

```sh
sirdar rca OMNI-1234 --pr https://github.com/acme/api/pull/456 \
  --resolution "Backfilled the null customer_id rows and added a NOT NULL constraint."
```

Anything the agent can't source from the PR or your `--resolution` text is left as a visible
`<fill: ...>` marker in the Resolution note, for you to complete by hand.
