package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/app"
	"github.com/srivathsanvenkateswaran/sirdar/internal/httpapi"
	"github.com/srivathsanvenkateswaran/sirdar/internal/httpapi/ui"
)

// TestServeRefusesRemoteAddress is the guard that matters most here: the
// API has no authentication, so binding anything but loopback has to be
// asked for explicitly.
func TestServeRefusesRemoteAddress(t *testing.T) {
	for _, addr := range []string{"0.0.0.0:7777", ":7777", "192.168.1.20:7777", "[::]:7777"} {
		var out, errb bytes.Buffer
		code := run([]string{"serve", "--addr", addr}, &out, &errb)
		if code != 2 {
			t.Errorf("%s: exit %d, want 2 (stdout %q)", addr, code, out.String())
			continue
		}
		if !strings.Contains(errb.String(), "--allow-remote") {
			t.Errorf("%s: stderr %q does not mention --allow-remote", addr, errb.String())
		}
		if out.Len() != 0 {
			t.Errorf("%s: refused invocation still printed %q", addr, out.String())
		}
	}
}

// TestServeWithoutWorkspace covers the other half: a loopback address is
// accepted, and the command then stops on the missing workspace rather
// than binding anything.
func TestServeWithoutWorkspace(t *testing.T) {
	t.Chdir(t.TempDir())
	var out, errb bytes.Buffer
	if code := run([]string{"serve", "--addr", "127.0.0.1:0"}, &out, &errb); code != 2 {
		t.Fatalf("exit %d, want 2 (stderr %q)", code, errb.String())
	}
	if !strings.Contains(errb.String(), ".sirdar/config.yaml") {
		t.Fatalf("stderr %q", errb.String())
	}
}

// TestServeAcceptsAGoldenDir keeps `sirdar serve` level with `sirdar eval`:
// the golden set the UI's eval routes replay is named on the command line,
// once, and never by a request.
func TestServeAcceptsAGoldenDir(t *testing.T) {
	t.Chdir(t.TempDir())
	var out, errb bytes.Buffer
	if code := run([]string{"serve", "--addr", "127.0.0.1:0", "--golden", t.TempDir()}, &out, &errb); code != 2 {
		t.Fatalf("exit %d, want 2 (stderr %q)", code, errb.String())
	}
	// It stopped on the missing workspace, which means --golden parsed.
	if strings.Contains(errb.String(), "not defined") {
		t.Fatalf("--golden is not a serve flag: %q", errb.String())
	}
	if !strings.Contains(errb.String(), ".sirdar/config.yaml") {
		t.Fatalf("stderr %q", errb.String())
	}
}

func TestServeRejectsUnknownFlag(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"serve", "--port", "7777"}, &out, &errb); code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
}

func TestIsLoopback(t *testing.T) {
	for addr, want := range map[string]bool{
		"127.0.0.1:7777": true,
		"localhost:7777": true,
		"[::1]:7777":     true,
		"127.0.0.1:0":    true,
		"0.0.0.0:7777":   false,
		":7777":          false,
		"[::]:7777":      false,
		"10.0.0.4:7777":  false,
		"sirdar.local:8": false,
		"7777":           false,
	} {
		if got := isLoopback(addr); got != want {
			t.Errorf("isLoopback(%q) = %v, want %v", addr, got, want)
		}
	}
}

func TestBrowseURL(t *testing.T) {
	for _, tc := range []struct {
		addr net.Addr
		want string
	}{
		{&net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 7777}, "http://127.0.0.1:7777"},
		// A listener on every interface still gets advertised as the one
		// address the operator is certain to be able to open.
		{&net.TCPAddr{IP: net.IPv4zero, Port: 7777}, "http://127.0.0.1:7777"},
		{&net.TCPAddr{IP: net.IPv6loopback, Port: 80}, "http://[::1]:80"},
	} {
		if got := browseURL(tc.addr); got != tc.want {
			t.Errorf("browseURL(%v) = %q, want %q", tc.addr, got, tc.want)
		}
	}
}

