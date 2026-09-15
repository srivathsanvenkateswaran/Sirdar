# Evaluating triage quality

`sirdar eval` replays tickets you have already triaged by hand and scores what the agent
produces against what you produced. It is a regression harness for a prompt, a playbook edit, a
model change, or a provider swap: change one of those, run the eval, and read the diff in the
table.

Nothing here is simulated. An eval hands the runner a stored bundle instead of calling the
tracker and the helpdesk, and then the ordinary path runs — the same prompt assembly, the same
session, the same schema validation, the same note. What is scored is a run.

```
sirdar eval                      every key in the golden set
sirdar eval OMNI-1 OMNI-2        just these
  --golden DIR                   golden set directory (default ~/.sirdar/golden)
  --provider claude|codex|openai
  --model NAME
  --concurrency N
  --retro                        replay at the commit the fix branched from and score
                                 against the merged pull request (see below)
  --with-rca                     with --retro: add a blind RCA run
  --rubric                       with --retro: one provider call comparing the two diffs
sirdar golden add KEY [--from RUN_ID] [--golden DIR] [--force]
sirdar golden add KEY --retro --pr URL [--pr URL...] [--as-of RFC3339] [--golden DIR] [--force]
sirdar golden list [--golden DIR]
sirdar golden migrate [KEY...] [--golden DIR]
```

Exit code is 1 if any key failed an assertion or produced a note that did not validate, so the
command drops into CI as it stands. `--retro` is the exception and always exits 0: it is a
measurement, not a gate.

## The golden set

```
~/.sirdar/golden/
  OMNI-1234/
    bundle/              copied from a completed run: ticket.json, thread.md, attachments/
    expected.json        optional: the assertions you are prepared to stand behind
    expected.md          optional: the note a human wrote for this ticket
  OMNI-1240/
    bundle/
  OMNI-3217/
    bundle/              a retrospective entry adds three files
    expected.json
    retro.json           the cutoff, the base commit and the merged pull request
    pr.diff
```

It lives outside the repository by default, and should stay there: the bundles hold real
customer conversations, names and attachments. `sirdar golden add` refuses a golden directory
that sits inside a git work tree for that reason — the next `git add -A` would publish it, and
a repository's history is not somewhere you can take a customer's conversation back out of.
`--force` overrides the refusal for a repository you are certain may hold it.

### Migrating a pre-eval golden set

An older `bundle/` split had a key's files directly under its directory — `OMNI-1234/ticket.json`
rather than `OMNI-1234/bundle/ticket.json`. `sirdar eval` cannot read that layout and says so by
name rather than reporting an empty golden set:

```
eval: ~/.sirdar/golden holds no golden bundles `sirdar eval` can read; 2 in the pre-eval layout
(files directly under the key, not under bundle/): OMNI-1234, OMNI-1240 — run `sirdar golden
migrate` to fix them, or `sirdar golden migrate KEY` for one at a time
```

`sirdar golden migrate` moves a key's bundle files into `bundle/` in place; `expected.json` and
`expected.md`, a human's own files, are left exactly where they are. With no key named it
migrates every entry still in the old layout.

Building an entry is two steps. Triage the ticket for real, then:

```
sirdar golden add OMNI-1234
```

That copies the newest completed triage run's bundle into `~/.sirdar/golden/OMNI-1234/bundle/`
and writes an `expected.json` skeleton from the note that run produced — its confidence, its
classification, and the files it cited. `--from RUN_ID` picks a specific run instead.

**The skeleton is a draft to trim, not a baseline to keep.** It is generated from what the agent
said, so keeping all of it asserts that the agent agreed with itself. Delete the lines you would
not defend in review, correct the ones the agent got wrong, and keep the two or three that
capture what a good answer to this ticket has to contain. An existing `expected.json` is never
overwritten by a second `golden add`.

If you have the note you wrote by hand, save it as `expected.md` in the same directory. It is
compared coarsely (see below) and is optional.

## `expected.json` assertions

One JSON object. Each key names a field of the triage note by dot path; a suffix on the key
chooses the comparison.

```json
{
  "rootCause.confidence": "medium",
  "classification": "code",
  "rootCause.codeRefs_contains": ["Domain/Inventory.API/"],
  "openQuestions_min": 3
}
```

