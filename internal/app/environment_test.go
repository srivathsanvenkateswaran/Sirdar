package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/srivathsanvenkateswaran/sirdar/internal/config"
	"github.com/srivathsanvenkateswaran/sirdar/internal/loginpath"
	"github.com/srivathsanvenkateswaran/sirdar/internal/provider"
)

// found resolves everything, missing resolves nothing.
func found(at string) func(string) string {
	return func(name string) string { return at }
}

func missing() func(string) string {
	return func(string) string { return "" }
}

const dockPATH = "/usr/bin:/bin:/usr/sbin:/sbin"

func loginShell(path string) loginpath.Result {
	return loginpath.Result{Source: "login shell /bin/zsh", Shell: "/bin/zsh", Flags: "-il", Path: path}
}

// The healthy case: the row names the shell the PATH came from and where
// the binary actually is.
func TestEnvironmentRowNamesThePATHSourceAndTheBinary(t *testing.T) {
	res := loginShell("/opt/homebrew/bin:" + dockPATH)
	res.Added = []string{"/Users/sri/.local/bin", "/Users/sri/go/bin"}
	c := environmentCheckFor(res, []binaryRef{{name: "claude", setting: "providers.claude.path"}}, found("/opt/homebrew/bin/claude"))

	if !c.OK {
		t.Fatalf("row failed: %+v", c)
	}
	for _, want := range []string{
		"PATH from the login shell /bin/zsh",
		"5 entries",
		"2 user bin directories appended",
		"claude → /opt/homebrew/bin/claude",
	} {
		if !strings.Contains(c.Detail, want) {
			t.Errorf("detail = %q, want %q in it", c.Detail, want)
		}
	}
}

// The CLI's case: nothing resolved a login shell, and the terminal's own
// PATH is the right answer.
func TestEnvironmentRowSaysWhenThePATHIsTheProcessEnvironment(t *testing.T) {
	c := environmentCheckFor(loginpath.Result{Source: "process", Path: dockPATH},
		[]binaryRef{{name: "claude", setting: "providers.claude.path"}}, found("/usr/bin/claude"))

	if !strings.Contains(c.Detail, "PATH from this process's environment, 4 entries") {
		t.Errorf("detail = %q, want the process environment named", c.Detail)
	}
}

// The bug, as the row would have reported it: the app is on launchd's
// PATH, claude is not on it, and the row says which key overrides the
// lookup.
func TestEnvironmentRowFailsWithTheFixWhenABinaryIsMissing(t *testing.T) {
	c := environmentCheckFor(loginpath.Result{Source: "process", Path: dockPATH},
		[]binaryRef{{name: "claude", setting: "providers.claude.path"}}, missing())

	if c.OK {
		t.Fatalf("row passed with no claude on PATH: %+v", c)
	}
	if !strings.Contains(c.Detail, "claude is not on the app's PATH") {
		t.Errorf("detail = %q, want the fix sentence", c.Detail)
	}
	if !strings.Contains(c.Detail, "providers.claude.path in .sirdar/config.yaml") {
		t.Errorf("detail = %q, want the override named", c.Detail)
	}
}

// A configured path that points at nothing is a different mistake from an
// uninstalled binary, and telling an operator to set the key they already
// set would be useless.
func TestEnvironmentRowNamesAConfiguredPathThatDoesNotExist(t *testing.T) {
	c := environmentCheckFor(loginpath.Result{Source: "process", Path: dockPATH},
		[]binaryRef{{name: "/opt/old/claude", setting: "providers.claude.path"}}, missing())

	if c.OK {
		t.Fatal("row passed with a configured path that does not exist")
	}
	if !strings.Contains(c.Detail, "providers.claude.path names /opt/old/claude, and there is no such file") {
		t.Errorf("detail = %q, want the configured path named", c.Detail)
	}
	if strings.Contains(c.Detail, "install it under") {
		t.Errorf("detail = %q, want no install advice for a path that was set deliberately", c.Detail)
	}
}

// A login shell that could not be read is a warning, not a failure: the
// inherited PATH still stands and may well have everything on it.
func TestEnvironmentRowWarnsWhenTheLoginShellCouldNotBeRead(t *testing.T) {
	res := loginpath.Result{Source: "process", Path: dockPATH, Err: errors.New("fork/exec /bin/zsh: no such file or directory")}
	c := environmentCheckFor(res, []binaryRef{{name: "claude", setting: "providers.claude.path"}}, found("/usr/bin/claude"))

	if !c.OK || c.Level != string(provider.LevelWarn) {
		t.Fatalf("row = %+v, want a warning that does not fail", c)
	}
	if !strings.Contains(c.Detail, "the login shell could not be read") {
		t.Errorf("detail = %q, want the shell failure named", c.Detail)
	}
}