// TestServeEndToEnd drives the whole browser surface the way the UI does:
// register a workspace, bind a free port, watch the event stream, start a
// triage over HTTP, and read back the run, its note and its event log. It
// is the one test that puts the HTTP layer, the application service, the
// watcher and a real Runner together in one process.
func TestServeEndToEnd(t *testing.T) {
	root, _ := newWorkspace(t, fakeClaude)
	t.Setenv("SIRDAR_FAKE_DOC", filepath.Join(testdataDir, "triage-doc.json"))

	// The registry lives under the operator's home; give this test its own
	// so it registers the temporary workspace and nothing else.
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	registryPath, err := app.DefaultRegistryPath()
	if err != nil {
		t.Fatal(err)
	}
	reg := &app.Registry{Path: registryPath}
	if _, err := ensureRegistered(reg, root); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	svc := app.New(reg, app.BuildDeps, app.Options{
		Interval: 20 * time.Millisecond,
		Stderr:   io.Discard,
	})
	svc.Start(ctx)

	// serveHTTP announces the address it bound on stdout, which is how the
	// test learns which port the kernel picked.
	pr, pw := io.Pipe()
	exited := make(chan int, 1)
	go func() {
		exited <- serveHTTP(ctx, httpapi.New(svc, ui.FS(), httpapi.LoopbackOnly(true)),
			"127.0.0.1:0", false, pw, io.Discard)
	}()
	t.Cleanup(func() {
		cancel()
		svc.Stop()
		select {
		case code := <-exited:
			if code != 0 {
				t.Errorf("serve exited %d", code)
			}
		case <-time.After(10 * time.Second):
			t.Error("serve did not return after its context was cancelled")
		}
	})

	line, err := bufio.NewReader(pr).ReadString('\n')
	if err != nil {
		t.Fatalf("reading the address line: %v", err)
	}
	base := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "Sirdar UI:"))
	if !strings.HasPrefix(base, "http://127.0.0.1:") {
		t.Fatalf("serve announced %q", line)
	}

	var workspaces []app.Workspace
	getJSON(t, base+"/api/workspaces", &workspaces)
	wsID := ""
	for _, ws := range workspaces {
		if ws.Root == filepath.Clean(root) {
			wsID = ws.ID
		}
	}
	if wsID == "" {
		t.Fatalf("the registered workspace is not listed: %+v", workspaces)
	}

	// Subscribe before starting anything, so the job's completion cannot be
	// published before there is anyone listening for it.
	finished := watchEvents(t, base+"/api/events", "job.finished")

	var started struct {
		JobID string `json:"jobId"`
	}
	postJSON(t, base+"/api/workspaces/"+wsID+"/triage", `{"keys":["OMNI-1"]}`, http.StatusAccepted, &started)
	if started.JobID == "" {
		t.Fatal("triage answered with no job id")
	}

	// Poll the run list the way the board does, until the run it started is
	// finished on disk.
	var done app.RunSummary
	deadline := time.Now().Add(60 * time.Second)
	for done.RunID == "" {
		if time.Now().After(deadline) {
			t.Fatal("the triage run never reached a terminal state")
		}
		var runs []app.RunSummary
		getJSON(t, base+"/api/workspaces/"+wsID+"/runs", &runs)
		for _, r := range runs {
			if r.Key == "OMNI-1" && r.Status == "completed" {
				done = r
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	if done.Usage.Turns != 3 {
		t.Errorf("run usage %+v", done.Usage)
	}

	note := getText(t, base+"/api/workspaces/"+wsID+"/runs/"+done.RunID+"/note?kind=triage")
	if !strings.Contains(note, "OMNI-1") {
		t.Errorf("triage note does not name the ticket:\n%s", note)
	}

	var page struct {
		Events []app.RunEvent `json:"events"`
		Next   int            `json:"next"`
	}
	getJSON(t, base+"/api/workspaces/"+wsID+"/runs/"+done.RunID+"/events?after=0", &page)
	kinds := make([]string, 0, len(page.Events))
	for _, e := range page.Events {
		kinds = append(kinds, e.Kind)
	}
	if strings.Join(kinds, ",") != "system,usage,final" {
		t.Errorf("event kinds %v", kinds)
	}
	// `next` is what a following call passes as `after`, so it is the index
	// of the last line — one-based, like the watcher's.
	if page.Next != len(page.Events) {
		t.Errorf("next %d for %d events", page.Next, len(page.Events))
	}

	select {
	case e := <-finished:
		if e.JobID != app.JobID(started.JobID) {
			t.Errorf("job.finished carried job %q, want %q", e.JobID, started.JobID)
		}
		if len(e.Outcomes) != 1 || e.Outcomes[0].Key != "OMNI-1" || e.Outcomes[0].Status != "completed" {
			t.Errorf("job.finished outcomes %+v", e.Outcomes)
		}
	case <-time.After(30 * time.Second):
		t.Error("the event stream never delivered job.finished")
	}
}

// TestServeWriteFlowsEndToEnd drives the three routes the browser surface
// added over the read-only one — add a run to the golden set, score the
// golden set, and start a fix — against a real workspace, a real git
// repository and the fake provider. Between them they are everything the
// UI can make this machine do that is not a triage.
//
// The fix is a dry run: it cuts the branch and writes the prompt and starts
// no agent, which is the whole of the flow that can be asserted without a
// model and a remote to push to.
func TestServeWriteFlowsEndToEnd(t *testing.T) {
	root, _ := newWorkspace(t, fakeClaude)
	t.Setenv("SIRDAR_FAKE_DOC", filepath.Join(testdataDir, "triage-doc.json"))
	initRepo(t, root)

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	registryPath, err := app.DefaultRegistryPath()
	if err != nil {
		t.Fatal(err)
	}
	reg := &app.Registry{Path: registryPath}
	wsID, err := ensureRegistered(reg, root)
	if err != nil {
		t.Fatal(err)
	}

	// The golden set is the shell's, named once and never by a request.
	golden := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	svc := app.New(reg, app.BuildDeps, app.Options{
		Interval:  20 * time.Millisecond,
		Stderr:    io.Discard,
		GoldenDir: golden,
	})
	svc.Start(ctx)

	pr, pw := io.Pipe()
	exited := make(chan int, 1)
	go func() {
		exited <- serveHTTP(ctx, httpapi.New(svc, ui.FS(), httpapi.LoopbackOnly(true)),
			"127.0.0.1:0", false, pw, io.Discard)
	}()
	t.Cleanup(func() {
		cancel()
		svc.Stop()
		select {
		case code := <-exited:
			if code != 0 {
				t.Errorf("serve exited %d", code)
			}
		case <-time.After(10 * time.Second):
			t.Error("serve did not return after its context was cancelled")
		}
	})

	line, err := bufio.NewReader(pr).ReadString('\n')
	if err != nil {
		t.Fatalf("reading the address line: %v", err)
	}
	base := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "Sirdar UI:"))
	ws := base + "/api/workspaces/" + wsID

	// A triage first: everything else here reads what it leaves behind.
	postJSON(t, ws+"/triage", `{"keys":["OMNI-1"]}`, http.StatusAccepted, nil)
	triage := waitForRun(t, base, wsID, "OMNI-1", "triage")

	// --- the golden set ---------------------------------------------
	var entry app.GoldenEntry
	postJSON(t, ws+"/golden", `{"runId":"`+triage.RunID+`"}`, http.StatusOK, &entry)
	if entry.Key != "OMNI-1" {
		t.Fatalf("golden entry %+v", entry)
	}
	if _, err := os.Stat(filepath.Join(golden, "OMNI-1", "bundle", "ticket.json")); err != nil {
		t.Fatalf("the run's bundle did not reach the golden set: %v", err)
	}
	var listed []app.GoldenEntry
	getJSON(t, ws+"/golden", &listed)
	if len(listed) != 1 || listed[0].Key != "OMNI-1" {
		t.Fatalf("golden list %+v", listed)
	}

	// --- the eval ----------------------------------------------------
	postJSON(t, ws+"/eval", `{"keys":["OMNI-1"]}`, http.StatusAccepted, nil)
	var reports []app.EvalReport
	waitFor(t, "an eval report", func() bool {
		reports = nil
		getJSON(t, ws+"/eval", &reports)
		return len(reports) > 0
	})
	report := reports[0]
	if report.Path == "" || len(report.Results) != 1 {
		t.Fatalf("eval report %+v", report)
	}
	if got := report.Results[0]; got.Key != "OMNI-1" || got.State != "completed" {
		t.Fatalf("eval result %+v", got)
	}
	// An eval run is a measurement, not a record of the ticket: it must
	// not have filed a note or reached the register.
	var rows []app.RegisterRow
	getJSON(t, ws+"/register", &rows)
	for _, row := range rows {
		if row.RunID == report.Results[0].RunID {
			t.Errorf("the eval run reached the register: %+v", row)
		}
	}

	// --- the fix -----------------------------------------------------
	postJSON(t, ws+"/fix", `{"key":"OMNI-1","dryRun":true}`, http.StatusAccepted, nil)
	fixRun := waitForRun(t, base, wsID, "OMNI-1", "fix")

	var detail app.RunDetail
	getJSON(t, ws+"/runs/"+fixRun.RunID, &detail)
	if detail.Fix == nil || detail.Fix.Branch == "" {
		t.Fatalf("the fix run recorded no branch: %+v", detail.Fix)
	}
	if detail.Fix.Pushed || detail.Fix.PRURL != "" {
		t.Errorf("a dry run pushed something: %+v", detail.Fix)
	}
	if out := git(t, root, "rev-parse", "--verify", "--quiet", "refs/heads/"+detail.Fix.Branch); out == "" {
		t.Errorf("the fix branch %s was not cut", detail.Fix.Branch)
	}
	// The prompt is the other half of what a dry run is for.
	if prompt := getText(t, ws+"/runs/"+fixRun.RunID+"/prompt"); !strings.Contains(prompt, "OMNI-1") {
		t.Errorf("the fix prompt does not name the ticket:\n%s", prompt)
	}
	// A fix run has no triage note of its own, so the run screen asks for
	// its own note.md with no kind. A dry run wrote none, so this is a 404
	// — the empty tab the UI shows — and not the 400 a kindless read used
	// to be. Asking a fix run for a *triage* note is the mismatch.
	if code := status(t, ws+"/runs/"+fixRun.RunID+"/note"); code != http.StatusNotFound {
		t.Errorf("the fix run's own note answered %d, want 404", code)
	}
	if code := status(t, ws+"/runs/"+fixRun.RunID+"/note?kind=triage"); code != http.StatusNotFound {
		t.Errorf("a fix run's \"triage\" note answered %d, want 404", code)
	}
	// On the triage run, the two are the same document.
	if own, named := getText(t, ws+"/runs/"+triage.RunID+"/note"),
		getText(t, ws+"/runs/"+triage.RunID+"/note?kind=triage"); own != named {
		t.Error("the run's own note and its triage note are different documents")
	}
}

