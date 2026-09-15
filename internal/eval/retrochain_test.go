package eval

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/config"
	"github.com/srivathsanvenkateswaran/sirdar/internal/provider"
	"github.com/srivathsanvenkateswaran/sirdar/internal/store"
	"github.com/srivathsanvenkateswaran/sirdar/internal/ticket"
)

// The chain this file exercises is the whole of `sirdar eval --retro`
// against a real repository: a bug committed at C1, the human's fix at C2,
// and a golden entry that says so. The retro puts the agent back at C1 and
// measures what it does against C1..C2.
//
// Everything below is real except the model. The git repository, the
// worktrees, the run directories, the note rendering, the commit and the
// diff are all the production code paths; only the provider is scripted, so
// what the test asserts is the wiring rather than a simulation of it.

// --- the repository ---------------------------------------------------

const (
	// buggySource is the code at C1: every row is buffered before
	// anything is written, which is the bug the ticket is about.
	buggySource = `package export

func WriteCSV(rows []string) string {
	all := ""
	for _, r := range rows {
		all += r + "\n"
	}
	return all
}
`
	// fixedSource is the code at C2, which is what a human merged.
	fixedSource = `package export

import "strings"

func WriteCSV(rows []string) string {
	var b strings.Builder
	for _, r := range rows {
		b.WriteString(r + "\n")
	}
	return b.String()
}
`
	// agentSource is what the scripted fix session writes. It is not the
	// human's change — a retro that only ever scored an identical diff
	// would prove nothing about the overlap measures — but it is the same
	// file, which is what the file-level scores are for.
	agentSource = `package export

import "bytes"

func WriteCSV(rows []string) string {
	var b bytes.Buffer
	for _, r := range rows {
		b.WriteString(r + "\n")
	}
	return b.String()
}
`
)

// chainTriageDoc is what the scripted triage session answers. Its code
// reference points at the file the merged pull request in fact changed,
// which is what makes the triage columns non-zero.
const chainTriageDoc = `{
  "ticket": {"key":"OMNI-1","title":"Export fails","trackerUrl":"https://t/OMNI-1","helpdeskId":"555","helpdeskUrl":"https://h/555","priority":"high","service":"omni","customer":"Acme","customerId":"4561"},
  "title": "Export fails for large orders",
  "complaint": "The export fails for large orders.",
  "timeline": [{"at":"2026-09-10T08:30:00+03:00","role":"customer","summary":"Reported the export failing."}],
  "reproSteps": ["Request a CSV export for a 600-line order."],
  "rootCause": {"hypothesis":"WriteCSV concatenates every row before returning.","confidence":"high","evidence":[{"source":"code","query":"WriteCSV","finding":"The whole file is built in one string."}],"codeRefs":["export/csv.go:4"]},
  "blastRadius": "Any order above 500 line items.",
  "classification": "code",
  "proposedFix": {"description":"Build the output incrementally.","files":["export/csv.go"],"remediationSql":"","risks":"none"},
  "openQuestions": []
}`

// chainFixReport is what the scripted fix session answers about the edit it
// made. Its testsRun entry is what BUILD reads.
const chainFixReport = `{
  "summary": "Build the CSV incrementally instead of concatenating every row",
  "filesChanged": ["export/csv.go"],
  "testsRun": [{"command":"go build ./...","result":"ok"}],
  "risks": "none",
  "deviationFromNote": ""
}`

const retroConfigYAML = `workspace: test
provider: claude
model: test-model
billing: subscription
notes:
  dir: NOTES
budget:
  maxTurns: 60
  maxMinutes: 25
  maxUsd: 5
concurrency: 1
playbooks: .sirdar/playbooks
`