| Form | Comparison |
|---|---|
| `field.path` | Equality against the value at that path, after JSON decoding: `"medium"`, `3`, `true`, or a whole object or array |
| `field.path_contains` | The value at the path must be an array, and every string listed must appear as a **substring** of at least one element |
| `field.path_min` | The value at the path must be an array of at least that many elements |

Dot paths walk objects by key and arrays by index, so `timeline.0.role` reads. A path that does
not exist is a failure, with "no value at ..." as the reason.

`_contains` is deliberately loose. Writing `"Domain/Inventory.API/"` says the note should have
pointed into that area, which is a claim that survives the next refactor; writing
`"Domain/Inventory.API/Export/Csv.cs:412"` says it should have cited one line, which stops being
true the moment somebody adds an import. The generated skeleton already strips a code reference
down to its bare file path for the same reason — no line number, no line range, and no
parenthetical annotation an agent tacked on: `Domain/Fin.Logic/Models/FinReportModel.cs:253-282
(default branch: main)` becomes `Domain/Fin.Logic/Models/FinReportModel.cs`. An annotation is
free text the agent will not phrase the same way twice, so keeping it verbatim in the assertion
would make `_contains` a substring check that can never match.

Every failure is printed under the table with the value that was actually there:

```
OMNI-1234: rootCause.confidence — want "high", got "medium"
OMNI-1234: openQuestions_min — want at least 3, got 1
```

## Scoring

Each key gets one row:

```
KEY        STATE      TURNS  COST  MINS  VALID  ASSERT  REFS      HEADS
OMNI-1234  completed  14     0.82  3.1   yes    3/4     2/3 67%   4/5 80%
OMNI-1240  completed  9      0.41  1.8   yes    4/4     -         -
```

- **STATE, TURNS, COST, MINS** come from the run's own state: what it cost to get this answer is
  half of whether the answer is any good.
- **VALID** is whether the session's JSON validated against the triage schema. A `no` here means
  there is nothing to score, and every assertion is counted as failed.
- **ASSERT** is passed over total from `expected.json`.
- **REFS and HEADS** are the coarse comparison against `expected.md`, and read `-` when there is
  none.

### Why the note comparison is coarse

Two notes about the same bug share almost no prose, and scoring prose similarity would measure
writing style rather than triage quality. Two things are worth measuring instead:

- **REFS** — the fraction of `path/file.ext:123` references in your note that also appear in the
  agent's. Did it look where you looked?
- **HEADS** — the fraction of `##` section headings in your note that also appear in the agent's,
  compared case-insensitively. Did it cover what you covered?

Neither is a grade, and a run can score 100% on both while being wrong. What they are good for
is telling you which two notes to open side by side: the report names the references and the
headings that are missing, which is usually the fastest route to the thing the agent did not
think to check.

## The report

Every invocation writes `.sirdar/eval/<timestamp>.json` in the workspace, carrying the provider,
the model, the golden directory, and the full result for every key including each individual
check and its failure detail. That is the file to diff across a prompt change, and the file to
keep when a run tells you something.

The runs themselves are ordinary runs, so their directories are under `.sirdar/runs/<KEY>/` as
usual, with the prompt, the events and the note each one produced. An eval that scores badly is
read by opening those.

### What an eval does not leave behind

A replay is a measurement, not a triage, and it is marked as one: its `state.json` carries
`"Eval": true`, and that mark changes what the run leaves behind.

The note it produces stays in the run directory as `note.md`. It is **not** filed into
`notes.dir`, where a real triage run puts it — that directory holds the note you wrote and read
for that ticket, and an eval overwriting it would cost you the note to compare against.

No row is appended to `.sirdar/register.jsonl`. The register is the audit index of tickets that
were actually worked; the eval's own record is the report under `.sirdar/eval/`.

And nothing downstream mistakes the replay for the key's newest triage. `sirdar rca` and
`sirdar fix` both start from the newest completed triage note for a key, and they skip eval
runs when they look — so replaying `OMNI-1234` today cannot put tomorrow's fix to work on a
bundle captured six months ago.

## Retrospective evaluation

