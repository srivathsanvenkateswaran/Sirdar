# Fix flow

`sirdar fix KEY` is the one Sirdar command that writes anything. Everything else in Sirdar is
read-only; this is the exception a person opens deliberately, after reading a triage note.

## Prerequisite: an approved triage note

`sirdar fix` reads the newest completed triage note for `KEY` and refuses to start unless its
frontmatter `status` is `triaged` or `fix-approved`. Any other status — `resolved`, `fix-pushed`,
`wont-fix`, or a status a workspace invented — means the ticket has moved on, and the command
tells you to read the note and re-triage or restore its status rather than running blind.

There is no separate approval record. Running the command *is* the approval: a person reads the
note's Proposed Fix, agrees with it, and runs `sirdar fix`. If you change your mind, change the
note's status and the fix will not run.

`sirdar rca` and `sirdar fix` both skip eval runs when they look for the newest triage note, so a
replay from `sirdar eval` can never be mistaken for a real triage.

## What the run does

1. **Preflight.** The working tree must be clean — a fix stages and commits everything in it, so
   it must not sweep up uncommitted work. A tree whose only uncommitted entries sit under
   `.sirdar/` gets a specific message pointing at `.git/info/exclude`, since that is what
   `sirdar init` writes and an older workspace may be missing. Then `git fetch origin`, and the
   default branch is read from `origin/HEAD` (falling back to `main` or `master` if that symbolic
   ref is not set).
2. **Branch.** `git checkout -B fix-<key>-<title-slug> origin/<default>`. `--base BRANCH`
   overrides both the branch cut from and the branch targeted. The fix branch is refused if it
   would equal the base branch; the default branch is never committed to and never force-pushed,
   and neither is anything else.
3. **Snapshot.** Before the session starts, `sirdar fix` sha256s every file under the workspace's
   `.sirdar/` (excluding its own `runs/`, `register.jsonl` and `eval/`) and under the directory
   git runs this repository's hooks from. See Confinement below.
4. **One session**, with write permission, given the triage note, the RCA note if one exists, and
   the workspace's playbooks. It is told to implement the note's Proposed Fix and nothing else,
   to run the workspace's build and tests, and to make no commits of its own. It answers with
   JSON: `summary`, `filesChanged`, `testsRun`, `risks`, and `deviationFromNote`.
5. **Snapshot again**, before the report is read and before any git command. A difference from
   the first snapshot fails the run outright: nothing is committed, nothing is pushed, and the
   run is marked `failed`. See Confinement below.
6. **Commit.** Everything but `.sirdar/` is staged (`git add -A -- . :(exclude).sirdar`) and
   committed with `git commit --no-verify`. The subject is `fix: <summary>`; the body carries the
   note's root cause and the files changed. The commit carries no AI attribution trailer of any
   kind — the engineer who approved the note is the author of the change.
