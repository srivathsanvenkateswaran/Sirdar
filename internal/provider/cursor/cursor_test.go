package cursor

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/provider"
)

// TestMain doubles as the fake `cursor-agent` binary: when
// SIRDAR_FAKE_CURSOR names a script, the test binary replays that script
// instead of running tests.
func TestMain(m *testing.M) {
	if script := os.Getenv("SIRDAR_FAKE_CURSOR"); script != "" {
		os.Exit(fakeCLI(script))
	}
	os.Exit(m.Run())
}

// fakeCLI replays a JSONL script on stdout. Directive lines drive the fake:
//
//	{"$exit":N}        exit with N
//	{"$stderr":"boom"} write a line to stderr
//	{"$block":true}    wait to be interrupted
//
// The command line is echoed to stderr as "ARGV:<json>" and any CURSOR_*
// variable that survived into the child as "ENV:<name>=<value>", so tests
// can inspect both through Result.StderrTail.
func fakeCLI(script string) int {
	if os.Getenv("SIRDAR_FAKE_CURSOR_IGNORE_INTERRUPT") == "" {
		interrupted := make(chan os.Signal, 1)
		signal.Notify(interrupted, os.Interrupt)
		go func() {
			<-interrupted
			fmt.Fprintln(os.Stderr, "SIGINT")
			os.Exit(0)
		}()
	}

	fmt.Fprintln(os.Stderr, "ARGV:"+mustJSON(os.Args[1:]))
	for _, name := range cursorEnvKeys {
		if v, ok := os.LookupEnv(name); ok {
			fmt.Fprintln(os.Stderr, "ENV:"+name+"="+v)
		}
	}
	// Proxy variables are reported by name only: the point is that they
	// survived into the child, and a proxy URL carries credentials.
	for _, name := range proxyEnvKeys {
		if _, ok := os.LookupEnv(name); ok {
			fmt.Fprintln(os.Stderr, "PROXY:"+name)
		}
	}

	f, err := os.Open(script)
	if err != nil {
		fmt.Fprintln(os.Stderr, "fake cursor:", err)
		return 2
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 16<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, `{"$`) {
			fmt.Fprintln(os.Stdout, line)
			continue
		}
		var directive struct {
			Exit   *int   `json:"$exit"`
			Stderr string `json:"$stderr"`
			Block  bool   `json:"$block"`
		}
		if err := json.Unmarshal([]byte(line), &directive); err != nil {
			fmt.Fprintln(os.Stderr, "fake cursor: bad directive:", line)
			return 2
		}
		switch {
		case directive.Exit != nil:
			return *directive.Exit
		case directive.Stderr != "":
			fmt.Fprintln(os.Stderr, directive.Stderr)
		case directive.Block:
			select {}
		}
	}
	return 0
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// fakeSpec builds a SessionSpec pointed at the test binary acting as the
// CLI, replaying the named script.
func fakeSpec(t *testing.T, script string, mutate func(*provider.SessionSpec)) provider.SessionSpec {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatalf("executable: %v", err)
	}
	// The child runs with spec.Cwd as its working directory, so a script
	// named relative to the test's own package directory has to be made
	// absolute before it is handed over.
	script, err = filepath.Abs(script)
	if err != nil {
		t.Fatalf("abs %s: %v", script, err)
	}
	spec := provider.SessionSpec{
		Cwd:          t.TempDir(),
		Prompt:       "triage the ticket",
		OutputSchema: []byte(`{"type":"object"}`),
		Binary:       self,
		Env:          append(os.Environ(), "SIRDAR_FAKE_CURSOR="+script),
	}
	if mutate != nil {
		mutate(&spec)
	}
	return spec
}

// drain collects every event a session emits and returns them with the
// session's Result.
func drain(t *testing.T, s provider.Session) ([]provider.Event, provider.Result) {
	t.Helper()
	var events []provider.Event
	for ev := range s.Events() {
		events = append(events, ev)
	}
	res, err := s.Wait()
	if err != nil {
		t.Fatalf("wait: %v", err)
	}
	return events, res
}

// argvOf pulls the fake CLI's recorded command line out of a stderr tail.
func argvOf(t *testing.T, tail []string) []string {
	t.Helper()
	for _, line := range tail {
		if !strings.HasPrefix(line, "ARGV:") {
			continue
		}
		var argv []string
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "ARGV:")), &argv); err != nil {
			t.Fatalf("decode ARGV: %v", err)
		}
		return argv
	}
	t.Fatalf("no ARGV line in stderr tail %v", tail)
	return nil
}

