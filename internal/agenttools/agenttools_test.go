package agenttools

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// toolByName finds one tool in a set, failing the test when it is missing.
func toolByName(t *testing.T, tools []Tool, name string) Tool {
	t.Helper()
	for _, tool := range tools {
		if tool.Spec().Name == name {
			return tool
		}
	}
	t.Fatalf("tool %q not in the read-only set", name)
	return nil
}

func call(t *testing.T, tool Tool, args string) (string, error) {
	t.Helper()
	return tool.Call(context.Background(), json.RawMessage(args))
}

func mustCall(t *testing.T, tool Tool, args string) string {
	t.Helper()
	out, err := call(t, tool, args)
	if err != nil {
		t.Fatalf("%s(%s): %v", tool.Spec().Name, args, err)
	}
	return out
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// --- path confinement ---------------------------------------------------

func TestResolveRejectsPathsOutsideRoot(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	writeFile(t, filepath.Join(outside, "secret.txt"), "top secret\n")
	writeFile(t, filepath.Join(root, "inside.txt"), "fine\n")

	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}

	o := Options{Root: root}.normalized()
	cases := []struct {
		name string
		path string
	}{
		{"parent traversal", "../" + filepath.Base(outside) + "/secret.txt"},
		{"absolute outside", filepath.Join(outside, "secret.txt")},
		{"symlink to outside", "escape/secret.txt"},
		{"symlinked directory itself", "escape"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := o.resolve(tc.path); err != errPathEscape {
				t.Fatalf("resolve(%q) = %v, want %v", tc.path, err, errPathEscape)
			}
		})
	}

	if _, err := o.resolve("inside.txt"); err != nil {
		t.Fatalf("resolve of an in-workspace file: %v", err)
	}
	if _, err := o.resolve("."); err != nil {
		t.Fatalf("resolve of the root itself: %v", err)
	}

	// The confinement is enforced by the tools, not just by resolve.
	tools := ReadOnlySet(Options{Root: root})
	if _, err := call(t, toolByName(t, tools, "read_file"), `{"path":"escape/secret.txt"}`); err == nil ||
		!strings.Contains(err.Error(), "path escapes workspace") {
		t.Fatalf("read_file through a symlink: err = %v, want path escapes workspace", err)
	}
	if _, err := call(t, toolByName(t, tools, "list_dir"), `{"path":".."}`); err == nil ||
		!strings.Contains(err.Error(), "path escapes workspace") {
		t.Fatalf("list_dir on the parent: err = %v, want path escapes workspace", err)
	}
}

// --- read_file ----------------------------------------------------------

func TestReadFileLineNumbersOffsetAndLimit(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "sample.txt"), "alpha\nbravo\ncharlie\ndelta\n")
	read := toolByName(t, ReadOnlySet(Options{Root: root}), "read_file")

	if got, want := mustCall(t, read, `{"path":"sample.txt"}`), "1\talpha\n2\tbravo\n3\tcharlie\n4\tdelta\n"; got != want {
		t.Fatalf("read_file whole file = %q, want %q", got, want)
	}
	if got, want := mustCall(t, read, `{"path":"sample.txt","offset":2,"limit":2}`), "2\tbravo\n3\tcharlie\n[truncated: line limit 2 reached]\n"; got != want {
		t.Fatalf("read_file offset/limit = %q, want %q", got, want)
	}
	out := mustCall(t, read, `{"path":"sample.txt","offset":2,"limit":1}`)
	if !strings.Contains(out, "[truncated: line limit 1 reached]") {
		t.Fatalf("read_file limit marker missing: %q", out)
	}
	out = mustCall(t, read, `{"path":"sample.txt","offset":99}`)
	if !strings.Contains(out, "[no lines") {
		t.Fatalf("read_file past EOF = %q", out)
	}
}

func TestReadFileRejectsBinaryAndDirectories(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "blob.bin"), []byte{0x7f, 'E', 'L', 'F', 0x00, 0x01, 0x02}, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	read := toolByName(t, ReadOnlySet(Options{Root: root}), "read_file")

	if _, err := call(t, read, `{"path":"blob.bin"}`); err == nil || !strings.Contains(err.Error(), "binary") {
		t.Fatalf("read_file on a binary file: err = %v", err)
	}
	if _, err := call(t, read, `{"path":"sub"}`); err == nil || !strings.Contains(err.Error(), "is a directory") {
		t.Fatalf("read_file on a directory: err = %v", err)
	}
	if _, err := call(t, read, `{}`); err == nil || !strings.Contains(err.Error(), "path is required") {
		t.Fatalf("read_file without a path: err = %v", err)
	}
}

