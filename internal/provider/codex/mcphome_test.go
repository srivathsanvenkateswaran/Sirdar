package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/srivathsanvenkateswaran/sirdar/internal/provider"
)

// ------------------------------------------------------------ fake MCP server

// fakeMCPServer answers the MCP stdio handshake as a server called name,
// exposing one tool. It is what the live smoke test puts in a workspace's
// .mcp.json, so Codex has something real to connect to without the test
// depending on any installed MCP server.
func fakeMCPServer(name string) int {
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	out := bufio.NewWriter(os.Stdout)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var msg struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params struct {
				ProtocolVersion string `json:"protocolVersion"`
			} `json:"params"`
		}
		if err := json.Unmarshal([]byte(line), &msg); err != nil || len(msg.ID) == 0 {
			continue
		}
		var result string
		switch msg.Method {
		case "initialize":
			version := msg.Params.ProtocolVersion
			if version == "" {
				version = "2025-06-18"
			}
			result = fmt.Sprintf(`{"protocolVersion":%q,"capabilities":{"tools":{"listChanged":false}},"serverInfo":{"name":%q,"version":"1.0.0"}}`, version, name)
		case "tools/list":
			result = fmt.Sprintf(`{"tools":[{"name":"%s_lookup","description":"look something up","inputSchema":{"type":"object","properties":{}}}]}`, name)
		case "resources/list":
			result = `{"resources":[]}`
		case "resources/templates/list":
			result = `{"resourceTemplates":[]}`
		case "prompts/list":
			result = `{"prompts":[]}`
		default:
			result = `{}`
		}
		fmt.Fprintf(out, `{"jsonrpc":"2.0","id":%s,"result":%s}`+"\n", msg.ID, result)
		_ = out.Flush()
	}
	return 0
}

// ------------------------------------------------------------------- helpers

// writeWorkspace creates a workspace with a .mcp.json naming two stdio
// servers, one of them with a ${VAR} in its env, plus the entries
// LoadWorkspaceServers is expected to skip.
func writeWorkspace(t *testing.T, binary string) string {
	t.Helper()
	root := t.TempDir()
	body := fmt.Sprintf(`{
  "mcpServers": {
    "timeteller": {"command": %q, "args": ["serve", "--quiet"]},
    "notes": {
      "command": %q,
      "args": ["--root", "${SIRDAR_TEST_NOTES_ROOT}"],
      "env": {"NOTES_TOKEN": "${SIRDAR_TEST_NOTES_TOKEN}", "NOTES_MODE": "read"}
    },
    "remote-thing": {"type": "http", "url": "https://example.invalid/mcp"}
  }
}`, binary, binary)
	if err := os.WriteFile(filepath.Join(root, ".mcp.json"), []byte(body), 0o600); err != nil {
		t.Fatalf("write .mcp.json: %v", err)
	}
	return root
}

// writeRealHome creates a stand-in for the operator's ~/.codex: a config
// they wrote, a login, and some state that a session must keep seeing.
func writeRealHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	cfg := `model = "gpt-5.6-luna"
model_reasoning_effort = "medium"

[mcp_servers.deployer]
command = "/usr/local/bin/deployer"
args = ["--mcp"]

[mcp_servers.deployer.env]
DEPLOY_TOKEN = "hunter2"

[projects."/work"]
trust_level = "trusted"
`
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	write("config.toml", cfg)
	write("auth.json", `{"auth_mode":"chatgpt","tokens":{"access_token":"at"}}`)
	write("history.jsonl", "{}\n")
	if err := os.Mkdir(filepath.Join(dir, "sessions"), 0o700); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}
	return dir
}

// childHome returns the CODEX_HOME the fake app-server was started with,
// which it echoes to stderr.
func childHome(t *testing.T, res provider.Result) string {
	t.Helper()
	for _, line := range res.StderrTail {
		if rest, ok := strings.CutPrefix(line, "ENV: CODEX_HOME="); ok {
			return rest
		}
	}
	t.Fatalf("the fake server reported no CODEX_HOME; tail=%v", res.StderrTail)
	return ""
}

// --------------------------------------------------------------------- tests

