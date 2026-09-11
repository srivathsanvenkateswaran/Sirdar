package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	// The values a ${VAR} must come from are in the session environment
	// below, not here: a variable that exists only in this process is one
	// internal/run would have stripped, and it must not reach the file.
	t.Setenv("SIRDAR_TEST_NOTES_ROOT", "from-the-wrong-environment")
	t.Setenv("SIRDAR_TEST_NOTES_TOKEN", "from-the-wrong-environment")

	real := writeRealHome(t)
	root := writeWorkspace(t, "/opt/bin/mcp")
	home, warnings, err := newScratchHome(root, []string{
		"CODEX_HOME=" + real,
		"SIRDAR_TEST_NOTES_ROOT=/work/notes",
		"SIRDAR_TEST_NOTES_TOKEN=s3cret",
	})
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
	if strings.Contains(got, "from-the-wrong-environment") {
		t.Errorf("a ${VAR} was expanded from this process's environment rather than the session's: %s", got)
	}

	// The directory holds a copy of the operator's login, so it is theirs
	// to read and nobody else's.
	if st, err := os.Stat(home.dir); err != nil {
		t.Fatalf("stat home: %v", err)
	} else if st.Mode().Perm() != 0o700 {
		t.Errorf("generated home mode = %v, want 0700", st.Mode().Perm())
	}
	if st, err := os.Stat(filepath.Join(home.dir, "config.toml")); err != nil {
		t.Fatalf("stat config.toml: %v", err)
	} else if st.Mode().Perm() != 0o600 {
		t.Errorf("generated config.toml mode = %v, want 0600", st.Mode().Perm())
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

		// The cases a line-based strip got wrong. Each one deleted the
		// rest of the operator's config, which is how a session would
		// have lost their model, their reasoning effort and their
		// project trust along with the servers.
		{
			name: "a multi-line string containing a table header",
			in: "instructions = \"\"\"\nDeclare a server like this:\n[mcp_servers.example]\ncommand = \"x\"\n\"\"\"\n" +
				"model = \"gpt-5.6-luna\"\n",
			want: "instructions = \"\"\"\nDeclare a server like this:\n[mcp_servers.example]\ncommand = \"x\"\n\"\"\"\n" +
				"model = \"gpt-5.6-luna\"\n",
		},
		{
			name: "a literal multi-line string containing a header",
			in:   "notes = '''\n[mcp_servers.a]\n'''\nmodel = \"x\"\n",
			want: "notes = '''\n[mcp_servers.a]\n'''\nmodel = \"x\"\n",
		},
		{
			name: "a multi-line dotted-key array is dropped whole",
			in:   "mcp_servers.a.args = [\n  \"--mcp\",\n  \"--quiet\",\n]\nmodel = \"x\"\n",
			want: "model = \"x\"\n",
		},
		{
			name: "a multi-line inline table is dropped whole",
			in:   "model = \"x\"\nmcp_servers = {\n  a = { command = \"c\" },\n}\nweb_search = true\n",
			want: "model = \"x\"\nweb_search = true\n",
		},
		{
			name: "a header after other keys keeps what came before",
			in:   "model = \"x\"\nmodel_reasoning_effort = \"medium\"\n\n[mcp_servers.a]\ncommand = \"c\"\n",
			want: "model = \"x\"\nmodel_reasoning_effort = \"medium\"\n",
		},
		{
			name: "a sub-table of a server is dropped with it",
			in:   "[mcp_servers.foo]\ncommand = \"c\"\n\n[mcp_servers.foo.env]\nTOKEN = \"hunter2\"\n\n[tui]\ntheme = \"dark\"\n",
			want: "[tui]\ntheme = \"dark\"\n",
		},
		{
			name: "the array-of-tables form is dropped",
			in:   "[[mcp_servers.a]]\ncommand = \"c\"\n\n[tui]\ntheme = \"dark\"\n",
			want: "[tui]\ntheme = \"dark\"\n",
		},
		{
			name: "a quoted mcp_servers key is dropped",
			in:   "\"mcp_servers\".a.command = \"c\"\nmodel = \"x\"\n",
			want: "model = \"x\"\n",
		},
		{
			name: "unrelated tables either side survive byte for byte",
			in:   "[tui]\ntheme = \"dark\"\n\n[mcp_servers.a]\ncommand = \"c\"\n\n[projects.\"/w\"]\ntrust_level = \"trusted\"\n",
			want: "[tui]\ntheme = \"dark\"\n\n[projects.\"/w\"]\ntrust_level = \"trusted\"\n",
		},
		{
			name: "an mcp_servers key under another table is that table's own",
			in:   "[profiles.dev]\nmcp_servers = { a = { command = \"c\" } }\nmodel = \"x\"\n",
			want: "[profiles.dev]\nmcp_servers = { a = { command = \"c\" } }\nmodel = \"x\"\n",
		},
		{
			name: "a comment mentioning a header is not one",
			in:   "# [mcp_servers.a] is what codex mcp add writes\nmodel = \"x\"\n",
			want: "# [mcp_servers.a] is what codex mcp add writes\nmodel = \"x\"\n",
		},
		{
			name: "a config that is nothing but servers comes back empty",
			in:   "[mcp_servers.a]\ncommand = \"c\"\n",
			want: "",
		},
		{
			name: "a header with no trailing newline still ends the file",
			in:   "model = \"x\"\n[mcp_servers.a]",
			want: "model = \"x\"\n",
		},
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

// TestDoctorSetting pins where the Doctor row's workspace and setting come
// from. With a configuration they come from it verbatim, which is the
// whole point of the DoctorWithConfig path: the desktop app's working
// directory is not the workspace. Without one, the fallback finds the
// workspace by its .sirdar marker and assumes the config's own default.
func TestDoctorSetting(t *testing.T) {
	root, only := doctorSetting(&provider.DoctorConfig{Root: "/w", MCPWorkspaceOnly: false})
	if root != "/w" || only {
		t.Errorf("doctorSetting(config) = %q, %v; want /w, false", root, only)
	}
	root, only = doctorSetting(&provider.DoctorConfig{Root: "/w", MCPWorkspaceOnly: true})
	if root != "/w" || !only {
		t.Errorf("doctorSetting(config on) = %q, %v; want /w, true", root, only)
	}

	ws := t.TempDir()
	if err := os.Mkdir(filepath.Join(ws, ".sirdar"), 0o700); err != nil {
		t.Fatalf("mkdir .sirdar: %v", err)
	}
	nested := filepath.Join(ws, "a", "b")
	if err := os.MkdirAll(nested, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	t.Chdir(nested)
	root, only = doctorSetting(nil)
	if !only {
		t.Error("doctorSetting(nil) reported workspaceOnly off; the config default is on")
	}
	// macOS hands out /var/folders paths that resolve through a symlink,
	// so compare what the walk found to what the walk would have started
	// from rather than to t.TempDir's own spelling.
	if want, err := filepath.EvalSymlinks(ws); err == nil {
		if got, _ := filepath.EvalSymlinks(root); got != want {
			t.Errorf("doctorSetting(nil) root = %q, want the workspace %q", got, want)
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

	check := mcpCheck(context.Background(), "codex", &provider.DoctorConfig{Root: root, MCPWorkspaceOnly: true})
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

// ------------------------------------------------------------ auth write-back

// refreshIn rewrites the generated home's auth.json the way Codex does
// when it refreshes the ChatGPT token mid-session.
func refreshIn(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "auth.json"), []byte(body), 0o600); err != nil {
		t.Fatalf("refresh auth.json: %v", err)
	}
}

// TestAuthRefreshIsWrittenBack is the other half of copying auth.json in.
// The copy protects the operator from a triage run corrupting their login;
// without a write-back it also throws away a refresh they need, leaving
// their own `codex` presenting a token this run has already replaced.
func TestAuthRefreshIsWrittenBack(t *testing.T) {
	real := writeRealHome(t)
	home, _, err := newScratchHome(t.TempDir(), []string{"CODEX_HOME=" + real})
	if err != nil {
		t.Fatalf("newScratchHome: %v", err)
	}
	refreshed := `{"auth_mode":"chatgpt","tokens":{"access_token":"refreshed"}}`
	refreshIn(t, home.dir, refreshed)

	warnings := home.remove()
	b, err := os.ReadFile(filepath.Join(real, "auth.json"))
	if err != nil {
		t.Fatalf("read the operator's auth.json: %v", err)
	}
	if string(b) != refreshed {
		t.Errorf("operator's auth.json = %s, want the refreshed token", b)
	}
	if st, err := os.Stat(filepath.Join(real, "auth.json")); err != nil {
		t.Fatal(err)
	} else if st.Mode().Perm() != 0o600 {
		t.Errorf("auth.json mode after write-back = %v, want 0600", st.Mode().Perm())
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "written back") {
		t.Errorf("warnings = %v, want one saying the refresh was written back", warnings)
	}
}

// TestAuthUntouchedWhenNothingRefreshed is the ordinary case: a session
// that never refreshed leaves the operator's file exactly as it was, and
// says nothing about it.
func TestAuthUntouchedWhenNothingRefreshed(t *testing.T) {
	real := writeRealHome(t)
	before, err := os.ReadFile(filepath.Join(real, "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	home, _, err := newScratchHome(t.TempDir(), []string{"CODEX_HOME=" + real})
	if err != nil {
		t.Fatalf("newScratchHome: %v", err)
	}
	if warnings := home.remove(); len(warnings) != 0 {
		t.Errorf("warnings = %v, want none", warnings)
	}
	after, err := os.ReadFile(filepath.Join(real, "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Errorf("auth.json = %s, want it untouched at %s", after, before)
	}
}

// TestAuthRefreshDiscardedWhenTheirsMovedOn is the collision. Two writers
// and no way to merge them: the run that did not ask to be an authority on
// the operator's login stands down, and says so rather than silently
// overwriting a token their own session just minted.
func TestAuthRefreshDiscardedWhenTheirsMovedOn(t *testing.T) {
	real := writeRealHome(t)
	home, _, err := newScratchHome(t.TempDir(), []string{"CODEX_HOME=" + real})
	if err != nil {
		t.Fatalf("newScratchHome: %v", err)
	}
	refreshIn(t, home.dir, `{"auth_mode":"chatgpt","tokens":{"access_token":"ours"}}`)

	// Their own codex refreshed it meanwhile.
	theirs := `{"auth_mode":"chatgpt","tokens":{"access_token":"theirs"}}`
	if err := os.WriteFile(filepath.Join(real, "auth.json"), []byte(theirs), 0o600); err != nil {
		t.Fatal(err)
	}

	warnings := home.remove()
	b, err := os.ReadFile(filepath.Join(real, "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != theirs {
		t.Errorf("auth.json = %s, want theirs left alone", b)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "discarded") {
		t.Errorf("warnings = %v, want one saying the refresh was discarded", warnings)
	}
}

// TestAuthWriteBackWithNoLogin covers an operator who has never run codex:
// no auth.json to copy, so there is nothing to write back and nothing to
// warn about.
func TestAuthWriteBackWithNoLogin(t *testing.T) {
	real := t.TempDir()
	if err := os.WriteFile(filepath.Join(real, "config.toml"), []byte("model = \"x\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	home, _, err := newScratchHome(t.TempDir(), []string{"CODEX_HOME=" + real})
	if err != nil {
		t.Fatalf("newScratchHome: %v", err)
	}
	if warnings := home.remove(); len(warnings) != 0 {
		t.Errorf("warnings = %v, want none", warnings)
	}
	if _, err := os.Stat(filepath.Join(real, "auth.json")); !os.IsNotExist(err) {
		t.Errorf("an auth.json was invented for an operator who has none: %v", err)
	}
}

// ------------------------------------------------------------- stale sweep

// TestSweepStaleHomes covers the SIGKILL leftover. remove never runs when
// a session is killed outright, so the next one clears out what is old
// enough to be nobody's — and nothing else in TMPDIR, which it shares.
func TestSweepStaleHomes(t *testing.T) {
	tmp := t.TempDir()
	now := time.Now()

	mk := func(name string, age time.Duration) string {
		dir := filepath.Join(tmp, name)
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatalf("mkdir %s: %v", name, err)
		}
		when := now.Add(-age)
		if err := os.Chtimes(dir, when, when); err != nil {
			t.Fatalf("chtimes %s: %v", name, err)
		}
		return dir
	}
	stale := mk(homePrefix+"stale", 48*time.Hour)
	fresh := mk(homePrefix+"fresh", time.Hour)
	// Right on the boundary: a home exactly at the age is not yet stale.
	boundary := mk(homePrefix+"boundary", staleHomeAge-time.Minute)
	other := mk("someone-elses-tempdir", 90*24*time.Hour)

	file := filepath.Join(tmp, homePrefix+"not-a-directory")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := now.Add(-72 * time.Hour)
	if err := os.Chtimes(file, old, old); err != nil {
		t.Fatal(err)
	}

	sweepStaleHomes(tmp, now)

	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("the stale home survived the sweep: %v", err)
	}
	for _, keep := range []string{fresh, boundary, other, file} {
		if _, err := os.Stat(keep); err != nil {
			t.Errorf("the sweep removed %s: %v", filepath.Base(keep), err)
		}
	}
}

// TestNewScratchHomeWritesALock pins the write side of the sweep's
// liveness check: every generated home carries a lock naming the process
// that created it, from the moment newScratchHome returns.
func TestNewScratchHomeWritesALock(t *testing.T) {
	real := writeRealHome(t)
	home, _, err := newScratchHome(t.TempDir(), []string{"CODEX_HOME=" + real})
	if err != nil {
		t.Fatalf("newScratchHome: %v", err)
	}
	defer home.forceRemove()

	pid, ok := lockPID(home.dir)
	if !ok || pid != os.Getpid() {
		t.Errorf("lockPID(%s) = %d, %v; want this process's pid %d, true", home.dir, pid, ok, os.Getpid())
	}
}

// TestSweepSkipsALiveLock covers the ordinary long, quiet turn: even past
// staleHomeAge, a home whose lock names a process that is still running is
// kept. Mtime alone would have taken it out from under a session that
// simply had not touched its own directory in 24 hours.
func TestSweepSkipsALiveLock(t *testing.T) {
	tmp := t.TempDir()
	now := time.Now()

	dir := filepath.Join(tmp, homePrefix+"live")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// This test process is certainly alive for the duration of the test,
	// so its own pid stands in for the session that owns the home.
	if err := writeLock(dir); err != nil {
		t.Fatalf("writeLock: %v", err)
	}
	// Writing the lock file just touched the directory, so back-date it
	// afterwards: this simulates a home whose lock was written at session
	// start and whose directory has not been touched since.
	old := now.Add(-48 * time.Hour)
	if err := os.Chtimes(dir, old, old); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	sweepStaleHomes(tmp, now)

	if _, err := os.Stat(dir); err != nil {
		t.Errorf("the sweep removed a home whose session is still running: %v", err)
	}
}

// TestSweepRemovesADeadLock is the SIGKILL case a lock file does not
// change: once a home is old enough and nothing holds its lock's pid any
// more, the sweep takes it, same as a home with no lock file at all.
func TestSweepRemovesADeadLock(t *testing.T) {
	tmp := t.TempDir()
	now := time.Now()

	// A pid guaranteed to have exited by the time the sweep reads it.
	cmd := exec.Command("true")
	if err := cmd.Run(); err != nil {
		t.Fatalf("run a throwaway process: %v", err)
	}
	dead := cmd.Process.Pid

	dir := filepath.Join(tmp, homePrefix+"dead")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	body := fmt.Sprintf("%d\n%s\n", dead, now.Format(time.RFC3339))
	if err := os.WriteFile(filepath.Join(dir, lockFileName), []byte(body), 0o600); err != nil {
		t.Fatalf("write lock: %v", err)
	}
	// Back-date the directory after writing the lock file, the same as
	// TestSweepSkipsALiveLock: the write itself touches the directory.
	old := now.Add(-48 * time.Hour)
	if err := os.Chtimes(dir, old, old); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	sweepStaleHomes(tmp, now)

	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("a stale home with a dead lock survived the sweep: %v", err)
	}
}

// TestLockPIDDistrustsAnOldStartTime is the unit-level check on lockPID
// itself: a lock recording this process's own pid — genuinely alive — is
// still not vouched for once its start time is old enough, and neither is
// one whose start time cannot be parsed at all.
func TestLockPIDDistrustsAnOldStartTime(t *testing.T) {
	write := func(t *testing.T, body string) string {
		t.Helper()
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, lockFileName), []byte(body), 0o600); err != nil {
			t.Fatalf("write lock: %v", err)
		}
		return dir
	}

	fresh := write(t, fmt.Sprintf("%d\n%s\n", os.Getpid(), time.Now().UTC().Format(time.RFC3339)))
	if pid, ok := lockPID(fresh); !ok || pid != os.Getpid() {
		t.Errorf("lockPID(fresh) = %d, %v; want this process's pid, true", pid, ok)
	}

	stale := write(t, fmt.Sprintf("%d\n%s\n", os.Getpid(), time.Now().Add(-8*24*time.Hour).UTC().Format(time.RFC3339)))
	if pid, ok := lockPID(stale); ok {
		t.Errorf("lockPID(stale) = %d, %v; want ok=false for a start time older than lockStaleAge", pid, ok)
	}

	unparsable := write(t, fmt.Sprintf("%d\nnot-a-time\n", os.Getpid()))
	if pid, ok := lockPID(unparsable); ok {
		t.Errorf("lockPID(unparsable) = %d, %v; want ok=false for a start time that does not parse", pid, ok)
	}
}

// TestSweepRemovesAStaleLockEvenIfAlive covers pid reuse: a lock naming a
// pid that is genuinely running is not enough to protect a home once the
// lock's own recorded start time is older than lockStaleAge — at that age,
// the process behind the pid is more plausibly a stranger that happened to
// land on the same number than the session that wrote the lock.
func TestSweepRemovesAStaleLockEvenIfAlive(t *testing.T) {
	tmp := t.TempDir()
	now := time.Now()

	dir := filepath.Join(tmp, homePrefix+"old-lock")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// This test process is certainly alive, so its own pid stands in for
	// the "still running" half of the scenario; the lock's start time is
	// what makes it stale.
	body := fmt.Sprintf("%d\n%s\n", os.Getpid(), now.Add(-8*24*time.Hour).UTC().Format(time.RFC3339))
	if err := os.WriteFile(filepath.Join(dir, lockFileName), []byte(body), 0o600); err != nil {
		t.Fatalf("write lock: %v", err)
	}
	old := now.Add(-48 * time.Hour)
	if err := os.Chtimes(dir, old, old); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	sweepStaleHomes(tmp, now)

	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("a home with a stale lock survived the sweep despite its pid being alive: %v", err)
	}
}

// TestSessionWritesBackARefreshedLogin is the write-back where it
// actually happens: at the end of a session, after the app-server has
// gone, with the outcome on the event stream rather than swallowed.
func TestSessionWritesBackARefreshedLogin(t *testing.T) {
	real := writeRealHome(t)
	root := writeWorkspace(t, "/opt/bin/mcp")
	refreshed := `{"auth_mode":"chatgpt","tokens":{"access_token":"refreshed-mid-session"}}`

	sess := startSession(t, "script-basic.jsonl", func(spec *provider.SessionSpec) {
		spec.Cwd = root
		spec.MCPStrict = true
		spec.Env = append(spec.Env, "CODEX_HOME="+real, "SIRDAR_FAKE_REFRESH_AUTH="+refreshed)
	})
	drain(sess)
	res, err := sess.Wait()
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}

	b, err := os.ReadFile(filepath.Join(real, "auth.json"))
	if err != nil {
		t.Fatalf("read the operator's auth.json: %v", err)
	}
	if string(b) != refreshed {
		t.Errorf("operator's auth.json = %s, want the token the session refreshed", b)
	}

	// The turn's completion has already closed the event stream by the
	// time Wait tears the home down, so the account of what happened to
	// the login travels in the tail the run records.
	var said bool
	for _, line := range res.StderrTail {
		if strings.HasPrefix(line, "sirdar: codex auth:") {
			said = true
		}
	}
	if !said {
		t.Errorf("the write-back happened without saying so; tail=%v", res.StderrTail)
	}
}