func TestOutputTruncationMarker(t *testing.T) {
	root := t.TempDir()
	var b strings.Builder
	for i := 0; i < 200; i++ {
		b.WriteString("a line of text that is long enough to matter\n")
	}
	writeFile(t, filepath.Join(root, "big.txt"), b.String())

	read := toolByName(t, ReadOnlySet(Options{Root: root, MaxOutputBytes: 300}), "read_file")
	out := mustCall(t, read, `{"path":"big.txt"}`)
	if len(out) > 300+64 {
		t.Fatalf("output %d bytes, want it capped near 300", len(out))
	}
	if !strings.Contains(out, "[truncated ") || !strings.HasSuffix(out, " bytes]") {
		t.Fatalf("truncation marker missing: %q", out)
	}
	if !strings.HasPrefix(out, "1\ta line") {
		t.Fatalf("truncated output should still start at line 1: %q", out)
	}
}

// --- list_dir -----------------------------------------------------------

func TestListDirMarksDirectoriesAndKeepsHiddenEntries(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "zeta.txt"), "z\n")
	writeFile(t, filepath.Join(root, ".env"), "SECRET=1\n")
	writeFile(t, filepath.Join(root, "sub", "nested.txt"), "n\n")

	list := toolByName(t, ReadOnlySet(Options{Root: root}), "list_dir")
	got := mustCall(t, list, `{"path":"."}`)
	want := ".env\nsub/\nzeta.txt\n"
	if got != want {
		t.Fatalf("list_dir = %q, want %q", got, want)
	}
	if got := mustCall(t, list, `{"path":"sub"}`); got != "nested.txt\n" {
		t.Fatalf("list_dir of a subdirectory = %q", got)
	}
}

// --- grep ---------------------------------------------------------------

func TestGrepGoFallbackSkipsNoiseAndBinaries(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "main.go"), "package main\n// needle here\n")
	writeFile(t, filepath.Join(root, "notes", "todo.md"), "a needle in prose\n")
	writeFile(t, filepath.Join(root, "node_modules", "dep", "index.js"), "// needle in a dependency\n")
	writeFile(t, filepath.Join(root, ".sirdar", "runs", "r1", "transcript.txt"), "needle from an earlier run\n")
	if err := os.WriteFile(filepath.Join(root, "blob.bin"), append([]byte("needle"), 0x00, 0x01), 0o644); err != nil {
		t.Fatal(err)
	}

	// Force the dependency-free walk rather than ripgrep.
	saved := rgPath
	rgPath = ""
	t.Cleanup(func() { rgPath = saved })

	grep := toolByName(t, ReadOnlySet(Options{Root: root}), "grep")
	out := mustCall(t, grep, `{"pattern":"needle"}`)
	if !strings.Contains(out, "main.go:2:// needle here") {
		t.Fatalf("grep missed the Go file: %q", out)
	}
	if !strings.Contains(out, "notes/todo.md:1:a needle in prose") {
		t.Fatalf("grep missed the nested file: %q", out)
	}
	for _, unwanted := range []string{"node_modules", ".sirdar/runs", "blob.bin"} {
		if strings.Contains(out, unwanted) {
			t.Fatalf("grep should have skipped %s: %q", unwanted, out)
		}
	}

	if got := mustCall(t, grep, `{"pattern":"needle","glob":"**/*.go"}`); got != "main.go:2:// needle here\n" {
		t.Fatalf("grep with a glob = %q", got)
	}
	if got := mustCall(t, grep, `{"pattern":"needle","path":"notes"}`); !strings.HasPrefix(got, "notes/todo.md:1:") {
		t.Fatalf("grep scoped to a directory = %q", got)
	}
	if got := mustCall(t, grep, `{"pattern":"no such text"}`); got != "[no matches]" {
		t.Fatalf("grep with no matches = %q", got)
	}
	if _, err := call(t, grep, `{"pattern":"("}`); err == nil || !strings.Contains(err.Error(), "invalid pattern") {
		t.Fatalf("grep with a bad regexp: err = %v", err)
	}
	if _, err := call(t, grep, `{"pattern":"needle","path":"../.."}`); err == nil ||
		!strings.Contains(err.Error(), "path escapes workspace") {
		t.Fatalf("grep outside the workspace: err = %v", err)
	}

	writeFile(t, filepath.Join(root, "many.txt"), strings.Repeat("needle\n", 10))
	out = mustCall(t, grep, `{"pattern":"needle","maxResults":3}`)
	if lines := strings.Count(out, "\n"); lines > 4 {
		t.Fatalf("maxResults not honoured, got %d lines: %q", lines, out)
	}
}

