# Shadow mode: implementation plan

Date: 2026-10-06. Spec (binding authority):
`docs/superpowers/specs/2026-10-06-shadow-mode-design.md`. It reads one field from the sibling
spec `docs/superpowers/specs/2026-10-06-investigation-skeleton-design.md`, which is built first:
the triage note's `reproduction` object, `{attempted, outcome: reproduced|not_reproduced|not_attempted,
where, steps, evidence}` or `null`, and the prompt's `TriageInput.Repro`. Every task here assumes
that work has landed. Where this plan and the shadow spec disagree, the spec wins and the task is
wrong; say so in the task report rather than guessing. The decisions this plan makes where the spec
is silent or ambiguous are listed at the end, under "Resolved ambiguities", and are part of the
contract.

The work adds rung 1 of the trust ladder: an unattended watcher that triages every new ticket
assigned to the operator as a `Shadow` run, files nothing, records everything in
`.sirdar/shadow.jsonl`, grades each run against what the operator later did, and shows a scorecard.
It also adds the playbook feedback loop ("Remember this" and RCA suggestions). Line numbers below
are from commit `d6877ac`; the skeleton work moves some of them, so find each anchor by the symbol
named beside it.

## Global Constraints

These bind every task.

- **Git identity.** Never set or override it. No `git -c user.name=…` / `git -c user.email=…`, no
  `git config user.*` at any scope, no `--author`, no `GIT_AUTHOR_*`, `GIT_COMMITTER_*` or `EMAIL`
  in the environment. The identity is whatever `git config user.email` already returns in the
  worktree. If a commit fails for want of an identity, stop and report it; do not invent one.
- **No AI attribution.** No `Co-Authored-By:` trailer and no "Generated with" footer in any
  commit message.
- **One commit per task**, made at the end of the task once every check below passes. Do not
  push. Commit message: the subject given in the task, in the repo's style
  `<area>: <behaviour>`, lower case, no trailing period. A body is optional; if written, it says
  why in plain sentences.
- **Commit by explicit paths.** `git add <path> <path> …` naming each file the task touched. Never
  `git add -A`, `git add .` or `git commit -a`. Never add `node_modules/`, anything under
  `.superpowers/`, or anything under `dist/` or `internal/httpapi/ui/dist/`.
- **Checks before every commit**, from the worktree root, all passing:
  - `gofmt -l .` prints nothing
  - `go vet ./...`
  - `go test ./...`
  - frontend tasks (H11, H12) also: `cd desktop/frontend && npx tsc --noEmit -p . && npx vitest run`
  `desktop/bridge_test.go` is in the root module, so `go test ./...` also checks that every new
  exported `app.Service` method is bridged or listed in `notBridged`, and that every name in
  `BridgeBindings` (`desktop/frontend/src/api/transport.ts`) exists on `*Bridge`.
- **Comments are full sentences that say why**, in the voice of the surrounding code. No comment
  restating the next line; no `// TODO` without the reason it is deferred.
- **CSS** uses the design tokens in `desktop/frontend/src/styles/tokens.css` (`var(--sd-…)`) for
  colour, space, radius and type, and logical properties only (`padding-inline`,
  `margin-block-start`, `inset-inline-end`, `border-inline-start`), never `left`/`right`/
  `margin-left` and friends. Text that may be Arabic carries `dir="auto"`; keys and numbers sit in
  `<bdi dir="ltr">`.
- **Nothing outward from a shadow run.** A shadow run sends no notification, writes no vault note
  and no register row, and makes no tracker or helpdesk write. The only outward message in this
  plan is the opt-in daily digest (H8), off by default, metadata only, to a `hooks.slack.com` URL.
