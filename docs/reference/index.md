# Reference

What Sirdar reads, what drives a run, and how the pieces fit. The exact config keys are in the
[configuration reference](../config.md); these pages are the map to them.

## Sources and structure { #sources-and-structure }

<div class="sd-cards" markdown>

- [Adapters](../adapters.md)

    The trackers and helpdesks with a built-in adapter, and the line-delimited JSON protocol for running your own.

- [Architecture](../architecture.md)

    The package map, the run lifecycle, the three read-only layers, and how the desktop app observes a run.

</div>

## Providers { #providers }

One page per value of `provider:`. Each names the binary, the keys under its block, the mechanism
that keeps a triage session read-only, and where the configuration reference goes into detail.

<div class="sd-cards" markdown>

- [Claude Code](providers/claude.md)

    `provider: claude`, the default. Write tools off, every other call judged by Sirdar's policy.

- [Codex](providers/codex.md)

    `provider: codex`. Runs in its own read-only sandbox with approvals on.

- [Qwen Code](providers/qwen.md)

    `provider: qwen`. A Gemini CLI fork that talks to any OpenAI-compatible endpoint; permissions through a hook.

- [OpenAI-compatible](providers/openai.md)

    `provider: openai`. Sirdar's own agent loop against any Chat Completions endpoint, hosted or local.

- [ACP agents](providers/acp.md)

    `provider: acp`. One adapter for every agent that speaks the Agent Client Protocol.

- [Cursor Agent CLI](providers/cursor.md)

    `provider: cursor`. Read-only because Cursor says so; `sirdar fix` is refused on it.

- [Antigravity](providers/agy.md)

    `provider: agy`. Disabled: Google's terms do not allow driving the CLI from another program.

</div>
