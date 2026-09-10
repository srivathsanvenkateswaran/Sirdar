package agenttools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

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
    "command": {"type": "string", "description": "Shell command to run with sh -c in the workspace root. It must match one of the workspace's allow-listed command patterns or it is refused."}
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

	cmd := exec.CommandContext(runCtx, "sh", "-c", command)
	cmd.Dir = root
	cmd.Env = execEnv()
	var buf bytes.Buffer
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

// allowed applies the workspace policy's Bash allow-list to the trimmed
// command, using the same matcher the permission policy uses so the model
// cannot reach anything the policy would refuse.
func (o Options) allowed(command string) bool {
	for _, pattern := range o.BashAllow {
		if provider.MatchGlob(pattern, command) {
			return true
		}
	}
	return false
}

// execEnv is the minimal environment child processes get: enough to find
// binaries and behave predictably, without handing the model the parent's
// credentials or tokens.
func execEnv() []string {
	env := []string{"PATH=" + pathOrDefault()}
	for _, name := range []string{"HOME", "LANG"} {
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
	return "/usr/local/bin:/usr/bin:/bin"
}