func TestSessionReadsTheStream(t *testing.T) {
	p := New()
	s, err := p.Start(context.Background(), fakeSpec(t, "testdata/script-basic.jsonl", nil))
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	events, res := drain(t, s)

	var kinds []provider.EventKind
	for _, ev := range events {
		kinds = append(kinds, ev.Kind)
	}
	want := map[provider.EventKind]int{
		provider.EvSystem:        3, // the unconfined-reads notice, init, the echoed prompt
		provider.EvAssistantText: 2,
		provider.EvToolStarted:   2,
		provider.EvToolFinished:  2,
		provider.EvUsage:         1,
		provider.EvFinal:         1,
	}
	got := map[provider.EventKind]int{}
	for _, k := range kinds {
		got[k]++
	}
	for kind, n := range want {
		if got[kind] != n {
			t.Errorf("%s events: got %d, want %d (all: %v)", kind, got[kind], n, kinds)
		}
	}
	if got[provider.EvError] != 0 {
		t.Errorf("unexpected error events: %v", kinds)
	}

	// The thinking deltas are dropped rather than reported as assistant
	// text: they are one line per token and are not the answer.
	for _, ev := range events {
		if ev.Kind == provider.EvAssistantText && strings.Contains(ev.Text, "Reading the code") {
			t.Errorf("thinking text reached the event stream: %q", ev.Text)
		}
	}

	if res.Handle != "f7d05f71-bce0-4e57-a6dd-797936cafb6c" {
		t.Errorf("handle %q", res.Handle)
	}
	if res.ExitErr != nil {
		t.Errorf("exit err: %v", res.ExitErr)
	}
	// Input tokens are all three counters, not just the uncached one.
	if want := int64(40371 + 5376 + 128); res.Usage.InputTok != want {
		t.Errorf("input tokens %d, want %d", res.Usage.InputTok, want)
	}
	if res.Usage.OutputTok != 233 {
		t.Errorf("output tokens %d", res.Usage.OutputTok)
	}
	// There is no cost on the wire, so the run must not be told there was.
	if res.Usage.CostUSD != 0 {
		t.Errorf("cost %v, want 0: the CLI reports none", res.Usage.CostUSD)
	}
	// Two assistant answers and two model_call_ids, counted once each.
	if res.Usage.Turns != 4 {
		t.Errorf("turns %d, want 4", res.Usage.Turns)
	}
}

func TestFinalIsParsedOutOfAFencedAnswer(t *testing.T) {
	p := New()
	s, err := p.Start(context.Background(), fakeSpec(t, "testdata/script-basic.jsonl", nil))
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	_, res := drain(t, s)

	var doc struct {
		Summary    string  `json:"summary"`
		Confidence float64 `json:"confidence"`
	}
	if err := json.Unmarshal(res.Final, &doc); err != nil {
		t.Fatalf("final %q: %v", res.Final, err)
	}
	if doc.Summary != "null pointer in handler" || doc.Confidence != 0.8 {
		t.Errorf("final %+v", doc)
	}
}

func TestToolEventsNameTheToolAndReportARefusal(t *testing.T) {
	p := New()
	s, err := p.Start(context.Background(), fakeSpec(t, "testdata/script-basic.jsonl", nil))
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	events, _ := drain(t, s)

	var started, finished []string
	var refusal string
	for _, ev := range events {
		switch ev.Kind {
		case provider.EvToolStarted:
			started = append(started, ev.Tool)
		case provider.EvToolFinished:
			finished = append(finished, ev.Tool)
			if strings.HasPrefix(ev.Text, "rejected:") {
				refusal = ev.Text
			}
		}
	}
	if fmt.Sprint(started) != "[Read Shell]" {
		t.Errorf("started tools %v", started)
	}
	if fmt.Sprint(finished) != "[Read Shell]" {
		t.Errorf("finished tools %v", finished)
	}
	if refusal != "rejected: blocked by policy" {
		t.Errorf("refusal %q", refusal)
	}
}