// retroRepo is the workspace a retro chain runs against: a git repository
// with the bug at C1 and the fix at C2, a bare origin nothing should ever
// reach, and a notes directory outside the tree the way a vault is.
type retroRepo struct {
	cfg    *config.Config
	root   string
	origin string
	notes  string
	c1     string
	c2     string
}

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func newRetroRepo(t *testing.T) *retroRepo {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}
	root := t.TempDir()
	notes := t.TempDir()
	gitIn(t, root, "init", "-b", "main", ".")
	// The commits are throwaway, but they still need an identity to be
	// made at all. The machine's own is used as it stands; a checkout with
	// none skips rather than inventing one.
	if err := exec.Command("git", "-C", root, "var", "GIT_AUTHOR_IDENT").Run(); err != nil {
		t.Skip("git has no author identity configured in this environment")
	}
	writeFile(t, filepath.Join(root, ".git", "info", "exclude"),
		".sirdar/runs/\n.sirdar/register.jsonl\n.sirdar/eval/\n.sirdar/worktrees/\n")

	writeFile(t, filepath.Join(root, ".sirdar", "config.yaml"),
		strings.Replace(retroConfigYAML, "NOTES", notes, 1))
	if err := os.MkdirAll(filepath.Join(root, ".sirdar", "playbooks"), 0o755); err != nil {
		t.Fatal(err)
	}

	writeFile(t, filepath.Join(root, "export", "csv.go"), buggySource)
	gitIn(t, root, "add", "-A")
	gitIn(t, root, "commit", "-q", "-m", "the export as it was when the ticket was filed")
	c1 := gitIn(t, root, "rev-parse", "HEAD")

	writeFile(t, filepath.Join(root, "export", "csv.go"), fixedSource)
	gitIn(t, root, "add", "-A")
	gitIn(t, root, "commit", "-q", "-m", "stream the export")
	c2 := gitIn(t, root, "rev-parse", "HEAD")

	origin := filepath.Join(t.TempDir(), "origin.git")
	gitIn(t, root, "init", "--bare", "-b", "main", origin)
	gitIn(t, root, "remote", "add", "origin", origin)
	gitIn(t, root, "push", "-q", "-u", "origin", "main")
	gitIn(t, root, "remote", "set-head", "origin", "main")

	cfg, err := config.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	return &retroRepo{cfg: cfg, root: root, origin: origin, notes: notes, c1: c1, c2: c2}
}

// originBranches is every branch the bare origin carries. A retro must
// leave it with exactly the one it started with.
func (r *retroRepo) originBranches(t *testing.T) []string {
	t.Helper()
	out := gitIn(t, r.root, "--git-dir", r.origin, "for-each-ref", "--format=%(refname:short)", "refs/heads/")
	if out == "" {
		return nil
	}
	return strings.Split(out, "\n")
}

// newRetroGoldenEntry writes the golden entry the retro replays: the as-of
// bundle, the ground truth, and the diff of the change a human merged.
func (r *retroRepo) newRetroGoldenEntry(t *testing.T, key string) string {
	t.Helper()
	golden := t.TempDir()
	dir := filepath.Join(golden, key)
	if err := ticket.WriteBundle(filepath.Join(dir, "bundle"), sampleBundle()); err != nil {
		t.Fatal(err)
	}
	prDiff := gitIn(t, r.root, "diff", r.c1, r.c2)
	writeFile(t, filepath.Join(dir, RetroDiffName), prDiff+"\n")

	data, err := json.MarshalIndent(Retro{
		Key:        key,
		AsOf:       time.Date(2026, 9, 10, 9, 0, 0, 0, time.UTC),
		BaseCommit: r.c1,
		PRURLs:     []string{"https://github.com/acme/omni/pull/482"},
		PRDiff:     RetroDiffName,
		PRFiles:    []string{"export/csv.go"},
		Redacted:   Redacted{PRLinks: 1, CommentsDropped: 2},
	}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, RetroFile), string(data)+"\n")
	return golden
}

// --- the scripted provider --------------------------------------------

// chainProvider answers the sessions a retro makes, in the order it makes
// them: the triage, then the fix — which edits the file the way an agent
// would, in whatever directory the session was put in — then the rca. It
// records the cwd and the source each session saw, which is what says the
// run really stood at C1.
type chainProvider struct {
	t *testing.T

	triageDoc string
	fixReport string
	rcaDoc    string

	// sawSource is the content of export/csv.go each session read, in
	// order, and sawCwd the directory each stood in.
	sawSource []string
	sawCwd    []string
	modes     []provider.Mode

	nonFix int
}

func (p *chainProvider) Name() string                                               { return "claude" }
func (p *chainProvider) Doctor(ctx context.Context, binary string) []provider.Check { return nil }

