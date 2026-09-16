---
title: Overview
hide:
  - navigation
  - toc
---

<div class="sd-hero" markdown>
<div class="sd-hero__copy" markdown>

# A ticket arrives. *Something reads it first.*

Sirdar works a support ticket end to end with the coding agent you already have a login for:
it writes the triage note, implements the fix on its own branch, and writes the root cause once
the fix is merged. It runs on your machine, under your logins, and never posts to the tracker.

<div class="sd-hero__actions" markdown>

[Get started](getting-started.md){ .sd-btn }

```sh
go install github.com/srivathsanvenkateswaran/sirdar/cmd/sirdar@latest
```

</div>
</div>

<img class="sd-hero__mark" src="design/2026-09-16-logo/final/sirdar-mark-dark.svg" alt="" width="180" height="180">
</div>

## Start here

<div class="sd-cards" markdown>

- [Getting started](getting-started.md)

    Install it, run `sirdar init`, and produce your first triage note.

- [Concepts](concepts.md)

    Workspaces, bundles, playbooks, runs, and the read-only guarantees that keep a run safe to leave unattended.

- [Configuration](config.md)

    Every key in `.sirdar/config.yaml`, with its default and what it changes.

</div>

## Run it

<div class="sd-cards" markdown>

- [Fix flow](fix.md)

    The one command that writes: a second session on its own branch in a linked worktree, and the diff to accept or reject.

- [Steer](steer.md)

    Continue a finished run with an instruction; the transcript grows in place and the note is rendered again.

- [Evaluation](eval.md)

    Replay tickets you triaged by hand and score what the agent produces against what you produced.

- [Webhooks](webhooks.md)

    Let `sirdar serve` triage a ticket the moment the tracker or helpdesk assigns it.

- [Notifications](notifications.md)

    A short digest of every finished run to Slack, Teams, or an HTTP receiver of your own.

- [Credentials](credentials.md)

    Tokens as references (`keychain:`, `file:`, `cmd:`, `env:`), never as literals in the config.

</div>

## Look it up

<div class="sd-cards" markdown>

- [Adapters](adapters.md)

    The trackers and helpdesks Sirdar reads, and the line-delimited JSON protocol for one it does not.

- [Providers](reference/index.md#providers)

    Claude Code, Codex, Qwen Code, Cursor, any ACP agent, or any OpenAI-compatible endpoint, and how each is held read-only.

- [Architecture](architecture.md)

    The packages, the run lifecycle, the three read-only layers, and how the desktop app observes a run.

- [Cutting a release](release.md)

    goreleaser from a tag; every release opens as a draft and a person publishes it.

</div>

## Behind the product

<div class="sd-cards" markdown>

- [Design](design/index.md)

    One language across the landing page, the desktop app, and this site, with the tokens that carry it.

- [Research](research/00-context.md)

    Why Sirdar exists, the landscape it sits in, and the wire formats of every CLI it drives.

- [Specs and plans](superpowers/specs/2026-09-10-sirdar-v0-triage-core-design.md)

    What v0 does, as designed, and the plans that built it.

</div>

<p class="sd-origin">On a Himalayan expedition the sirdar is the lead Sherpa: the one who assigns
the team's work, decides who goes up and when, and answers to the client for the outcome. The
name is a tribute to the Sherpa people, whose work on the mountain makes every ascent possible
and is rarely the part that gets photographed.</p>