func TestArgsCarryTheReadOnlyGuarantee(t *testing.T) {
	p := New()
	s, err := p.Start(context.Background(), fakeSpec(t, "testdata/script-basic.jsonl", nil))
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	_, res := drain(t, s)
	argv := argvOf(t, res.StderrTail)
	joined := strings.Join(argv, " ")

	for _, want := range []string{
		"-p", "--output-format stream-json", "--model auto", "--mode ask",
		"--trust", "--sandbox enabled", "--disable-project-configs",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("argv %q is missing %q", joined, want)
		}
	}
	// Every write-capable tool is excluded on a read-only session.
	for _, tool := range writeTools {
		if !strings.Contains(joined, "--exclude-tools "+tool) {
			t.Errorf("argv %q does not exclude %s", joined, tool)
		}
	}
	// permissions.fetch is empty here, so the fetch tools go too.
	for _, tool := range fetchTools {
		if !strings.Contains(joined, "--exclude-tools "+tool) {
			t.Errorf("argv %q does not exclude %s", joined, tool)
		}
	}
	// --stream-partial-output would report the whole answer once per token.
	if strings.Contains(joined, "--stream-partial-output") {
		t.Errorf("argv %q passes --stream-partial-output", joined)
	}
	// The prompt is the last positional argument, and it carries the
	// schema, because there is no --json-schema flag.
	last := argv[len(argv)-1]
	if !strings.HasPrefix(last, "triage the ticket") {
		t.Errorf("last arg is not the prompt: %q", last)
	}
	if !strings.Contains(last, `{"type":"object"}`) {
		t.Errorf("prompt does not carry the schema: %q", last)
	}
}

func TestFetchToolsStayWhenTheWorkspaceAllowsAHost(t *testing.T) {
	p := New()
	spec := fakeSpec(t, "testdata/script-basic.jsonl", func(s *provider.SessionSpec) {
		s.Policy = &provider.PermissionPolicy{FetchAllow: []string{"docs.example.com"}}
	})
	s, err := p.Start(context.Background(), spec)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	_, res := drain(t, s)
	joined := strings.Join(argvOf(t, res.StderrTail), " ")
	for _, tool := range fetchTools {
		if strings.Contains(joined, "--exclude-tools "+tool) {
			t.Errorf("argv %q excludes %s although permissions.fetch names a host", joined, tool)
		}
	}
}

func TestMCPToolsGoWhenTheWorkspaceDeclaresNoServers(t *testing.T) {
	p := New()
	spec := fakeSpec(t, "testdata/script-basic.jsonl", func(s *provider.SessionSpec) {
		s.MCPStrict = true
	})
	s, err := p.Start(context.Background(), spec)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	_, res := drain(t, s)
	joined := strings.Join(argvOf(t, res.StderrTail), " ")
	for _, tool := range mcpTools {
		if !strings.Contains(joined, "--exclude-tools "+tool) {
			t.Errorf("argv %q does not exclude %s", joined, tool)
		}
	}
}

func TestPlanModeAndModelOverride(t *testing.T) {
	p := NewConfig(Config{Mode: "plan", Model: "composer-2.5"})
	s, err := p.Start(context.Background(), fakeSpec(t, "testdata/script-basic.jsonl", nil))
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	_, res := drain(t, s)
	joined := strings.Join(argvOf(t, res.StderrTail), " ")
	if !strings.Contains(joined, "--mode plan") {
		t.Errorf("argv %q", joined)
	}
	if !strings.Contains(joined, "--model composer-2.5") {
		t.Errorf("argv %q", joined)
	}
}

func TestSpecModelBeatsTheWorkspaceModel(t *testing.T) {
	p := NewConfig(Config{Model: "composer-2.5"})
	spec := fakeSpec(t, "testdata/script-basic.jsonl", func(s *provider.SessionSpec) {
		s.Model = "gpt-5.4-nano-low"
	})
	s, err := p.Start(context.Background(), spec)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	_, res := drain(t, s)
	if joined := strings.Join(argvOf(t, res.StderrTail), " "); !strings.Contains(joined, "--model gpt-5.4-nano-low") {
		t.Errorf("argv %q", joined)
	}
}

func TestResumeIsPassedThrough(t *testing.T) {
	p := New()
	spec := fakeSpec(t, "testdata/script-basic.jsonl", func(s *provider.SessionSpec) {
		s.Resume = "008be211-0c74-4d05-a518-d55919e16378"
	})
	s, err := p.Start(context.Background(), spec)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	_, res := drain(t, s)
	if joined := strings.Join(argvOf(t, res.StderrTail), " "); !strings.Contains(joined, "--resume 008be211-0c74-4d05-a518-d55919e16378") {
		t.Errorf("argv %q", joined)
	}
}

func TestFixModeIsRefused(t *testing.T) {
	p := New()
	spec := fakeSpec(t, "testdata/script-basic.jsonl", func(s *provider.SessionSpec) {
		s.Mode = provider.ModeFix
	})
	s, err := p.Start(context.Background(), spec)
	if err == nil {
		s.Cancel()
		_, _ = s.Wait()
		t.Fatal("a fix session started; it must be refused")
	}
	if !errors.Is(err, ErrFixUnsupported) {
		t.Errorf("error %v, want ErrFixUnsupported", err)
	}
}