func (p *chainProvider) Start(ctx context.Context, spec provider.SessionSpec) (provider.Session, error) {
	body, _ := os.ReadFile(filepath.Join(spec.Cwd, "export", "csv.go"))
	p.sawSource = append(p.sawSource, string(body))
	p.sawCwd = append(p.sawCwd, spec.Cwd)
	p.modes = append(p.modes, spec.Mode)

	doc := ""
	switch {
	case spec.Mode.IsFix():
		if err := os.WriteFile(filepath.Join(spec.Cwd, "export", "csv.go"), []byte(agentSource), 0o644); err != nil {
			p.t.Fatalf("the scripted fix could not edit the file: %v", err)
		}
		doc = p.fixReport
	default:
		if p.nonFix == 0 {
			doc = p.triageDoc
		} else {
			doc = p.rcaDoc
		}
		p.nonFix++
	}

	s := &stubSession{events: make(chan provider.Event, 4), done: make(chan struct{})}
	go func() {
		s.emit(provider.Event{Kind: provider.EvUsage, Turns: 5, CostUSD: 0.4})
		s.emit(provider.Event{Kind: provider.EvFinal, Final: json.RawMessage(doc)})
		close(s.events)
	}()
	return s, nil
}

// --- the test ---------------------------------------------------------

// TestRetroChainScoresARealTriageAndFixAtTheBaseCommit is the end-to-end
// case: two real sessions at the commit the fix branched from, scored
// against the diff a human merged, leaving the workspace and the remote
// exactly as they were.
func TestRetroChainScoresARealTriageAndFixAtTheBaseCommit(t *testing.T) {
	repo := newRetroRepo(t)
	golden := repo.newRetroGoldenEntry(t, "OMNI-1")
	p := &chainProvider{t: t, triageDoc: chainTriageDoc, fixReport: chainFixReport}

	deps := newDeps(t, repo.cfg, p)
	rep, err := RunRetro(context.Background(), NewRetroDeps(deps, ""), nil,
		RetroOptions{GoldenDir: golden})
	if err != nil {
		t.Fatalf("RunRetro: %v", err)
	}
	if len(rep.Results) != 1 {
		t.Fatalf("results: %+v", rep.Results)
	}
	r := rep.Results[0]
	// The table is the command's output; printing it makes a failing
	// assertion below readable without a second run.
	t.Logf("retro table:\n%s", rep.Table())
	if r.Reason != "" {
		t.Fatalf("the chain reported a reason: %s", r.Reason)
	}

	// Both sessions stood in a worktree of their own at C1, reading the
	// code as it was before the fix. The main tree is at C2, so a chain
	// that ignored --at would have read fixedSource here.
	if len(p.sawSource) != 2 {
		t.Fatalf("sessions started: %d, want the triage and the fix", len(p.sawSource))
	}
	for i, body := range p.sawSource {
		if body != buggySource {
			t.Errorf("session %d read the code as\n%s\nwant the code at %s", i, body, short(repo.c1))
		}
		if p.sawCwd[i] == repo.root {
			t.Errorf("session %d ran in the operator's own tree", i)
		}
	}
	if p.modes[0].IsFix() || !p.modes[1].IsFix() {
		t.Errorf("session modes %v, want a read-only triage then a fix", p.modes)
	}

	// The triage stage: a completed run whose note is scored against the
	// files the pull request touched.
	if r.Triage == nil || !r.Triage.OK() {
		t.Fatalf("triage stage: %+v", r.Triage)
	}
	if r.Triage.RunID == "" || r.Triage.Turns != 5 {
		t.Errorf("triage stage did not carry the run's own numbers: %+v", r.Triage)
	}
	if r.TriageScore == nil {
		t.Fatal("the triage was not scored")
	}
	if r.TriageScore.CodeRefsPathOverlap.Score == 0 {
		t.Errorf("the note pointed at the file the change touched and scored 0: %+v", r.TriageScore)
	}
	if r.TriageScore.PRFilesHit.Score == 0 {
		t.Errorf("the note names export/csv.go and PRFILES scored 0: %+v", r.TriageScore)
	}
	if r.TriageScore.Classification != "code" || r.TriageScore.Confidence != "high" {
		t.Errorf("triage score: %+v", r.TriageScore)
	}

	// The fix stage: a commit in its own worktree, a diff beside it, and a
	// file overlap with the merged change.
	if r.Fix == nil || !r.Fix.OK() {
		t.Fatalf("fix stage: %+v", r.Fix)
	}
	if r.Fix.Commit == "" {
		t.Errorf("the fix stage made no commit: %+v", r.Fix)
	}
	if r.Fix.DiffPath == "" {
		t.Fatalf("the fix stage wrote no diff: %+v", r.Fix)
	}
	diff, err := os.ReadFile(r.Fix.DiffPath)
	if err != nil {
		t.Fatalf("read the agent's diff: %v", err)
	}
	if !strings.Contains(string(diff), "export/csv.go") {
		t.Errorf("the agent's diff does not touch the file:\n%s", diff)
	}
	if r.FixScore == nil {
		t.Fatal("the fix was not scored")
	}
	if r.FixScore.FilesJaccard.Score == 0 {
		t.Errorf("both diffs touch export/csv.go and FILES scored 0: %+v", r.FixScore)
	}
	if r.FixScore.LinesAdded.Agent == 0 || r.FixScore.LinesAdded.PR == 0 {
		t.Errorf("line counts: %+v", r.FixScore)
	}
	if r.FixScore.BuildPassed == nil || !*r.FixScore.BuildPassed {
		t.Errorf("the fix reported a passing build and BUILD reads %v", r.FixScore.BuildPassed)
	}

	// The cost is every session the key spent.
	if r.CostUSD < 0.79 {
		t.Errorf("cost %.2f, want both sessions", r.CostUSD)
	}

	// --- what a retro must not leave behind ---------------------------

	// No note in the notes directory: the vault holds what a human wrote
	// about this ticket, and a measurement must not overwrite it.
	entries, err := os.ReadDir(repo.notes)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("a retro filed notes into notes.dir: %v", names)
	}
	// The note the triage did produce is in its run directory, which is
	// what the score read and what the fix implemented.
	if _, err := os.Stat(r.Triage.NotePath); err != nil {
		t.Errorf("the triage note is not in the run directory: %v", err)
	}

	// No register row: the register is the audit index of tickets that
	// were actually worked.
	if _, err := os.Stat(filepath.Join(repo.root, ".sirdar", "register.jsonl")); !os.IsNotExist(err) {
		body, _ := os.ReadFile(filepath.Join(repo.root, ".sirdar", "register.jsonl"))
		t.Errorf("a retro wrote a register row:\n%s", body)
	}

	// Nothing reached the remote: the origin still carries only the branch
	// it started with, still at C2.
	if got := repo.originBranches(t); len(got) != 1 || got[0] != "main" {
		t.Errorf("the origin's branches are %v, want only main", got)
	}
	if head := gitIn(t, repo.root, "--git-dir", repo.origin, "rev-parse", "refs/heads/main"); head != repo.c2 {
		t.Errorf("origin/main moved to %s, want %s", short(head), short(repo.c2))
	}

	// And the operator's tree is where it was, on main at C2 with the
	// merged code in it.
	if head := gitIn(t, repo.root, "rev-parse", "HEAD"); head != repo.c2 {
		t.Errorf("the main tree's HEAD moved to %s", short(head))
	}
	if branch := gitIn(t, repo.root, "rev-parse", "--abbrev-ref", "HEAD"); branch != "main" {
		t.Errorf("the main tree is on %q", branch)
	}
	body, err := os.ReadFile(filepath.Join(repo.root, "export", "csv.go"))
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != fixedSource {
		t.Errorf("the main tree's working file changed:\n%s", body)
	}

	// Both runs are marked as evals, which is what keeps them out of
	// everything a later real run reads.
	for _, id := range []string{r.Triage.RunID, r.Fix.RunID} {
		_, state, err := store.Open(repo.root, id)
		if err != nil {
			t.Fatalf("open run %s: %v", id, err)
		}
		if !state.Eval {
			t.Errorf("run %s (%s) is not marked as an eval", id, state.Kind)
		}
		if state.At != repo.c1 {
			t.Errorf("run %s stood at %q, want %s", id, state.At, short(repo.c1))
		}
	}
	// Nothing downstream can mistake the replay for this key's triage.
	if path, err := store.LatestNote(repo.root, "OMNI-1", store.KindTriage); err == nil {
		t.Errorf("a retro's triage note is the key's newest: %s", path)
	}

	// The report is written where the Eval screen reads it from.
	path, err := rep.Write(repo.root)
	if err != nil {
		t.Fatalf("write the report: %v", err)
	}
	if !strings.HasSuffix(path, RetroSuffix) {
		t.Errorf("report path %s", path)
	}
}

