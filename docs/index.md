# Sirdar

Sirdar is an open-source harness that turns a support ticket into a root-cause note. A ticket
lands in a helpdesk or tracker, and a coding agent you already have a login for — Claude Code,
Codex, or any OpenAI-compatible model — reads it through the MCP servers your workspace grants
it, works out what's going on, and writes up a triage note for a human to review. It runs on
your own machine, under your own logins, and it never touches the tracker or helpdesk itself.

Once a person has made the fix and merged it, Sirdar writes the two notes that close the loop:
an RCA note explaining why the bug happened, and a Resolution note recording exactly what
changed. Sirdar never opens a pull request on its own — it documents a fix a human already
made, rather than making one itself.

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
