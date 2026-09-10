# Dogfood follow-ups

What the 2026-09-10 dogfood run
(`.superpowers/sdd/2026-09-10-sirdar-v0-triage-core/dogfood-report.md`) turned up that the
fix pass did not close, with the file and line to start from. D1–D12 are otherwise fixed;
these are the remainders.

## Not fixable inside this repo

- **`ToolSearch` never reaches the permission policy.** The run's event log shows one
  `ToolSearch` call that ran although the tool is in neither `AlwaysAllowed` nor
  `AlwaysDenied` and should have been refused by fallthrough
  (`internal/provider/policy.go:76`). Some Claude Code tools evidently do not route through
  `--permission-prompt-tool`, which means deny-by-default is not the backstop it looks like.
  Needs a scripted probe against the CLI to establish which tools bypass it, and then either
  `--disallowedTools` entries for them or an upstream report.
- **`tracker.list` `assignee` takes a display name (D4).** The contract is now written down
  (`docs/adapters.md:69`): an adapter resolves the value or rejects it, but never answers an
  unresolvable filter with `[]`. The `sirdar-janus` adapter that returned `[]` for an email
  address lives outside this repo and still has to be changed.
- **The playbook set is written for the interactive skill, not for a Sirdar session.**
  `30-apm.md` sends the agent to `security find-generic-password`, which is not on the bash
  allow-list; `40-database.md` describes SQL access through servers that were `failed` for the
  whole run; `10-helpdesk.md` tells it to fetch attachments Sirdar has already downloaded, and
  gives `janus.silqfi.center` as the tracker host where the adapter uses
  `janus.silqtech.ai`. A `00-environment.md` stating what the session actually has is the
  cheapest fix. These are workspace files under `.sirdar/playbooks/`, not repo files. The
  prompt preamble now carries the two rules that cost the most turns (bash segments,
  unreadable attachments) — `internal/prompt/preamble.md:19`.

- **Janus `tracker.get` takes 20-30 s and times out about half the time.** The adapter answers
  a single key by listing 200 tickets in the project
  (`GET /ticket/tickets?limit=200&project_key=OMNI`), which exceeded the 30 s HTTP timeout on
  two of four dry runs on 2026-09-10 and on every `doctor` probe. Doctor now says so in as many
  words (`cmd/sirdar/cmd_doctor.go:180`), but the fix is a `GET /ticket/tickets/<key>` in the
  adapter, which is outside this repo.
- **`CustomerID` is still empty for this workspace.** D6 is fixed — `include=contacts,assignee,
  departments` populates `Customer` ("NEQSA SWEET") and `Contact` — but `CustomerID` comes from
  the `cf_company_id` custom field, which this org does not set on tickets or accounts. The
  agent has to keep resolving the tenant from the thread and the logs, as it did on the first
  run. Either the field gets populated in Desk, or the mapping needs another source.

## Deferred in this repo

- **`doctor` has no warning level.** `provider.Check` is `OK bool`
  (`internal/provider/provider.go:123`), so the MCP row reports "every user-level MCP server
  is visible to the agent" as an `[OK]` line whose detail starts with `warning:`
  (`cmd/sirdar/cmd_doctor.go:217`). A third state — `[??]`, non-zero-exit-free — would read
  better and would suit the "describe passed but the credential is untested" case too.
- **The write-verb heuristic only reads the leading verb.** `MCPLooksLikeWrite`
  (`internal/provider/policy.go:132`) denies `create_incident` and, after stripping a
  repeated server prefix, `slack_send_message`. It allows a write tool whose name does not
  lead with a verb — `grafana_api_request` is the example in this session's own tool list.
  `permissions.mcp` is the answer for a workspace that cares; a per-server default-deny list
  shipped with Sirdar would be better.
- **The prompt still inlines the head of `thread.md`** (`internal/run/prepare.go:38`,
  `internal/prompt/prompt.go:221`). The heading no longer lies about it, but for a thread
  shorter than 40 lines the whole conversation is paid for twice: once inline and once when
  the agent reads the bundle file.
- **Turn counts between result lines are inferred.** The Claude CLI reports `num_turns` only
  on the result line, so the running counter counts assistant messages instead
  (`internal/provider/claude/claude.go:532`). It matched `num_turns` on this run's shape but
  is not the same quantity, and it is corrected the moment the result line lands.
- **Codex reports usage only at the end.** The D10 fix is in the Claude adapter's stream
  reader; `internal/provider/codex/codex.go:673` still fills `Usage` once, on
  `turn/completed`, so a long Codex run shows zeros throughout.
- **The spec's permission table is out of date.** `docs/superpowers/specs/
  2026-09-10-sirdar-v0-triage-core-design.md:331` still reads "any MCP tool → allow", which
  is what D2 was. The design doc is a dated record, so it was left alone; `docs/config.md`
  carries the current rules.
