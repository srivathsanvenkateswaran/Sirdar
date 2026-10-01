package claude

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/procgroup"
	"github.com/srivathsanvenkateswaran/sirdar/internal/provider"
)

// probeTimeout bounds one probe: the CLI writes its system/init line
// before it talks to the API, so a probe that has not seen one in this
// long is not going to.
const probeTimeout = 30 * time.Second

// ErrNoInit is a probe whose CLI exited, or was given up on, without
// writing a system/init line naming a model.
var ErrNoInit = errors.New("claude: no system/init line named a model")

// probeArgs is the cheapest session the CLI will start: a one-character
// prompt, one turn, no tools, no MCP servers, nothing saved to disk. The
// model is read off the init line, which the CLI writes before its first
// request, and the process is stopped as soon as that line arrives.
//
// Claude Code has no command that lists the models a login can use
// (`claude --help` on 2.1.283 offers none, and no settings file records
// them), so asking it to resolve each alias is the only way to learn what
// "opus" means on this login today.
func probeArgs(alias string) []string {
	return []string{
		"-p", ".",
		"--max-turns", "1",
		"--output-format", "stream-json",
		"--verbose",
		"--model", alias,
		"--no-session-persistence",
		"--tools", "",
		"--strict-mcp-config", "--mcp-config", emptyMCPConfig,
	}
}

// ProbeModel asks the CLI at binary which model alias resolves to, and
// answers with the id its system/init line reports — `opus` comes back as
// `claude-opus-4-5-20251101`, say. env is the environment the probe runs
// in; empty means this process's own, with the API key and gateway
// variables taken out the way a subscription-billed run takes them out, so
// the probe asks the same login a run would.
func ProbeModel(ctx context.Context, binary, alias string, env []string) (string, error) {
	if binary == "" {
		binary = defaultBinary
	}
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()

	dir, err := os.MkdirTemp("", "sirdar-probe-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)

	cmd := exec.CommandContext(ctx, binary, probeArgs(alias)...)
	procgroup.Setup(cmd)
	cmd.Cancel = func() error { return procgroup.Kill(cmd) }
	cmd.WaitDelay = 2 * time.Second
	// An empty directory, so no project settings, CLAUDE.md or .mcp.json
	// of whichever workspace is open colours the answer.
	cmd.Dir = dir
	cmd.Env, _ = childEnv(provider.SessionSpec{Env: env})
	cmd.Stdin = strings.NewReader("")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", err
	}
	tail := &tailWriter{max: 5}
	cmd.Stderr = tail
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("claude: %w", err)
	}
	model, readErr := initModel(stdout)
	// The answer is in hand, or never coming: either way the session is
	// not wanted, and stopping it here is what keeps the probe from
	// spending a turn.
	cancel()
	_ = cmd.Wait()
	if readErr != nil {
		if lines := tail.snapshot(); len(lines) > 0 {
			return "", fmt.Errorf("%w: %s", readErr, strings.TrimSpace(lines[0]))
		}
		return "", readErr
	}
	return model, nil
}

// initModel reads stream-json lines until the system/init line and
// answers with the model it names.
func initModel(r io.Reader) (string, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), maxLineBytes)
	for sc.Scan() {
		var l streamLine
		if json.Unmarshal(sc.Bytes(), &l) != nil {
			continue
		}
		if l.Type == "system" && l.Subtype == "init" {
			if m := strings.TrimSpace(l.Model); m != "" {
				return m, nil
			}
			return "", ErrNoInit
		}
	}
	return "", ErrNoInit
}