// A missing binary outranks the shell warning: the run cannot start, so
// the row fails outright.
func TestEnvironmentRowFailsOverTheShellWarning(t *testing.T) {
	res := loginpath.Result{Source: "process", Path: dockPATH, Err: errors.New("timed out")}
	c := environmentCheckFor(res, []binaryRef{{name: "claude", setting: "providers.claude.path"}}, missing())

	if c.OK {
		t.Fatal("row passed")
	}
	if c.Level != "" {
		t.Errorf("Level = %q, want it left for levelled() to fill in as a failure", c.Level)
	}
}

// provider: openai spawns nothing, and the row says so rather than
// reporting on a binary that does not exist for it.
func TestEnvironmentRowWithNoBinaryToFind(t *testing.T) {
	c := environmentCheckFor(loginpath.Result{Source: "process", Path: dockPATH}, nil, missing())
	if !c.OK {
		t.Fatalf("row failed: %+v", c)
	}
	if !strings.Contains(c.Detail, "spawns no binary of its own") {
		t.Errorf("detail = %q, want it to say there is nothing to find", c.Detail)
	}
}

func TestProviderBinariesPerProvider(t *testing.T) {
	cases := []struct {
		name    string
		set     func(*config.Config)
		want    string
		setting string
	}{
		{"claude by default", func(*config.Config) {}, "claude", "providers.claude.path"},
		{"codex", func(c *config.Config) { c.Provider = "codex" }, "codex", "providers.codex.path"},
		{"qwen", func(c *config.Config) { c.Provider = "qwen"; c.Qwen = &config.QwenConfig{} }, "qwen", "qwen.path"},
		{"cursor", func(c *config.Config) { c.Provider = "cursor"; c.Cursor = &config.CursorConfig{} }, "cursor-agent", "cursor.path"},
		{"agy", func(c *config.Config) { c.Provider = "agy"; c.Agy = &config.AgyConfig{} }, "agy", "agy.path"},
		{"acp names the agent", func(c *config.Config) {
			c.Provider = "acp"
			c.ACP = &config.ACPConfig{Command: "opencode"}
		}, "opencode", "acp.command"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &config.Config{Root: "/ws", Provider: "claude"}
			tc.set(cfg)
			refs := providerBinaries(cfg)
			if len(refs) != 1 {
				t.Fatalf("refs = %+v, want one", refs)
			}
			if refs[0].name != tc.want || refs[0].setting != tc.setting {
				t.Errorf("ref = %+v, want %q under %q", refs[0], tc.want, tc.setting)
			}
		})
	}
}

// provider: openai runs the loop in this process, so there is no binary.
func TestProviderBinariesIsEmptyForOpenAI(t *testing.T) {
	cfg := &config.Config{Root: "/ws", Provider: "openai"}
	if refs := providerBinaries(cfg); len(refs) != 0 {
		t.Errorf("refs = %+v, want none", refs)
	}
}

// A bare name stays a name for PATH to answer; only a value written as a
// path is made absolute against the workspace.
func TestProviderBinariesExpandsOnlyPathShapedValues(t *testing.T) {
	cfg := &config.Config{Root: "/ws", Provider: "claude"}
	cfg.Providers.Claude.Path = "claude-next"
	if got := providerBinaries(cfg)[0].name; got != "claude-next" {
		t.Errorf("name = %q, want the bare name left for PATH", got)
	}

	cfg.Providers.Claude.Path = "bin/claude"
	if got := providerBinaries(cfg)[0].name; got != "/ws/bin/claude" {
		t.Errorf("name = %q, want it resolved against the workspace", got)
	}
}

// A workspace that still carries a path for the provider it is not using
// gets that binary checked too: it is one edit away from being the active
// one, and the failure is worth knowing about now.
func TestProviderBinariesIncludesAnotherProvidersConfiguredPath(t *testing.T) {
	cfg := &config.Config{Root: "/ws", Provider: "claude"}
	cfg.Providers.Codex.Path = "/opt/homebrew/bin/codex"

	refs := providerBinaries(cfg)
	if len(refs) != 2 {
		t.Fatalf("refs = %+v, want claude and the configured codex", refs)
	}
	if refs[1].name != "/opt/homebrew/bin/codex" || refs[1].setting != "providers.codex.path" {
		t.Errorf("second ref = %+v, want the configured codex path", refs[1])
	}
}

// The doctor report is what an operator reads first, so the row has to be
// in it, not merely available.
func TestRunDoctorIncludesTheEnvironmentRow(t *testing.T) {
	cfg, err := config.Load(newWorkspace(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range RunDoctor(context.Background(), cfg) {
		if c.Name == "environment" {
			if c.Level == "" {
				t.Errorf("the environment row carries no level: %+v", c)
			}
			if !strings.Contains(c.Detail, "PATH from") {
				t.Errorf("detail = %q, want it to say where PATH came from", c.Detail)
			}
			return
		}
	}
	t.Fatal("RunDoctor reported no environment row")
}
