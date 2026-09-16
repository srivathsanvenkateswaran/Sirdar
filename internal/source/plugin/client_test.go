package plugin

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/source"
	"github.com/srivathsanvenkateswaran/sirdar/internal/testbin"
)

// TestMain is also every fake adapter these tests drive. testbin.Dispatch
// turns this binary into the fake it was started as, which is the only way
// an adapter fixture exists on every operating system: a #!/bin/sh script
// is not an executable on Windows — CreateProcess refuses it with "%1 is
// not a valid Win32 application" — and the stuck adapter below used to be
// exactly that.
func TestMain(m *testing.M) {
	testbin.Dispatch(map[string]func() int{
		"stuckadapter": stuckAdapterMain,
	})
	os.Exit(m.Run())
}

// quoted wraps an adapter path for Start, whose splitCommand breaks the
// command on spaces. A temp directory can contain one, and on Windows
// routinely sits under a user profile name that does.
func quoted(path string) string { return `"` + path + `"` }

func buildFileAdapter(t *testing.T) string {
	t.Helper()
	// The ".exe" matters: on Windows an extensionless file is not an
	// executable at all, and exec refuses to start one with "executable
	// file not found in %PATH%" even when handed its full path.
	bin := filepath.Join(t.TempDir(), "file-adapter"+testbin.Ext)
	cmd := exec.Command("go", "build", "-o", bin, "../../../examples/adapters/file")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	return bin
}

func TestClientRoundTrip(t *testing.T) {
	bin := buildFileAdapter(t)
	var stderr bytes.Buffer
	c, err := Start(context.Background(), quoted(bin)+` -file "testdata/tickets.json"`, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	d, err := c.Describe(context.Background())
	if err != nil || d.Name != "file" || len(d.Roles) != 2 {
		t.Fatalf("%+v %v", d, err)
	}

	tt, err := c.Get(context.Background(), "OMNI-1")
	if err != nil || tt.Title != "Export fails" || tt.HelpdeskRef != "555" {
		t.Fatalf("%+v %v", tt, err)
	}

	if _, err := c.Get(context.Background(), "OMNI-404"); err == nil {
		t.Fatal("want not_found")
	} else if se, ok := err.(*source.Error); !ok || se.Code != source.NotFound {
		t.Fatalf("got %v", err)
	}

	th, err := c.Threads(context.Background(), "555")
	if err != nil || len(th) != 2 || th[0].Role != "customer" {
		t.Fatalf("%+v %v", th, err)
	}

	dir := t.TempDir()
	atts, err := c.Attachments(context.Background(), "555", filepath.Join(dir, "attachments"))
	if err != nil || len(atts) != 1 {
		t.Fatalf("%+v %v", atts, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "attachments", "1-shot.png")); err != nil {
		t.Fatal(err)
	}
	if atts[0].Path != "attachments/1-shot.png" {
		t.Fatal(atts[0].Path)
	}
}

func TestClientShutdownTimeout(t *testing.T) {
	// A process that ignores shutdown must be killed within ~5s.
	c, err := Start(context.Background(), "sleep 60", &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { c.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(8 * time.Second):
		t.Fatal("Close did not return")
	}
}

// stuckAdapterMain answers exactly one request (describe, always sent
// first and so always id 1) and then hangs: it never reads or answers
// anything after that, simulating an adapter wedged mid-request. It runs
// in a copy of this test binary that stuckAdapter installs, dispatched by
// TestMain on the name it was installed under.
func stuckAdapterMain() int {
	// Consuming the describe line keeps the client's write from blocking
	// on a full pipe, and keeps this process from turning that write into
	// a broken pipe by exiting into it.
	bufio.NewReader(os.Stdin).ReadString('\n')
	fmt.Println(`{"id":1,"result":{"name":"stuck","roles":["tracker"],"version":"1"}}`)
	time.Sleep(60 * time.Second)
	return 0
}

// stuckAdapter installs stuckAdapterMain as a real executable and returns
// the command that starts it.
func stuckAdapter(t *testing.T) string {
	t.Helper()
	return quoted(testbin.Install(t, t.TempDir(), "stuckadapter", "stuckadapter"))
}

func TestClientCloseUnblocksHangingCall(t *testing.T) {
	// Reproduces the deadlock this test guards against: call() must not
	// hold c.mu for the duration of a blocking wait, or Close can never
	// acquire it to send shutdown and start its kill timer.
	command := stuckAdapter(t)
	c, err := Start(context.Background(), command, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := c.Describe(context.Background()); err != nil {
		t.Fatalf("describe: %v", err)
	}

	getErr := make(chan error, 1)
	go func() {
		_, err := c.Get(context.Background(), "OMNI-1")
		getErr <- err
	}()
	time.Sleep(100 * time.Millisecond) // let Get actually block in call()

	closeDone := make(chan struct{})
	go func() { c.Close(); close(closeDone) }()

	select {
	case <-closeDone:
	case <-time.After(7 * time.Second):
		t.Fatal("Close did not return")
	}

	select {
	case err := <-getErr:
		if err == nil {
			t.Fatal("want error from Get once the adapter is closed")
		}
	case <-time.After(1 * time.Second):
		t.Fatal("Get did not return after Close")
	}
}

// TestSplitCommandExpandsTildeEverywhere is D3: only argv[0] was expanded,
// so an adapter started as
// `~/bin/sirdar-janus --token-cmd-file ~/.sirdar/janus-token.sh` was handed
// the literal string "~/.sirdar/janus-token.sh". There is no shell in the
// path to expand it, the token command failed, and doctor still said the
// tracker was fine.
func TestSplitCommandExpandsTildeEverywhere(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory")
	}
	argv := splitCommand(`~/bin/sirdar-janus --token-cmd-file ~/.sirdar/janus-token.sh --flag value`)
	want := []string{
		filepath.Join(home, "bin/sirdar-janus"),
		"--token-cmd-file",
		filepath.Join(home, ".sirdar/janus-token.sh"),
		"--flag",
		"value",
	}
	if len(argv) != len(want) {
		t.Fatalf("argv %q", argv)
	}
	for i := range want {
		if argv[i] != want[i] {
			t.Fatalf("argv[%d] = %q, want %q", i, argv[i], want[i])
		}
	}
}