func TestChildEnvStripsTheEndpointAndCredentialVariables(t *testing.T) {
	p := New()
	spec := fakeSpec(t, "testdata/script-basic.jsonl", func(s *provider.SessionSpec) {
		s.Env = append(s.Env,
			"CURSOR_API_KEY=secret",
			"CURSOR_API_ENDPOINT=https://evil.example",
			"CURSOR_AUTH_TOKEN=secret",
			"CURSOR_API_URL=https://evil.example",
			"CURSOR_DATA_DIR=/tmp/elsewhere",
			"CURSOR_STATSIG_OVERRIDES=x",
		)
	})
	s, err := p.Start(context.Background(), spec)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	events, res := drain(t, s)

	for _, line := range res.StderrTail {
		if strings.HasPrefix(line, "ENV:") {
			t.Errorf("a Cursor variable reached the child: %q", line)
		}
	}
	// Each removal is said once, to the operator, and never carries the
	// value that was removed.
	var notices int
	for _, ev := range events {
		if ev.Kind != provider.EvSystem || !strings.HasPrefix(ev.Text, "removed ") {
			continue
		}
		notices++
		if strings.Contains(ev.Text, "secret") || strings.Contains(ev.Text, "evil.example") {
			t.Errorf("a notice carried the stripped value: %q", ev.Text)
		}
	}
	if notices != len(cursorEnvKeys) {
		t.Errorf("%d removal notices, want %d", notices, len(cursorEnvKeys))
	}
}

func TestUSDBudgetIsReportedAsUnenforceable(t *testing.T) {
	p := New()
	spec := fakeSpec(t, "testdata/script-basic.jsonl", func(s *provider.SessionSpec) {
		s.Budget.MaxUSD = 5
	})
	s, err := p.Start(context.Background(), spec)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	events, _ := drain(t, s)
	for _, ev := range events {
		if ev.Kind == provider.EvSystem && strings.Contains(ev.Text, "budget.maxUsd") {
			return
		}
	}
	t.Error("no notice that budget.maxUsd cannot stop a Cursor run")
}

func TestSendAlwaysFails(t *testing.T) {
	p := New()
	s, err := p.Start(context.Background(), fakeSpec(t, "testdata/script-basic.jsonl", nil))
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	// The CLI takes one message per session, so the runner has to learn
	// this and carry the retry into a resumed session instead.
	if err := s.Send(context.Background(), "try again"); err == nil {
		t.Error("Send succeeded; the CLI has no stdin protocol")
	}
	if err := s.CloseInput(); err != nil {
		t.Errorf("CloseInput: %v", err)
	}
	drain(t, s)
}

func TestExitOneWithNoResultLineIsReportedWithItsStderr(t *testing.T) {
	script := writeScript(t, `{"type":"system","subtype":"init","session_id":"s1","model":"GPT-5.4 Nano Low"}
{"$stderr":"ActionRequiredError: Named models unavailable Free plans can only use Auto."}
{"$exit":1}
`)
	p := New()
	s, err := p.Start(context.Background(), fakeSpec(t, script, nil))
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	events, res := drain(t, s)

	if res.ExitErr == nil {
		t.Fatal("exit 1 was not reported")
	}
	if !strings.Contains(res.ExitErr.Error(), "Named models unavailable") {
		t.Errorf("exit err %v does not carry the stderr tail", res.ExitErr)
	}
	// No result line means no final event, which is what makes the run
	// fail rather than accept an empty answer.
	for _, ev := range events {
		if ev.Kind == provider.EvFinal {
			t.Error("a final event arrived without a result line")
		}
	}
	// The handle is still readable, because it is on the init line.
	if res.Handle != "s1" {
		t.Errorf("handle %q", res.Handle)
	}
}

func TestMalformedLineBecomesAnError(t *testing.T) {
	script := writeScript(t, "not json at all\n"+
		`{"type":"result","subtype":"success","is_error":false,"result":"{}","session_id":"s2","usage":{"inputTokens":1,"outputTokens":1}}`+"\n")
	p := New()
	s, err := p.Start(context.Background(), fakeSpec(t, script, nil))
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	events, _ := drain(t, s)
	for _, ev := range events {
		if ev.Kind == provider.EvError && ev.Text == "malformed line" {
			return
		}
	}
	t.Error("a malformed line was not reported")
}