// TestGeneratedConfigToml is the heart of the mechanism: the home a
// workspace-only session runs against declares the workspace's servers,
// with ${VAR} expanded, and none of the operator's own.
func TestGeneratedConfigToml(t *testing.T) {
	t.Setenv("SIRDAR_TEST_NOTES_ROOT", "/work/notes")
	t.Setenv("SIRDAR_TEST_NOTES_TOKEN", "s3cret")

	real := writeRealHome(t)
	root := writeWorkspace(t, "/opt/bin/mcp")
	home, warnings, err := newScratchHome(root, []string{"CODEX_HOME=" + real})
	if err != nil {
		t.Fatalf("newScratchHome: %v", err)
	}
	defer home.forceRemove()

	if len(warnings) != 1 || !strings.Contains(warnings[0], "remote-thing") {
		t.Errorf("warnings = %v, want one about the http server", warnings)
	}
	if got := strings.Join(home.servers, ","); got != "notes,timeteller" {
		t.Errorf("home.servers = %q, want notes,timeteller", got)
	}

	b, err := os.ReadFile(filepath.Join(home.dir, "config.toml"))
	if err != nil {
		t.Fatalf("read generated config.toml: %v", err)
	}
	got := string(b)

	want := `model = "gpt-5.6-luna"
model_reasoning_effort = "medium"

[projects."/work"]
trust_level = "trusted"

[mcp_servers.notes]
command = "/opt/bin/mcp"
args = ["--root", "/work/notes"]

[mcp_servers.notes.env]
"NOTES_MODE" = "read"
"NOTES_TOKEN" = "s3cret"

[mcp_servers.timeteller]
command = "/opt/bin/mcp"
args = ["serve", "--quiet"]
`
	if got != want {
		t.Errorf("generated config.toml:\n%s\nwant:\n%s", got, want)
	}
	for _, leaked := range []string{"deployer", "hunter2"} {
		if strings.Contains(got, leaked) {
			t.Errorf("the operator's own MCP server survived into the generated config: %s", got)
		}
	}

	// The login is copied, not linked: a refresh in a triage run must not
	// be able to rewrite the operator's own auth.json.
	authCopy := filepath.Join(home.dir, "auth.json")
	st, err := os.Lstat(authCopy)
	if err != nil {
		t.Fatalf("lstat auth.json: %v", err)
	}
	if st.Mode()&os.ModeSymlink != 0 {
		t.Error("auth.json is a symlink into the operator's home, want a copy")
	}
	if b, err := os.ReadFile(authCopy); err != nil || !strings.Contains(string(b), "chatgpt") {
		t.Errorf("auth.json copy = %q, %v", b, err)
	}

	// Everything else is linked, so sessions, history and skills behave as
	// they do in an ordinary run.
	for _, name := range []string{"history.jsonl", "sessions"} {
		st, err := os.Lstat(filepath.Join(home.dir, name))
		if err != nil {
			t.Fatalf("lstat %s: %v", name, err)
		}
		if st.Mode()&os.ModeSymlink == 0 {
			t.Errorf("%s is not a symlink to the operator's home", name)
		}
	}
}

// TestScratchHomeWithNoWorkspaceFile pins the safe reading of the setting:
// a workspace with no .mcp.json gets a home that declares no servers, not
// the operator's.
func TestScratchHomeWithNoWorkspaceFile(t *testing.T) {
	real := writeRealHome(t)
	home, warnings, err := newScratchHome(t.TempDir(), []string{"CODEX_HOME=" + real})
	if err != nil {
		t.Fatalf("newScratchHome: %v", err)
	}
	defer home.forceRemove()
	if len(warnings) != 0 || len(home.servers) != 0 {
		t.Errorf("warnings=%v servers=%v, want neither", warnings, home.servers)
	}
	b, err := os.ReadFile(filepath.Join(home.dir, "config.toml"))
	if err != nil {
		t.Fatalf("read config.toml: %v", err)
	}
	if strings.Contains(string(b), "mcp_servers") {
		t.Errorf("generated config.toml still declares MCP servers:\n%s", b)
	}
	if !strings.Contains(string(b), `model = "gpt-5.6-luna"`) {
		t.Errorf("the operator's model was dropped:\n%s", b)
	}
}