// TestRetroChainWithRCARunsBlindFromItsOwnTriageNote: the rca stage reviews
// the note the retro's own triage just made. That note is an eval's, so
// store.LatestNote will not return it — the stage has to be handed it, and
// this is what says it is.
func TestRetroChainWithRCARunsBlindFromItsOwnTriageNote(t *testing.T) {
	repo := newRetroRepo(t)
	golden := repo.newRetroGoldenEntry(t, "OMNI-1")
	p := &chainProvider{t: t, triageDoc: chainTriageDoc, fixReport: chainFixReport, rcaDoc: chainRCADoc}

	deps := newDeps(t, repo.cfg, p)
	rep, err := RunRetro(context.Background(), NewRetroDeps(deps, ""), []string{"OMNI-1"},
		RetroOptions{GoldenDir: golden, WithRCA: true})
	if err != nil {
		t.Fatalf("RunRetro: %v", err)
	}
	r := rep.Results[0]
	if r.Reason != "" {
		t.Fatalf("the chain reported a reason: %s", r.Reason)
	}
	if r.RCA == nil || !r.RCA.OK() {
		t.Fatalf("rca stage: %+v", r.RCA)
	}
	if len(p.sawSource) != 3 {
		t.Fatalf("sessions started: %d, want a triage, a fix and an rca", len(p.sawSource))
	}
	if p.sawSource[2] != buggySource {
		t.Errorf("the rca session did not stand at %s:\n%s", short(repo.c1), p.sawSource[2])
	}

	// Blind: the prompt carries the triage note and no pull request.
	prompt, err := os.ReadFile(filepath.Join(repo.root, ".sirdar", "runs", "OMNI-1", r.RCA.RunID, "prompt.md"))
	if err != nil {
		t.Fatalf("read the rca prompt: %v", err)
	}
	if !strings.Contains(string(prompt), "Export fails for large orders") {
		t.Errorf("the rca prompt does not carry the retro's own triage note:\n%s", prompt)
	}
	if strings.Contains(string(prompt), "github.com/acme/omni/pull/482") {
		t.Errorf("the rca was shown the pull request:\n%s", prompt)
	}

	// Still nothing filed and nothing pushed.
	entries, err := os.ReadDir(repo.notes)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("the rca stage filed a note: %d entries in notes.dir", len(entries))
	}
	if got := repo.originBranches(t); len(got) != 1 || got[0] != "main" {
		t.Errorf("the origin's branches are %v, want only main", got)
	}
}