func TestCancelStopsABlockedSession(t *testing.T) {
	script := writeScript(t, `{"type":"system","subtype":"init","session_id":"s3"}
{"$block":true}
`)
	p := New()
	s, err := p.Start(context.Background(), fakeSpec(t, script, nil))
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	go func() {
		time.Sleep(50 * time.Millisecond)
		s.Cancel()
	}()

	done := make(chan struct{})
	go func() {
		for range s.Events() {
		}
		_, _ = s.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("a cancelled session did not end")
	}
}

func TestDoctorReadsTheVersionTheLoginAndTheFixRefusal(t *testing.T) {
	binary := writeStub(t, map[string]string{
		"--version": "2026.09.10-fd3934a",
		"status":    "✓ Logged in as someone@example.com",
		"about":     "About Cursor CLI\n\nCLI Version         2026.09.10-fd3934a\nSubscription Tier   Free\n",
	})
	checks := New().Doctor(context.Background(), binary)

	byName := map[string]provider.Check{}
	for _, c := range checks {
		byName[c.Name] = c
	}
	if c := byName["cursor-agent --version"]; !c.OK || c.Detail != "2026.09.10-fd3934a" {
		t.Errorf("version row %+v", c)
	}
	// The account's email address must not reach a doctor report: it gets
	// pasted into tickets.
	if c := byName["cursor-agent status"]; !c.OK || strings.Contains(c.Detail, "@") {
		t.Errorf("status row %+v", c)
	}
	if c := byName["cursor model"]; !c.OK || !strings.Contains(c.Detail, "Free") {
		t.Errorf("model row %+v", c)
	}
	fix := byName["cursor fix"]
	if fix.Severity() != provider.LevelWarn || !strings.Contains(fix.Detail, "refused") {
		t.Errorf("fix row %+v", fix)
	}
	reads := byName["cursor reads"]
	if reads.Severity() != provider.LevelWarn || !strings.Contains(reads.Detail, "not confined") {
		t.Errorf("reads row %+v", reads)
	}
	if !strings.Contains(reads.Detail, "permissions.readAlso") {
		t.Errorf("reads row %+v does not name the setting it overrules", reads)
	}
}

// TestSessionSaysReadsAreNotConfined: the doctor row is what an operator
// reads before a run, and this notice is what they read after one. A
// provider Sirdar cannot ask about a read has to say so on the run's own
// event log, where the rest of what the session could reach is recorded.
func TestSessionSaysReadsAreNotConfined(t *testing.T) {
	s, err := New().Start(context.Background(), fakeSpec(t, "testdata/script-basic.jsonl", nil))
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	events, _ := drain(t, s)
	for _, ev := range events {
		if ev.Kind == provider.EvSystem && strings.Contains(ev.Text, "reads are not confined") {
			return
		}
	}
	t.Error("the session never said that reads are unconfined on this provider")
}

func TestDoctorFailsANamedModelOnAFreePlan(t *testing.T) {
	binary := writeStub(t, map[string]string{
		"--version": "2026.09.10-fd3934a",
		"status":    "✓ Logged in as someone@example.com",
		"about":     "Subscription Tier   Free\n",
	})
	checks := NewConfig(Config{Model: "composer-2.5"}).Doctor(context.Background(), binary)
	for _, c := range checks {
		if c.Name != "cursor model" {
			continue
		}
		if c.OK {
			t.Errorf("model row %+v: a named model on a Free plan fails before the first token", c)
		}
		if !strings.Contains(c.Detail, "composer-2.5") {
			t.Errorf("model row %+v does not name the model", c)
		}
		return
	}
	t.Error("no model row")
}

func TestDoctorWithConfigSaysWorkspaceOnlyMCPCannotBeHonoured(t *testing.T) {
	binary := writeStub(t, map[string]string{"--version": "2026.09.10-fd3934a"})
	cd, ok := NewConfig(Config{}).(provider.ConfigDoctor)
	if !ok {
		t.Fatal("the provider does not implement ConfigDoctor")
	}
	checks := cd.DoctorWithConfig(context.Background(), binary, provider.DoctorConfig{
		Root: t.TempDir(), MCPWorkspaceOnly: true,
	})
	for _, c := range checks {
		if c.Name != "cursor mcp" {
			continue
		}
		if c.Severity() != provider.LevelWarn {
			t.Errorf("mcp row %+v: not honouring the setting is a warning, not a failure", c)
		}
		if !strings.Contains(c.Detail, "cannot be honoured") {
			t.Errorf("mcp row %+v does not say the setting is not honoured", c)
		}
		return
	}
	t.Error("no mcp row")
}

