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
	root, _ := newWorkspace(t, "fakeclaude.sh")
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
	go func() { exited <- serveHTTP(ctx, httpapi.New(svc, ui.FS()), "127.0.0.1:0", false, pw, io.Discard) }()
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
	root, _ := newWorkspace(t, "fakeclaude.sh")
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
	root, _ := newWorkspace(t, "fakeclaude.sh")
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
	root, _ := newWorkspace(t, "fakeclaude.sh")
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