// TestSessionRunsAgainstScratchHome checks the child env and the copied
// login from the session the runner would actually start.
func TestSessionRunsAgainstScratchHome(t *testing.T) {
	t.Setenv("SIRDAR_TEST_NOTES_ROOT", "/work/notes")
	t.Setenv("SIRDAR_TEST_NOTES_TOKEN", "s3cret")
	real := writeRealHome(t)
	root := writeWorkspace(t, "/opt/bin/mcp")

	sess := startSession(t, "script-basic.jsonl", func(spec *provider.SessionSpec) {
		spec.Cwd = root
		spec.MCPConfig = filepath.Join(root, ".mcp.json")
		spec.MCPStrict = true
		spec.Env = append(spec.Env, "CODEX_HOME="+real, envKeepHome+"=1")
	})
	evs := drain(sess)
	res, err := sess.Wait()
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}

	dir := childHome(t, res)
	if dir == real || !strings.Contains(filepath.Base(dir), "sirdar-codex-home-") {
		t.Fatalf("child CODEX_HOME = %q, want a generated home", dir)
	}
	// SIRDAR_KEEP_CODEX_HOME=1 keeps it past Wait, for debugging.
	defer os.RemoveAll(dir)
	cfg, err := os.ReadFile(filepath.Join(dir, "config.toml"))
	if err != nil {
		t.Fatalf("read config.toml: %v", err)
	}
	for _, want := range []string{"[mcp_servers.notes]", "[mcp_servers.timeteller]", `"NOTES_TOKEN" = "s3cret"`} {
		if !strings.Contains(string(cfg), want) {
			t.Errorf("generated config.toml missing %s:\n%s", want, cfg)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "auth.json")); err != nil {
		t.Errorf("auth.json was not copied into the generated home: %v", err)
	}

	var summary, warning string
	for _, ev := range evs {
		if ev.Kind != provider.EvSystem {
			continue
		}
		if strings.HasPrefix(ev.Text, "mcp.workspaceOnly: ") {
			summary = ev.Text
		}
		if strings.HasPrefix(ev.Text, "mcp: ") {
			warning = ev.Text
		}
	}
	if !strings.Contains(summary, "notes, timeteller") {
		t.Errorf("no event named the servers the thread sees: %q", summary)
	}
	if !strings.Contains(warning, "remote-thing") {
		t.Errorf("no event reported the skipped .mcp.json entry: %q", warning)
	}
}

// TestScratchHomeRemovedOnWait pins deliverable three: the home lives
// exactly as long as the session.
func TestScratchHomeRemovedOnWait(t *testing.T) {
	real := writeRealHome(t)
	root := writeWorkspace(t, "/opt/bin/mcp")

	sess := startSession(t, "script-basic.jsonl", func(spec *provider.SessionSpec) {
		spec.Cwd = root
		spec.MCPStrict = true
		spec.Env = append(spec.Env, "CODEX_HOME="+real)
	})
	drain(sess)
	res, err := sess.Wait()
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}

	dir := childHome(t, res)
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("the generated home %s survived Wait: %v", dir, err)
	}
	if _, err := os.Stat(filepath.Join(real, "auth.json")); err != nil {
		t.Errorf("the operator's own home was damaged by the cleanup: %v", err)
	}
}

// TestNoScratchHomeWhenWorkspaceOnlyIsOff is today's behaviour, unchanged:
// the session runs against the operator's own Codex configuration.
func TestNoScratchHomeWhenWorkspaceOnlyIsOff(t *testing.T) {
	real := writeRealHome(t)
	root := writeWorkspace(t, "/opt/bin/mcp")

	sess := startSession(t, "script-basic.jsonl", func(spec *provider.SessionSpec) {
		spec.Cwd = root
		spec.Env = append(spec.Env, "CODEX_HOME="+real)
	})
	evs := drain(sess)
	res, err := sess.Wait()
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if got := childHome(t, res); got != real {
		t.Errorf("child CODEX_HOME = %q, want the operator's own home %q", got, real)
	}
	for _, ev := range evs {
		if ev.Kind == provider.EvSystem && strings.HasPrefix(ev.Text, "mcp.workspaceOnly:") {
			t.Errorf("a session with workspaceOnly off reported %q", ev.Text)
		}
	}
}

