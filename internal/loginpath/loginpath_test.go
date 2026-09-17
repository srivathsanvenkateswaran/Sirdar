package loginpath

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

const sep = string(os.PathListSeparator)

// launchdPATH is what a macOS GUI process actually inherits when it is
// started from the Dock. Every case below starts from it.
const launchdPATH = "/usr/bin:/bin:/usr/sbin:/sbin"

func split(path string) []string { return strings.Split(path, sep) }

func has(path, dir string) bool {
	for _, entry := range split(path) {
		if entry == dir {
			return true
		}
	}
	return false
}

func indexOf(path, dir string) int {
	for i, entry := range split(path) {
		if entry == dir {
			return i
		}
	}
	return -1
}

func shellReporting(value string) runner {
	return func(context.Context, string, string) (string, error) { return value, nil }
}

// The bug this package exists for: the Dock's PATH has no /opt/homebrew/bin,
// the terminal's does, and after resolve the process has the terminal's.
func TestResolveTakesTheLoginShellPATH(t *testing.T) {
	shell := "/opt/homebrew/bin:/usr/bin:/bin"
	res := resolve(context.Background(), "darwin", "/bin/zsh", launchdPATH, "/Users/sri", shellReporting(shell))

	if res.Source != "login shell /bin/zsh" {
		t.Errorf("Source = %q, want the login shell", res.Source)
	}
	if res.Flags != "-il" {
		t.Errorf("Flags = %q, want the interactive attempt to have answered", res.Flags)
	}
	if !has(res.Path, "/opt/homebrew/bin") {
		t.Errorf("PATH = %q, want /opt/homebrew/bin on it", res.Path)
	}
	if got, want := indexOf(res.Path, "/opt/homebrew/bin"), 0; got != want {
		t.Errorf("/opt/homebrew/bin at %d, want %d — the shell's own order is kept", got, want)
	}
}

// -il is tried first and -l is the fallback, which is the point of having
// two: a profile that errors under an interactive shell still yields a
// PATH.
func TestResolveFallsBackToTheNonInteractiveLoginShell(t *testing.T) {
	var tried []string
	run := func(_ context.Context, _, flags string) (string, error) {
		tried = append(tried, flags)
		if flags == "-il" {
			return "", errors.New("zsh: no matches found")
		}
		return "/opt/homebrew/bin:/usr/bin", nil
	}
	res := resolve(context.Background(), "darwin", "/bin/zsh", launchdPATH, "/Users/sri", run)

	if strings.Join(tried, ",") != "-il,-l" {
		t.Errorf("attempts = %v, want -il then -l", tried)
	}
	if res.Flags != "-l" {
		t.Errorf("Flags = %q, want -l", res.Flags)
	}
	if res.Err != nil {
		t.Errorf("Err = %v, want none once the fallback answered", res.Err)
	}
	if !has(res.Path, "/opt/homebrew/bin") {
		t.Errorf("PATH = %q, want the fallback's answer", res.Path)
	}
}

// A shell that cannot be run at all leaves the inherited PATH in place and
// says why. The extra directories are still appended: they are the half of
// the fix that does not depend on a shell answering.
func TestResolveKeepsTheProcessPATHWhenNoShellAnswers(t *testing.T) {
	boom := errors.New("fork/exec /bin/zsh: no such file or directory")
	run := func(context.Context, string, string) (string, error) { return "", boom }
	res := resolve(context.Background(), "darwin", "/bin/zsh", launchdPATH, "/Users/sri", run)

	if res.Source != "process" {
		t.Errorf("Source = %q, want process", res.Source)
	}
	if !errors.Is(res.Err, boom) {
		t.Errorf("Err = %v, want the shell's failure", res.Err)
	}
	if !has(res.Path, "/usr/bin") {
		t.Errorf("PATH = %q, want the inherited entries kept", res.Path)
	}
	if !has(res.Path, "/opt/homebrew/bin") {
		t.Errorf("PATH = %q, want the extra directories appended anyway", res.Path)
	}
}