- **Existing non-shadow behaviour is unchanged.** Every existing test keeps passing without edits
  to its expectations. Eval runs keep their current flow: only `State.Shadow` is new in the
  exclusions that eval did not already have (busy check, Queue's latest run, the Runs list).
- **No edits under `/Users/srivathsanv/Documents/Work/Coding/GitHub/OXO.APIs`.** The controller
  makes the operator-side config change for H-live.
- Exact strings in this plan (JSON names, ledger event names, routes, CLI flags, messages, UI
  labels) are part of the contract. Do not paraphrase them.
- Tests are offline: the stub provider and stub tracker in `internal/run/run_test.go`
  (`stubProvider`, `stubTracker`, `newWorkspace`, `newRunner`, `replyThenNote`) and
  `internal/app/stubs_test.go` (`stubProvider`, `stubTracker`, `newWorkspace`, `newRegistry`,
  `replyThenNote`, `finalEvent`). No test reaches a network, `gh`, or a real provider CLI.

## Task graph

| Task | Depends on |
|---|---|
| H1 measurement flag | none |
| H2 unattended policy | H1 |
| H3 config block | none |
| H4 ledger, selection, scorecard | none |
| H5 judge, PR reader, tracker.outcome | none |
| H6 outcomes and grading | H1, H4, H5 |
| H7 gate and watcher | H2, H3, H4 |
| H8 outcome wiring, digest, doctor | H6, H7 |
| H9 playbook suggestions | H2, H5 |
| H10 API, bridge, CLI | H8, H9 |
| H11 Shadow screen | H10 |
| H12 read-only session, Remember, Settings | H11 |
| H-live | all of the above |

Wave 1 in parallel: H1, H3, H4, H5. Wave 2 in parallel: H2, H6. Wave 3 in parallel: H7, H9.
Then H8, H10, H11, H12, H-live.

---

### Task H1: a shadow run is a measurement, and every eval exclusion covers it

Touches: `internal/store/run.go`, `internal/store/register.go`, `internal/store/run_test.go` (or
the store package's existing test file), `internal/run/run.go`, `internal/run/prepare.go`,
`internal/run/execute.go`, `internal/run/steer.go`, `internal/run/shadow_test.go` (new),
`internal/fix/fix.go`, `internal/fix/steer.go`, `internal/app/types.go`, `internal/app/hooks.go`,
`internal/app/service.go`, `internal/app/livesteer.go`, `internal/app/shadowrun_test.go` (new)

Depends on: none

**Store (`internal/store/run.go`).** In `State` (line 121), directly after `Eval` (line 164), add,
each with a doc comment saying why:

```go
// Shadow marks a run the shadow watcher started unattended …
Shadow bool `json:",omitempty"`
// Denied records every tool call a shadow run's policy refused, oldest first, capped at MaxDenied.
Denied []DeniedCall `json:",omitempty"`
// Questions records every question a shadow run asked in words, oldest first.
Questions []string `json:",omitempty"`
// RCAInputs is what an rca run was handed: the PR URL and the engineer's resolution text.
RCAInputs *RCAInputs `json:",omitempty"`
```

and the types and method:

```go
type DeniedCall struct {
	At      time.Time `json:"at"`
	Tool    string    `json:"tool"`
	Summary string    `json:"summary"`
}
type RCAInputs struct {
	PRURL      string `json:"prUrl,omitempty"`
	Resolution string `json:"resolution,omitempty"`
}
const MaxDenied = 20
const RCAResolutionMax = 4000

// Measurement reports whether the run is a measurement rather than a record of a ticket:
// an eval replay or a shadow run. Neither is filed, registered, steered, or read as
// "the newest triage note".
func (s State) Measurement() bool { return s.Eval || s.Shadow }
```

`LatestNote` (line 491): the skip at line 497 becomes `s.Measurement()`, and its doc comment names
shadow runs beside eval runs.

**Register (`internal/store/register.go`).** `RegisterRow` (line 18) gains
`PRURL string `json:",omitempty"`` after `Company`, with a comment: set on RCA rows to the merged
PR the RCA read. (H2 fills it.)

**Runner options (`internal/run/run.go`).** `Options` (line 55) gains, after `Eval` (line 89):

```go
// Shadow marks the run as a shadow measurement (see store.State.Shadow). It implies NoNotify.
Shadow bool
```

`Resume` (line 440): the `prepared` literal at line 460 adds `noNotify: state.Shadow`, so a
resumed shadow run never notifies. (Eval resumes keep today's behaviour.)

**Prepare (`internal/run/prepare.go`).** In the `prepared` literal (line ~197) `noNotify` becomes
`o.NoNotify || o.DryRun || o.Shadow`. The `State` literal (lines 199-217) sets `Shadow: o.Shadow`.
`ReplyFirst` is unchanged: a shadow triage is reply-first because `NoteOnly` stays false.

**Exclusions moved from `.Eval` to `.Measurement()`:**

| File:line (d6877ac) | Function |
|---|---|
| `internal/run/execute.go:2351` | `writeNote` (comment names shadow runs too) |
| `internal/run/execute.go:2425` | `existingTriageNote` |
| `internal/run/execute.go:2497` | `appendRegister` |
| `internal/fix/fix.go:586` | `reviewedCommit` |
| `internal/fix/steer.go:185` | register append after a fix steer |
| `internal/app/livesteer.go:38` | `QueueLiveSteer` |

`internal/run/execute.go:814` (`sessionSpec`, `Policy.Ask`) is H2's, not this task's.

**Steer refusal (`internal/run/steer.go:205`, `RefuseSteer`).** Keep the eval branch as is and add
before it:

```go
if state.Shadow {
	return fmt.Errorf("run: %s is a shadow run; it is a measurement and is not steered", state.RunID)
}
```

`QueueLiveSteer` (`internal/app/livesteer.go:38`) gets the same split: shadow →
`fmt.Errorf("%w: run %s is a shadow run; it is a measurement and is not steered", ErrSteerRefused, runID)`.

**App read side.**
- `internal/app/types.go`: `RunSummary` (line 74) gains `Shadow bool `json:"shadow,omitempty"``
  after `ReplyFirst`, with a comment; `SummaryOf` (line 575) sets `Shadow: s.Shadow`. Beside
  `ErrInvalidArgument` (line 834) add the two sentinels later tasks share, so H8 and H9 can land
  in either order: `var ErrBadInput = errors.New("app: bad input")` and
  `var ErrNoSuchSuggestion = errors.New("app: no such suggestion")`, each with a comment naming
  the HTTP status it becomes (400, 404; H10 maps them).
- `internal/app/hooks.go` `busyReason` (line 96): both loops `continue` on `st.Shadow`, so a
  running or recent shadow run neither blocks a webhook triage nor starts its cooldown.
- `internal/app/service.go` `Queue` (line 661): the `LatestRun` decoration (lines 708-711) uses the
  newest state with `!st.Shadow`, not `states[0]`.
- `internal/app/service.go` `Runs` (line 422): split into unexported
  `func (s *Service) runs(wsID, key string, includeShadow bool) ([]RunSummary, error)` that skips
  `st.Shadow` unless `includeShadow`, and `Runs` calling it with `false`. Signature of `Runs`
  unchanged.

**Tests.**
- store: `TestMeasurement` table: zero state → false; `Eval` → true; `Shadow` → true; both → true.
  `TestLatestNoteSkipsShadowRuns`: two completed triage runs for `OMNI-1`, both with `note.md`; the
  newer has `Shadow: true`; `LatestNote(root, "OMNI-1", KindTriage)` returns the older run's path.
- run (`internal/run/shadow_test.go`): `TestShadowTriageFilesNothing`: `newWorkspace` with a notes
  dir; stub provider `replyThenNote("reply", finalEvent(<valid triage doc used by existing note
  tests>))`; a recording `notify.Notifier` on `Deps.Notifier`; `Triage(ctx, []string{"OMNI-1"},
  Options{Shadow: true})`. Expect: status `completed`; `note.md` and `result.json` in the run dir;
  `cfg.Notes.Dir` has no entries; `<root>/.sirdar/register.jsonl` does not exist; the notifier was
  called 0 times; the state has `Shadow == true` and `ReplyFirst == true`.
  `TestShadowResumeDoesNotNotify`: a shadow run left `blocked` with reason `interrupted` and a
  handle; `Resume` with a recording notifier completes it; notifier called 0 times.
  `TestRefuseSteerShadow`: `RefuseSteer(120, 25, 8, State{RunID: "r1", Status: completed, Shadow: true})`
  errors with text containing `is a shadow run`.
- app (`internal/app/shadowrun_test.go`): `TestBusyReasonIgnoresShadow`: states
  `[{Shadow: true, Status: running, Kind: triage}]` and cooldown 10m → `""`; the same with a
  shadow run updated 1 min ago and status completed → `""`. `TestQueueLatestRunSkipsShadow`: stub
  tracker lists `OMNI-1` (type bug); two runs exist, the newer shadow; `Queue` row's
  `LatestRun.RunID` is the older run's id. `TestRunsHidesShadow`: `Runs(ws, "")` omits the shadow
  run; `runs(ws, "", true)` includes it with `Shadow: true`. `TestQueueLiveSteerRefusesShadow`: a
  running shadow run → `errors.Is(err, ErrSteerRefused)` and the message contains `shadow run`.

Commit: `run: a shadow run is a measurement that files, registers and steers nothing`

---

### Task H2: a shadow run asks nobody and records what it was refused and what it asked

Touches: `internal/run/execute.go`, `internal/run/prepare.go`, `internal/run/run.go`,
`internal/prompt/prompt.go`, `internal/prompt/session.go`, `internal/prompt/prompt_test.go`,
`internal/run/shadow_policy_test.go` (new)

Depends on: H1

**Policy (`internal/run/execute.go`, `sessionSpec`, line 814).**

```go
spec.Policy.Ask = cfg.AskOperator() && !p.state.Measurement()
spec.Policy.Grants = sessionGrants(p)
if p.state.Shadow {
	// Nobody can answer a shadow run's question, so nobody can have granted it anything.
	spec.Policy.Grants = nil
}
```

**Denied calls (`handleEvent`, `case provider.EvPermission`, line 1267).** Before the existing
`ask` handling:

```go
if p.state.Shadow && ev.Decision == "deny" && len(p.state.Denied) < store.MaxDenied {
	p.state.Denied = append(p.state.Denied, store.DeniedCall{
		At: r.now(), Tool: ev.Tool, Summary: strings.TrimSpace(toolDetail(ev.Input)),
	})
}
```

The session continues: with `Ask` false the policy's plain deny never produces `ex.ask`.
`handleNoteEvent` (line 1353) is unchanged: the note turn's own denies are not recorded.

**Questions (`case provider.EvQuestion`, line 1279).** Add
`if p.state.Shadow { p.state.Questions = append(p.state.Questions, ev.Text) }`. The run still ends
`blocked` with `askedPrefix + question` (line 618-619); the auto-answer is the watcher's (H7).

**Shadow notice (`internal/prompt`).** `TriageInput` (`prompt.go` line 33) gains
`Shadow bool` with a comment. Add in `prompt.go`:

```go
// ShadowHeading names the unattended-run section.
const ShadowHeading = "# Unattended run"

// ShadowNotice is the text of that section, fixed so a measurement does not move between runs.
const ShadowNotice = "This run is unattended. Nobody will read your reply before the ticket is worked, " +
	"and nobody can answer a question or approve a refused tool. Investigate as you would for the " +
	"operator. When a tool is refused or you need an answer, record what you wanted and why under " +
	"openQuestions, and continue."

func shadowSection(on bool) string // "" when !on, else ShadowHeading + "\n\n" + ShadowNotice
```

Placement: in `Triage` (`prompt.go` line 146) immediately after the `operatorRequestSection`
append (whether or not an instruction exists); in `TriageReply` (`session.go` line 139),
immediately before the final `taskSection(...)`. `RCA`, `RCAReply` and the note-turn prompts are
unchanged.

**Prepare (`internal/run/prepare.go`).** In the `in := prompt.TriageInput{…}` literal (line 293)
set `Shadow: o.Shadow`. `runner.Options` (`run.go`) gains `NoReproduce bool` ("leaves the staging
reproduction out of a shadow run, for shadow.reproduce: false"). Where the investigation skeleton
fills `in.Repro` (its §4), leave it nil when `o.Shadow && o.NoReproduce`, and append the warning
`staging reproduction off: shadow.reproduce is false` to `p.state.Warnings`.

**RCA inputs.** In `prepare`, for `kind == store.KindRCA && rca != nil` and either `rca.PRURL` or
`rca.Resolution` non-empty, before the `WriteState` at line 237 set
`p.state.RCAInputs = &store.RCAInputs{PRURL: strings.TrimSpace(rca.PRURL), Resolution: clipped}`
where `clipped` is `rca.Resolution` cut to `store.RCAResolutionMax` runes. In `completeRCA`
(`execute.go` line 2220) the RCA register row (line 2287) sets
`PRURL: rcaPRURL(p)` (empty when `RCAInputs` is nil). The resolution row gets no PRURL.

**Tests (`internal/run/shadow_policy_test.go`, `internal/prompt/prompt_test.go`).**
- `TestShadowPolicyNeverAsks`: config with `permissions.ask: true`; a provider that records the
  `SessionSpec` it was given; a shadow triage → recorded `spec.Policy.Ask == false` and
  `spec.Policy.Grants == nil`; the same triage without `Shadow` → `Ask == true`.
- `TestShadowRecordsDeniedCalls`: the reply turn emits `EvPermission{Decision: "deny", Tool: "Bash",
  Input: {"command":"psql -c 'select 1'"}}`, then reply text, then the note turn completes. Run is
  `completed`; `State.Denied` has 1 entry, `Tool == "Bash"`, `Summary` contains `psql`.
- `TestShadowDeniedCallsCapped`: 25 deny events → `len(State.Denied) == 20`.
- `TestNonShadowDoesNotRecordDenied`: the first test without `Shadow` → `Denied` empty.
- `TestShadowQuestionRecorded`: the reply turn emits `EvQuestion{Text: "Which tenant?"}` and ends.
  Status `blocked`, reason `agent asked: Which tenant?`, `Questions == ["Which tenant?"]`. Then
  `Resume(ctx, runID, ResumeOptions{Answer: "x"})` with a session that asks `"Which env?"` again →
  `blocked`, `Questions == ["Which tenant?", "Which env?"]`, and the resumed session's first prompt
  was `"x"`.
- `TestShadowPromptNotice` (prompt package): `TriageReply(TriageInput{Shadow: true, …})` contains
  `ShadowHeading` and `ShadowNotice`, and the notice's index is less than the index of the task
  section's heading; with `Shadow: false` neither string appears. `Triage(...)` with `Shadow: true`
  contains the notice after the preamble and before the language section.
- `TestShadowNoReproduce`: with `Options{Shadow: true, NoReproduce: true}` and a workspace whose
  `repro.hosts` is set, the prompt has no reproduction section and the state warnings contain
  `staging reproduction off: shadow.reproduce is false`.
- `TestRCARecordsInputs`: an RCA run with `PRURL: "https://github.com/o/r/pull/9"` and a 5,000-rune
  resolution → `State.RCAInputs.PRURL` is that URL, `Resolution` has 4,000 runes; the register's
  RCA row has `PRURL` set and the resolution row does not.

Commit: `run: a shadow run asks nobody, and records what it was refused and what it asked`

---

### Task H3: the shadow config block

Touches: `internal/config/shadow.go` (new), `internal/config/shadow_test.go` (new),
`internal/config/config.go`, `docs/config.md`

Depends on: none

**Types (`internal/config/shadow.go`).**

```go
type ShadowConfig struct {
	Enabled         bool           `yaml:"enabled"`
	Interval        string         `yaml:"interval"`
	Assignee        string         `yaml:"assignee"`
	Types           *[]string      `yaml:"types"`
	Statuses        []string       `yaml:"statuses"`
	Since           string         `yaml:"since"`
	MaxPerDay       int            `yaml:"maxPerDay"`
	MaxUSDPerDay    float64        `yaml:"maxUSDPerDay"`
	Concurrency     int            `yaml:"concurrency"`
	Model           string         `yaml:"model"`
	PauseAtQuota    *float64       `yaml:"pauseAtQuota"`
	YieldToOperator *bool          `yaml:"yieldToOperator"`
	Reproduce       *bool          `yaml:"reproduce"`
	Outcomes        ShadowOutcomes `yaml:"outcomes"`
	Judge           ShadowJudge    `yaml:"judge"`
	Digest          ShadowDigest   `yaml:"digest"`
}
type ShadowOutcomes struct {
	Interval   string `yaml:"interval"`
	WindowDays int    `yaml:"windowDays"`
	Tracker    *bool  `yaml:"tracker"`
}
type ShadowJudge struct{ Model string `yaml:"model"` }
type ShadowDigest struct {
	Enabled    bool   `yaml:"enabled"`
	At         string `yaml:"at"`
	WebhookURL string `yaml:"webhookUrl"`
}
```

`Config` (`config.go`, after `Repos` at line 696) gains `Shadow ShadowConfig `yaml:"shadow"``.
`KnownFields(true)` already applies, so a misspelled key fails the load.

**Defaults.** `applyShadowDefaults(c *Config)`, called at the end of `applyDefaults` (line 760):
`Interval "15m"`, `Assignee "me"`, `Since "armed"`, `MaxPerDay 10`, `MaxUSDPerDay 10`,
`Concurrency 1`, `PauseAtQuota 0.85`, `YieldToOperator true`, `Reproduce true`,
`Outcomes.Interval "6h"`, `Outcomes.WindowDays 30`, `Outcomes.Tracker true`, `Digest.At "09:00"`.
Pointers are set only when nil; `Types` stays nil (absent) so the accessor can fall back. Trim
`Model`, `Judge.Model`, `Assignee`.

**Durations.** `func ParseDuration(s string) (time.Duration, error)`: Go syntax, plus a whole
number of days with a `d` suffix (`"7d"` = 168h).

**Accessors** (methods on `*Config`, nil-safe where the receiver may be a hand-built Config):

```go
func (c *Config) ShadowInterval() time.Duration
func (c *Config) ShadowOutcomesInterval() time.Duration
func (c *Config) ShadowSince() (since time.Time, armed bool) // armed=true for "armed"
func (c *Config) ShadowTypes() []string // *Types folded by NormalizeQueueTypes; else c.QueueTypes()
func (c *Config) ShadowPauseAtQuota() float64
func (c *Config) ShadowYield() bool
func (c *Config) ShadowReproduce() bool
func (c *Config) ShadowOutcomesTracker() bool
func (c *Config) ShadowModel() string      // "" means the workspace model
func (c *Config) ShadowJudgeModel() string // Judge.Model, else c.Model
func (c *Config) ShadowDigestAt() (hour, minute int)
```

**Validation.** `validateShadow(c *Config) error`, called from `Validate` right after
`validateNotify` (line 964). Every message starts `config: shadow.<key>: `.

| Key | Rule | Message tail (after `config: shadow.<key>: `) |
|---|---|---|
| `enabled` | true requires `sources.tracker` | `requires sources.tracker` |
| `interval` | parses; 5m ≤ d ≤ 24h | `must be a duration from 5m to 24h, got %q` |
| `assignee` | non-empty after trim | `must not be empty` |
| `types` | present but empty after folding → error | `must name at least one type (or "*")` |
| `statuses` | each non-empty after trim | `entry %d is empty` |
| `since` | `armed`, `2006-01-02`, or RFC 3339 | `must be armed, a date (2006-01-02) or an RFC 3339 time, got %q` |
| `maxPerDay` | 1..100 | `must be from 1 to 100, got %d` |
| `maxUSDPerDay` | 0 < x ≤ 500 | `must be > 0 and <= 500, got %v` |
| `maxUSDPerDay` | when `concurrency > 1`: ≥ `concurrency × budget.maxUsd` | `must be at least concurrency × budget.maxUsd (%v) when concurrency > 1` |
| `concurrency` | 1..4 | `must be from 1 to 4, got %d` |
| `pauseAtQuota` | 0, or 0 < x ≤ 1 | `must be 0 (off) or in (0, 1], got %v` |
| `outcomes.interval` | 15m..168h | `must be a duration from 15m to 7d, got %q` |
| `outcomes.windowDays` | 1..180 | `must be from 1 to 180, got %d` |
| `digest.at` | `HH:MM`, 00-23:00-59 | `must be HH:MM, got %q` |
| `digest.webhookUrl` | required when `digest.enabled`; must be a credential ref (`credentialRef`) | `is required when digest.enabled is true` / the `credentialRef` message |

The `hooks.slack.com` host check is at send time and in doctor (H8), because the config holds only
the reference.

**Docs.** `docs/config.md` gains a "Shadow mode" section: the YAML block from spec §10, the table
of keys, defaults and validation above, and two sentences on what a shadow run leaves behind
(nothing outside `.sirdar/`) and where the ledger is (`.sirdar/shadow.jsonl`).

**Tests (`internal/config/shadow_test.go`).** `TestShadowDefaults`: a minimal valid config with no
`shadow:` block loads; assert every default above, `ShadowTypes()` equals `QueueTypes()`,
`ShadowJudgeModel()` equals `Model`. `TestShadowTypesFallBack`: tracker `queue.types: [bug,
incident]` and no shadow types → `ShadowTypes()` is `[bug incident]`. `TestShadowValidation`:
table-driven, one case per row above with a failing value and the expected message substring, plus
one passing case per row at each boundary (`interval: 5m`, `24h`; `maxPerDay: 1`, `100`;
`pauseAtQuota: 0`, `1`; `outcomes.interval: 7d`; `since: 2026-10-01`). `TestShadowUnknownKey`:
`shadow: {intervall: 5m}` fails the load. `TestParseDuration`: `"7d"` → 168h, `"90m"` → 90m,
`"d"` and `"1.5d"` fail.

Commit: `config: a shadow block with daily caps, a quota pause and outcome settings`

---

### Task H4: the shadow ledger, start selection, and the scorecard arithmetic

Touches: `internal/shadow/ledger.go` (new), `internal/shadow/select.go` (new),
`internal/shadow/report.go` (new), `internal/shadow/verdict.go` (new), and their `_test.go` files

Depends on: none

A new pure package. It imports `internal/config` (for `MatchQueueType`) and the standard library
only, so it can be built before anything else lands.

**Ledger (`ledger.go`).**

```go
const Version = 1
func LedgerPath(root string) string // <root>/.sirdar/shadow.jsonl

type Entry struct {
	V   int       `json:"v"`
	At  time.Time `json:"at"`
	Ev  string    `json:"ev"` // armed seen started finished outcome graded skipped paused resumed digest
	Key string    `json:"key,omitempty"`

	Since *time.Time `json:"since,omitempty"` // armed
	By    string     `json:"by,omitempty"`    // armed, resumed

	Title     string     `json:"title,omitempty"`     // seen
	Type      string     `json:"type,omitempty"`      // seen
	Status    string     `json:"status,omitempty"`    // seen: ticket; finished: run; outcome: tracker
	CreatedAt *time.Time `json:"createdAt,omitempty"` // seen
	Baseline  bool       `json:"baseline,omitempty"`  // seen

	RunID         string `json:"runId,omitempty"`         // started, finished, outcome
	Attempt       int    `json:"attempt,omitempty"`       // started
	Model         string `json:"model,omitempty"`         // started
	OperatorFirst bool   `json:"operatorFirst,omitempty"` // started
	Backfill      bool   `json:"backfill,omitempty"`      // started
	Resume        bool   `json:"resume,omitempty"`        // started: a retry that resumed the handle

	Reason       string   `json:"reason,omitempty"` // finished, graded, skipped, paused
	CostUSD      float64  `json:"costUSD,omitempty"`
	ElapsedSec   float64  `json:"elapsedSec,omitempty"`
	Turns        int      `json:"turns,omitempty"`
	Verdict      *Verdict `json:"verdict,omitempty"`      // finished; outcome (operator-triage)
	Reproduction string   `json:"reproduction,omitempty"` // finished
	NoteFiled    bool     `json:"noteFiled,omitempty"`    // finished
	Denied       []Denied `json:"denied,omitempty"`       // finished
	Questions    []string `json:"questions,omitempty"`    // finished

	Source         string     `json:"source,omitempty"` // outcome: operator-rca operator-fix operator-triage manual tracker
	PRURLs         []string   `json:"prUrls,omitempty"`
	Resolution     string     `json:"resolution,omitempty"`
	Summary        string     `json:"summary,omitempty"`        // outcome: operator RCA summary
	Classification string     `json:"classification,omitempty"` // outcome: operator RCA classification
	ResolvedAt     *time.Time `json:"resolvedAt,omitempty"`

	ShadowRunID         string    `json:"shadowRunId,omitempty"` // graded
	Basis               string    `json:"basis,omitempty"`
	Final               bool      `json:"final,omitempty"`
	Grade               string    `json:"grade,omitempty"` // confirmed partial wrong no-answer ungradable
	PRFilesHit          *Fraction `json:"prFilesHit,omitempty"`
	CodeRefsPathOverlap *Fraction `json:"codeRefsPathOverlap,omitempty"`
	JudgeModel          string    `json:"judgeModel,omitempty"`
	LeadMinutes         *float64  `json:"leadMinutes,omitempty"`
	Warning             string    `json:"warning,omitempty"`

	Until *time.Time `json:"until,omitempty"` // paused
	Day   string     `json:"day,omitempty"`   // digest: local date 2006-01-02
}
type Verdict struct {
	Classification string   `json:"classification"`
	Confidence     string   `json:"confidence"`
	Hypothesis     string   `json:"hypothesis"` // at most 300 runes
	CodeRefs       []string `json:"codeRefs"`
	OpenQuestions  []string `json:"openQuestions,omitempty"`
}
type Denied struct {
	Tool    string `json:"tool"`
	Summary string `json:"summary"`
}
type Fraction struct {
	Matched int     `json:"matched"`
	Total   int     `json:"total"`
	Score   float64 `json:"score"`
}

func Append(root string, e Entry) error
func Read(root string) ([]Entry, error)
```

`Append` sets `V = Version` and a zero `At` to `time.Now()`, marshals one line, refuses a line of
64 KiB or more (`shadow: ledger line is %d bytes, over the 64 KiB limit`), creates `.sirdar/` when
missing, and writes with one `Write` call on a file opened
`os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600`. `Read` returns `nil, nil` for a missing file and
skips any line that does not parse (a torn last line after a crash).

**Fold (`ledger.go`).**

```go
type Attempt struct {
	Started  Entry
	Finished *Entry
}
type Ticket struct {
	Key, Title, Type, Status string
	CreatedAt    time.Time
	SeenAt       time.Time
	Baseline     bool
	Backfill     bool
	Attempts     []Attempt // in ledger order
	Outcomes     []Entry
	Grade        *Entry // the newest graded line whose ShadowRunID is the latest attempt's RunID
	Skipped      string // reason of the newest skipped line
}
func Fold(entries []Entry) map[string]*Ticket
func (t *Ticket) Latest() *Attempt                  // nil when never started
func Armed(entries []Entry) (since time.Time, ok bool) // newest armed line
func PausedUntil(entries []Entry, now time.Time) (until time.Time, reason string, paused bool)
```

`PausedUntil`: the newest `paused` line counts only if no `resumed` line is newer and its `Until`
is after `now`. A later `graded` line for the same `ShadowRunID` supersedes an earlier one.

**Day accounting (`select.go`).**

```go
type Day struct {
	Runs int     // started lines with attempt 1 whose At is in today's local calendar day
	USD  float64 // today's finished.costUSD + inFlightUSD + today's graded.costUSD
}
func Today(entries []Entry, inFlightUSD float64, now time.Time) Day
```

"Today" is `now`'s local date (`now.Location()`).

**Selection (`select.go`).**

```go
type Listed struct {
	Key, Title, Type, Status string
	CreatedAt time.Time // zero when the adapter gives none
}
type Policy struct {
	Types        []string // already folded
	Statuses     []string // case-folded match when non-empty
	Since        time.Time
	ArmedAt      time.Time // At of the armed line; zero when since is a fixed time
	MaxPerDay    int
	MaxUSDPerDay float64
	Concurrency  int
	Interval     time.Duration
}
const StaleAfter = 72 * time.Hour
const MaxAttempts = 2
type Start struct {
	Key     string
	Attempt int    // 1, or 2 for a retry
	RetryOf string // the previous attempt's RunID on a retry
}
type Plan struct {
	Seen    []Entry // new seen lines, in listed order
	Skipped []Entry // new skipped lines
	Start   []Start // in start order, already cut to the caps
	CapHit  string  // "daily run cap reached (10 of 10)" or "daily USD cap reached ($10.00 of $10.00)" or ""
}
func Select(entries []Entry, listed []Listed, now time.Time, p Policy, inFlight int, inFlightUSD float64) Plan
```

Rules, in order:
1. Filter `listed`: `config.MatchQueueType(p.Types, t.Type)`; when `p.Statuses` is non-empty,
   `strings.EqualFold` against one of them; and `!t.CreatedAt.Before(p.Since)` when `CreatedAt`
   is non-zero.
2. Each filtered key with no ledger entry gets a `seen` line (`Title`, `Type`, `Status`,
   `CreatedAt` when non-zero). A ticket with zero `CreatedAt` gets `Baseline: true` when this is
   the first tick after arming: `p.ArmedAt` is non-zero and no existing `seen` line has
   `At >= p.ArmedAt`. Baseline keys are never started.
3. Candidates: seen, non-baseline, not skipped, never started, sorted by `CreatedAt` ascending
   (zero last, then key); then retry-eligible keys sorted by their last `finished.At`.
4. A seen, never-started key whose `seen.At` is older than `StaleAfter` gets a `skipped` line with
   reason `stale: seen 72h ago and never started` (hours as an integer) and is not a candidate.
5. Retry-eligible: `len(Attempts) < MaxAttempts`, the last attempt has a `finished` line at least
   `p.Interval` before `now`, and its `Status`/`Reason` is one of: `failed` with a reason not
   containing `not_found`; `blocked` with reason `interrupted`; `blocked` with a reason starting
   `rate limited` or `model limit: `.
6. Caps: walk candidates while `inFlight + len(Start) < p.Concurrency`; a first attempt also needs
   `Today(...).Runs + newFirsts < p.MaxPerDay`; every start needs `Today(...).USD < p.MaxUSDPerDay`.
   The first cap that stops the walk names `CapHit`.

**Verdict readers (`verdict.go`).**

```go
func VerdictOf(runDir string) (*Verdict, bool)      // result.json → classification, rootCause.{confidence, hypothesis≤300 runes, codeRefs}, openQuestions; false when absent/unparseable
func ReproductionOf(runDir string) string           // result.json reproduction.outcome; "unknown" when absent, null, or not one of the three
```

**Scorecard (`report.go`).**

```go
type ShadowReport struct {
	Since            string     `json:"since"` // RFC 3339
	Headline         Headline   `json:"headline"`
	Rows             []Row      `json:"rows"`
	ByClassification []ClassRow `json:"byClassification"`
}
type Headline struct {
	Started            int     `json:"started"`
	Graded             int     `json:"graded"`
	Confirmed          float64 `json:"confirmed"`          // 0..1 of graded
	ConfirmedOrPartial float64 `json:"confirmedOrPartial"` // 0..1 of graded
	MedianElapsedSec   float64 `json:"medianElapsedSec"`
	ReadyBefore        int     `json:"readyBefore"` // graded rows with leadMinutes > 0
	ReadyOf            int     `json:"readyOf"`     // graded rows with a leadMinutes value
	CostUSD            float64 `json:"costUSD"`
	MedianCostUSD      float64 `json:"medianCostUSD"`
}
type Row struct {
	Key                 string      `json:"key"`
	Title               string      `json:"title"`
	RunID               string      `json:"runId"`
	StartedAt           string      `json:"startedAt"`
	Status              string      `json:"status"` // the latest attempt's finished status, "running" before it
	Classification      string      `json:"classification"`
	Hypothesis          string      `json:"hypothesis"` // first sentence, at most 160 runes
	Grade               string      `json:"grade"`      // "" while ungraded
	GradeReason         string      `json:"gradeReason"`
	Basis               string      `json:"basis"`
	Final               bool        `json:"final"`
	CostUSD             float64     `json:"costUSD"` // all attempts' run cost + every graded line's judge cost
	ElapsedSec          float64     `json:"elapsedSec"`
	Tags                []string    `json:"tags"` // provisional, operator first, backfill, no answer, ungradable
	Outcome             *RowOutcome `json:"outcome,omitempty"`
	Reproduction        string      `json:"reproduction"`
	Denied              []Denied    `json:"denied"`
	Questions           []string    `json:"questions"`
	PRFilesHit          *Fraction   `json:"prFilesHit,omitempty"`
	CodeRefsPathOverlap *Fraction   `json:"codeRefsPathOverlap,omitempty"`
	LeadMinutes         *float64    `json:"leadMinutes,omitempty"`
	Warning             string      `json:"warning,omitempty"`
}
type RowOutcome struct {
	Sources    []string `json:"sources"`
	PRURLs     []string `json:"prUrls"`
	Resolution string   `json:"resolution"`
	Status     string   `json:"status"`
}
type ClassRow struct {
	Classification string `json:"classification"`
	Graded         int    `json:"graded"`
	Confirmed      int    `json:"confirmed"`
	Partial        int    `json:"partial"`
	Wrong          int    `json:"wrong"`
}
func Report(entries []Entry, since time.Time) ShadowReport
```

Rows: one per key whose first `started.At >= since`, newest first by that time; `Denied` and
`Questions` are never nil (empty slices). Headline set: those rows minus any row tagged
`backfill` or `operator first` (baseline keys never have rows). `Graded` counts headline rows with
a final grade in {confirmed, partial, wrong, no-answer}; `ungradable` is excluded from the
denominator. `Confirmed` and `ConfirmedOrPartial` are 0 when `Graded` is 0. `MedianElapsedSec` is
over headline rows whose latest attempt finished `completed`. `CostUSD` is the sum of headline
rows' `CostUSD`; `MedianCostUSD` is the median of the same. Median of an even count is the mean of
the middle two. `ByClassification` groups final-graded headline rows by the shadow verdict's
classification (empty classification as `unknown`), sorted by `Graded` descending then name.

**Tests.**
- `ledger_test.go`: `TestAppendRead`: three entries round-trip with `v: 1`. `TestReadSkipsTornLine`:
  file with two good lines and a third `{"v":1,"at":"2026` → two entries, no error.
  `TestAppendRefusesHugeLine`: a `Resolution` of 70,000 bytes errors and the file is unchanged.
  `TestFoldSupersedesGrade`: started r1, finished r1, graded r1 `partial final:false`, graded r1
  `confirmed final:true` → `Ticket.Grade.Grade == "confirmed"`. `TestFoldGradeFollowsLatestAttempt`:
  a grade for r1 and a second attempt r2 → `Grade` is nil. `TestPausedUntil`: paused until
  now+1h → paused; a later `resumed` → not paused; paused until now−1m → not paused.
- `select_test.go`: `TestSelect` table-driven over `(entries, listed, now, policy, inFlight,
  inFlightUSD) → (seen keys, skipped keys, start keys, CapHit)`, one case each for: type filter
  drops a `task`; status filter keeps only `Open`; `since` drops an older ticket; baseline on first
  tick after armed for a zero `CreatedAt` and not on a later tick; stale skip after 73h; retry
  eligible after `failed provider exited`; no retry after `failed not_found: no such issue`; no
  retry within one interval; retry after `blocked interrupted`; no third attempt; count cap (9
  started today, max 10 → one start, then `CapHit` names the count); USD cap (`$10.00` spent →
  no start, `CapHit` names USD); concurrency 1 with one in flight → no start; oldest `CreatedAt`
  first. `TestTodayCounts`: attempt-2 starts do not count; yesterday's lines do not count;
  in-flight and judge USD do.
- `verdict_test.go`: fixtures `result.json` with a `reproduction` object of each outcome →
  `ReproductionOf` returns it; `"reproduction": null` → `unknown`; missing key → `unknown`;
  `VerdictOf` cuts a 500-rune hypothesis to 300.
- `report_test.go`: `TestReportHeadline`: six keys: confirmed, partial, wrong, no-answer (all
  final), one provisional partial (final false), one `backfill` confirmed → `Started 5`, `Graded 4`,
  `Confirmed 0.25`, `ConfirmedOrPartial 0.5`; the backfill row is in `Rows` with tag `backfill`
  and not in the headline. `TestReportMedians`: elapsed 100, 300, 200, 400 → 250; costs include
  the judge cost. `TestReportLead`: leads +47, −5, nil → `ReadyBefore 1`, `ReadyOf 2`.
  `TestReportOperatorFirstExcluded`. `TestReportSince`: a row started before `since` is absent.

Commit: `shadow: an append-only ledger, the start selection and the scorecard arithmetic`

---

### Task H5: the judge call and PR reader are shared, and adapters may answer tracker.outcome

Touches: `internal/eval/rubric.go`, `internal/eval/retro.go`, `internal/eval/judge_test.go` (new),
`internal/ticket/ticket.go`, `internal/source/source.go`, `internal/source/plugin/protocol.go`,
`internal/source/plugin/client.go`, `internal/source/plugin/client_test.go`,
`internal/source/plugin/testdata/*` (outcome fixture), `examples/adapters/file/main.go`,
`examples/adapters/file/main_test.go`, `docs/adapters.md`

Depends on: none

**`eval.JudgeOnce` (`internal/eval/rubric.go`).** Factor the body of `providerRubric.Judge`
(line 150) into:

```go
// JudgeOnce asks the workspace's provider one question with no tools and a structured answer.
func JudgeOnce(ctx context.Context, deps runner.Deps, model, prompt string, schema []byte) (json.RawMessage, float64, error)
```

Same `SessionSpec` as today (no MCP, `MCPStrict: true`, `Budget{MaxTurns: 2, MaxMinutes: 5}`,
`ModeTriage`, policy rooted at `deps.Config.Root`), `CloseInput`, the `rubricGrace` cancel, the
cost maximum, `extractJSON` fallback. `model == ""` means `deps.Config.Model`. A nil provider or
config errors `eval: no provider to judge with`. Errors keep today's wording with `rubric`
replaced by `judge`. `providerRubric.Judge` becomes `JudgeOnce(...)` then `RubricFromJSON`.

**PR reader (`internal/eval/retro.go`).** Rename `checkPRURL` (line 451) to exported `CheckPRURL`
and update its callers. `prMeta` (line 355) gains `Title, Body string`; `ghView` (line 462) adds
`title,body` to `--json`. Export `pullRequestDiff` (line 389) as `PullRequestDiff`. Add:

```go
type PRText struct{ URL, Title, Body string }
type PullRequestSet struct {
	Files []string // changedFiles order: sorted, deduplicated
	PRs   []PRText
	Diff  []byte
}
// PullRequestFiles reads every named pull request through gh, read-only.
func PullRequestFiles(ctx context.Context, urls []string) (PullRequestSet, error)
```

It checks each URL with `CheckPRURL`, uses `readPullRequests`, `changedFiles` and
`PullRequestDiff`.

**`ticket.Outcome` (`internal/ticket/ticket.go`).**

```go
// Outcome is how a tracker record ended, as tracker.outcome reports it. Go names verbatim on the wire.
type Outcome struct {
	Key          string
	Status       string
	Resolution   string    // at most 8,000 characters
	ResolvedAt   time.Time `json:",omitzero"`
	PullRequests []string
	Fields       map[string]string
}
const OutcomeResolutionMax = 8000
```

**`source.Outcomer` (`internal/source/source.go`, after `Transitioner`).**

```go
// Outcomer is implemented by trackers that can say how a record ended. It is a read.
type Outcomer interface {
	Outcome(ctx context.Context, key string) (ticket.Outcome, error)
}
```

**Plugin (`internal/source/plugin`).** `Describe` (`protocol.go` line 30) gains
`Methods []string `json:"methods,omitempty"``; the `Request.Method` comment lists
`tracker.outcome`. `client.go`: add `hasMethod(ctx, name string) error` beside `hasRole` (line
235), answering `&source.Error{Code: source.Unsupported, Message: "adapter does not advertise " + name}`;
and

```go
func (c *Client) Outcome(ctx context.Context, key string) (ticket.Outcome, error)
```

which requires role `tracker` and method `tracker.outcome`, calls `tracker.outcome` with
`{"key": key}`, cuts `Resolution` to `ticket.OutcomeResolutionMax` runes, and errors
`source.Internal` `tracker.outcome answered for %q, not %q` when the key differs. A static
assertion `var _ source.Outcomer = (*Client)(nil)`.

**File adapter (`examples/adapters/file/main.go`).** The fixture gains `"methods": [...]` and
`"outcomes": {"KEY": ticket.Outcome}`. `describe` returns the fixture's `Methods`. A new
`case "tracker.outcome"` answers the fixture's outcome or `not_found`. Update the doc comment's
fixture shape.

**Docs (`docs/adapters.md`).** Add the "Optional methods" section with the spec's §6(c) text
verbatim (the `describe` sentence, the method table row, the `ticket.Outcome` field list and the
`not_found` and no-unrelated-PR rules).

**Tests.**
- `internal/eval/judge_test.go`: `TestJudgeOnceReturnsFinal`: a stub provider whose session emits
  `EvUsage{CostUSD: 0.03}` and `EvFinal{Final: {"verdict":"confirmed"}}` → raw JSON equal, cost
  0.03, and the recorded spec has `MCPStrict == true`, `Budget.MaxTurns == 2`,
  `OutputSchema` equal to the schema passed. `TestJudgeOnceExtractsFromProse`: no final, result
  text `here {"a":1} done` → `{"a":1}`. `TestJudgeOnceNoAnswer` → error containing `gave no answer`.
  The existing rubric tests pass unchanged. `TestCheckPRURL`: `https://github.com/o/r/pull/1` ok;
  `-x`, `github.com/o/r/pull/1`, `ftp://h/x` fail.
- `client_test.go`: `TestOutcomeAdvertised`: file adapter fixture with `methods:
  ["tracker.outcome"]` and an outcome for `OMNI-1` (status `Done`, two PRs, `ResolvedAt`) →
  fields round-trip. `TestOutcomeNotAdvertised`: same outcome fixture, no `methods` → error with
  `Code == source.Unsupported` and message `adapter does not advertise tracker.outcome`.
  `TestOutcomeResolutionCut`: a 9,000-rune resolution → 8,000. `TestOutcomeUnknownKey` →
  `source.NotFound`.
- `examples/adapters/file/main_test.go`: `describe` carries `methods`; `tracker.outcome` answers.

Commit: `eval: the judge call and PR reader are shared, and adapters may answer tracker.outcome`

---

### Task H6: outcomes from the operator's runs, manual entry and the tracker, graded by a judge

Touches: `internal/shadow/outcome.go` (new), `internal/shadow/grade.go` (new),
`internal/shadow/outcome_test.go` (new), `internal/shadow/grade_test.go` (new),
`internal/shadow/testdata/` (new fixtures)

Depends on: H1, H4, H5 (H1 adds `State.RCAInputs`; tests write those states directly, so H2's
writer is not needed)

**Outcomes (`internal/shadow/outcome.go`).**

```go
// OperatorOutcomes reads the operator's own non-measurement runs on key and returns the outcome
// lines the ledger does not already hold, plus the StartedAt of the operator's first such run.
func OperatorOutcomes(root, key string, have []Entry, now time.Time) (lines []Entry, firstStart time.Time, err error)

// ManualOutcome validates and builds a manual outcome line.
func ManualOutcome(key string, prURLs []string, resolution string, now time.Time) (Entry, error)

// TrackerOutcome asks an Outcomer and returns a new line, or ok=false when it says nothing new.
func TrackerOutcome(ctx context.Context, o source.Outcomer, key string, have []Entry, now time.Time) (line Entry, ok bool, err error)
```

`OperatorOutcomes` walks `store.List(root, key)` skipping `st.Measurement()`:
- `KindRCA`, `completed` → `Source: "operator-rca"`, `RunID`, `PRURLs` from
  `st.RCAInputs.PRURL` (when set), `Resolution` from `st.RCAInputs.Resolution`, `Summary` and
  `Classification` from the run's `result.json` `rca.summary` / `rca.classification`.
- `KindFix` with `st.Fix.PRURL != ""` → `Source: "operator-fix"`, `RunID`, `PRURLs: [PRURL]`.
- `KindTriage`, `completed`, with a readable `result.json` → `Source: "operator-triage"`, `RunID`,
  `Verdict` from `VerdictOf`.
`firstStart` is the minimum `StartedAt` over every non-measurement run of the key (any kind, any
status). Dedupe: a line is new unless `have` holds an `outcome` with the same `Source`, `RunID`,
sorted `PRURLs`, `Resolution`, `Status`.

`ManualOutcome`: each URL passes `eval.CheckPRURL`; at least one URL or non-blank resolution
(`shadow: an outcome needs a --pr URL or a resolution`); `Source: "manual"`; resolution trimmed
and cut to 8,000 runes.

`TrackerOutcome`: `source.Unsupported` → `ok=false, err=nil`; `source.NotFound` → `ok=false,
err`; otherwise a `Source: "tracker"` line with `Status`, `Resolution`, `PRURLs: PullRequests`,
`ResolvedAt` (nil when zero), deduped as above.

**Grading (`internal/shadow/grade.go`).**

```go
type Judge func(ctx context.Context, prompt string, schema []byte) (json.RawMessage, float64, error)
var JudgeSchema = []byte(<the spec §7 schema verbatim>)
var ErrNotEnoughTruth = errors.New("shadow: no outcome strong enough to grade against")
var readPullRequests = eval.PullRequestFiles // replaced in tests

type ShadowRun struct {
	Dir      string // the shadow run's directory
	Started  Entry
	Finished Entry
}
type GradeInput struct {
	Run           ShadowRun
	Outcomes      []Entry
	OperatorStart time.Time // zero when the operator never ran the key
	JudgeModel    string
	Now           time.Time
}
func Basis(outcomes []Entry) (basis string, final bool, ok bool)
func JudgePrompt(shadowText string, v *Verdict, truth Truth) string
func Grade(ctx context.Context, judge Judge, in GradeInput) (Entry, error)
func Ungradable(key, shadowRunID string, now time.Time) Entry
```

`Basis`: tokens `pr` (any outcome has a PR URL) and `resolution` (any non-blank `Resolution`),
joined with `+` in that order → final. Else any `operator-rca` or `manual` → that source name,
final. Else any `operator-triage` → `operator-triage`, final false. Else `ok=false`.

`Grade`:
1. When the run has no `answer.md` and no `result.json` in `Run.Dir` → a `graded` line with
   `Grade: "no-answer"`, `Final: true`, `Basis: "none"`, `Reason: "the run ended without a reply or note"`,
   no judge call.
2. `Basis` not ok → `ErrNotEnoughTruth`.
3. PR URLs (union across outcomes, first-seen order) → `readPullRequests`. On error the line gets
   `Warning: "PR files unread: <first line of err>"` and grading continues. On success,
   `eval.ScoreTriage(resultJSON, text, set.Files)` where `resultJSON` is `result.json` (empty when
   absent) and `text` is `note.md`, else `answer.md`; copy its two fractions into `PRFilesHit` and
   `CodeRefsPathOverlap`.
4. `JudgePrompt` input: the shadow verdict (classification, confidence, hypothesis, codeRefs,
   openQuestions) or, without `result.json`, `answer.md` cut to 12,000 runes; the truth
   (resolution texts, operator RCA summary and classification, operator triage hypothesis under
   the heading `Operator's triage (may also be wrong)`, PR titles and bodies, changed files, and
   the PR diff cut to 20,000 bytes). It states the three verdicts in the spec's words and asks for
   the schema's JSON only.
5. `judge(ctx, prompt, JudgeSchema)`; parse `{verdict, sameRootCause, reason}`; a verdict outside
   {confirmed, partial, wrong} → error `shadow: the judge answered %q`. `reason` trimmed, cut to
   200 runes.
6. Line: `Ev: "graded"`, `Key`, `ShadowRunID: Run.Finished.RunID`, `Basis`, `Final`, `Grade`,
   `Reason`, the two fractions, `JudgeModel`, `CostUSD` (judge cost), `LeadMinutes =
   OperatorStart − Finished.At` in minutes rounded to one decimal (nil when `OperatorStart` is
   zero), `Warning`.

`Ungradable`: `graded`, `Grade: "ungradable"`, `Final: true`, `Basis: "window"`,
`Reason: "no outcome within the window"`.

**Tests (fixtures under `internal/shadow/testdata/`, run dirs built in `t.TempDir()` with
`store.CreateID` + `WriteState`).**
- `TestOperatorOutcomesRCA`: a completed RCA run with `RCAInputs{PRURL, Resolution}` and a
  `result.json` with `rca.summary`/`rca.classification` → one `operator-rca` line with all four.
- `TestOperatorOutcomesFix`, `TestOperatorOutcomesTriage`.
- `TestOperatorOutcomesSkipMeasurements`: shadow and eval runs on the key give nothing and do not
  move `firstStart`.
- `TestOperatorOutcomesDedupe`: run twice with the first result as `have` → second call returns
  none.
- `TestManualOutcome`: valid; `--pr` `notaurl` → error; neither PR nor resolution → error.
- `TestTrackerOutcomeUnsupported`, `TestTrackerOutcomeNew`, `TestTrackerOutcomeDedupe`.
- `TestBasis` table: `[pr]` → `pr`, final; `[pr+resolution]` → `pr+resolution`; `[operator-rca no
  pr no resolution]` → `operator-rca`, final; `[operator-triage]` → `operator-triage`, not final;
  `[tracker status only]` → not ok.
- `TestGradeNoAnswer`: empty run dir → `no-answer`, judge never called.
- `TestGradeWithPRFiles`: `readPullRequests` stubbed to return files `[src/a.cs, src/b.cs]`;
  `result.json` codeRefs `[src/a.cs:12]`; stub judge returns
  `{"verdict":"confirmed","sameRootCause":true,"reason":"same null check"}` cost 0.03 →
  `Grade confirmed`, `PRFilesHit {1,2,0.5}`, `CodeRefsPathOverlap {1,1,1}`, `CostUSD 0.03`.
- `TestGradePRReadFails`: stub errors `gh not on PATH` → `Warning` set, fractions nil, judge
  still called.
- `TestGradeUnknownVerdict` → error, no line.
- `TestGradeLead`: operator started 47 min after `finished.At` → `LeadMinutes 47`.
- `TestJudgePromptLabelsOperatorTriage`: the prompt contains `Operator's triage (may also be wrong)`.

Commit: `shadow: outcomes from the operator's runs, manual entries and the tracker, graded by a judge`

---

### Task H7: an autonomy gate and a shadow watcher that triages new tickets unattended

Touches: `internal/app/autonomy.go` (new), `internal/app/shadow.go` (new),
`internal/app/shadowlock.go` (new), `internal/app/pidalive_unix.go` (new, `//go:build !windows`),
`internal/app/pidalive_windows.go` (new), `internal/app/service.go`, `internal/app/types.go`,
`internal/app/autonomy_test.go` (new), `internal/app/shadow_test.go` (new)

Depends on: H2, H3, H4

**Autonomy gate (`autonomy.go`).**

```go
type ActionKind string
const ActionShadowTriage ActionKind = "shadow-triage"

// AutonomyGate is the single place that decides whether an unattended action may start.
type AutonomyGate struct { /* mu; per (wsID, kind) slot counts */ }
func newAutonomyGate() *AutonomyGate
func (g *AutonomyGate) TryAcquire(wsID string, kind ActionKind, limit int) (release func(), ok bool)
func (g *AutonomyGate) InFlight(wsID string, kind ActionKind) int
```

`release` is idempotent. Shadow runs, their auto-answer resumes and retries all hold a slot; judge
calls do not.

**Service changes (`service.go`).** `Options` (line 50) gains
`Shadow bool // run the shadow watcher; serve and shadow watch set it, the desktop shell does not`.
`Service` (line 86) gains `gate *AutonomyGate` and `shadow *shadowState` (per-workspace watcher
state, guarded by its own mutex). `New` initialises both. `Start` (line 145) calls
`s.startShadow(ctx)` after the run watcher starts, when `s.opts.Shadow`. `Stop` releases every
shadow lock this process holds after the jobs have ended.

**Event (`types.go`).** Add `KindShadowStatus = "shadow.status"` to the kind constants (line 26)
and `Shadow *ShadowStatus `json:"shadow,omitempty"`` to `Event` (line 534).

**Status (`shadow.go`).**

```go
type ShadowStatus struct {
	Enabled           bool        `json:"enabled"`
	State             string      `json:"state"` // watching paused gated locked-elsewhere stopped disabled
	Reason            string      `json:"reason"`
	PausedUntil       string      `json:"pausedUntil,omitempty"`
	LastPoll          string      `json:"lastPoll,omitempty"`
	NextPoll          string      `json:"nextPoll,omitempty"`
	LastError         string      `json:"lastError,omitempty"`
	ConsecutiveErrors int         `json:"consecutiveErrors"`
	Today             ShadowToday `json:"today"`
	InFlight          []string    `json:"inFlight"` // run ids
	HasLedger         bool        `json:"hasLedger"`
	Breaches          int         `json:"breaches"` // finished lines today whose reason starts "read-only breach" or names a blind run
}
type ShadowToday struct {
	Runs         int     `json:"runs"`
	MaxPerDay    int     `json:"maxPerDay"`
	USD          float64 `json:"usd"`
	MaxUSDPerDay float64 `json:"maxUSDPerDay"`
}
type ShadowStarted struct {
	Key     string `json:"key"`
	RunID   string `json:"runId,omitempty"`
	JobID   JobID  `json:"jobId,omitempty"`
	Skipped string `json:"skipped,omitempty"` // why it did not start
}
var ErrShadowLocked = errors.New("app: shadow is watched by another process")
const shadowNoAnswer = "Nobody is available to answer. Continue with your best judgement and list this question under openQuestions."
const shadowFirstTick = 30 * time.Second
const shadowSupervise = 30 * time.Second
const shadowRatePause = 30 * time.Minute
const shadowModelPause = time.Hour
```

Unexported service methods (H10 wraps them):

```go
func (s *Service) shadowStatus(wsID string) (ShadowStatus, error)
func (s *Service) shadowPoll(ctx context.Context, wsID string) (ShadowStatus, error)
func (s *Service) shadowPause(wsID string, d time.Duration) (ShadowStatus, error) // d <= 0 means 24h
func (s *Service) shadowResume(wsID string) (ShadowStatus, error)
func (s *Service) shadowRun(ctx context.Context, wsID string, keys []string, backfill bool) ([]ShadowStarted, error)
func (s *Service) shadowTick(ctx context.Context, ws Workspace, cfg *config.Config) error
```

`shadowStatus` works in any process: when this process holds no watcher for the workspace it reads
the lock file (`locked-elsewhere`, reason `shadow: watched by pid N (serve)`), else `stopped`
(reason `not watching; run sirdar serve or sirdar shadow watch`), or `disabled` when
`shadow.enabled` is false; `Today` and `HasLedger` come from the ledger either way.
`shadowPause` appends `{ev: paused, until: now+d, reason: "paused by the operator"}`;
`shadowResume` appends `{ev: resumed, by: "operator"}`. Both publish the new status.
`shadowPoll` takes the lock when nobody holds it (and releases it after the tick unless this
process is watching), else returns `ErrShadowLocked`.

**Lock (`shadowlock.go`).** `.sirdar/shadow.lock`, created `O_CREATE|O_EXCL|O_WRONLY, 0o600`,
contents `{"pid":N,"host":"…","by":"serve|watch|poll","startedAt":"RFC3339"}`. Touched
(`os.Chtimes`) on every tick. Stale when `host` equals this host and `!pidAlive(pid)`, or when its
mtime is older than `3 × interval`; a stale lock is removed and taken. `pidAlive`: unix
`syscall.Kill(pid, 0)` is nil or `EPERM`; windows `os.FindProcess(pid)` succeeds.

**Supervisor.** `startShadow` runs one goroutine (counted in `s.running`) that every
`shadowSupervise` lists registered workspaces, reloads each config (`s.load`), and for each with
`shadow.enabled`: takes or keeps the lock (`locked-elsewhere` when it cannot), and calls
`shadowTick` when `now >= nextPoll` (first poll `shadowFirstTick` after start). A workspace whose
config turns `enabled` off releases its lock and shows `disabled` at the next pass.

**One tick (`shadowTick`)**, spec §4:
1. Recover (first tick per workspace): for every `started` with no `finished`, read the run's
   state. Terminal → write its `finished` line. `preparing`/`running` → write the state
   `blocked`, reason `interrupted`, and a `finished` line with that status and reason.
2. Arm: when `ShadowSince()` says `armed` and the ledger has no `armed` line, append
   `{ev: armed, since: now, by: <lock's by>}`.
3. Gate, in order, recording the first reason in the status and returning: ledger paused →
   `paused`, reason `paused until <15:04>`; quota (below) → `paused`; yield (`ShadowYield()` and any
   non-measurement run in the workspace `preparing`/`running`) → `gated`, reason
   `deferring to your run <runId>`; `shadow.Today(...)` at either cap → `gated` with the cap text.
4. List: `deps.Tracker.List(ctx, source.ListFilter{Assignee: queueAssignee(cfg, cfg.Shadow.Assignee),
   Order: source.OrderNewest, Limit: 200})` through `s.build`. On error: `source.Auth` → state
   `gated`, reason `tracker credentials rejected`, held until the config file's mtime changes;
   `source.Unsupported` → state `disabled`, reason from the error; anything else →
   `consecutiveErrors++`, `lastError`, next poll at `interval × 2^errors` capped at
   `4 × interval`. A success resets the count.
5. Record and select: `shadow.Select(entries, listed, now, policy, gate.InFlight, inFlightUSD)`;
   append its `seen` and `skipped` lines. Any append error aborts the tick before starting
   anything (`lastError` set).
6. Start each `Plan.Start` (below) while `gate.TryAcquire(ws.ID, ActionShadowTriage,
   cfg.Shadow.Concurrency)` succeeds.
Then set `lastPoll`, `nextPoll = now + interval`, publish the status if it changed.

**Quota check.** From `s.Quota()` take the reading whose `Provider` equals `string(cfg.Provider)`.
Paused when `ShadowPauseAtQuota() > 0` and `SevenDay.Utilization`, `FiveHour.Utilization` or
`UsedPercent/100` is `>=` it. Reason, for the highest window:
`seven-day quota at 88% (pause at 85%), resets 2026-10-09 14:00` (`five-hour` for the other;
`quota` for Codex; local time `2006-01-02 15:04`; `resets` part omitted when unknown).

**Starting one run.** `runID := store.NewRunID(now)`; append
`{ev: started, key, runId, attempt, model: <shadow model or workspace model>, operatorFirst,
backfill, resume}` where `operatorFirst` is true when any non-measurement run on the key started
before now, and `resume` is true for a retry whose previous run has a `Handle` (that retry
reuses the previous run id and runs `Resume`; otherwise a fresh run id and `Triage`). If the
append fails, release the slot and abort. Then
`s.startJob(ctx, ws.ID, "", model, release, work, onBuildError)` where `work`:
1. Clones the config: `cc := *deps.Config`; `remaining := cfg.Shadow.MaxUSDPerDay − Today.USD`;
   `cc.Budget.MaxUSD = min(cc.Budget.MaxUSD, remaining)`; `deps.Config = &cc`.
2. Runs `r.Triage(jctx, []string{key}, runner.Options{Shadow: true, NoNotify: true, Model:
   cfg.ShadowModel(), RunID: runID, AsOf: asOf, NoReproduce: !cfg.ShadowReproduce(),
   Concurrency: 1})`, or `r.Resume(jctx, runID, runner.ResumeOptions{})` for a resume retry.
3. Auto-answer: if the outcome is `blocked` with reason prefix `agent asked: ` and
   `len(State.Questions) < 2`, `r.Resume(jctx, runID, runner.ResumeOptions{Answer: shadowNoAnswer})`.
4. Writes `finished` from the final state through
   `shadowFinished(key string, st store.State, dir string, now time.Time) shadow.Entry`:
   `status`, `reason` (`asked twice` when blocked asked and `len(Questions) >= 2`), `costUSD`,
   `elapsedSec`, `turns`, `verdict` (`shadow.VerdictOf`; absent when `NoteWarning` is set),
   `reproduction` (`shadow.ReproductionOf`), `noteFiled` (completed, no `NoteWarning`, `note.md`
   exists), `denied` (from `State.Denied`), `questions`.
5. Pauses: blocked with reason prefix `rate limited` → append `paused` until the `resets at` time
   in the reason, else now + `shadowRatePause`, reason `rate limited`; blocked with
   `runner.ModelLimited(reason) != ""` → `paused` until now + `shadowModelPause`, reason
   `model limit`.
6. Publishes the status.

**`shadowRun` (CLI `shadow run`).** Requires `sources.tracker`. For each key, in order: checks
the key; when `backfill`, `deps.Tracker.Get` and refuses a ticket with zero `CreatedAt`
(`Skipped: "no creation time; cannot backfill"`), using `AsOf = CreatedAt` and marking the
started line `backfill: true`; applies the same gate (paused, quota, yield, caps, slot) and
records the first refusal in `Skipped`; appends a `seen` line for an unseen key; then starts as
above. It does not need the lock or `shadow.enabled`.

**Tests (`autonomy_test.go`, `shadow_test.go`)**, with a Service built over `newRegistry`, a
DepsBuilder returning the stub tracker and stub provider, and `Options.Now` a settable clock.
Tick tests call `shadowTick` directly.
- `TestGateSemaphore`: limit 1 → second `TryAcquire` fails until the first `release`; release is
  idempotent; two workspaces do not share slots.
- `TestShadowClonedBudget`: workspace `budget.maxUsd: 8`, `shadow.maxUSDPerDay: 10`, ledger with
  `$7.00` finished today → the provider's recorded `SessionSpec.Budget.MaxUSD` is `3`.
- `TestShadowTickStartsNewTicket`: tracker lists `OMNI-1` (bug, created now) after arming → ledger
  gets `armed`, `seen`, `started`, `finished` (status completed, verdict set); the run's state has
  `Shadow: true`; no file in the notes dir; no `register.jsonl`.
- `TestShadowTickYields`: a non-shadow run `running` in the workspace → state `gated`, reason
  starts `deferring to your run`, nothing started.
- `TestShadowTickQuotaGate`: `s.quota` seeded with claude seven-day 0.88 → state `paused`, reason
  `seven-day quota at 88% (pause at 85%), resets …`; nothing started.
- `TestShadowTickPaused`: ledger `paused` until +1h → nothing started; after `resumed` → starts.
- `TestShadowAutoAnswer`: first session emits `EvQuestion` → blocked; the auto-answer resume's
  first prompt equals `shadowNoAnswer`; it completes; one `started` line, `finished` completed.
- `TestShadowAskedTwice`: both sessions ask → `finished` reason `asked twice`, status blocked.
- `TestShadowRateLimitPauses`: session emits `EvRateLimited{ResetsAt: now+2h}` → `paused` until
  now+2h; next tick within the pause starts nothing; after it, the run is resumed once
  (`started` attempt 2 with `resume: true` and the same run id).
- `TestShadowRecoverInterrupted`: a `started` line for a run whose state is `running` → first tick
  writes the state `blocked interrupted` and a `finished` line; after one interval it is retried
  once.
- `TestShadowLockTakeover`: lock file with pid 999999 on this host → taken. `TestShadowLockedElsewhere`:
  lock with `os.Getppid()` → state `locked-elsewhere`, reason `shadow: watched by pid <n> (serve)`,
  and `shadowPoll` returns `ErrShadowLocked`.
- `TestShadowAdapterBackoff`: `List` errors twice → `consecutiveErrors 2`, `nextPoll` = last poll +
  `4 × interval`; an `auth` error → `gated` `tracker credentials rejected`.
- `TestShadowConfigReload`: enabled → watching; config rewritten with `enabled: false` → next
  supervise pass shows `disabled` and the lock is gone.
- `TestShadowRunBackfill`: `shadowRun(ctx, ws, ["OMNI-1"], true)` → started line `backfill: true`
  and the run's bundle manifest records the cutoff (`AsOf` path); a ticket with no `CreatedAt` →
  `Skipped` `no creation time; cannot backfill`.
- `TestShadowStatusWithoutWatcher`: a Service without `Options.Shadow`, no lock → `stopped`;
  `shadow.enabled: false` → `disabled`; `HasLedger` follows the file.

Commit: `app: an autonomy gate and a shadow watcher that triages new tickets unattended`

---

### Task H8: shadow outcomes are collected and graded, with a metadata-only digest and a doctor row

Touches: `internal/app/shadowoutcomes.go` (new), `internal/app/shadowdigest.go` (new),
`internal/app/doctor_shadow.go` (new), `internal/app/shadow.go`, `internal/app/service.go`
(`observe`), `internal/app/doctor.go`, `internal/notify/slack.go`, `internal/run/run.go`
(`credentialEnvNames`), `internal/app/shadowoutcomes_test.go` (new),
`internal/app/doctor_shadow_test.go` (new), `internal/notify/notify_test.go`,
`internal/run/run_test.go` (one test added)

Depends on: H6, H7

**Aliases (`shadowoutcomes.go`).** `type ShadowReport = shadow.ShadowReport` and
`type ShadowRow = shadow.Row`, so the wire types have one definition.

**Collect and grade.**

```go
var shadowJudge = eval.JudgeOnce // replaced in tests
var digestPost = notify.PostSlackText // replaced in tests
var digestHostOK = func(host string) bool { return host == "hooks.slack.com" } // replaced in tests

func (s *Service) shadowCollectAndGrade(ctx context.Context, ws Workspace, cfg *config.Config, keys []string) ([]ShadowRow, error)
func (s *Service) shadowOutcome(ctx context.Context, wsID, key string, prURLs []string, resolution string) (ShadowRow, error)
func (s *Service) shadowRegrade(ctx context.Context, wsID string, keys []string) ([]ShadowRow, error)
func (s *Service) shadowReport(wsID string, since time.Time) (ShadowReport, error)
```

`shadowCollectAndGrade` (empty `keys` means every ledger key), per folded ticket with a latest
attempt that has a `finished` line and is not retry-eligible:
1. Finished more than `outcomes.windowDays` ago with no final grade → append `shadow.Ungradable`.
2. `shadow.OperatorOutcomes`; when `ShadowOutcomesTracker()` and `deps.Tracker` implements
   `source.Outcomer`, `shadow.TrackerOutcome`. Append every new line.
3. Grade when there is no grade yet, or the current grade is not final, or a new outcome line was
   appended this pass: `shadow.Grade(ctx, judge, GradeInput{…, JudgeModel: cfg.ShadowJudgeModel()})`
   where `judge` closes over `deps` and calls `shadowJudge`. `ErrNotEnoughTruth` → skip. A judge
   error → no line, `lastError` set, and a per-key per-local-day failure count; at 3 the key is
   not judged again that day.
4. Return the rows of the keys touched, from `shadow.Report(entries, time.Time{})`.

`shadowOutcome`: `shadow.ManualOutcome` (an invalid URL or empty input wraps `ErrBadInput`, the
sentinel H1 added), appends it, then
`shadowCollectAndGrade` for that key. `shadowRegrade` is `shadowCollectAndGrade` for `keys`.
`shadowReport` reads the ledger and calls `shadow.Report`.

**Tick step.** In `shadowTick` (H7), after starting runs: when `now >= nextOutcomes` run
`shadowCollectAndGrade(ctx, ws, cfg, nil)` and set `nextOutcomes = now + ShadowOutcomesInterval()`
(first outcome pass on the first tick).

**Trigger (`service.go`, `observe`, line 211).** Add: on `KindRunUpdated` with `e.Run != nil`,
`!e.Run.Shadow`, `e.Run.Status == "completed"`, `e.Run.Kind` `rca` or `fix`, and `s.opts.Shadow`,
start a goroutine (counted in `s.running`) that loads the workspace and, if the ledger has a
finished shadow run for `e.Run.Key`, calls `shadowCollectAndGrade(ctx, ws, cfg, []string{key})`.

**Digest (`shadowdigest.go`).**

```go
func digestText(entries []shadow.Entry, day time.Time) string
func (s *Service) maybeSendDigest(ctx context.Context, ws Workspace, cfg *config.Config, now time.Time)
```

`digestText` (counts only, no titles, hypotheses or ticket text):
`Sirdar shadow, 2026-10-06: started 3, graded 2, confirmed 50%, cost $2.10` then one line per key
graded that day: `OMNI-3520 confirmed`. `maybeSendDigest` runs in each tick when
`digest.enabled`, local time is past `digest.at`, and no `{ev: digest, day: <today>}` line exists:
resolves `digest.webhookUrl` with `config.Resolver{Keychain: KeychainFor()}`, refuses any host but
`hooks.slack.com` (`lastError`: `shadow.digest.webhookUrl: host must be hooks.slack.com`), posts
with `notify.PostSlackText`, and on success appends `{ev: digest, day}`.

`internal/notify/slack.go` gains
`func PostSlackText(ctx context.Context, webhookURL, text string) error` that posts
`slackMessage{Text: text}` through the existing `transport{}.post` (same client, timeouts and
retry as run notifications).

`internal/run/run.go` `credentialEnvNames` (line 283): append `cfg.Shadow.Digest.WebhookURL` to
`refs` so no agent session can read it.

**Doctor row (`doctor_shadow.go`).** `func shadowCheck(ctx context.Context, cfg *config.Config) (Check, bool)`,
appended in `RunDoctor` (`doctor.go` line 58) after the fetch/transcribe checks; `false` (no row)
when `sources.tracker` is unset. Name `shadow`. Parts joined with `; `, level the worst of them:
- assignee: `IsSelf` and the adapter does not resolve self and `SelfOf(cfg) == ""` → fail
  `assignee me resolves to nothing; set me: in config.yaml`; else ok `assignee <resolved>`.
- listing: `List` with the shadow filter, limit 20, filtered by `ShadowTypes()` → ok
  `lists N open <types> tickets`, or fail with the error's first line.
- ledger: `git -C <root> check-ignore -q .sirdar/shadow.jsonl` exit 1 → warn
  `.sirdar/shadow.jsonl is not git-ignored`; same for `.sirdar/playbook-suggestions.jsonl`; exit
  128 (not a repo) → no part.
- outcome method: an `exec` adapter whose `describe` lacks `tracker.outcome` → ok
  `tracker.outcome unsupported; outcomes come from your runs and manual entries`; listed → ok
  `tracker.outcome supported`.
- `enabled: false` → ok `off`.

**Tests.**
- `TestCollectGradesOperatorRCA`: ledger with a finished shadow run (result.json, codeRefs) for
  `OMNI-1`; a completed operator RCA run with `RCAInputs{Resolution: "Null tenant on refund"}` and
  no PR (so nothing calls `gh`); `shadowJudge` stubbed to return
  `{"verdict":"confirmed","sameRootCause":true,"reason":"same cause"}` at cost 0.03 → ledger gains
  an `outcome` (`operator-rca`) and a `graded` line (`confirmed`, final, basis `resolution`,
  `costUSD 0.03`).
- `TestCollectProvisionalThenFinal`: first only an operator triage → `operator-triage`, final
  false; then a manual outcome → a second graded line, final true.
- `TestCollectUngradable`: finished 31 days ago, `windowDays 30` → `ungradable`.
- `TestJudgeFailureCappedPerDay`: judge errors → no graded line; the fourth pass on the same day
  does not call the judge.
- `TestObserveTriggersCollection`: publish a `run.updated` for a completed non-shadow RCA on a
  ledger key → a graded line appears (wait on the ledger with a bounded poll).
- `TestManualOutcomeBadInput`: `notaurl` → `errors.Is(err, ErrBadInput)`.
- `TestDigestTextIsMetadataOnly`: entries with titles and hypotheses containing `Customer Ali`
  → text has counts and `OMNI-1 confirmed` and does not contain `Customer Ali`.
- `TestDigestOncePerDay`: `digest.webhookUrl: env:SHADOW_HOOK` set (with `t.Setenv`) to an
  `httptest` URL, `digestHostOK` stubbed to accept it, `digestPost` left real: two ticks after
  `09:00` → one POST and one `digest` line.
- `TestDigestRefusesOtherHost`: the real `digestHostOK` and an `https://example.com/x` URL → no
  POST, `lastError` `shadow.digest.webhookUrl: host must be hooks.slack.com`.
- notify: `TestPostSlackText` posts `{"text": …}`.
- run: `TestCredentialEnvNamesIncludesDigest`: `env:SHADOW_HOOK` → name present.
- doctor: `TestShadowCheckMeUnresolved` (exec adapter, no `me:`, git identity empty) → fail;
  `TestShadowCheckLedgerNotIgnored` (temp git repo without an exclude) → warn;
  `TestShadowCheckNoTracker` → no row.

Commit: `app: shadow outcomes are collected and graded, with a metadata-only digest and a doctor row`

---

### Task H9: operator corrections and RCA suggestions queue as playbook additions

Touches: `internal/store/suggestions.go` (new), `internal/store/suggestions_test.go` (new),
`internal/run/execute.go` (`completeRCA`), `internal/run/suggestions_test.go` (new),
`internal/app/suggestions.go` (new), `internal/app/suggestions_test.go` (new)

Depends on: H2, H5 (`ErrBadInput` and `ErrNoSuchSuggestion` come from H1)

The JSONL format lives in `internal/store` because `internal/run` writes RCA proposals and cannot
import `internal/app`; the service logic is in `internal/app/suggestions.go` as the spec names.

**Store (`internal/store/suggestions.go`).**

```go
func SuggestionsPath(root string) string // <root>/.sirdar/playbook-suggestions.jsonl
type SuggestionLine struct {
	V        int       `json:"v"`
	At       time.Time `json:"at"`
	Ev       string    `json:"ev"` // proposed accepted dismissed
	ID       string    `json:"id"`
	Source   string    `json:"source,omitempty"` // remember rca
	RunID    string    `json:"runId,omitempty"`
	Key      string    `json:"key,omitempty"`
	Playbook string    `json:"playbook,omitempty"`
	Addition string    `json:"addition,omitempty"`
	Reason   string    `json:"reason,omitempty"`
	CostUSD  float64   `json:"costUSD,omitempty"`
	Edited   bool      `json:"edited,omitempty"`
}
func SuggestionID(playbook, addition string) string // "s-" + hex of the first 6 bytes of sha256(playbook+addition)
func AppendSuggestion(root string, l SuggestionLine) (appended bool, err error)
func ReadSuggestions(root string) ([]SuggestionLine, error)
func PendingSuggestions(lines []SuggestionLine) []SuggestionLine // proposed, no later accepted/dismissed; oldest first
```

Same write rules as the shadow ledger (`O_APPEND`, one write, under 64 KiB, `0o600`, skip
unparseable lines on read). `AppendSuggestion` for `proposed` fills `ID` when empty and returns
`false, nil` without writing when that id is already pending.

**RCA proposals (`internal/run/execute.go`, `completeRCA`, line 2260).** After
`writePlaybookSuggestions` succeeds and when `!p.state.Measurement()`, append one `proposed` line
per `f.RCA.PlaybookSuggestions` entry: `Source: "rca"`, `RunID`, `Key`, `Playbook`, `Addition`,
`Reason`. An append error is a warning `playbook suggestions: <err>`.

**Service (`internal/app/suggestions.go`).**

```go
type Suggestion struct {
	ID       string  `json:"id"`
	At       string  `json:"at"`
	Source   string  `json:"source"` // remember | rca
	RunID    string  `json:"runId"`
	Key      string  `json:"key"`
	Playbook string  `json:"playbook"`
	Addition string  `json:"addition"`
	Reason   string  `json:"reason"`
	CostUSD  float64 `json:"costUSD"`
}
var rememberJudge = eval.JudgeOnce // replaced in tests
const rememberTimeout = 90 * time.Second
const rememberAdditionMax = 600
const correctionsPlaybook = "90-corrections.md"
var rememberSchema = []byte(`{"type":"object","additionalProperties":false,"required":["playbook","addition","reason"],"properties":{"playbook":{"type":"string"},"addition":{"type":"string"},"reason":{"type":"string"}}}`)

func (s *Service) suggestions(wsID string) ([]Suggestion, error)
func (s *Service) remember(ctx context.Context, wsID, runID string, index int) (Suggestion, error)
func (s *Service) acceptSuggestion(wsID, id, playbook, addition string) (Suggestion, error)
func (s *Service) dismissSuggestion(wsID, id string) error
```

`remember`:
1. Opens the run; a measurement run → `ErrBadInput` `run %s is a measurement; its messages are not remembered`.
2. `index == 0` is the run's `Instruction` (empty → `ErrBadInput`). `index >= 1` is the 1-based
   position among parseable lines of `events.jsonl`, the same counting as `Service.Events`; the
   event must be `steer` or `answer` (`ErrBadInput` otherwise). The assistant context is the last
   `assistant_text` before it (a `replace` message, or the concatenated deltas of the last
   message), else `answer.md`, cut to 4,000 runes.
3. Lists `s.Playbooks(wsID)` (name, title, lede).
4. `rememberJudge(ctx90s, deps, cfg.Model, prompt, rememberSchema)`. The prompt gives the
   operator's message, the assistant context and the playbook list, and asks for an existing
   playbook name or a new `NN-slug.md`, an addition of at most 600 characters written as a
   standing rule rather than the story of this ticket, and a one-sentence reason.
5. Accepts the answer when `playbook` is a listed name or `ValidPlaybookName`; addition trimmed
   and cut to 600 runes, non-empty.
6. Any failure in 4 or 5 → the fallback: `playbook: 90-corrections.md`,
   `addition: "- <2006-01-02> <key>: <operator message, one line, cut to 500 runes>"`,
   `reason: "the suggestion could not be drafted; kept the message as written"`, cost as spent.
7. `store.AppendSuggestion` (`Source: "remember"`), returns the suggestion (also when it was
   already pending).

`acceptSuggestion`: the id must be pending (`ErrNoSuchSuggestion`). `playbook`/`addition` empty →
the proposal's. A name failing `ValidPlaybookName` → `ErrBadInput`. Existing playbook (in
`s.Playbooks`) → `s.SavePlaybook(wsID, name, strings.TrimRight(body, "\n") + "\n\n" + addition + "\n")`
with `body` from `s.Playbook` read at accept time; new → `s.AddPlaybook(wsID, name,
"# " + headingFor(name) + "\n\n" + addition + "\n")`, where `headingFor` mirrors the frontend's
(`20-orders.md` → `Orders`). Append `accepted` with `Playbook` and `Edited` (target or text
changed). `dismissSuggestion` appends `dismissed` (pending check as above).

**Tests.**
- store: `TestSuggestionID` (stable, `s-` + 12 hex), `TestAppendSuggestionDedupe` (same proposal
  twice → one line; after `dismissed`, proposing again appends), `TestPendingSuggestions`.
- run: `TestRCAAppendsSuggestions`: an RCA whose document has two `playbookSuggestions` → two
  `proposed` lines with `source: rca`; `playbook-suggestions.md` still written.
  `TestEvalRCAAppendsNoSuggestions`: same with `Eval: true` → no file.
- app: `TestRememberDrafts`: a run with a `steer` event at index 3 and an assistant reply before
  it; `rememberJudge` stub returns `{"playbook":"20-orders.md","addition":"- Orders: check the tax rounding before the PSP.","reason":"repeated"}`
  → pending suggestion with those values, `source remember`. `TestRememberFallback`: stub errors
  → `90-corrections.md` and `- 2026-10-06 OMNI-1: <text>`. `TestRememberBadPlaybook`: stub returns
  `playbook: "../x.md"` → fallback. `TestRememberInstruction` (index 0). `TestRememberRefusesShadow`.
  `TestAcceptAppendsToExisting`: existing `20-orders.md` body `# Orders\n\nLede.\n`, edited on disk
  after the proposal → the saved body is the edited text, a blank line, the addition; `accepted`
  line `edited: false`. `TestAcceptCreatesNew`: `95-new-thing.md` → body `# New thing\n\n<addition>\n`.
  `TestAcceptInvalidName` → `ErrBadInput`. `TestAcceptUnknownID` → `ErrNoSuchSuggestion`.
  `TestDismiss`.

Commit: `app: operator corrections and RCA suggestions queue as playbook additions`

---

### Task H10: shadow and suggestion routes, the bridge, and the shadow and suggestions commands

Touches: `internal/app/shadowapi.go` (new), `internal/httpapi/server.go`,
`internal/httpapi/service.go`, `internal/httpapi/shadow.go` (new), `internal/httpapi/fake_test.go`,
`internal/httpapi/shadow_test.go` (new), `desktop/bridge.go`, `desktop/bridge_test.go`,
`cmd/sirdar/cmd_shadow.go` (new), `cmd/sirdar/cmd_suggestions.go` (new),
`cmd/sirdar/cmd_serve.go`, `cmd/sirdar/main.go`, `cmd/sirdar/cmd_shadow_test.go` (new),
`docs/config.md` (CLI paragraph in the Shadow mode section)

Depends on: H8, H9

**Exported service methods (`internal/app/shadowapi.go`)**, thin wrappers over H7/H8/H9:

```go
type ShadowOutcomeInput struct {
	Key        string   `json:"key"`
	PRURLs     []string `json:"prUrls"`
	Resolution string   `json:"resolution"`
}
type AcceptSuggestionInput struct {
	Playbook string `json:"playbook,omitempty"`
	Addition string `json:"addition,omitempty"`
}
func (s *Service) ShadowStatus(wsID string) (ShadowStatus, error)
func (s *Service) ShadowReport(wsID, since string) (ShadowReport, error) // "" = 30 days; "7d"; "2026-10-01"
func (s *Service) ShadowPoll(ctx context.Context, wsID string) (ShadowStatus, error)
func (s *Service) ShadowPause(wsID, forDuration string) (ShadowStatus, error) // "" = 24h; config.ParseDuration
func (s *Service) ShadowResume(wsID string) (ShadowStatus, error)
func (s *Service) ShadowOutcome(ctx context.Context, wsID string, in ShadowOutcomeInput) (ShadowRow, error)
func (s *Service) ShadowRegrade(ctx context.Context, wsID string, keys []string) ([]ShadowRow, error)
func (s *Service) ShadowRun(ctx context.Context, wsID string, keys []string, backfill bool) ([]ShadowStarted, error)
func (s *Service) Remember(ctx context.Context, wsID, runID string, index int) (Suggestion, error)
func (s *Service) Suggestions(wsID string) ([]Suggestion, error)
func (s *Service) AcceptSuggestion(wsID, id string, in AcceptSuggestionInput) (Suggestion, error)
func (s *Service) DismissSuggestion(wsID, id string) error
```

Every method checks ids with `checkID`. A bad `since` or `for` → `ErrBadInput`.

**HTTP.** Add to the `Service` interface (`internal/httpapi/service.go` line 15) every method
above except `ShadowRun`, and to `classify` (line 116): `app.ErrShadowLocked` → 403 `forbidden`;
`app.ErrBadInput` → 400 `bad_request`; `app.ErrNoSuchSuggestion` → 404 `not_found`. Register in
`server.go` after the playbook routes (line 128), handlers in `internal/httpapi/shadow.go`:

| Route | Handler body | Answers |
|---|---|---|
| `GET /api/workspaces/{id}/shadow/status` | — | `ShadowStatus` |
| `GET /api/workspaces/{id}/shadow/report?since=` | — | `ShadowReport` |
| `POST /api/workspaces/{id}/shadow/poll` | empty | `ShadowStatus` |
| `POST /api/workspaces/{id}/shadow/pause` | `{"for": "4h"}` optional | `ShadowStatus` |
| `POST /api/workspaces/{id}/shadow/resume` | empty | `ShadowStatus` |
| `POST /api/workspaces/{id}/shadow/outcomes` | `{key, prUrls, resolution}` | `ShadowRow` |
| `POST /api/workspaces/{id}/shadow/regrade` | `{key}` (required) | `ShadowRow` (first of the slice, 404 `not_found` when the key has no row) |
| `POST /api/workspaces/{id}/runs/{runId}/remember` | `{index}` | `Suggestion` |
| `GET /api/workspaces/{id}/suggestions` | — | `Suggestion[]` (never null) |
| `POST /api/workspaces/{id}/suggestions/{sid}/accept` | `{playbook?, addition?}` | `Suggestion` |
| `POST /api/workspaces/{id}/suggestions/{sid}/dismiss` | empty | 204 |

Bodies decode with the existing `decode` (unknown fields are a 400). The guard already covers
every non-GET route; add a test that proves it for one shadow route.

**Bridge (`desktop/bridge.go`).** Methods, each a pass-through with `callTimeout` contexts where
a context is needed: `ShadowStatus(ws string)`, `ShadowReport(ws, since string)`,
`ShadowPoll(ws string)`, `ShadowPause(ws, forDuration string)`, `ShadowResume(ws string)`,
`ShadowOutcome(ws string, in app.ShadowOutcomeInput)`, `ShadowRegrade(ws, key string)
(app.ShadowRow, error)`, `Remember(ws, runId string, index int)`, `Suggestions(ws string)` (nil →
empty slice), `AcceptSuggestion(ws, id string, in app.AcceptSuggestionInput)`,
`DismissSuggestion(ws, id string) error`. `desktop/bridge_test.go`: add all eleven to
`bridgeMethods`, and `"ShadowRun": "the CLI's shadow run; the UI never starts a shadow run by hand"`
to `notBridged`.

**Serve (`cmd/sirdar/cmd_serve.go` line 90).** `app.Options{…, Shadow: true}`. When the workspace
has `shadow.enabled`, print `sirdar serve: shadow mode is on; this process polls the tracker every <interval>`.

**CLI (`cmd/sirdar/cmd_shadow.go`, `cmd_suggestions.go`)**, registered in `commands` with
`init()` like `serve`, and listed in `usageText` (`main.go` line 53):
`  shadow      watch, run, grade and report shadow triages (rung 1)` and
`  suggestions list, accept or dismiss pending playbook additions`.
Both resolve the workspace like `serve` (`serveRoot`, `DefaultRegistryPath`, `ensureRegistered`)
and build `app.New(reg, app.BuildDeps, app.Options{Stderr: app.Synced(stderr)})`.

| Command | Behaviour | Exit |
|---|---|---|
| `sirdar shadow watch [--once]` | `Options.Shadow = true`; without `--once`, `Start` and block until interrupted, printing each `shadow.status` change as `shadow: <state>: <reason>`; with `--once`, `ShadowPoll`, wait for every job it started (subscribe, `job.finished`), print the status | 0; 1 on `ErrShadowLocked` with `shadow: watched by pid N (by)` |
| `sirdar shadow run KEY... [--backfill]` | `ShadowRun`, wait for the started jobs, print `KEY RUN_ID STATUS` or `KEY skipped: <reason>` | 1 when nothing started |
| `sirdar shadow status` | the status as `key: value` lines (`state`, `reason`, `today: 3 of 10 runs, $2.10 of $10.00`, `next poll`, `in flight`) | 0 |
| `sirdar shadow report [--since 7d\|2026-10-01] [--json]` | headline block (`Graded 4 of 5 started`, `Confirmed 25% (50% confirmed or partial)`, `Median time 4m10s · ready before you started: 1 of 2`, `Cost $3.20 · median $0.80 per ticket`) then a tabwriter table `KEY GRADE CLASSIFICATION COST HYPOTHESIS` (tags in brackets after the grade); `--json` prints the `ShadowReport` | 0 |
| `sirdar shadow outcome KEY [--pr URL]... [--resolution TEXT\|@file]` | `ShadowOutcome`; `@file` reads the file; prints the key's row | 1 on bad input |
| `sirdar shadow grade [KEY...]` | `ShadowRegrade`; prints `KEY GRADE BASIS` per row | 0 |
| `sirdar shadow pause [--for 4h]` / `sirdar shadow resume` | prints the status | 0 |
| `sirdar suggestions [list]` | `ID PLAYBOOK SOURCE` then the addition indented | 0 |
| `sirdar suggestions accept ID [--playbook NAME]` | `AcceptSuggestion` | 1 on unknown id |
| `sirdar suggestions dismiss ID` | `DismissSuggestion` | 1 on unknown id |

`--pr` is repeatable (a `stringsFlag` type). Usage errors return `exitUsage`.

**Tests.**
- httpapi (`shadow_test.go`, extend `fake` in `fake_test.go` with recorded calls): each route
  reaches its method with the decoded arguments; `ShadowPoll` returning `app.ErrShadowLocked` →
  403; `ShadowOutcome` with `ErrBadInput` → 400; `DismissSuggestion` unknown → 404; a POST to
  `/shadow/pause` with `Origin: https://evil.example` → 403 from the guard; `GET /suggestions` with
  none → `[]`.
- bridge: the existing surface tests pass with the new lists.
- cmd (`cmd_shadow_test.go`, the temp-workspace helpers `cli_test.go` uses): `shadow report --json`
  over a fixture ledger prints JSON whose `headline.graded` is 2; `shadow report` prints `Graded 2
  of 3 started`; `shadow outcome OMNI-1 --pr notaurl` exits 1 and names the URL;
  `shadow pause --for 4h` then `shadow status` prints `state: paused`; `suggestions` lists a
  fixture proposal; `suggestions dismiss s-000000000000` exits 1; `run([]string{"help"})` usage
  lists `shadow` and `suggestions`.

Commit: `serve: shadow and suggestion routes, the bridge, and sirdar shadow and suggestions`

---

### Task H11: a Shadow screen with the scorecard, rows and a manual outcome form

Touches: `desktop/frontend/src/api/types.ts`, `desktop/frontend/src/api/transport.ts`,
`desktop/frontend/src/api/parity.test.ts` (only if a count assertion needs it),
`desktop/frontend/src/store/fakeTransport.ts`, `desktop/frontend/src/store/appStore.ts`,
`desktop/frontend/src/store/appStore.test.ts`, `desktop/frontend/src/lib/routes.ts`,
`desktop/frontend/src/lib/routes.test.ts`, `desktop/frontend/src/components/shell/Sidebar.tsx`,
`desktop/frontend/src/components/shell/Sidebar.test.tsx`,
`desktop/frontend/src/components/shell/icons.tsx`, `desktop/frontend/src/App.tsx`,
`desktop/frontend/src/screens/Shadow.tsx` (new), `desktop/frontend/src/screens/Shadow.test.tsx`
(new), `desktop/frontend/src/screens/shadow.css` (new)

Depends on: H10

**Contract (`api/types.ts`).** `RunSummary` gains `shadow?: boolean`. Add `ShadowStatus`,
`ShadowToday`, `ShadowReport`, `ShadowHeadline`, `ShadowRow`, `ShadowRowOutcome`,
`ShadowClassRow`, `ShadowFraction`, `ShadowDenied`, `ShadowOutcomeInput`, `PlaybookSuggestion`
(the Go `app.Suggestion`) and `AcceptSuggestionInput`, field for field with the Go JSON names in
H4, H7, H9 and H10. `AppEvent` gains
`| { kind: 'shadow.status'; workspaceId: string; shadow: ShadowStatus }`. `Transport` and
`TRANSPORT_METHODS` gain: `shadowStatus(ws)`, `shadowReport(ws, since?)`, `shadowPoll(ws)`,
`shadowPause(ws, forDuration?)`, `shadowResume(ws)`, `shadowOutcome(ws, input)`,
`shadowRegrade(ws, key)`, `remember(ws, runId, index)`, `suggestions(ws)`,
`acceptSuggestion(ws, id, input)`, `dismissSuggestion(ws, id)`.

**Transports (`api/transport.ts`).** HTTP implementations over the H10 routes; Wails
implementations over the H10 bridge names, each declared in `interface BridgeBindings` (line 445)
with its Go signature. `suggestions` maps a null to `[]`. **Fake (`store/fakeTransport.ts`)**:
in-memory status, report and suggestions, recording calls.

**Store (`store/appStore.ts`).** `run.updated` (line 521): a run with `shadow: true` is not
upserted into `runsByWorkspace` (it stays out of Sessions and the Board); the initial `runs` load
filters `shadow` too. `shadow.status` events are kept per workspace (`shadowByWorkspace`), and the
store loads `shadowStatus` when a workspace is selected.

**Route and sidebar.** `Screen` gains `{ name: 'shadow' }`; `lib/routes.ts` parses `#/shadow` and
`routeHash` returns it. `Sidebar.tsx`: `NavName` gains `'shadow'`; `ROWS` (line 38) puts
`{ name: 'shadow', label: 'Shadow', icon: <ShadowIcon /> }` between Register and Eval; the row is
shown only when the current workspace's status has `enabled` or `hasLedger` (same filtering
pattern as `library` at line 231). `App.tsx` renders `<Shadow …/>` for the screen.

**Screen (`screens/Shadow.tsx`).** Props: `transport`, `workspaceId`, `status?: ShadowStatus`,
`onOpenRun(runId)`. Loads `shadowReport(ws)` on mount and after each action.
- Page head (`ui/page-head`): title `Shadow`; status line from `statusLine(status, now)`:
  `Watching · next poll 10:45 · 3 of 10 today · $2.10 of $10`; `Paused until 14:00 · …`;
  `Gated: <reason> · …`; `Watched by another process · <reason>`; `Not watching · <reason>`;
  `Off`. Primary action: `Pause` (calls `shadowPause`) when state is `watching` or `gated`;
  `Resume` (calls `shadowResume`) when `paused`; none otherwise.
- Four `StatCard`s: `Graded` value `4`, detail `of 5 started`; `Confirmed` `25%`, detail `50%
  confirmed or partial`; `Median time` `4 min`, detail `Ready before you started: 1 of 2`; `Cost`
  `$3.20`, detail `$0.80 median per ticket`. With `graded == 0`, Confirmed shows `—`.
- `DataTable` caption `Shadow runs`, columns: Key (a button whose text is the key in
  `<bdi dir="ltr">`, calling `onOpenRun(row.runId)`), Title (`dir="auto"`), Shadow said
  (`classification · hypothesis`, `dir="auto"`), Grade (`ui/badge`, `title` = `gradeReason`; empty
  grade reads `Pending`; tags as small chips after it), Cost (`$0.84`, numeric). `empty`:
  `No shadow runs yet. New tickets assigned to you are triaged here once shadow mode is on.`
- Row drawer (`ui/drawer`, title the key): outcome (sources, PR links, resolution), file overlap
  (`2 of 3 PR files named · 1 of 2 code refs in the PR`), reproduction word, denied calls
  (`Bash psql -c …`), questions, and the manual outcome form: PR URLs (one per line textarea,
  label `Pull requests`), `Resolution` textarea, `Save` → `shadowOutcome(ws, {key, prUrls,
  resolution})`; on success the drawer shows the returned row's grade; on error the message
  inline.
- `shadow.css` with tokens and logical properties only.

**Tests.**
- `Shadow.test.tsx`: renders the four stat cards and two rows from a fake report; the status line
  reads `Watching · next poll 10:45 · 3 of 10 today · $2.10 of $10` for a fixture status at a fixed
  clock; Pause calls `shadowPause` and Resume shows for `paused`; clicking a key calls
  `onOpenRun('r1')`; opening the drawer and submitting `https://github.com/o/r/pull/9` and `Fixed
  null tenant` calls `shadowOutcome` with `{key: 'OMNI-1', prUrls: ['https://github.com/o/r/pull/9'],
  resolution: 'Fixed null tenant'}`; an Arabic title renders with `dir="auto"`.
- `Sidebar.test.tsx`: no Shadow row when status is `{enabled: false, hasLedger: false}`; present
  between Register and Eval when `enabled`.
- `appStore.test.ts`: a `run.updated` with `shadow: true` does not appear in `runsByWorkspace`; a
  `shadow.status` event updates `shadowByWorkspace`.
- `routes.test.ts`: `#/shadow` round-trips.
- `parity.test.ts` passes (both transports implement the eleven methods).

Commit: `ui: a Shadow screen with the scorecard, rows and a manual outcome form`

---

### Task H12: shadow runs open read-only, and Remember this feeds Settings suggestions

Touches: `desktop/frontend/src/screens/session/SessionConversation.tsx`,
`desktop/frontend/src/screens/session/SessionConversation.test.tsx`,
`desktop/frontend/src/screens/session/session-conversation.css`,
`desktop/frontend/src/screens/Session.tsx` (only if the banner sits there),
`desktop/frontend/src/screens/settings/PlaybooksPage.tsx`,
`desktop/frontend/src/screens/settings/PlaybooksPage.test.tsx`,
`desktop/frontend/src/screens/settings/shared.tsx`, `desktop/frontend/src/screens/Settings.tsx`,
`desktop/frontend/src/screens/settings/settings.css`

Depends on: H11

**Read-only shadow session.** When `detail.shadow`:
- A `ui/banner` above the transcript reads `Shadow run. Nothing was filed, sent or registered`.
- The composer `mode` (`SessionConversation.tsx` line 466) returns
  `{ kind: 'disabled', reason: 'Shadow runs are measurements. Start a session to continue this work.' }`
  before every other branch.
- No Update note / Save as note / Fix actions are offered (they hide when the composer is
  disabled for this reason).

**Remember this.** On every `you` bubble (`SessionConversation.tsx` line 545) whose underlying
event is `steer` or `answer`, on a run that is not `shadow` (eval runs never reach this screen's
composer either), a hover/focus action button `Remember this` (visible on `:hover` and
`:focus-within`, keyboard reachable). It calls `transport.remember(ws, runId, index)` where
`index` is the 1-based event index `Service.Events` counts. Check how `item.index` is numbered in
`screens/session/model.ts` (it comes from the feed's `IndexedEvent`); if it is 0-based, pass
`item.index + 1`, and pin that with a test. The model item gains `event: 'steer' | 'answer' |
'grant'` so the screen can tell them apart; `grant` bubbles get no action. On success, toast
(`useAppStore().toast`) `Suggested an addition to <playbook>. Review it in Settings › Playbooks.`;
on failure, toast the error with tone `error`. While the call is in flight the button is
disabled and reads `Remembering…`.

**Pending suggestions card (`PlaybooksPage.tsx`).** The `Playbooks` hook in `shared.tsx` gains
`suggestions: Loaded<PlaybookSuggestion[]>` loaded with the playbook list, plus `accept(id,
input)` and `dismiss(id)` that call the transport and reload both lists. Above the existing
`Files in .sirdar/playbooks` card (line 505), a `SettingCard` `Pending suggestions`, shown only
when there is at least one. Each row: target playbook name, the addition clamped to three lines
with a `Show all` toggle, and the source line `Remembered on OMNI-3413` (source `remember`) or
`RCA of OMNI-3217` (source `rca`), keys in `<bdi dir="ltr">`. Actions: `Accept` opens an inline
editor with a target `<select>` (every existing playbook name plus the suggestion's own target
when it is new) and an editable textarea holding the addition, then `Save` →
`acceptSuggestion(ws, id, {playbook, addition})` (only fields that changed are sent); `Dismiss`
→ `dismissSuggestion`.

**Tests.**
- `SessionConversation.test.tsx`: a shadow run's detail shows the banner text and a disabled
  composer whose title is the shadow reason; a non-shadow run with a `steer` event shows
  `Remember this` on its bubble, clicking calls `remember(ws, runId, <expected index>)` and toasts
  `Suggested an addition to 20-orders.md. Review it in Settings › Playbooks.`; a shadow run shows
  no `Remember this`.
- `PlaybooksPage.test.tsx`: no card with zero suggestions; with one `remember` suggestion the card
  shows `Remembered on OMNI-3413` and three-line clamp with `Show all`; Accept → change the
  target to `30-logs.md` and edit the text → `acceptSuggestion` called with
  `{playbook: '30-logs.md', addition: '<edited>'}`; Dismiss → `dismissSuggestion` called and the
  row disappears after reload.

Commit: `ui: shadow runs open read-only, and Remember this feeds Settings suggestions`

---

### Task H-live: the 3-ticket backfill smoke in OXO.APIs

Touches: nothing in the Sirdar repo unless a defect is found (then a fix task is opened, not
patched here). The controller, not a subagent, edits
`/Users/srivathsanv/Documents/Work/Coding/GitHub/OXO.APIs/.sirdar/config.yaml`.

Depends on: H1-H12, and the investigation skeleton landed on `main`

**Build.** From the Sirdar `main` worktree: `go build -o /tmp/sirdar-shadow ./cmd/sirdar`. Run
every command below from `/Users/srivathsanv/Documents/Work/Coding/GitHub/OXO.APIs`.

**Operator-side config (controller adds to `OXO.APIs/.sirdar/config.yaml`).**

```yaml
shadow:
  enabled: false        # the smoke uses `shadow run --backfill`; the watcher stays off
  assignee: me
  types: [bug]
  since: armed
  maxPerDay: 3
  maxUSDPerDay: 5
  concurrency: 1
  model: ""
  pauseAtQuota: 0.85
  yieldToOperator: true
  reproduce: true
  outcomes:
    interval: 6h
    windowDays: 30
    tracker: true
  judge:
    model: ""
  digest:
    enabled: false
```

`.sirdar/` is already in `.git/info/exclude`, so the ledger and suggestions file are ignored. The
workspace has no `me:` block and the Janus `exec` adapter does not resolve `me`, so `me` resolves
through the git identity (`SelfOf`); doctor must show it. `notify` is not configured in this
workspace, which keeps Slack quiet independently of the shadow guarantees.

**Steps.**
1. `/tmp/sirdar-shadow doctor`: the `shadow` row is not `fail`; it names the resolved assignee,
   lists N open bug tickets, and says `tracker.outcome unsupported; outcomes come from your runs
   and manual entries`. No `not git-ignored` warning.
2. Snapshot before:
   `find "$HOME/Documents/Work/Obsidian/Support Duty/Sirdar" -type f -print0 | sort -z | xargs -0 shasum > /tmp/vault-before.txt`
   and `shasum .sirdar/register.jsonl > /tmp/register-before.txt`.
3. Quota check: `/tmp/sirdar-shadow shadow status` and the quota meter. The operator's Claude
   seven-day window may already be above 85%. The smoke goes through the gate: if
   `shadow run` reports `skipped: seven-day quota at …% (pause at 85%), resets …`, that refusal
   is the gate working. Record it, stop, and ask the owner whether to wait for the reset or to
   raise `pauseAtQuota` for the smoke. Do not change `pauseAtQuota` without the owner's answer.
4. Pick 3 tickets resolved in the last two weeks whose PR and resolution the operator knows
   (from `sirdar register` RCA rows, or the Janus recently-closed list). Run
   `/tmp/sirdar-shadow shadow run --backfill KEY1 KEY2 KEY3`. With `maxPerDay: 3` all three fit
   the count cap; `maxUSDPerDay: 5` caps each run at what remains (`budget.maxUsd` is 8), so a
   third run may stop `over_budget`, which is a valid finding.
5. For each key: `/tmp/sirdar-shadow shadow outcome KEY --pr <url> --resolution "<one line>"`.
   Then `/tmp/sirdar-shadow shadow grade` and `/tmp/sirdar-shadow shadow report` (and `--json`).
6. Snapshot after with the same two commands into `/tmp/vault-after.txt` and
   `/tmp/register-after.txt`; `diff` both pairs: they must be identical.
7. Check each shadow run directory (`.sirdar/runs/<KEY>/<runId>/`): `state.json` has
   `"Shadow": true`, `note.md` and `result.json` exist (or a `NoteWarning`), `events.jsonl` has no
   `ask` permission events, and `bundle/` manifest shows the backfill cutoff.
8. Open the Shadow screen (`/tmp/sirdar-shadow serve --open`, Brave): the three rows, the four
   stat cards, a key opening the read-only session with the banner and disabled composer.
   `serve` now runs the watcher with `enabled: false`, so the status reads `Off`.
9. Report: per key the grade, basis, cost and elapsed time; the headline (backfill rows are
   excluded from it by design, so expect `0 of 0` headline and three tagged `backfill` rows); the
   diff results; anything the gate refused.

The 5-day forward pass, the induced crash and `docs/research/NN-shadow-dogfood.md` (spec "Live
pass" steps 3-5) follow later with `enabled: true` and are not part of this smoke.

---

## Resolved ambiguities

These are decisions this plan made where the spec was silent or two readings were possible.

1. **`includeShadow` on `Service.Runs`.** No screen needs a list of shadow runs: report rows carry
   `runId`, and the Session screen loads a run with `Run(ws, runId)`. So `Runs` keeps its
   signature and hides shadow runs; `includeShadow` is the unexported `runs(wsID, key, true)`.
2. **Shadow-only exclusions.** `busyReason`, Queue's `LatestRun` and `Runs` skip `State.Shadow`,
   not `Measurement()`, so eval runs behave as today.
3. **A resumed shadow run never notifies.** `Runner.Resume` did not carry `noNotify`; it now sets
   it from `State.Shadow`. Without this an auto-answer or retry resume would post a run
   notification.
4. **Where the shadow notice sits.** In the reply prompt the operator's request is part of the
   final task section, so the notice goes immediately before it; in the note-only `Triage` prompt
   it goes where the Operator's request section goes.
5. **`shadow.reproduce: false`.** Implemented as `runner.Options.NoReproduce`, which leaves the
   skeleton's `TriageInput.Repro` nil and records a warning.
6. **Quota and rate-limit gating.** `app.Quota` has no "rate limited" marker, so a provider rate
   limit gates through the ledger's `paused` line (spec §13), and the quota gate compares window
   utilization (or Codex `usedPercent/100`) with `pauseAtQuota`. A quota stop shows state
   `paused` with the spec's reason text; caps, yield and rejected credentials show `gated`.
7. **Retry of a run with a handle** reuses its run id (a `started` line with `attempt: 2`,
   `resume: true`); without a handle it starts a fresh run id.
8. **Grade basis.** Tokens `pr` and `resolution` joined with `+`; else `operator-rca` or `manual`;
   else `operator-triage` (provisional). A tracker status alone does not grade.
9. **Headline denominators.** `no-answer` counts as a final grade (a miss); `ungradable` does not.
10. **Suggestion id.** The spec's text (first 6 bytes of sha256, hex) wins over its shortened
    example: `s-` plus 12 hex characters.
11. **Suggestions file location in code.** The JSONL format lives in `internal/store` because
    `internal/run` (RCA proposals) cannot import `internal/app`.
12. **Remember on the run's Instruction.** `index 0` means the Instruction. The conversation
    screen does not render the Instruction as a bubble today, so the UI action appears on
    `steer`/`answer` bubbles only; the API and CLI accept index 0.
13. **Toast link.** The app's toast carries text only, so the Remember toast names the playbook
    and says `Review it in Settings › Playbooks.` rather than linking.
14. **Digest repeat after restart.** The digest appends a `{ev: digest, day}` ledger line so a
    restart after `digest.at` does not send twice. Slack incoming webhooks are not signed, so the
    "HMAC path" applies only in the sense of the shared notify transport; the host check
    (`hooks.slack.com`) is done after resolving the credential, at send time and in doctor.
15. **Config durations** accept Go syntax plus a whole-day `d` suffix, so `outcomes.interval: 7d`
    and `--since 7d` both parse.
16. **PR titles and bodies for the judge.** `ghView` now also asks `gh` for `title,body`;
    `eval.PullRequestFiles` returns files, texts and the diff in one `PullRequestSet`.
17. **Service methods across tasks.** H7, H8 and H9 add unexported methods only; H10 adds every
    exported wrapper, route and bridge entry in one commit, so `desktop/bridge_test.go` is edited
    once. `ShadowRun` is CLI-only and listed in `notBridged`.
18. **New error sentinels.** `app.ErrBadInput` (400), `app.ErrShadowLocked` (403),
    `app.ErrNoSuchSuggestion` (404).