func TestStripMCPServers(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "inline table",
			in:   "model = \"x\"\nmcp_servers = { a = { command = \"c\" } }\nweb_search = true\n",
			want: "model = \"x\"\nweb_search = true\n",
		},
		{
			name: "dotted key",
			in:   "mcp_servers.a.command = \"c\"\nmodel = \"x\"\n",
			want: "model = \"x\"\n",
		},
		{
			name: "table and sub-table, next table kept",
			in:   "[mcp_servers.a]\ncommand = \"c\"\n\n[mcp_servers.a.env]\nT = \"1\"\n\n[projects.\"/w\"]\ntrust_level = \"trusted\"\n",
			want: "[projects.\"/w\"]\ntrust_level = \"trusted\"\n",
		},
		{
			name: "a project path mentioning mcp_servers is kept",
			in:   "[projects.\"/w/mcp_servers\"]\ntrust_level = \"trusted\"\n",
			want: "[projects.\"/w/mcp_servers\"]\ntrust_level = \"trusted\"\n",
		},
		{name: "empty", in: "", want: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := stripMCPServers(tc.in); got != tc.want {
				t.Errorf("stripMCPServers(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestTOMLString(t *testing.T) {
	cases := map[string]string{
		`plain`:            `"plain"`,
		`with "quotes"`:    `"with \"quotes\""`,
		`C:\path`:          `"C:\\path"`,
		"tab\there":        `"tab\there"`,
		"line\nbreak":      `"line\nbreak"`,
		"bell\x07":         `"bell\u0007"`,
		"unicode ✓ kept":   `"unicode ✓ kept"`,
		"${NOT_EXPANDED}":  `"${NOT_EXPANDED}"`,
		"trailing space  ": `"trailing space  "`,
	}
	for in, want := range cases {
		if got := tomlString(in); got != want {
			t.Errorf("tomlString(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestWorkspaceOnlyFromEnv(t *testing.T) {
	cases := map[string]bool{
		"":      true,
		"1":     true,
		"true":  true,
		"0":     false,
		"false": false,
		"OFF":   false,
	}
	for value, want := range cases {
		env := []string{"PATH=/usr/bin"}
		if value != "" {
			env = append(env, envWorkspaceOnly+"="+value)
		}
		if got := workspaceOnly(env); got != want {
			t.Errorf("workspaceOnly(%s=%q) = %v, want %v", envWorkspaceOnly, value, got, want)
		}
	}
}

func TestSpecRoot(t *testing.T) {
	if got := specRoot("/w", "/elsewhere/.mcp.json"); got != "/elsewhere" {
		t.Errorf("specRoot with a config path = %q", got)
	}
	if got := specRoot("/w", ""); got != "/w" {
		t.Errorf("specRoot without a config path = %q", got)
	}
}

// TestLiveWorkspaceOnlyMCP runs the real `codex app-server` against a
// generated home and asks it which MCP servers it has. It starts no turn,
// so it spends no model quota. Set SIRDAR_CODEX_LIVE=1 to run it.
func TestLiveWorkspaceOnlyMCP(t *testing.T) {
	if os.Getenv("SIRDAR_CODEX_LIVE") != "1" {
		t.Skip("set SIRDAR_CODEX_LIVE=1 to run the live Codex smoke test")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	root := t.TempDir()
	body := fmt.Sprintf(`{"mcpServers":{
  "timeteller": {"command": %q, "env": {"SIRDAR_FAKE_MCP": "timeteller"}},
  "notes": {"command": %q, "env": {"SIRDAR_FAKE_MCP": "notes", "NOTES_TOKEN": "${SIRDAR_TEST_NOTES_TOKEN}"}}
}}`, exe, exe)
	if err := os.WriteFile(filepath.Join(root, ".mcp.json"), []byte(body), 0o600); err != nil {
		t.Fatalf("write .mcp.json: %v", err)
	}
	if err := os.Mkdir(filepath.Join(root, ".sirdar"), 0o700); err != nil {
		t.Fatalf("mkdir .sirdar: %v", err)
	}
	t.Setenv("SIRDAR_TEST_NOTES_TOKEN", "live-smoke-token")
	t.Setenv(envWorkspaceOnly, "1")
	t.Chdir(root)

	check := mcpCheck(context.Background(), "codex")
	t.Logf("doctor row: %s — %s (OK=%v)", check.Name, check.Detail, check.OK)
	if !check.OK {
		t.Fatalf("mcp check failed: %s", check.Detail)
	}
	for _, want := range []string{"notes", "timeteller", filepath.Join(root, ".mcp.json")} {
		if !strings.Contains(check.Detail, want) {
			t.Errorf("doctor row missing %q: %s", want, check.Detail)
		}
	}
}
