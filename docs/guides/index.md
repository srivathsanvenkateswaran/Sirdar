# Guides

How to wire a workspace, run the three commands, and keep the loop honest. Each page stands
alone; the [Getting started](../getting-started.md) walk-through is the order to read them in
the first time.

## Set it up { #set-it-up }

<div class="sd-cards" markdown>

- [Configuration](../config.md)

    Every key in `.sirdar/config.yaml`: sources, providers, budgets, permissions, and what each default is.

- [Credentials](../credentials.md)

    Tokens as references to the keychain, a file, a helper command, or an environment variable. Never a literal.

</div>

## Run it { #run-it }

<div class="sd-cards" markdown>

- [Fix flow](../fix.md)

    The one command that writes: a second session on its own branch in a linked worktree, and the diff to accept or reject.

- [Steer](../steer.md)

    Continue a finished run with an instruction; the transcript grows in place and the note is rendered again.

- [Evaluation](../eval.md)

    Replay tickets you triaged by hand and score what the agent produces against what you produced.

</div>

## Let it run without you { #let-it-run-without-you }

<div class="sd-cards" markdown>

- [Webhooks](../webhooks.md)

    Let `sirdar serve` triage a ticket the moment the tracker or helpdesk assigns it.

- [Notifications](../notifications.md)

    A short digest of every finished run to Slack, Teams, or an HTTP receiver of your own.

</div>

## Ship it { #ship-it }

<div class="sd-cards" markdown>

- [Cutting a release](../release.md)

    goreleaser from a tag; every release opens as a draft and a person publishes it.

</div>