// --- glob ---------------------------------------------------------------

func TestGlobDoubleStar(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "main.go"), "x\n")
	writeFile(t, filepath.Join(root, "internal", "app", "run.go"), "x\n")
	writeFile(t, filepath.Join(root, "internal", "app", "run_test.go"), "x\n")
	writeFile(t, filepath.Join(root, "README.md"), "x\n")
	writeFile(t, filepath.Join(root, ".git", "config"), "x\n")

	glob := toolByName(t, ReadOnlySet(Options{Root: root}), "glob")
	got := mustCall(t, glob, `{"pattern":"**/*.go"}`)
	want := "internal/app/run.go\ninternal/app/run_test.go\nmain.go\n"
	if got != want {
		t.Fatalf("glob **/*.go = %q, want %q", got, want)
	}
	if got := mustCall(t, glob, `{"pattern":"internal/*/run.go"}`); got != "internal/app/run.go\n" {
		t.Fatalf("glob with a single-segment wildcard = %q", got)
	}
	if got := mustCall(t, glob, `{"pattern":"*.md"}`); got != "README.md\n" {
		t.Fatalf("glob on base names = %q", got)
	}
	if got := mustCall(t, glob, `{"pattern":"**/*.rs"}`); got != "[no matches]" {
		t.Fatalf("glob with no matches = %q", got)
	}
	if got := mustCall(t, glob, `{"pattern":"**/config"}`); strings.Contains(got, ".git") {
		t.Fatalf("glob should skip .git: %q", got)
	}
}

// --- bash ---------------------------------------------------------------

func TestBashAllowListAndDenial(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "here.txt"), "x\n")
	tools := ReadOnlySet(Options{Root: root, BashAllow: []string{"echo *", "ls", "false"}})
	bash := toolByName(t, tools, "bash")

	if got := mustCall(t, bash, `{"command":"echo hello"}`); got != "hello\n" {
		t.Fatalf("bash echo = %q", got)
	}
	if got := mustCall(t, bash, `{"command":"ls"}`); !strings.Contains(got, "here.txt") {
		t.Fatalf("bash runs in the workspace root, got %q", got)
	}
	if got := mustCall(t, bash, `{"command":"false"}`); !strings.Contains(got, "[exit status 1]") {
		t.Fatalf("bash should report a non-zero exit as output, got %q", got)
	}

	_, err := call(t, bash, `{"command":"rm -rf /"}`)
	if err == nil || err.Error() != "denied: command not in the allow-list" {
		t.Fatalf("denied command: err = %v, want exactly the denial text", err)
	}
	// The allow-list is anchored: a prefix match is not enough.
	if _, err := call(t, bash, `{"command":"ls /etc"}`); err == nil || err.Error() != "denied: command not in the allow-list" {
		t.Fatalf("unanchored match should be denied, err = %v", err)
	}
	// An empty allow-list denies everything.
	empty := toolByName(t, ReadOnlySet(Options{Root: root}), "bash")
	if _, err := call(t, empty, `{"command":"echo hello"}`); err == nil || err.Error() != "denied: command not in the allow-list" {
		t.Fatalf("empty allow-list: err = %v", err)
	}
}

func TestBashTimeout(t *testing.T) {
	root := t.TempDir()
	saved := bashTimeout
	bashTimeout = 100 * time.Millisecond
	t.Cleanup(func() { bashTimeout = saved })

	bash := toolByName(t, ReadOnlySet(Options{Root: root, BashAllow: []string{"sleep *"}}), "bash")
	start := time.Now()
	_, err := call(t, bash, `{"command":"sleep 2"}`)
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("bash timeout: err = %v", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("bash took %s, the timeout should have fired far sooner", elapsed)
	}
}

