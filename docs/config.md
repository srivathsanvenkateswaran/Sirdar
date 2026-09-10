# Configuration reference

`sirdar init` writes `.sirdar/config.yaml` for the current workspace with placeholders and
comments; every other command loads it by walking up from the working directory to the nearest
directory that has it. Unknown keys are an error, so a typo in the file fails at load time
rather than being silently ignored.

## Keys

| Key | Type | Default | Meaning |
|---|---|---|---|
| `workspace` | string | directory name (set by `init`) | A label for the workspace; not otherwise interpreted |
| `provider` | string | `claude` | Which agent CLI drives runs: `claude` or `codex` |
| `model` | string | `""` (provider default) | Model name passed to the provider; empty uses the provider's own default |
| `billing` | string | `subscription` | `subscription` strips `ANTHROPIC_API_KEY` from the agent's environment so it uses your CLI login; `api` leaves it in place so usage is billed to the key |
| `sources.tracker` | object, optional | unset | The tracker adapter; see Sources below |
| `sources.helpdesk` | object, optional | unset | The helpdesk adapter; see Sources below |
| `sources.*.adapter` | string | none (required) | `exec` (external adapter process) or `zohodesk` (built in) |
| `sources.*.command` | string | none (required for `exec`) | Path to the adapter executable |
| `sources.*.orgId` | string | none (required for `zohodesk`) | Zoho Desk organisation id |
| `sources.*.baseUrl` | string | none (required for `zohodesk`) | Zoho Desk API base URL |
| `sources.*.token` | string | none (required for `zohodesk`) | Credential reference (`env:NAME` or `keychain:SERVICE`) |
| `notes.dir` | string | `.sirdar/notes` | Where rendered notes are copied; expands `~` and relative paths against the workspace root |
| `notes.templates` | string | `""` (embedded defaults) | Directory holding `triage.md.tmpl`, `rca.md.tmpl`, `resolution.md.tmpl` overrides |
| `notes.filenames.triage` | string | `"{key} {slug}.md"` | Filename pattern for triage notes |
| `notes.filenames.rca` | string | `"{key} RCA {slug}.md"` | Filename pattern for RCA notes |
| `notes.filenames.resolution` | string | `"{key} RES {slug}.md"` | Filename pattern for resolution notes |
| `budget.maxTurns` | int | `60` | Agent turns before a run is marked `over_budget` |
| `budget.maxMinutes` | int | `25` | Wall-clock minutes before a run is cancelled and marked `over_budget` |
| `budget.maxUsd` | float | `5` | Cost, from provider usage events, before a run is marked `over_budget` |
| `concurrency` | int | `1` | Parallel runs across the keys passed to `sirdar triage`; overridable with `--concurrency` |
| `permissions.bash` | list of string | `[]` | Glob patterns the agent's `Bash` tool calls must match to be allowed; see Bash permission globs below |
| `playbooks` | string | `.sirdar/playbooks` | Directory of playbook markdown files loaded into the prompt, in filename order |
| `providers.claude.path` | string | `""` (look up `claude` on `PATH`) | Path to the Claude Code binary |
| `providers.codex.path` | string | `""` (look up `codex` on `PATH`) | Path to the Codex binary |

`{key}` and `{slug}` in a filename pattern are replaced with the ticket key and a slugified
title. `provider`, `billing`, and `concurrency` are validated at load time: an unrecognised
`provider` or `billing` value, or a `concurrency` below 1, fails config load with a message
naming the offending key. Budget values must all be greater than zero. A configured source's
adapter-specific fields are required only for that adapter; `sources.tracker` and
`sources.helpdesk` are each optional, but a source config with no `adapter` set is an error.

## Credential references

`sources.*.token` (and any credential in config) is never a literal secret: config load
rejects a value that doesn't start with `env:` or `keychain:`. Two forms:

- `env:NAME` reads the environment variable `NAME` at fetch time. Works on every platform.
- `keychain:SERVICE` reads a generic password from the macOS login keychain via
  `security find-generic-password -s SERVICE -w`. **macOS only**: on other platforms a
  `keychain:` ref fails to resolve.

Resolved values are held in memory only: never written to a run directory, and never placed in
the agent's environment.

## `permissions.bash` glob semantics

Each entry in `permissions.bash` is a glob pattern matched against the agent's `Bash` tool
command, trimmed of leading and trailing whitespace. A command is allowed if it matches any
pattern in the list; everything else, including every non-`Bash` write tool, is denied.

- `*` matches any run of characters, including spaces and `/`. `git log*` matches
  `git log --oneline -20` and `git log -- some/path`.
  The pattern is a full-string match, not a prefix, so `git log*` also matches
  `git logout`, not just `git log ...`.
- `?` matches exactly one character.
- Matching is case-sensitive.
- The pattern is anchored to the whole command string, not a prefix or substring: `git log`
  without a trailing `*` matches only the exact command `git log`, with no arguments.

## Templates override

Each note type (`triage`, `rca`, `resolution`) has a Go `text/template` file. Defaults are
embedded in the binary; setting `notes.templates` to a directory containing
`triage.md.tmpl`, `rca.md.tmpl`, and/or `resolution.md.tmpl` overrides one or more of them
(a missing file in that directory falls back to the embedded default for that kind).

`sirdar init --templates` writes the three embedded defaults into `.sirdar/templates`, ready to
edit; point `notes.templates` at that directory to use them.

Templates render against `{{.doc ...}}` (the validated note JSON) and `{{.meta ...}}` (run
metadata: key, tracker/helpdesk URLs, customer, dates, run id, provider). Template functions:

| Function | Signature | Use |
|---|---|---|
| `join` | `join sep list` | Joins a JSON array of strings with `sep` |
| `fill` | `fill "NAME" value` | Renders `value`, or a visible `<fill: NAME>` marker when it's empty, for fields only a human can complete |
| `date` | `date value` | Passes a date string through unchanged; a named place to format dates from later |
| `default` | `default fallback value` | Renders `value`, or `fallback` when it's empty |
| `yq` | `yq value` | Renders `value` as a YAML double-quoted, escaped scalar; used on every frontmatter value so colons, quotes, and non-Latin text can't break the frontmatter block |

`sirdar doctor` and `sirdar init` both render every active template against a built-in sample
document and parse the resulting frontmatter as YAML, so a broken override is caught before a
real run rather than after.

## Providers

`provider: claude` (default) or `provider: codex` selects which agent CLI drives runs;
`--provider` on `triage` and `rca` overrides it per invocation.

- `providers.claude.path`: path to the `claude` binary. Empty (the default) looks it up on
  `PATH`.
- `providers.codex.path`: path to the `codex` binary. Empty (the default) looks it up on
  `PATH`.
- `billing: subscription` (default) removes `ANTHROPIC_API_KEY` from the agent's child
  environment so the run authenticates with the CLI's own login and draws on your subscription.
  `billing: api` leaves the key in place, so the run is billed per token against that key
  instead.
