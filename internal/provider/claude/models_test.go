package claude

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/testbin"
)

// fakeProbe is a `claude` that answers a probe the way the real CLI does:
// a hook line, then the system/init line naming what the alias resolved
// to, and then — because a real session would go on to call the API — it
// hangs, so a probe that waited for the result line would time out. An
// alias it does not know is refused on stderr, as the CLI refuses a model
// a login cannot use.
func fakeProbe() int {
	args := testbin.Args()
	if !slices.Contains(args, "--no-session-persistence") || !slices.Contains(args, "--strict-mcp-config") {
		fmt.Fprintln(os.Stderr, "probe args missing", args)
		return 2
	}
	i := slices.Index(args, "--model")
	alias := ""
	if i >= 0 && i+1 < len(args) {
		alias = args[i+1]
	}
	resolved := map[string]string{
		"opus":   "claude-opus-4-5-20251101",
		"sonnet": "claude-sonnet-4-5-20250929",
		"haiku":  "claude-haiku-4-5-20251001",
	}[alias]
	if resolved == "" {
		fmt.Fprintf(os.Stderr, "There's an issue with the selected model (%s).\n", alias)
		return 1
	}
	fmt.Println(`{"type":"system","subtype":"hook_started","hook_name":"SessionStart"}`)
	fmt.Printf(`{"type":"system","subtype":"init","cwd":"/tmp/x","session_id":"s","tools":[],"model":%q,"permissionMode":"default"}`+"\n", resolved)
	time.Sleep(time.Minute)
	return 0
}

func TestInitModelReadsTheFixture(t *testing.T) {
	raw, err := os.ReadFile("testdata/probe-init.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	got, err := initModel(strings.NewReader(string(raw)))
	if err != nil || got != "claude-opus-4-5-20251101" {
		t.Fatalf("initModel = %q, %v", got, err)
	}
}

func TestInitModelWithoutAnInitLine(t *testing.T) {
	for _, in := range []string{
		"",
		"not json\n" + `{"type":"result","subtype":"error"}` + "\n",
		`{"type":"system","subtype":"init","model":"  "}` + "\n",
	} {
		if got, err := initModel(strings.NewReader(in)); !errors.Is(err, ErrNoInit) || got != "" {
			t.Fatalf("initModel(%q) = %q, %v; want ErrNoInit", in, got, err)
		}
	}
}

func TestProbeModelStopsAtTheInitLine(t *testing.T) {
	bin := testbin.Install(t, t.TempDir(), "claude-probe", "claude-probe")
	start := time.Now()
	got, err := ProbeModel(context.Background(), bin, "opus", os.Environ())
	if err != nil || got != "claude-opus-4-5-20251101" {
		t.Fatalf("ProbeModel = %q, %v", got, err)
	}
	// The fake hangs after the init line; the probe must not wait for it.
	if d := time.Since(start); d > 10*time.Second {
		t.Fatalf("probe took %s: it waited past the init line", d)
	}
}

func TestProbeModelReportsARefusal(t *testing.T) {
	bin := testbin.Install(t, t.TempDir(), "claude-probe", "claude-probe")
	_, err := ProbeModel(context.Background(), bin, "fable", os.Environ())
	if !errors.Is(err, ErrNoInit) || !strings.Contains(err.Error(), "issue with the selected model") {
		t.Fatalf("err = %v", err)
	}
}

func TestProbeArgsAreTheCheapestSession(t *testing.T) {
	a := strings.Join(probeArgs("sonnet"), " ")
	for _, want := range []string{"-p .", "--max-turns 1", "--output-format stream-json", "--model sonnet", "--no-session-persistence", "--strict-mcp-config"} {
		if !strings.Contains(a, want) {
			t.Fatalf("probe args %q lack %q", a, want)
		}
	}
}