An ordinary golden entry is built from a run you already made, so it measures a new prompt
against an old session. A retrospective entry is built from a ticket that was closed months ago
and fixed by a pull request that is already merged, so it measures against what a person
actually did. There is no run to copy from and no note to skeleton off: the entry is assembled
from the ticket itself.

```
sirdar golden add OMNI-3217 --retro --pr https://github.com/acme/omni/pull/482
```

That writes:

```
~/.sirdar/golden/OMNI-3217/
  bundle/              the ticket as it stood at the cutoff, with manifest.json
  expected.json        {} — assertions are yours to add
  retro.json           the ground truth
  pr.diff              the merged pull request's diff
```

`retro.json` carries `key`, `asOf`, `baseCommit` (the pull request's `baseRefOid`, and for
several pull requests the earliest one's), `prUrls`, `prDiff` (always `"pr.diff"`), `prFiles`
(every path the pull requests touched, deduplicated and sorted) and `redacted`, which repeats
the bundle manifest's three counts.

The ticket is read through the same configured tracker and helpdesk adapters a triage run uses,
with the same read-only calls — no MCP server is involved, and no provider session is started
at all, so a workspace whose agent CLI is not installed on this machine can still build an
entry. The pull request is read through `gh pr view` and `gh pr diff`, executed directly rather
than through a shell. A missing or logged-out `gh` fails the command with the thing to fix,
before anything is written.

### The cutoff

The bundle is assembled as of one instant, which is the point: a closed ticket's conversation
ends with somebody posting the fix, and replaying it whole would score an agent on reading the
answer. `--as-of RFC3339` sets the instant outright. Otherwise it is the **earliest** of two
pieces of evidence about when the work started:

- the ticket's first transition into an `in_progress` status, when the tracker adapter
  implements `source.Transitioner` and reports one;
- the earliest `--pr`'s `created_at`, read from `gh pr view --json createdAt,baseRefOid,mergeCommit,files`.

Most adapters expose no status history, and a helpdesk-only workspace has no tracker at all, so
the fallback is the pull request's `created_at` alone. That is later than the real pickup, and
the error is in the direction of leaving a little early triage in the bundle — evidence the
engineer did have — rather than removing what they had. It never leaves the fix in, because a
pull request's own references are redacted whatever the cutoff. Where the cutoff came from is
printed with the entry; if you disagree with it, pass `--as-of`.

### What the cutoff removes

- **Thread messages written after it.** A message an adapter could not date is kept: a missing
  timestamp is a gap in the source, and treating it as "later" would empty the bundle for
  whichever helpdesk reports one.
- **Attachments only a dropped message pointed at.** An attachment carries no timestamp of its
  own, so the message that referenced it is the only evidence of when it arrived; one that no
  message references at all is kept, because nothing dates it. The file is deleted from
  `bundle/attachments/`, not merely unlisted — leaving a screenshot of the green build in the
  directory hands the session exactly what the cutoff took out of the thread.
- **Every pull-request reference**, in the tracker's title, description and fields, in the
  helpdesk's subject and fields, and in the messages that survived. A GitHub `/pull/N` or
  `/pulls/N` URL, a GitLab `/merge_requests/N` URL, and a prose mention (`PR #482`, `pull
  request 482`, `MR!17`) each become `[redacted: pull request]`. A tracker field whose name says
  it holds nothing but pull requests — `prs`, `pr_url`, `pullRequests` and the rest of the
  spellings — is replaced whole rather than scanned, because a tracker that renders its links as
  a branch name or a bare `#482` would otherwise slip one past the patterns.

The marker is deliberately visible. The point is to take away what the fix was, not to pretend
nothing was taken away: a silent deletion leaves a sentence that reads as if the customer never
mentioned anything, which is a different ticket from the one the engineer picked up.

What was removed is counted in `bundle/manifest.json` beside `ticket.json`:

```json
{
  "asOf": "2026-03-02T10:30:00Z",
  "commentsDropped": 4,
  "attachmentsDropped": 1,
  "prLinks": 3
}
```

The counts do **not** go into the bundle's `warnings`, which is what the prompt quotes to the
agent. "Four later comments were dropped" tells a session being measured on this ticket that
there is a conversation it is not being shown, and how much of one. The manifest is for you and
for the scoring; the operator sees the same line on stderr when the entry is built.

### What is still visible

Redaction is not anonymisation. The bundle still says that something was redacted, and a
determined session could infer that a fix exists — which it could anyway, from a ticket that is
closed. What it cannot read is which pull request, which files, or which commit, and those are
what a retrospective score is measured on.

The same cutoff is available to a run directly, as `run.Options.AsOf`. It applies to a fetched
bundle only: a replayed golden bundle is taken as it stands, because one built with a cutoff
already carries it, and cutting it twice would say it had two.

## Retro: scoring against the fix that was merged

An ordinary eval scores a replay against what a human wrote about the ticket. A retro scores it
against what a human **did** about it.

For a golden key that carries a `retro.json`, `sirdar eval --retro` puts the agent back where the
engineer stood: the bundle captured as of the moment work started, before any comment named the
cause, and the repository at the commit the fix branched from. Then it runs the flow the engineer
ran — triage, then a fix — and compares the result against the pull request that closed the
ticket.

```
sirdar eval --retro                    every golden key that has a retro.json
sirdar eval --retro OMNI-1 OMNI-2      just these
  --with-rca                           add a blind RCA run (a third session per key)
  --rubric                             add one provider call per key that reads both diffs
```

Three sessions, at most, per key:

1. **Triage** at the base commit, from the as-of bundle. It is an eval-marked run like any other
   replay: the note stays in the run directory, nothing is filed, and no register row is written.
2. **`fix --local`** at the same commit, starting from *that* triage note rather than the newest
   note for the key. It commits in its own worktree and stops: nothing is pushed and no pull
   request is opened. The diff of that commit is what gets scored.
3. **RCA**, only with `--with-rca`, and blind — it is never given the pull request URL, because an
   RCA shown the answer is not measuring anything.

`--retro` always exits 0. There is no threshold a retro passes or fails, and an exit code would
invite one to be invented. A key whose bundle will not load, whose triage went nowhere, or whose
build has no `--at` is a row with a reason printed under the table; the other keys still score.

### The golden entry

A retro key is an ordinary golden entry with two more files in it:

```
~/.sirdar/golden/OMNI-1234/
  bundle/         the ticket as of `asOf`, with the PR links stripped out
  expected.json   as before, optional
  retro.json      { key, asOf, baseCommit, prUrls, prDiff, prFiles, redacted }
  pr.diff         the unified diff of the merged pull request
```

`sirdar golden add KEY --retro --pr URL` writes both. `baseCommit` is what every session in the
retro stands at; `prFiles` is the list the score compares against, and a `retro.json` that carries
none falls back to the paths in `pr.diff` itself. `redacted` counts what the as-of capture took
out — PR links, later comments — so a reader can tell how much of the ticket the agent was
deliberately not shown.

### Scoring

```
KEY        CLASS  CONF    REFS      PRFILES   FILES     HUNKS     BUILD  RUBRIC     COST
OMNI-1234  code   high    1/2 50%   2/3 67%   1/2 50%   1/2 50%   yes    partial    3.85
OMNI-1240  data   medium  0/1 0%    0/2 0%    0/3 0%    0/2 0%    no     different  2.10
```

The triage columns:

- **CLASS, CONF** are the note's own `classification` and `rootCause.confidence`, copied out
  rather than judged. There is no ground truth for either; what they are for is reading beside
  the overlap columns — a `high` next to a `0/2` is the row to open first.
- **REFS** is the fraction of the note's `rootCause.codeRefs` whose **file** the pull request in
  fact touched. Line numbers and the annotations a generated skeleton leaves on
  (`Export/Csv.cs:253-282 (default branch)`) come off first: the question is whether the agent
  pointed at the right file, not whether it guessed the right line.
- **PRFILES** is the other direction — the fraction of the pull request's files the note names
  anywhere, in its references or in its prose. A file cited by its tail (`Export/Csv.cs` for
  `src/Domain/Inventory.API/Export/Csv.cs`) counts, and so does a bare file name in a sentence.
  The looseness is deliberate and it cuts both ways, which is why the files the note **missed**
  are printed under the table rather than only counted.

The fix columns compare two unified diffs taken against the same commit:

- **FILES** is the Jaccard overlap of the two file sets — files in both, over files in either. It
  is not a fraction of the pull request, because neither side is the reference: an agent that
  changed three files the pull request never touched is as interesting as one that missed three.
  Two empty diffs score a dash, not 100%: that is two missing diffs, not a match.
- **HUNKS** is the fraction of the pull request's hunks whose file and line window a hunk of the
  agent's also touches. Both diffs are against the same base commit, so the comparison is on the
  **base** side: a base-side line number means the same line in both, while the new-side numbers
  are renumbered independently by each diff and could not be compared at all. A file the pull
  request renamed lines up by the name it had at the base commit. A hunk that only adds lines
  covers no base line of its own, so its window is the gap it was inserted into — two insertions
  at the same point are the same edit.
- **BUILD** is what the fix session reported about its own build and tests, read off the
  `testsRun` entries of its report. A session that ran nothing, or wrote a result that reads
  neither way, is a dash rather than a failure.
- The report also carries **linesAdded** and **linesRemoved** for both sides. There is no score
  on those: a fix half the size of the human's is not half as good, and the two numbers beside
  each other are the whole point.

### `--rubric`

Everything above is arithmetic on two diffs. `--rubric` adds the one judged column: one extra
call to the workspace's own provider, given the pull request's diff and the agent's diff and
nothing else — no tools, no MCP servers, no repository to wander into — answering a fixed JSON
rubric:

```json
{"sameRootCause": true, "sameFix": false, "verdict": "partial", "reasoning": "…"}
```

`verdict` is `equivalent`, `partial` or `different`; `reasoning` is capped at 400 characters and
printed under the table. It is off by default because it costs a session per key and because it
is an opinion: when a retro says something surprising, RUBRIC is the column to distrust first.

### The retro report

Every `--retro` invocation writes `.sirdar/eval/<timestamp>-retro.json`, next to the ordinary
eval reports and named apart from them. It carries every score above, the run id of each session,
the diff each one left behind, and the cost of the whole key including the rubric call — so the
two notes and the two diffs can be opened side by side, which is what the table is for.

## From the desktop app

The desktop build and `sirdar serve` show the same golden set and the same report on their Eval
screen: tick the keys to replay or leave them all unticked to replay the set, override the
provider and model for that one run if you are comparing two, and read the score table back
from the report the run wrote under `.sirdar/eval/`. A completed triage run has an **Add to
golden set** button, which does what `sirdar golden add --from RUN_ID` does.

Below it, a **Retro** section draws the last retro report's table, read from
`GET /api/workspaces/{id}/eval/retro/latest`. A workspace that has never run one says so rather
than showing an empty table. The eval route starts a retro when its body carries
`{"retro": true}`, with `withRca` and `rubric` alongside it.

Which directory that is comes from the command line and nowhere else:

```
sirdar serve --golden DIR
```

A request cannot name it. The bundles are real customers' conversations, and a UI that let a
caller point the eval routes at an arbitrary directory would be a way to read any bundle on the
machine.

## What an eval and a fix are each trusted with

A replay is read-only in the same sense every triage run is: the session gets the read tools,
no editing tool at all, and a shell allow-list (`permissions.bash`) matched segment by segment.
Scoring a replay changes nothing in the workspace except the report under `.sirdar/eval/`.

`sirdar fix` is the exception, and it is worth being plain about the size of it. A fix session
may edit files — confined to the workspace's source, never `.git/`, never `.sirdar/`, never the
directory `core.hooksPath` names — and its shell allow-list (`permissions.fixBash`) defaults to
the read-only git commands plus `make *`, `go build*`, `go test*`, `npm test*`, `dotnet build*`
and `dotnet test*`.

Those build and test entries execute the workspace's own build system, which executes whatever
the repository tells it to: a Makefile target, a `go:generate` directive, an npm `pretest`
script. Sirdar does not read any of that and no allow-list can. A fix has to build and test
what it changed or its report is worth nothing, so fix mode trusts the workspace's build system
the way your own shell does when you check out a branch and type `make test`. The gate on what
the session actually did is the pull request it opens, reviewed like any other change. A
workspace that cannot extend that trust should run `sirdar fix` in a container.