// writeScript puts a fake-CLI script in a temp file and returns its path.
func writeScript(t *testing.T, body string) string {
	t.Helper()
	path := t.TempDir() + "/script.jsonl"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write script: %v", err)
	}
	return path
}

// writeStub builds a shell script that answers Doctor's subcommands, keyed
// by the first argument.
func writeStub(t *testing.T, answers map[string]string) string {
	t.Helper()
	var b strings.Builder
	b.WriteString("#!/bin/sh\ncase \"$1\" in\n")
	for arg, out := range answers {
		fmt.Fprintf(&b, "  %s) printf '%%s\\n' %s ;;\n", arg, shellQuote(out))
	}
	b.WriteString("  *) exit 9 ;;\nesac\n")

	path := t.TempDir() + "/cursor-agent"
	if err := os.WriteFile(path, []byte(b.String()), 0o700); err != nil {
		t.Fatalf("write stub: %v", err)
	}
	if _, err := exec.LookPath(path); err != nil {
		t.Fatalf("stub not executable: %v", err)
	}
	return path
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// --- read-only breaches ------------------------------------------------

// collect sorts a session's events into the buckets the breach tests ask
// about.
func collect(events []provider.Event) (breaches, errs, finals []provider.Event) {
	for _, ev := range events {
		switch ev.Kind {
		case provider.EvBreach:
			breaches = append(breaches, ev)
		case provider.EvError:
			errs = append(errs, ev)
		case provider.EvFinal:
			finals = append(finals, ev)
		}
	}
	return breaches, errs, finals
}

// TestCompletedEditIsABreach is the whole of what this adapter can do
// about a write. Sirdar cannot refuse a Cursor tool call — print mode
// approves its own — so the first it hears of an edit is the line saying
// it finished, and the only honest response is to end the run over it.
//
// The run layer's half of this is pinned by
// internal/run TestBreachEndsTheRunAndFilesNothing: an EvBreach cancels
// the session and fails the run with the event's first line as the
// reason, filing no note and no register row. So what is asserted here is
// the contract that test depends on — the kind, the first line, the tool
// — and that it is raised even though the session went on to produce a
// perfectly good answer.
func TestCompletedEditIsABreach(t *testing.T) {
	script := writeScript(t, `{"type":"system","subtype":"init","session_id":"s-edit","model":"Auto"}
{"type":"tool_call","subtype":"started","call_id":"c1","tool_call":{"editToolCall":{"args":{"path":"/work/a.txt","streamContent":"hello\n"}},"toolCallId":"c1"},"model_call_id":"mc-1","session_id":"s-edit"}
{"type":"tool_call","subtype":"completed","call_id":"c1","tool_call":{"editToolCall":{"args":{"path":"/work/a.txt"},"result":{"success":{"path":"/work/a.txt","linesAdded":1,"message":"Wrote contents to /work/a.txt"}}},"toolCallId":"c1"},"model_call_id":"mc-1","session_id":"s-edit"}
{"type":"result","subtype":"success","is_error":false,"result":"{\"summary\":\"done\"}","session_id":"s-edit","usage":{"inputTokens":10,"outputTokens":2}}
`)
	p := New()
	s, err := p.Start(context.Background(), fakeSpec(t, script, nil))
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	events, _ := drain(t, s)
	breaches, errs, finals := collect(events)

	if len(breaches) != 1 {
		t.Fatalf("a completed edit was not raised as a breach: %+v", events)
	}
	breach := breaches[0]
	if got := firstLine([]byte(breach.Text)); got != "read-only breach: Edit /work/a.txt" {
		t.Errorf("breach reason %q", got)
	}
	if breach.Tool != "Edit" {
		t.Errorf("breach tool %q", breach.Tool)
	}
	// A breach is not an error: the run layer counts errors towards a
	// malformed-line threshold a run is meant to survive, and this is not
	// something a run survives.
	if len(errs) != 0 {
		t.Errorf("a breach was also raised as an error: %+v", errs)
	}
	// The session had already answered. That answer is worth less than
	// the fact that the guarantee failed, and the run layer refuses to
	// file it — but the event still has to reach it for the reasons in
	// handleFinal, so this only pins that the breach is not suppressed by
	// a successful-looking session.
	if len(finals) != 1 {
		t.Errorf("final events %+v", finals)
	}
	// The breach must say where to look, since the widening is in a file
	// Sirdar cannot read.
	if !strings.Contains(breach.Text, "--exclude-tools") {
		t.Errorf("the breach does not name what failed: %q", breach.Text)
	}
}

// TestRejectedEditIsNotABreach is the ordinary outcome and the one the
// whole read-only guarantee rests on: the tool was refused, nothing was
// written, and the session carries on to answer.
func TestRejectedEditIsNotABreach(t *testing.T) {
	script := writeScript(t, `{"type":"system","subtype":"init","session_id":"s-rej","model":"Auto"}
{"type":"tool_call","subtype":"started","call_id":"c1","tool_call":{"editToolCall":{"args":{"path":"/work/a.txt"}},"toolCallId":"c1"},"model_call_id":"mc-1","session_id":"s-rej"}
{"type":"tool_call","subtype":"completed","call_id":"c1","tool_call":{"editToolCall":{"result":{"rejected":{"path":"/work/a.txt","reason":"tool not available in ask mode","isReadonly":false}}},"toolCallId":"c1"},"model_call_id":"mc-1","session_id":"s-rej"}
{"type":"result","subtype":"success","is_error":false,"result":"{\"summary\":\"done\"}","session_id":"s-rej","usage":{"inputTokens":10,"outputTokens":2}}
`)
	p := New()
	s, err := p.Start(context.Background(), fakeSpec(t, script, nil))
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	events, res := drain(t, s)
	breaches, _, finals := collect(events)

	if len(breaches) != 0 {
		t.Fatalf("a refused edit was reported as a breach: %+v", breaches)
	}
	if len(finals) != 1 {
		t.Fatalf("the session did not answer: %+v", events)
	}
	if string(res.Final) != `{"summary":"done"}` {
		t.Errorf("final %q", res.Final)
	}
	// The refusal still reaches the operator, because it is the only
	// evidence on this provider that the read-only mode did its job.
	var summary string
	for _, ev := range events {
		if ev.Kind == provider.EvToolFinished {
			summary = ev.Text
		}
	}
	if summary != "rejected: tool not available in ask mode" {
		t.Errorf("tool summary %q", summary)
	}
}

// TestCompletedSwitchModeIsABreach covers the tool that would end the
// read-only guarantee without writing anything: the model has a tool for
// changing its own execution mode, and an ask-mode session that completed
// one is no longer an ask-mode session.
func TestCompletedSwitchModeIsABreach(t *testing.T) {
	for _, key := range []string{"switchModeToolCall", "switch_modeToolCall"} {
		t.Run(key, func(t *testing.T) {
			script := writeScript(t, `{"type":"system","subtype":"init","session_id":"s-mode"}
{"type":"tool_call","subtype":"completed","call_id":"c1","tool_call":{"`+key+`":{"args":{"fromModeId":"ask","toModeId":"agent"},"result":{"success":{"autoApproved":true}}},"toolCallId":"c1"},"model_call_id":"mc-1","session_id":"s-mode"}
{"type":"result","subtype":"success","is_error":false,"result":"{}","session_id":"s-mode","usage":{"inputTokens":1,"outputTokens":1}}
`)
			p := New()
			s, err := p.Start(context.Background(), fakeSpec(t, script, nil))
			if err != nil {
				t.Fatalf("start: %v", err)
			}
			events, _ := drain(t, s)
			breaches, _, _ := collect(events)
			if len(breaches) != 1 {
				t.Fatalf("a completed switch_mode was not raised as a breach: %+v", events)
			}
			if got := firstLine([]byte(breaches[0].Text)); !strings.HasPrefix(got, "read-only breach: SwitchMode") {
				t.Errorf("breach reason %q", got)
			}
			if !strings.Contains(breaches[0].Text, "switch_mode_tool_call") {
				t.Errorf("the breach does not name the excluded tool: %q", breaches[0].Text)
			}
		})
	}
}

// TestCompletedReadIsNotABreach keeps the watch off the tools a triage
// session is there to use.
func TestCompletedReadIsNotABreach(t *testing.T) {
	p := New()
	s, err := p.Start(context.Background(), fakeSpec(t, "testdata/script-basic.jsonl", nil))
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	events, _ := drain(t, s)
	if breaches, _, _ := collect(events); len(breaches) != 0 {
		t.Errorf("the baseline script raised a breach: %+v", breaches)
	}
}

// TestFixIsRefusedBeforeGit pins the early half of the refusal.
// provider.RefuseFix is asked by `sirdar fix` before it fetches the
// default branch, cuts a branch or adds a worktree, so a workspace that
// names this provider is left exactly as the operator had it.
func TestFixIsRefusedBeforeGit(t *testing.T) {
	p := New()
	fs, ok := p.(provider.FixSupport)
	if !ok {
		t.Fatal("the provider does not implement FixSupport, so `sirdar fix` would cut a branch first")
	}
	if fs.SupportsFix() {
		t.Error("SupportsFix is true")
	}
	err := provider.RefuseFix(p)
	if !errors.Is(err, ErrFixUnsupported) {
		t.Errorf("RefuseFix = %v, want ErrFixUnsupported", err)
	}
}

// --- proxy variables ---------------------------------------------------

// TestProxyVariablesPassThroughAndAreReported covers the environment
// Sirdar deliberately does not touch: on a network that needs a proxy,
// stripping these would leave a session that never reaches Cursor. What
// they cost is that whatever terminates the TLS can read the ticket, so
// they are said out loud instead — by name, never by value.
func TestProxyVariablesPassThroughAndAreReported(t *testing.T) {
	p := New()
	spec := fakeSpec(t, "testdata/script-basic.jsonl", func(s *provider.SessionSpec) {
		s.Env = append(s.Env,
			"HTTPS_PROXY=http://user:hunter2@proxy.corp:3128",
			"NODE_EXTRA_CA_CERTS=/etc/ssl/corp.pem",
		)
	})
	s, err := p.Start(context.Background(), spec)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	events, res := drain(t, s)

	var reached []string
	for _, line := range res.StderrTail {
		if strings.HasPrefix(line, "PROXY:") {
			reached = append(reached, strings.TrimPrefix(line, "PROXY:"))
		}
	}
	if fmt.Sprint(reached) != "[HTTPS_PROXY NODE_EXTRA_CA_CERTS]" {
		t.Errorf("the proxy variables did not reach the child: %v", reached)
	}

	var notice string
	for _, ev := range events {
		if ev.Kind == provider.EvSystem && strings.Contains(ev.Text, "HTTPS_PROXY") {
			notice = ev.Text
		}
	}
	if notice == "" {
		t.Fatalf("no notice that a proxy is in the path: %+v", events)
	}
	if !strings.Contains(notice, "NODE_EXTRA_CA_CERTS") {
		t.Errorf("the notice does not name every variable: %q", notice)
	}
	if strings.Contains(notice, "hunter2") || strings.Contains(notice, "proxy.corp") {
		t.Errorf("the notice carried the proxy URL: %q", notice)
	}
	// And they are not stripped as the CURSOR_* variables are.
	if strings.Contains(notice, "removed ") {
		t.Errorf("a proxy variable was reported as removed: %q", notice)
	}
}

// TestProxyDoctorRow covers the same finding on the report an operator
// reads before a run rather than during one, and its absence when there is
// nothing to warn about.
func TestProxyDoctorRow(t *testing.T) {
	row, ok := proxyCheck([]string{"PATH=/usr/bin", "https_proxy=http://user:hunter2@proxy.corp:3128"})
	if !ok {
		t.Fatal("no proxy row although https_proxy is set")
	}
	if row.Severity() != provider.LevelWarn {
		t.Errorf("proxy row %+v: a proxy is a warning, not a failure", row)
	}
	if !strings.Contains(row.Detail, "https_proxy") {
		t.Errorf("proxy row %+v does not name the variable", row)
	}
	if strings.Contains(row.Detail, "hunter2") {
		t.Errorf("proxy row %+v carries the value", row)
	}
	if _, ok := proxyCheck([]string{"PATH=/usr/bin"}); ok {
		t.Error("a proxy row appeared with no proxy set")
	}
}

// TestDoctorStatusFailureReportsTheErrorOnly keeps `cursor-agent status`
// output out of a failing row. Its success path prints the account's email
// address, doctor reports get pasted into tickets, and a non-zero exit is
// no promise that nothing was printed first.
func TestDoctorStatusFailureReportsTheErrorOnly(t *testing.T) {
	binary := writeStub(t, map[string]string{"--version": "2026.09.10-fd3934a"})
	for _, c := range New().Doctor(context.Background(), binary) {
		if c.Name != "cursor-agent status" {
			continue
		}
		if c.OK {
			t.Fatalf("status row %+v: the stub exits 9 on `status`", c)
		}
		if strings.Contains(c.Detail, "@") || strings.Contains(c.Detail, "Logged in") {
			t.Errorf("status row %+v carries the command's output", c)
		}
		if !strings.Contains(c.Detail, "exit status") {
			t.Errorf("status row %+v does not carry the error", c)
		}
		return
	}
	t.Error("no status row")
}