func TestBashEnvironmentIsMinimal(t *testing.T) {
	t.Setenv("SIRDAR_SECRET_TOKEN", "must-not-leak")
	root := t.TempDir()
	bash := toolByName(t, ReadOnlySet(Options{Root: root, BashAllow: []string{"env"}}), "bash")
	out := mustCall(t, bash, `{"command":"env"}`)
	if strings.Contains(out, "must-not-leak") {
		t.Fatalf("the parent environment leaked into the child: %q", out)
	}
	if !strings.Contains(out, "PATH=") {
		t.Fatalf("PATH should be passed through, got %q", out)
	}
}

// TestBashDeniesASecondCommandBehindAnOperator covers the allow-list hole
// a trailing wildcard opens: "echo *" would otherwise match the whole of
// "echo hi; rm -rf /", because the wildcard spans the semicolon.
func TestBashDeniesASecondCommandBehindAnOperator(t *testing.T) {
	root := t.TempDir()
	bash := toolByName(t, ReadOnlySet(Options{Root: root, BashAllow: []string{"echo *", "head*"}}), "bash")

	for _, command := range []string{
		"echo hi; rm -rf /",
		"echo hi | rm -rf /",
		"echo hi && rm -rf /",
		"echo hi & rm -rf /",
		"echo $(rm -rf /)",
		"echo `rm -rf /`",
	} {
		out, err := call(t, bash, `{"command":`+quote(command)+`}`)
		if err == nil || err.Error() != "denied: command not in the allow-list" {
			t.Errorf("%q: out = %q, err = %v, want the denial text", command, out, err)
		}
	}

	// A pipeline whose every segment is allow-listed still runs.
	if got := mustCall(t, bash, `{"command":"echo hi | head -1"}`); got != "hi\n" {
		t.Fatalf("allowed pipeline = %q", got)
	}
}

// TestBashStaysInTheWorkspace is the confinement the other tools get from
// resolving a path and the bash tool has to get from reading one: `cat *`
// approves cat, not every file on the machine. It is a heuristic and the
// package doc says so; these are the cases it is meant to catch.
func TestBashStaysInTheWorkspace(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module demo\n")
	writeFile(t, filepath.Join(root, "sub", "inner.txt"), "inner\n")
	bash := toolByName(t, ReadOnlySet(Options{Root: root, BashAllow: []string{"cat *"}}), "bash")

	if got := mustCall(t, bash, `{"command":"cat go.mod"}`); got != "module demo\n" {
		t.Fatalf("cat go.mod = %q", got)
	}
	if got := mustCall(t, bash, `{"command":"cat sub/inner.txt"}`); got != "inner\n" {
		t.Fatalf("cat sub/inner.txt = %q", got)
	}
	if got := mustCall(t, bash, `{"command":"cat `+filepath.Join(root, "go.mod")+`"}`); got != "module demo\n" {
		t.Fatalf("an absolute path inside the root = %q", got)
	}

	for _, command := range []string{
		"cat ../../../etc/passwd",
		"cat /etc/passwd",
		"cat ~/.ssh/id_rsa",
		`cat "../../../etc/passwd"`,
	} {
		out, err := call(t, bash, `{"command":`+quote(command)+`}`)
		if err == nil || err.Error() != "denied: command not in the allow-list" {
			t.Errorf("%q: out = %q, err = %v, want the denial text", command, out, err)
		}
	}
}

// TestBashDeniesRedirection covers the other half of what an allow-list
// cannot see: `cat *` says nothing about the file a redirection would
// write, and `cat x > ~/.zshrc` is not the command the pattern approved.
func TestBashDeniesRedirection(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module demo\n")
	bash := toolByName(t, ReadOnlySet(Options{Root: root, BashAllow: []string{"cat *", "git log*", "rg *"}}), "bash")

	for _, command := range []string{
		"git log > /tmp/x",
		"cat go.mod >> ~/.zshrc",
		"rg foo 2>/dev/null",
		"cat < go.mod",
		"rg foo <(git log)",
	} {
		out, err := call(t, bash, `{"command":`+quote(command)+`}`)
		if err == nil || err.Error() != "denied: command not in the allow-list" {
			t.Errorf("%q: out = %q, err = %v, want the denial text", command, out, err)
		}
	}

	// Quoted, the same character is a search pattern.
	if _, err := call(t, bash, `{"command":"rg \"a>b\" go.mod"}`); err != nil {
		t.Errorf("a quoted > was treated as a redirection: %v", err)
	}
}