// status is the response code of a GET, for the routes whose absence is
// the thing being asserted.
func status(t *testing.T, url string) int {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode
}

// initRepo turns a test workspace into the git checkout a fix needs: one
// commit, a bare origin, and Sirdar's own run records excluded the way
// `sirdar init` excludes them.
func initRepo(t *testing.T, root string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}
	git(t, root, "init", "-q", "-b", "main", ".")
	// The commits are throwaway but still need an identity to be made at
	// all. The machine's own is used as it stands; a checkout with none
	// skips rather than inventing one.
	if err := exec.Command("git", "-C", root, "var", "GIT_AUTHOR_IDENT").Run(); err != nil {
		t.Skip("git has no author identity configured in this environment")
	}
	if err := os.MkdirAll(filepath.Join(root, ".git", "info"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".git", "info", "exclude"),
		[]byte(strings.Join(gitExcludes, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("# fixture\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, root, "add", "-A")
	git(t, root, "commit", "-q", "-m", "init")

	origin := filepath.Join(t.TempDir(), "origin.git")
	git(t, root, "init", "-q", "--bare", "-b", "main", origin)
	git(t, root, "remote", "add", "origin", origin)
	git(t, root, "push", "-q", "-u", "origin", "main")
	git(t, root, "remote", "set-head", "origin", "main")
}

// git runs a git command in dir, failing the test on anything but success.
func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// serveDeadline is how long the polling helpers give a real agent run.
const serveDeadline = 60 * time.Second

// waitFor polls until done answers true, the way the board polls.
func waitFor(t *testing.T, what string, done func() bool) {
	t.Helper()
	deadline := time.Now().Add(serveDeadline)
	for !done() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// waitForRun polls the run list until a run of this kind for this key has
// finished, and fails if it finished as anything but completed.
func waitForRun(t *testing.T, base, wsID, key, kind string) app.RunSummary {
	t.Helper()
	var found app.RunSummary
	waitFor(t, kind+" run for "+key, func() bool {
		var runs []app.RunSummary
		getJSON(t, base+"/api/workspaces/"+wsID+"/runs", &runs)
		for _, r := range runs {
			if r.Key != key || r.Kind != kind {
				continue
			}
			switch r.Status {
			case "completed", "failed", "over_budget", "blocked":
				found = r
				return true
			}
		}
		return false
	})
	if found.Status != "completed" {
		t.Fatalf("the %s run ended %s: %s", kind, found.Status, found.Reason)
	}
	return found
}

// watchEvents opens the SSE stream and returns a channel carrying the first
// event of the named kind. The request lives until the test ends.
func watchEvents(t *testing.T, url, kind string) <-chan app.Event {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	resp, err := http.DefaultClient.Do(req.WithContext(ctx))
	if err != nil {
		t.Fatal(err)
	}
	if got := resp.Header.Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("event stream content type %q", got)
	}

	out := make(chan app.Event, 1)
	go func() {
		defer resp.Body.Close()
		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 0, 64<<10), 4<<20)
		named := false
		for scanner.Scan() {
			switch line := scanner.Text(); {
			case line == "event: "+kind:
				named = true
			case named && strings.HasPrefix(line, "data: "):
				var e app.Event
				if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &e) == nil {
					out <- e
					return
				}
				named = false
			case line == "":
			default:
				named = false
			}
		}
	}()
	// The handler subscribes just after it flushes the headers Do waited
	// for; the first event is a whole agent run away, but do not race it.
	time.Sleep(50 * time.Millisecond)
	return out
}

