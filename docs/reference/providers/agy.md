# Antigravity

`provider: agy` is disabled. Google's Antigravity terms do not allow driving the CLI from another
program, and an account that does it can be banned, so config load refuses the value and so does
`--provider agy` on `triage` and `rca`.

| | |
|---|---|
| Config value | `provider: agy`, refused at config load |
| Binary | `agy`, or the path in `agy.path` |
| The refusal | `provider agy is disabled: Google's Antigravity terms do not allow driving the CLI from another program; choose claude, codex, openai, acp, qwen or cursor` |
| Taking it off | `agy.acknowledgeTerms: true`, at your own risk and not recommended |
| Doctor | Until that flag is set, one row: `agy — disabled (Antigravity terms)` |
| The adapter | Stays in the tree at `internal/provider/agy`; the reference still describes what it does |

## In the configuration reference

- [`provider: agy`](../../config.md#provider-agy): how a session was allowed to read and nothing
  else, plan mode and slash commands, what the provider cannot do, its environment and doctor
  rows, and what has and has not been verified.

## Related

- [Research: Antigravity wire formats](../../research/10-antigravity-wire-formats.md)
