package agenttools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/procgroup"
	"github.com/srivathsanvenkateswaran/sirdar/internal/provider"
)

// bashTimeout bounds one shell command. Tests shorten it.
var bashTimeout = 60 * time.Second

// errDenied is the exact text a denied command produces. The agent loop
// shows it to the model verbatim, and the string is asserted by tests, so
// do not reword it.
var errDenied = errors.New("denied: command not in the allow-list")

var bashSchema = json.RawMessage(`{
  "$schema": "http://json-schema.org/draft-07/schema#",
  "type": "object",
  "additionalProperties": false,
  "properties": {
    "command": {"type": "string", "description": "Shell command to run in the workspace root, through sh -c (cmd /C on Windows). It must match one of the workspace's allow-listed command patterns or it is refused."}
  },
  "required": ["command"]
}`)

func (o Options) bashTool() Tool {
	allow := "none configured"
	if len(o.BashAllow) > 0 {
		allow = strings.Join(o.BashAllow, ", ")
	}
	return toolFunc{
		spec: Spec{
			Name: "bash",
			Description: "Run a read-only shell command in the workspace root. Only commands matching the workspace allow-list are permitted (" + allow +
				"); anything else is refused. Output is combined stdout and stderr, capped.",
			Parameters: bashSchema,
		},
		call: o.bash,
	}
}

func (o Options) bash(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		Command string `json:"command"`
	}
	if err := decodeArgs(args, &a); err != nil {
		return "", err
	}
	command := strings.TrimSpace(a.Command)
	if command == "" {
		return "", errors.New("bash: command is required")
	}
	if !o.allowed(command) {
		return "", errDenied
	}
	root, err := o.root()
	if err != nil {
		return "", err
	}

	runCtx, cancel := context.WithTimeout(ctx, bashTimeout)
	defer cancel()

	shell, flag := shellFor(runtime.GOOS)
	cmd := exec.CommandContext(runCtx, shell, flag, command)
	cmd.Dir = root
	cmd.Env = execEnv()
	// The command runs in a process group of its own so a timeout can kill
	// everything it started: `sh -c "rg ... | head"` leaves children that
	// keep the output pipe open, and killing only the shell would leave
	// them running and Wait blocked on that pipe. WaitDelay is the backstop
	// for a grandchild that survives the group kill.
	procgroup.Setup(cmd)
	cmd.Cancel = func() error { return procgroup.Kill(cmd) }
	cmd.WaitDelay = killGrace
	var buf lockedBuffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	runErr := cmd.Run()

	out, _ := truncate(buf.String(), o.MaxOutputBytes)
	if runCtx.Err() == context.DeadlineExceeded {
		return out, fmt.Errorf("bash: command timed out after %s", bashTimeout)
	}
	if runErr != nil {
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			if out != "" && !strings.HasSuffix(out, "\n") {
				out += "\n"
			}
			return out + "[exit status " + strconv.Itoa(exitErr.ExitCode()) + "]", nil
		}
		return out, fmt.Errorf("bash: %v", runErr)
	}
	return out, nil
}

// shellFor names the command interpreter this platform runs an allow-listed
// command through: `sh -c` everywhere Sirdar has a POSIX shell, `cmd /C` on
// Windows, which has no /bin/sh at all. It takes the platform as an argument
// so both branches are testable from either kind of machine.
//
// The allow-list is matched against the command text before it gets here
// (Options.allowed), on the same splitter the permission policy uses, so a
// command that hides a second one behind a pipe or a `&&` is already
// refused whichever interpreter would have run it. What changes across
// platforms is only whether the one allowed command can be started.
func shellFor(goos string) (shell, flag string) {
	if goos == "windows" {
		return "cmd", "/C"
	}
	return "sh", "-c"
}

// killGrace is how long a timed-out command's process group has to die
// before exec gives up waiting on the pipes it holds.
const killGrace = 2 * time.Second

// lockedBuffer collects a command's combined output. A command killed at
// the timeout can leave a child writing into it after Wait has returned, so
// every access is guarded.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// allowed applies the workspace policy's Bash allow-list to the trimmed
// command, using the same matcher the permission policy uses so the model
// cannot reach anything the policy would refuse — including a command that
// hides a second one behind a pipe or a semicolon, and one whose arguments
// point outside the workspace root the command runs in.
func (o Options) allowed(command string) bool {
	ok, _ := provider.MatchCommand(o.Root, o.BashAllow, command)
	return ok
}

// execEnv is the minimal environment child processes get: enough to find
// binaries and behave predictably, without handing the model the parent's
// credentials or tokens.
//
// Windows needs more of the environment than a Unix child does before
// anything runs at all: cmd.exe resolves itself through COMSPEC, the C
// runtime and every Win32 API load their DLLs relative to SystemRoot, and
// PATHEXT is what makes `git` find git.exe rather than a file literally
// named "git". Passing HOME alone there produces a shell that cannot start.
// None of those four carry a credential.
func execEnv() []string {
	env := []string{"PATH=" + pathOrDefault()}
	names := []string{"HOME", "LANG"}
	if runtime.GOOS == "windows" {
		names = []string{"USERPROFILE", "HOMEDRIVE", "HOMEPATH", "SystemRoot", "windir", "COMSPEC", "PATHEXT", "TEMP", "TMP"}
	}
	for _, name := range names {
		if v := os.Getenv(name); v != "" {
			env = append(env, name+"="+v)
		}
	}
	return env
}

func pathOrDefault() string {
	if p := os.Getenv("PATH"); p != "" {
		return p
	}
	if runtime.GOOS == "windows" {
		// No PATH at all is close to unrecoverable on Windows, but the
		// system directories are where cmd.exe and the shipped tools live,
		// so this at least lets a command start.
		root := os.Getenv("SystemRoot")
		if root == "" {
			root = `C:\Windows`
		}
		return root + `\system32;` + root
	}
	return "/usr/local/bin:/usr/bin:/bin"
}
