# Sirdar

Sirdar is an open-source harness that works a support ticket end to end: it writes the triage,
implements the fix, and writes the root cause. A ticket lands in a helpdesk or tracker; a coding
agent you already have a login for (Claude Code, Codex, Copilot, Cursor, Qwen, OpenCode, Kimi, or
any OpenAI-compatible model) reads it with the code, the logs and the MCP servers your workspace
grants it, and answers with a triage note: a root-cause hypothesis with cited evidence, a proposed
fix, and a reply draft in the customer's language.

You read the note. If you agree, `sirdar fix` runs a second session on its own branch in a linked
git worktree, implements that fix while you steer it from the composer, and shows you the diff to
accept or reject. Once the fix is merged, `sirdar rca` writes the two notes that close the loop: an
RCA note explaining why the bug happened, and a Resolution note recording exactly what changed.

It runs on your own machine, under your own logins. The triage session can only read, the fix
session can only write inside its worktree, nothing is pushed without you, and Sirdar never posts
to the tracker or helpdesk.

On a Himalayan expedition the sirdar is the lead Sherpa: the one who assigns the team's work,
decides who goes up and when, and answers to the client for the outcome. The name is a tribute
to the Sherpa people, whose work on the mountain makes every ascent possible and is rarely the
part that gets photographed.

## Where to start

- New to Sirdar: [Getting started](getting-started.md) — install it, run `sirdar init`, and
  produce your first triage note.
- Understanding the model: [Concepts](concepts.md) — workspaces, bundles, playbooks, runs, and
  the read-only guarantees that keep every run safe to leave unattended.
- Wiring up your workspace: [Configuration](config.md) for every `.sirdar/config.yaml` key, and
  [Sources](adapters.md) for the trackers and helpdesks Sirdar talks to.
- Why Sirdar exists at all: the [research](research/02-landscape.md) behind the decision to
  build it, and the [design spec](superpowers/specs/2026-09-10-sirdar-v0-triage-core-design.md)
  for what v0 actually does.

## Status

v0: command-line triage core; the board is next.