// An interactive profile that greets the operator writes to stdout before
// printf does. The PATH is the last line, not the whole capture.
func TestResolveIgnoresAProfilesChatter(t *testing.T) {
	out := "Welcome back!\nnvm: using node v22\n/opt/homebrew/bin:/usr/bin"
	res := resolve(context.Background(), "darwin", "/bin/zsh", launchdPATH, "/Users/sri", shellReporting(out))

	if got := split(res.Path)[0]; got != "/opt/homebrew/bin" {
		t.Errorf("first entry = %q, want /opt/homebrew/bin", got)
	}
	if strings.Contains(res.Path, "Welcome") {
		t.Errorf("PATH = %q, want none of the greeting", res.Path)
	}
}

// A shell whose whole output is a message rather than a PATH is not
// believed, and the process PATH stands.
func TestResolveRejectsAnAnswerThatIsNotAPATH(t *testing.T) {
	res := resolve(context.Background(), "darwin", "/bin/zsh", launchdPATH, "/Users/sri", shellReporting("command not found"))
	if res.Source != "process" {
		t.Errorf("Source = %q, want process", res.Source)
	}
}

// Windows is the no-op: nothing is run, and the PATH is handed back
// untouched — not even the Unix-shaped extra directories are appended.
func TestResolveIsANoOpOnWindows(t *testing.T) {
	ran := false
	run := func(context.Context, string, string) (string, error) { ran = true; return "", nil }
	before := `C:\Windows\system32;C:\Users\sri\go\bin`
	res := resolve(context.Background(), "windows", "", before, `C:\Users\sri`, run)

	if ran {
		t.Error("ran a login shell on Windows")
	}
	if res.Path != before {
		t.Errorf("PATH = %q, want it unchanged", res.Path)
	}
	if res.Source != "process" {
		t.Errorf("Source = %q, want process", res.Source)
	}
}

func TestShellFor(t *testing.T) {
	cases := []struct {
		goos, shellEnv, want string
	}{
		{"darwin", "/bin/zsh", "/bin/zsh"},
		{"darwin", "", "/bin/zsh"},
		{"darwin", "  ", "/bin/zsh"},
		{"darwin", "/opt/homebrew/bin/fish", "/opt/homebrew/bin/fish"},
		{"linux", "", "/bin/sh"},
		{"linux", "/usr/bin/bash", "/usr/bin/bash"},
		{"freebsd", "", "/bin/sh"},
		{"windows", `C:\Windows\system32\cmd.exe`, ""},
	}
	for _, c := range cases {
		if got := shellFor(c.goos, c.shellEnv); got != c.want {
			t.Errorf("shellFor(%q, %q) = %q, want %q", c.goos, c.shellEnv, got, c.want)
		}
	}
}

// The merge is the half that runs whatever the shell said: every missing
// directory is appended once, in the documented order, behind everything
// the shell already had.
func TestMergeAppendsWhatIsMissing(t *testing.T) {
	base := "/opt/homebrew/bin:/usr/bin"
	path, added := merge(base, extraDirs, "/Users/sri")

	if got := strings.Join(split(path)[:2], sep); got != base {
		t.Errorf("PATH starts %q, want the base kept in order at the front", got)
	}
	for _, want := range []string{
		"/Users/sri/.local/bin", "/usr/local/bin", "/Users/sri/go/bin",
		"/Users/sri/.npm-global/bin", "/Users/sri/.bun/bin", "/Users/sri/.cargo/bin",
		"/Users/sri/.claude/local", "/Users/sri/.opencode/bin", "/Users/sri/bin",
	} {
		if !has(path, want) {
			t.Errorf("PATH = %q, want %q appended", path, want)
		}
		if !contains(added, want) {
			t.Errorf("Added = %v, want %q named", added, want)
		}
	}
	if contains(added, "/opt/homebrew/bin") {
		t.Errorf("Added = %v, want /opt/homebrew/bin left out: the base already had it", added)
	}
}