// chainRCADoc is a valid rca answer for the scripted rca session.
const chainRCADoc = `{
  "rca": {
    "title": "Export times out on large orders",
    "summary": "WriteCSV concatenated every row before returning. Large orders exceeded the request timeout. Building the output incrementally fixes it.",
    "impact": {"customersAffected":"1","recordsAffected":"n/a","financialImpact":"none","firstOccurrence":"2026-06-01","detection":"customer report","timeToDetect":"months"},
    "timeline": [{"at":"2026-09-10T08:30:00+03:00","event":"Customer reported the failure.","evidence":"ticket 555"}],
    "rootCause": {"description":"WriteCSV builds the whole file in one string.","codeRefs":["export/csv.go:4"],"offendingCode":"all += r","mechanism":"Nothing is written until the loop ends."},
    "contributingFactors": ["No test above 100 line items."],
    "evidence": {"database":[],"logs":[],"apm":[],"code":[],"attachments":[]},
    "blastRadius": {"query":"select count(*) ...","count":"37","scope":"systemic","reasoning":"Same code path for every large order."},
    "whyNotCaughtEarlier": "Load tests used small fixtures.",
    "prevention": [{"action":"Add a 1000-line load test.","type":"test","owner":"platform","ticket":"OMNI-2"}],
    "openQuestions": [],
    "classification": "code",
    "severity": "medium",
    "confidence": "high",
    "origin": "omni",
    "triageReview": {"verdict":"confirmed","gotRight":"The file and the mechanism.","missed":"Nothing material.","whyMissed":"n/a"},
    "lessons": ["Stream anything unbounded."],
    "playbookSuggestions": [{"playbook":"exports","addition":"Check the row count first.","reason":"It is the fastest signal."}]
  },
  "resolution": {
    "title": "Build the CSV incrementally",
    "resolutionType": "code-fix",
    "whatWasWrong": "The export concatenated every row in memory.",
    "whatWeChanged": "WriteCSV now appends to a buffer.",
    "codeChange": null,
    "dataChange": null,
    "verification": [],
    "customerOutcome": {"told":"Fix deployed.","confirmedFixed":"pending","helpdeskClosed":"no","trackerStatus":"in review"},
    "residualRisk": [],
    "lessons": "Stream anything unbounded."
  }
}`
