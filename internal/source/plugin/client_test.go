package plugin

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/source"
)

func buildFileAdapter(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "file-adapter")
	cmd := exec.Command("go", "build", "-o", bin, "../../../examples/adapters/file")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	return bin
}

func TestClientRoundTrip(t *testing.T) {
	bin := buildFileAdapter(t)
	var stderr bytes.Buffer
	c, err := Start(context.Background(), bin+` -file "testdata/tickets.json"`, &stderr)
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

// stuckAdapter builds a script that answers exactly one request (describe,
// always sent first and so always id 1) and then hangs: it never reads or
// answers anything after that, simulating an adapter wedged mid-request.
func stuckAdapter(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "stuck.sh")
	script := "#!/bin/sh\n" +
		"read line\n" +
		`echo '{"id":1,"result":{"name":"stuck","roles":["tracker"],"version":"1"}}'` + "\n" +
		"sleep 60\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestClientCloseUnblocksHangingCall(t *testing.T) {
	// Reproduces the deadlock this test guards against: call() must not
	// hold c.mu for the duration of a blocking wait, or Close can never
	// acquire it to send shutdown and start its kill timer.
	bin := stuckAdapter(t)
	c, err := Start(context.Background(), bin, &bytes.Buffer{})
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