func getJSON(t *testing.T, url string, dst any) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: %s\n%s", url, resp.Status, body)
	}
	if err := json.Unmarshal(body, dst); err != nil {
		t.Fatalf("GET %s: %v\n%s", url, err, body)
	}
}

func getText(t *testing.T, url string) string {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: %s\n%s", url, resp.Status, body)
	}
	return string(body)
}

func postJSON(t *testing.T, url, body string, want int, dst any) {
	t.Helper()
	resp, err := http.Post(url, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	answer, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != want {
		t.Fatalf("POST %s: %s, want %d\n%s", url, resp.Status, want, answer)
	}
	if dst != nil {
		if err := json.Unmarshal(answer, dst); err != nil {
			t.Fatalf("POST %s: %v\n%s", url, err, answer)
		}
	}
}

// appendWebhooks bolts a webhooks block onto a test workspace's config.
func appendWebhooks(t *testing.T, root, block string) {
	t.Helper()
	path := filepath.Join(root, ".sirdar", "config.yaml")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(body, []byte(block)...), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestServeHooksOffByDefault(t *testing.T) {
	root, _ := newWorkspace(t, fakeClaude)
	var errb bytes.Buffer
	opts, ok := serveHooks(root, app.WorkspaceID(root), false, &errb)
	if !ok {
		t.Fatalf("serveHooks failed: %s", errb.String())
	}
	if len(opts) != 0 {
		t.Error("a workspace that configures no webhooks still got the hook routes")
	}
	if errb.Len() != 0 {
		t.Errorf("stderr %q", errb.String())
	}
}

// An operator who turns the hooks on is told the listener they are on
// cannot be reached by a hosted tracker, and what to do about it.
func TestServeHooksWarnsAboutReachability(t *testing.T) {
	root, _ := newWorkspace(t, fakeClaude)
	t.Setenv("SIRDAR_TEST_HOOK_SECRET", "s3cret")
	appendWebhooks(t, root, "\nwebhooks:\n  enabled: true\n  sources:\n    generic:\n      secret: env:SIRDAR_TEST_HOOK_SECRET\n")

	var loopback bytes.Buffer
	opts, ok := serveHooks(root, app.WorkspaceID(root), false, &loopback)
	if !ok || len(opts) != 1 {
		t.Fatalf("serveHooks: ok=%v opts=%d stderr %q", ok, len(opts), loopback.String())
	}
	if !strings.Contains(loopback.String(), "--allow-remote") {
		t.Errorf("stderr %q does not say how to expose the endpoints", loopback.String())
	}
	if !strings.Contains(loopback.String(), "generic") {
		t.Errorf("stderr %q does not name the enabled source", loopback.String())
	}
	// The hook URL is served under one workspace id, so the line has to
	// print the id rather than a placeholder the operator has to look up.
	if !strings.Contains(loopback.String(), app.WorkspaceID(root)) {
		t.Errorf("stderr %q does not name the workspace the hooks are served for", loopback.String())
	}

	var remote bytes.Buffer
	if _, ok := serveHooks(root, app.WorkspaceID(root), true, &remote); !ok {
		t.Fatalf("serveHooks: %s", remote.String())
	}
	if !strings.Contains(remote.String(), "TLS") {
		t.Errorf("stderr %q does not warn about TLS", remote.String())
	}
}

// A secret that cannot be resolved stops the command: a hook endpoint that
// is up but rejects every delivery is worse than one that never started.
func TestServeHooksStopsOnAnUnresolvableSecret(t *testing.T) {
	root, _ := newWorkspace(t, fakeClaude)
	t.Setenv("SIRDAR_TEST_HOOK_SECRET", "")
	appendWebhooks(t, root, "\nwebhooks:\n  enabled: true\n  sources:\n    generic:\n      secret: env:SIRDAR_TEST_HOOK_SECRET\n")

	var errb bytes.Buffer
	if _, ok := serveHooks(root, app.WorkspaceID(root), false, &errb); ok {
		t.Fatal("a missing secret still started the hooks")
	}
	if !strings.Contains(errb.String(), "SIRDAR_TEST_HOOK_SECRET") {
		t.Errorf("stderr %q does not name the missing credential", errb.String())
	}
}