func quote(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// --- web_fetch ----------------------------------------------------------

// allowLoopback lifts the private-address guard for one test. Every
// web_fetch test necessarily talks to an httptest server on 127.0.0.1,
// which is exactly what the guard is there to refuse in production;
// TestWebFetchRefusesPrivateAddresses covers the guard itself.
func allowLoopback(t *testing.T) {
	t.Helper()
	previous := allowPrivateAddrs
	allowPrivateAddrs = func() bool { return true }
	t.Cleanup(func() { allowPrivateAddrs = previous })
}

func TestWebFetchRefusesPrivateAddresses(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("secret"))
	}))
	defer srv.Close()

	fetch := toolByName(t, ReadOnlySet(Options{Root: t.TempDir(), HTTP: srv.Client()}), "web_fetch")

	out, err := call(t, fetch, `{"url":"`+srv.URL+`/"}`)
	if err == nil || !strings.Contains(err.Error(), "loopback") {
		t.Fatalf("loopback fetch: out = %q, err = %v", out, err)
	}
	if strings.Contains(out, "secret") {
		t.Fatalf("a refused fetch must return nothing: %q", out)
	}
	// The cloud metadata address is the one that matters most: it is
	// link-local, it needs no credentials, and it hands out the machine's.
	if _, err := call(t, fetch, `{"url":"http://169.254.169.254/latest/meta-data/"}`); err == nil ||
		!strings.Contains(err.Error(), "link-local") {
		t.Fatalf("metadata fetch: err = %v", err)
	}
	for _, addr := range []string{"10.0.0.5", "192.168.1.1", "172.16.0.9", "127.0.0.1", "169.254.169.254", "100.64.0.1", "::1", "fd00::1", "fe80::1", "0.0.0.0"} {
		if !blockedIP(net.ParseIP(addr)) {
			t.Errorf("blockedIP(%s) = false, want true", addr)
		}
	}
	for _, addr := range []string{"93.184.216.34", "2606:2800:220:1:248:1893:25c8:1946", "8.8.8.8"} {
		if blockedIP(net.ParseIP(addr)) {
			t.Errorf("blockedIP(%s) = true, want false", addr)
		}
	}
}

func TestWebFetchAcceptsTextAndJSON(t *testing.T) {
	allowLoopback(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/json":
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			_, _ = w.Write([]byte(`{"status":"ok"}`))
		case "/html":
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte("<html><head><style>p{}</style></head><body><h1>Title</h1><p>Hello &amp; goodbye</p></body></html>"))
		case "/png":
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write([]byte("\x89PNG\r\n"))
		case "/missing":
			w.WriteHeader(http.StatusNotFound)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	fetch := toolByName(t, ReadOnlySet(Options{Root: t.TempDir(), HTTP: srv.Client()}), "web_fetch")

	if got := mustCall(t, fetch, `{"url":"`+srv.URL+`/json"}`); got != `{"status":"ok"}` {
		t.Fatalf("web_fetch json = %q", got)
	}
	got := mustCall(t, fetch, `{"url":"`+srv.URL+`/html"}`)
	if !strings.Contains(got, "Title") || !strings.Contains(got, "Hello & goodbye") {
		t.Fatalf("web_fetch html = %q", got)
	}
	if strings.Contains(got, "<") || strings.Contains(got, "p{}") {
		t.Fatalf("web_fetch should strip tags and style content: %q", got)
	}
	if _, err := call(t, fetch, `{"url":"`+srv.URL+`/png"}`); err == nil ||
		!strings.Contains(err.Error(), "unsupported content type") {
		t.Fatalf("web_fetch png: err = %v", err)
	}
	if _, err := call(t, fetch, `{"url":"`+srv.URL+`/missing"}`); err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("web_fetch 404: err = %v", err)
	}
	if _, err := call(t, fetch, `{"url":"file:///etc/passwd"}`); err == nil ||
		!strings.Contains(err.Error(), "unsupported scheme") {
		t.Fatalf("web_fetch file scheme: err = %v", err)
	}
	if _, err := call(t, fetch, `{"url":"/relative"}`); err == nil || !strings.Contains(err.Error(), "absolute") {
		t.Fatalf("web_fetch relative url: err = %v", err)
	}
}