// Nothing appears twice, whether the duplicate came from the shell, from
// the extras, or from a trailing slash making one entry look like another.
func TestMergeDedupes(t *testing.T) {
	base := "/usr/bin:/opt/homebrew/bin:/usr/bin:/opt/homebrew/bin/:/Users/sri/bin"
	path, added := merge(base, extraDirs, "/Users/sri")

	counts := map[string]int{}
	for _, entry := range split(path) {
		counts[strings.TrimSuffix(entry, "/")]++
	}
	for entry, n := range counts {
		if n > 1 {
			t.Errorf("%q appears %d times in %q", entry, n, path)
		}
	}
	if contains(added, "/Users/sri/bin") {
		t.Errorf("Added = %v, want ~/bin left out: the base already had it", added)
	}
	if got := split(path)[0]; got != "/usr/bin" {
		t.Errorf("first entry = %q, want the first occurrence kept", got)
	}
}

// With no home directory to expand them against, the "~/" entries are
// dropped rather than joined literally: a directory named "~" on the PATH
// is worse than a missing one.
func TestMergeDropsTildeEntriesWithoutAHome(t *testing.T) {
	path, added := merge("/usr/bin", extraDirs, "")
	if strings.Contains(path, "~") {
		t.Errorf("PATH = %q, want no literal ~ entry", path)
	}
	for _, want := range []string{"/opt/homebrew/bin", "/usr/local/bin"} {
		if !contains(added, want) {
			t.Errorf("Added = %v, want the absolute extras still appended (%q)", added, want)
		}
	}
}

func TestMergeOnAnEmptyBase(t *testing.T) {
	path, _ := merge("", extraDirs, "/Users/sri")
	if got := split(path)[0]; got != "/Users/sri/.local/bin" {
		t.Errorf("first entry = %q, want the extras to stand alone", got)
	}
}

func TestLastLine(t *testing.T) {
	cases := []struct{ in, want string }{
		{"/usr/bin", "/usr/bin"},
		{"hello\n/usr/bin", "/usr/bin"},
		{"hello\r\n/usr/bin", "/usr/bin"},
		{"/usr/bin\n", "/usr/bin"},
		{"/usr/bin\n\n  \n", "/usr/bin"},
		{"", ""},
	}
	for _, c := range cases {
		if got := lastLine(c.in); got != c.want {
			t.Errorf("lastLine(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// Current answers before Apply has ever run — which is every CLI
// invocation — with the process environment as it stands.
func TestCurrentWithoutApply(t *testing.T) {
	mu.Lock()
	saved := current
	current = nil
	mu.Unlock()
	t.Cleanup(func() {
		mu.Lock()
		current = saved
		mu.Unlock()
	})

	got := Current()
	if got.Source != "process" {
		t.Errorf("Source = %q, want process", got.Source)
	}
	if got.Path != os.Getenv("PATH") {
		t.Errorf("Path = %q, want the process PATH", got.Path)
	}
}

// Each attempt is capped on its own, so a profile that blocks costs the
// window one timeout and not the whole startup.
func TestResolveCapsEachAttempt(t *testing.T) {
	var deadlines int
	run := func(ctx context.Context, _, flags string) (string, error) {
		d, ok := ctx.Deadline()
		if !ok {
			t.Errorf("%s attempt had no deadline", flags)
		} else if until := time.Until(d); until <= 0 || until > shellTimeout {
			t.Errorf("%s attempt deadline in %v, want within %v", flags, until, shellTimeout)
		} else {
			deadlines++
		}
		if flags == "-il" {
			return "", context.DeadlineExceeded
		}
		return "/opt/homebrew/bin:/usr/bin", nil
	}
	res := resolve(context.Background(), "darwin", "/bin/zsh", launchdPATH, "/Users/sri", run)

	if deadlines != 2 {
		t.Errorf("capped %d attempts, want 2", deadlines)
	}
	if res.Flags != "-l" {
		t.Errorf("Flags = %q, want the timed-out interactive attempt to have fallen back", res.Flags)
	}
}

func contains(list []string, want string) bool {
	for _, got := range list {
		if got == want {
			return true
		}
	}
	return false
}
