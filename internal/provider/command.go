package provider

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
)

// UnwrapCommand turns the "command" field an agent sends with a shell
// approval — Codex's item/commandExecution/requestApproval, ACP's
// session/request_permission for an execute kind — into the shell command
// the permission policy decides on.
//
// Codex 0.154 sends a string — the whole command line, login-shell wrapper
// and all — but the field has been an argv array in other app-server
// builds, and an ACP agent may send either, so both are read. A string
// that is not a shell invocation is the command itself and passes through
// untouched; a string or argv that is `<sh|bash|zsh> -c|-lc|-ic|-lic
// <script>` yields the script; anything else shaped like a shell, and any
// argv that is not that wrapper, is an error, which the caller turns into
// a denial.
//
// An argv of a plain command is refused rather than joined back into a
// line: `["echo","a b"]` and `echo a b` are different commands, and a
// policy that matches on text cannot tell which one it was handed.
func UnwrapCommand(field json.RawMessage) (string, error) {
	if len(field) == 0 {
		return "", nil
	}

	var argv []string
	if err := json.Unmarshal(field, &argv); err == nil {
		script, ok := shellWrapper(argv)
		if !ok {
			return "", fmt.Errorf("command argv %v is not a %s wrapper, and an argv "+
				"cannot be matched against permissions.bash as text", argv, strings.Join(wrapperShells, "/"))
		}
		return script, nil
	}

	var line string
	if err := json.Unmarshal(field, &line); err != nil {
		return "", fmt.Errorf("command is neither a string nor an argv array, so what it " +
			"would run cannot be checked")
	}
	tokens := shellTokens(line)
	if len(tokens) == 0 || !isWrapperShell(tokens[0]) {
		return line, nil
	}
	script, ok := shellWrapper(tokens)
	if !ok {
		return "", fmt.Errorf("%q invokes a shell in a shape Sirdar cannot read, so what "+
			"it would run cannot be checked; only `<shell> -c <script>` is unwrapped", line)
	}
	return script, nil
}

// wrapperShells are the shells whose -c invocation is peeled off. Matching
// is on the basename, so /bin/zsh counts and /usr/local/bin/fish does not.
var wrapperShells = []string{"sh", "bash", "zsh"}

func isWrapperShell(word string) bool {
	base := filepath.Base(word)
	for _, sh := range wrapperShells {
		if base == sh {
			return true
		}
	}
	return false
}

// shellWrapper reports the script of a `<shell> <flags> <script>` argv, and
// whether the argv had exactly that shape. The flag token has to be a
// single-dash bundle of the login/interactive/-c letters — -c, -lc, -ic,
// -lic — because any other letter can change what the shell does with the
// script, and a trailing argument would become $0 or $1 inside it, neither
// of which the matched text would show.
func shellWrapper(argv []string) (string, bool) {
	if len(argv) != 3 || !isWrapperShell(argv[0]) {
		return "", false
	}
	flags := argv[1]
	if len(flags) < 2 || flags[0] != '-' || flags[1] == '-' {
		return "", false
	}
	sawC := false
	for _, r := range flags[1:] {
		switch r {
		case 'c':
			sawC = true
		case 'l', 'i':
		default:
			return "", false
		}
	}
	if !sawC {
		return "", false
	}
	return argv[2], true
}

// shellTokens splits a command line into words the way the policy's own
// argument scan does: single and double quotes group, a backslash escapes
// the next byte, and the quotes themselves are dropped. It is only ever
// asked whether the line is a shell wrapper, so it needs to get the first
// three words right, not to be a shell.
func shellTokens(line string) []string {
	var (
		out   []string
		cur   strings.Builder
		quote = byte(0)
		open  bool
	)
	flush := func() {
		if open {
			out = append(out, cur.String())
		}
		cur.Reset()
		open = false
	}
	for i := 0; i < len(line); i++ {
		ch := line[i]
		switch {
		case quote != 0:
			if ch == quote {
				quote = 0
				continue
			}
			cur.WriteByte(ch)
			open = true
		case ch == '\'' || ch == '"':
			quote = ch
			open = true
		case ch == '\\' && i+1 < len(line):
			i++
			cur.WriteByte(line[i])
			open = true
		case ch == ' ' || ch == '\t':
			flush()
		default:
			cur.WriteByte(ch)
			open = true
		}
	}
	flush()
	return out
}