7. **Push and pull request.** `git push --no-verify -u origin <branch>`. Then, if `gh` is on
   `PATH` and authenticated, `gh pr create` with title `[KEY] fix: <summary>` and a body carrying
   the symptom, the root cause, the fix, the checks run, and the tracker and helpdesk links from
   the note. Without `gh` — or if `gh pr create` fails — the branch is still pushed, and the
   compare-page URL (built from the remote's GitHub or GitLab URL) plus the ready-to-paste title
   and body are printed instead. `--no-pr` pushes and stops there without attempting `gh`.
8. **Record.** Both copies of the triage note (the run copy and the filed copy) get their
   frontmatter set to `status: fix-pushed`, plus `pr:` and `commit:` when those exist. A
   `kind: fix` row is appended to `.sirdar/register.jsonl`. A note or a row that fails to write is
   reported and skipped rather than failing the command — the branch is already pushed by then.

Both the commit and the push run with `--no-verify`. A repository's hooks are code, and the
commit and push happen moments after an agent session had write access to the tree; `--no-verify`
means a hook written during that session is not what executes it. Nothing stops an operator from
running their own hooks over the branch afterwards — the pull request is where that check
belongs.

`--dry-run` stops after the branch and the prompt: no session, no commit, no push. It writes the
prompt to `.sirdar/runs/<KEY>/<run-id>/prompt.md` so you can read exactly what would be sent.

## The deviation gate and `--accept-deviation`

If the agent's `deviationFromNote` field is non-empty — it found the cause elsewhere, the code
had moved, the note's fix would have broken something — the commit is still made, but the run
stops before the push. The deviation is printed prominently, the branch stays local and
unpushed, and the command exits 1. This is deliberate: a fix that quietly became a different
change from the one approved is the failure worth catching, so the agent is asked to declare it
and the harness stops on it rather than pushing regardless.

Read the diff on that local branch. If you accept it, rerun `sirdar fix KEY --accept-deviation`.

A rerun with `--accept-deviation` does not start a second agent session. It looks for the newest
completed fix run for the key whose branch still points at exactly the commit that run recorded.
When it finds one, it pushes **that commit** — the one you already read — and opens the pull
request for it, skipping the session, the commit step, and the register row entirely. If the
branch has moved on, been deleted, or was never recorded (an older run, or one that failed before
recording a commit), the ordinary flow runs from scratch instead: a fresh branch, a fresh session,
a fresh commit.

## Confinement, per provider

A fix session gets more than a triage session: the read-only tools, plus `Edit`, `Write` and
`MultiEdit` (or, in Sirdar's own agent loop, `write_file` and `edit_file`). `NotebookEdit` stays
refused — nothing in the flow edits a notebook. Shell commands are matched against
`permissions.fixBash` instead of `permissions.bash`. How much of that confinement each provider
actually enforces differs, so the guard that does not depend on the provider is a separate, third
layer:

| Provider | Confinement |
| --- | --- |
| `claude`, `openai` | `provider.PermissionPolicy.Decide` judges every `Edit`/`Write`/`MultiEdit`/`write_file`/`edit_file` call before it runs (`decideWrite` in `internal/provider/policy.go`), plus the snapshot guard |
| `codex` | Codex's own `sandbox: workspace-write` confines the session to the thread's working directory; `decideWrite` is never consulted, because Codex approves its own tool calls under `approvalPolicy: untrusted` without asking Sirdar, plus the snapshot guard |
| `acp` | Whatever the ACP agent itself implements for `session/request_permission`, plus the snapshot guard |

**The policy path check (`decideWrite`).** For Claude and `openai`, every write-shaped tool call
carries a target path (`file_path`, `notebook_path`, or `path`). `decideWrite` resolves it with
`provider.ResolveWithin`, the same symlink-resolving confinement every path-taking surface in
Sirdar goes through, and refuses anything that does not land inside the workspace root. A target
that does resolve inside the root is checked again with `provider.ReservedWrite`, which refuses
`.git` at any depth, the workspace's own `.sirdar`, and the repository's `core.hooksPath` when it
sets one (carried on `PermissionPolicy.ExtraReserved`).

**The agenttools confinement.** Sirdar's own agent loop (`provider: openai`) checks the same two
rules a second time, inside the tool implementation itself
(`internal/agenttools/agenttools.go`'s `resolve` and `resolveWrite`), rather than trusting the
caller in front of it. `resolve` calls the same `provider.ResolveWithin`; `resolveWrite` then
calls the same `provider.ReservedWrite` with the loop's own `Options.ExtraReserved`. A tool that
is only as safe as the policy calling it is not safe on its own.

**The reserved `.git`/`.sirdar`/hooks directory.** `provider.ReservedWrite` folds case on every
segment comparison, so `.GIT/hooks/pre-commit` is refused on macOS's and Windows's
case-insensitive filesystems exactly as `.git/hooks/pre-commit` is refused on Linux.
`provider.HooksDir` names the directory git actually runs this repository's hooks from:
`core.hooksPath` (read once per run with `git config --get core.hooksPath`, with a leading `~` or
`~user` expanded to a home directory the way git itself expands it) when the repository sets one,
and `<root>/.git/hooks` otherwise. That path is carried as an extra reserved directory on top of
the two built-in ones, so a repository using husky, lefthook, or a checked-in `.githooks/` is
covered the same as one using the default `.git/hooks`.

**The snapshot guard (`internal/fix/guard.go`).** This is the only layer that does not depend on
a provider honouring anything. Before the session starts, and again the instant it ends, before
the first git command, `sirdar fix` takes a sha256 of every file (mode included, so making a file
executable counts as a change) under the workspace's `.sirdar/` — excluding `runs/`,
`register.jsonl`, and `eval/`, which the run legitimately writes to itself — and under whatever
`provider.HooksDir` names for that repository. A file created, modified, or deleted between the
two snapshots fails the run: nothing is restored, nothing is committed, and nothing is pushed. The
run's `state.json` is marked `failed`. Nothing is restored deliberately — the operator's own copy
of what changed is treated as better evidence than an automatic revert, and the point of the
guard is that whatever produced the change was never supposed to be able to make it.

**Codex's sandbox** is a `sandbox: workspace-write` config value Sirdar sets on the thread; Sirdar
does not implement or verify that Codex's own sandbox actually confines writes to the working
directory. The snapshot guard is the real backstop if that sandbox lets a write through,
including one onto `.git`.

## `permissions.fixBash`

Shell commands in a fix session are matched against `permissions.fixBash`, not
`permissions.bash` — a fix needs to branch, build and test, and widening the triage list would
hand every read-only run the same reach. The syntax is identical to `permissions.bash`: the same
segment splitting on `|`, `||`, `&&`, and `;`, the same refusal of command substitution and
redirection (bar `2>&1` and `2>/dev/null`), the same workspace-root check on path-shaped
arguments.

The default:

```yaml
permissions:
  fixBash:
    - "git status*"
    - "git diff*"
    - "git log*"
    - "git show*"
    - "git grep*"
    - "git blame*"
    - "dotnet build*"
    - "dotnet test*"
    - "npm test*"
    - "go build*"
    - "go test*"
    - "make *"
```

The git entries are the read-only ones, named individually rather than as `git *`, because
Sirdar itself makes the branch, the commit, and the push — after the report comes back and after
the deviation check — so nothing in the agent's own shell access needs `git commit`, `git push`,
`git reset`, or `git config`. Naming your own `permissions.fixBash` list replaces the default
outright.

Some git flags and one subcommand are refused whatever pattern matches, because each moves where
git reads its configuration, writes its output, or runs code from:

- `--output`, `--output-directory`, `-o`, `--upload-pack`, and `--receive-pack` are denied
  wherever they appear in the command — git accepts them after the subcommand too.
- `-c`, `-C`, `--git-dir`, `--work-tree`, `--exec-path`, and `--config-env` are denied only
  *before* the subcommand, since git accepts them nowhere else there; this lets `git grep -c foo`
  and `git rev-parse --git-dir`, which reuse the same short flags after the subcommand for an
  unrelated meaning, through.
- `git config` is refused outright, because it writes the settings — `core.hooksPath` among
  them — that decide what every later git command does.
- A `GIT_*` environment assignment (`GIT_DIR=x git log`, `env GIT_DIR=x git log`, or
  `GIT_DIR=x make test`) ahead of any command, git or not, is refused the same way, since it
  reaches the same configuration those flags do.

A `permissions.fixBash` command's own flag-value path arguments go through `provider.ReservedWrite`
too, not only `Edit`/`Write`/`MultiEdit`: `go test -coverprofile=.git/hooks/pre-commit` matches
a `go test*` pattern and is refused anyway, because the path it names is a hook the next commit
runs.

What the allow-list does not confine: `make *`, `go test*`, `npm test*`, and `dotnet test*` run
the workspace's own build system, which runs whatever the repository tells it to — a Makefile
target, a `go:generate` directive, an npm `pretest` script. Sirdar does not read any of that, and
no allow-list can. This is the accepted residual of fix mode: a fix has to build and test what it
changed, and the trust extended is the trust you already give your own shell when you check out a
branch and run `make test`. The pull request is the review gate for what the session actually
did. A workspace that needs more than that should run `sirdar fix` in a container.

## `fix.prIncludesComplaint`

By default, the pull request's Symptom section quotes the triage note's **title**, not its
complaint. The complaint is the customer's own words out of a support ticket, and a pull request
is often public or read by people with no business seeing that conversation.

```yaml
fix:
  prIncludesComplaint: true
```

Turn it on only for a private repository where the ticket text is already visible to the same
people. The triage note is always linked from the pull request body either way, through the
tracker and helpdesk URLs.

## What is unverified

No live run of `sirdar fix` exists yet against a real model or a real GitHub/GitLab remote — it
has only been exercised against a scripted fake provider and fixture repositories in tests.
Codex's `sandbox: workspace-write` is a config value Sirdar sets but does not implement or
verify; the snapshot guard is the actual backstop if it lets a write through. Whether
`item/fileChange/requestApproval` fires under Codex's `sandbox: workspace-write` and
`approvalPolicy: untrusted` has not itself been watched on a live `codex app-server` turn: the
file-change approval path was built off the app-server's declared `ServerRequest` schema, the
same way the command-approval and MCP-elicitation paths were, but no turn that has Codex actually
write a file has been run against it.
