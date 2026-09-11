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
sirdar golden add KEY [--from RUN_ID] [--golden DIR]
sirdar golden list [--golden DIR]
```

Exit code is 1 if any key failed an assertion or produced a note that did not validate, so the
command drops into CI as it stands.

## The golden set

```
~/.sirdar/golden/
  OMNI-1234/
    bundle/              copied from a completed run: ticket.json, thread.md, attachments/
    expected.json        optional: the assertions you are prepared to stand behind
    expected.md          optional: the note a human wrote for this ticket
  OMNI-1240/
    bundle/
```

It lives outside the repository by default, and should stay there: the bundles hold real
customer conversations, names and attachments.

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
true the moment somebody adds an import. The generated skeleton already strips the `:line` off
the references it copies, for the same reason.

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