func TestWebFetchCapsBodyAtOneMiB(t *testing.T) {
	allowLoopback(t)
	body := strings.Repeat("0123456789abcdef", (maxFetchBytes/16)+4096)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	fetch := toolByName(t, ReadOnlySet(Options{
		Root:           t.TempDir(),
		HTTP:           srv.Client(),
		MaxOutputBytes: 4 << 20,
	}), "web_fetch")

	got := mustCall(t, fetch, `{"url":"`+srv.URL+`/"}`)
	if !strings.HasSuffix(got, "[truncated at 1 MiB]") {
		t.Fatalf("web_fetch cap marker missing, output %d bytes", len(got))
	}
	if body := strings.TrimSuffix(got, "\n[truncated at 1 MiB]"); len(body) != maxFetchBytes {
		t.Fatalf("web_fetch returned %d body bytes, want %d", len(body), maxFetchBytes)
	}
}

func TestWebFetchRefusesOffHostRedirect(t *testing.T) {
	allowLoopback(t)
	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("internal data"))
	}))
	defer elsewhere.Close()

	var origin *httptest.Server
	origin = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/same" {
			http.Redirect(w, r, origin.URL+"/ok", http.StatusFound)
			return
		}
		if r.URL.Path == "/ok" {
			w.Header().Set("Content-Type", "text/plain")
			_, _ = w.Write([]byte("landed"))
			return
		}
		http.Redirect(w, r, elsewhere.URL+"/", http.StatusFound)
	}))
	defer origin.Close()

	fetch := toolByName(t, ReadOnlySet(Options{Root: t.TempDir(), HTTP: origin.Client()}), "web_fetch")

	out, err := call(t, fetch, `{"url":"`+origin.URL+`/away"}`)
	if err == nil || !strings.Contains(err.Error(), "refusing redirect off the original host") {
		t.Fatalf("off-host redirect: out = %q, err = %v", out, err)
	}
	if strings.Contains(out, "internal data") {
		t.Fatalf("the off-host body must never reach the model: %q", out)
	}
	if got := mustCall(t, fetch, `{"url":"`+origin.URL+`/same"}`); got != "landed" {
		t.Fatalf("same-host redirect should be followed, got %q", got)
	}
}

// --- specs --------------------------------------------------------------

func TestReadOnlySetSpecs(t *testing.T) {
	tools := ReadOnlySet(Options{Root: t.TempDir()})
	want := []string{"read_file", "list_dir", "grep", "glob", "bash", "web_fetch"}
	if len(tools) != len(want) {
		t.Fatalf("ReadOnlySet returned %d tools, want %d", len(tools), len(want))
	}
	for i, name := range want {
		if got := tools[i].Spec().Name; got != name {
			t.Fatalf("tool %d is %q, want %q", i, got, name)
		}
	}
	for _, tool := range tools {
		spec := tool.Spec()
		t.Run(spec.Name, func(t *testing.T) {
			if strings.TrimSpace(spec.Description) == "" {
				t.Fatal("description is empty")
			}
			var schema struct {
				Schema               string                     `json:"$schema"`
				Type                 string                     `json:"type"`
				AdditionalProperties *bool                      `json:"additionalProperties"`
				Properties           map[string]json.RawMessage `json:"properties"`
				Required             []string                   `json:"required"`
			}
			if err := json.Unmarshal(spec.Parameters, &schema); err != nil {
				t.Fatalf("Parameters is not valid JSON: %v", err)
			}
			if schema.Schema != "http://json-schema.org/draft-07/schema#" {
				t.Fatalf("$schema = %q, want draft-07", schema.Schema)
			}
			if schema.Type != "object" {
				t.Fatalf("type = %q, want object", schema.Type)
			}
			if schema.AdditionalProperties == nil || *schema.AdditionalProperties {
				t.Fatal("additionalProperties must be false")
			}
			if len(schema.Required) == 0 {
				t.Fatal("required must name at least one property")
			}
			for _, name := range schema.Required {
				if _, ok := schema.Properties[name]; !ok {
					t.Fatalf("required names %q, which is not a declared property", name)
				}
			}
		})
	}
}

func TestOptionsDefaults(t *testing.T) {
	o := Options{}.normalized()
	if o.MaxOutputBytes != DefaultMaxOutputBytes {
		t.Fatalf("MaxOutputBytes = %d, want %d", o.MaxOutputBytes, DefaultMaxOutputBytes)
	}
	if !filepath.IsAbs(o.Root) {
		t.Fatalf("Root = %q, want an absolute path", o.Root)
	}
}

func TestMatchGlob(t *testing.T) {
	cases := []struct {
		pattern, name string
		want          bool
	}{
		{"**/*.go", "main.go", true},
		{"**/*.go", "a/b/c.go", true},
		{"*.go", "a/b/c.go", true},
		{"a/*.go", "a/b/c.go", false},
		{"a/**/c.go", "a/b/x/c.go", true},
		{"a/**/c.go", "a/c.go", true},
		{"**/*.go", "main.rs", false},
		{"docs/**", "docs/a/b.md", true},
		{"docs/**", "src/a.md", false},
	}
	for _, tc := range cases {
		if got := matchGlob(tc.pattern, tc.name); got != tc.want {
			t.Errorf("matchGlob(%q, %q) = %v, want %v", tc.pattern, tc.name, got, tc.want)
		}
	}
}

// TestGrepRipgrepMatchesFallback runs the same search twice — once through
// ripgrep, once through the Go walk — and requires identical output, so the
// two paths cannot drift apart on machines that have rg installed.
func TestGrepRipgrepMatchesFallback(t *testing.T) {
	if rgPath == "" {
		t.Skip("ripgrep is not on PATH")
	}
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "main.go"), "package main\n// needle here\n")
	writeFile(t, filepath.Join(root, "notes", "todo.md"), "a needle in prose\n")
	writeFile(t, filepath.Join(root, "node_modules", "dep", "index.js"), "// needle in a dependency\n")
	writeFile(t, filepath.Join(root, ".sirdar", "runs", "r1", "transcript.txt"), "needle from an earlier run\n")

	grep := toolByName(t, ReadOnlySet(Options{Root: root}), "grep")
	withRg := mustCall(t, grep, `{"pattern":"needle"}`)

	saved := rgPath
	rgPath = ""
	fallback := mustCall(t, grep, `{"pattern":"needle"}`)
	rgPath = saved

	sorted := func(s string) string {
		lines := strings.Split(strings.TrimSpace(s), "\n")
		sort.Strings(lines)
		return strings.Join(lines, "\n")
	}
	if sorted(withRg) != sorted(fallback) {
		t.Fatalf("ripgrep and the Go walk disagree:\nrg:\n%s\nwalk:\n%s", withRg, fallback)
	}
	if strings.Contains(withRg, "node_modules") || strings.Contains(withRg, ".sirdar/runs") {
		t.Fatalf("ripgrep path did not apply the skip list: %q", withRg)
	}
}

// TestGrepRipgrepBranch drives the ripgrep code path with a stub binary, so
// the invocation, the "no matches" exit code and the fall back to the Go
// walk on any other failure are covered on machines without ripgrep.
func TestGrepRipgrepBranch(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "main.go"), "package main\n// needle here\n")

	bin := filepath.Join(t.TempDir(), "fake-rg")
	writeFile(t, bin, "#!/bin/sh\ncase \"$*\" in\n  *silent*) exit 1;;\n  *broken*) echo 'rg: boom' >&2; exit 2;;\nesac\necho \"$(pwd)/main.go:2:// needle here\"\n")
	if err := os.Chmod(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	saved := rgPath
	rgPath = bin
	t.Cleanup(func() { rgPath = saved })

	grep := toolByName(t, ReadOnlySet(Options{Root: root}), "grep")

	if got := mustCall(t, grep, `{"pattern":"needle"}`); got != "main.go:2:// needle here\n" {
		t.Fatalf("ripgrep output should be rendered relative to the root, got %q", got)
	}
	if got := mustCall(t, grep, `{"pattern":"silent"}`); got != "[no matches]" {
		t.Fatalf("exit code 1 means no matches, got %q", got)
	}
	// Exit code 2 is a ripgrep failure: the Go walk answers instead.
	writeFile(t, filepath.Join(root, "broken.txt"), "broken line\n")
	if got := mustCall(t, grep, `{"pattern":"broken"}`); got != "broken.txt:1:broken line\n" {
		t.Fatalf("a ripgrep failure should fall back to the Go walk, got %q", got)
	}
}
