// Package agenttools implements the read-only tool set that Sirdar's own
// agent loop (internal/provider/openai) exposes to a model: reading files,
// listing directories, searching, globbing, running allow-listed shell
// commands and fetching a URL. Every argument arrives from the model and is
// therefore untrusted, so each tool re-checks it here rather than trusting
// the loop: file paths are confined to the workspace root through the real
// (symlink-resolved) path, shell commands must match the workspace policy's
// allow-list, and every tool's output is capped.
//
// The two confinements are not equally strong, and the difference matters.
// read_file, list_dir, grep and glob resolve a path and check where it
// really lands, so a symlink out of the workspace is refused. The bash tool
// cannot do that: it hands a string to a shell, and all the policy can do
// is read that string. So provider.MatchCommand refuses the operators it
// cannot judge — command and process substitution, redirection — and the
// arguments that look like paths outside the root. That is a heuristic and
// not a sandbox: it does not follow symlinks, does not know which of a
// program's arguments are paths, and cannot see a path a program derives at
// run time. An allow-list naming a program that reads a config file, spawns
// a shell of its own, or resolves its own paths gives that program the
// reach of the machine. A run that has to be confined for real needs a
// container around the process; this package narrows what an ordinary
// mistake or an ordinary prompt injection reaches, not what a determined
// one does.
package agenttools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/srivathsanvenkateswaran/sirdar/internal/provider"
)

// Spec describes one tool to the model: a stable name, a one-paragraph
// description and a JSON Schema (draft-07) for its arguments.
type Spec struct {
	Name        string
	Description string
	Parameters  json.RawMessage
}

// Tool is a single callable the agent loop offers the model. Call returns
// the text handed back to the model; an error becomes a tool-error message
// in the transcript, never a crash.
type Tool interface {
	Spec() Spec
	Call(ctx context.Context, args json.RawMessage) (string, error)
}

// Options configures the tool set.
//
// Root is the workspace directory every path argument is confined to, and
// the directory bash commands run in and are expected to stay inside.
// BashAllow holds the glob patterns from the workspace policy; a bash
// command runs only when provider.MatchCommand approves it against them
// and against Root. FetchAllow holds the host globs from
// permissions.fetch; web_fetch retrieves a URL only when
// provider.DecideFetchURL approves it against them, and an empty list —
// the default — allows nothing. HTTP is
// the client web_fetch borrows its transport from (nil means a default
// client). MaxOutputBytes caps every tool's output; 0 means
// DefaultMaxOutputBytes.
type Options struct {
	Root           string
	BashAllow      []string
	FetchAllow     []string
	HTTP           *http.Client
	MaxOutputBytes int

	// ExtraReserved names directories the writing tools refuse on top of
	// .git and .sirdar: the repository's core.hooksPath when it sets one.
	// It comes from the session's permission policy, so the tool and the
	// policy in front of it reserve the same paths.
	ExtraReserved []string
}

// DefaultMaxOutputBytes is the per-call output cap when Options leaves
// MaxOutputBytes at zero.
const DefaultMaxOutputBytes = 64 << 10

// errPathEscape is returned for every path argument that resolves outside
// the workspace root. The text is stable: the loop shows it to the model.
var errPathEscape = errors.New("path escapes workspace")

func (o Options) normalized() Options {
	if o.MaxOutputBytes <= 0 {
		o.MaxOutputBytes = DefaultMaxOutputBytes
	}
	if strings.TrimSpace(o.Root) == "" {
		o.Root = "."
	}
	if abs, err := filepath.Abs(o.Root); err == nil {
		o.Root = abs
	}
	return o
}

// ReadOnlySet returns the six read-only tools, in a stable order:
// read_file, list_dir, grep, glob, bash, web_fetch.
func ReadOnlySet(o Options) []Tool {
	o = o.normalized()
	return []Tool{
		o.readFileTool(),
		o.listDirTool(),
		o.grepTool(),
		o.globTool(),
		o.bashTool(),
		o.webFetchTool(),
	}
}

// toolFunc adapts a closure to the Tool interface.
type toolFunc struct {
	spec Spec
	call func(ctx context.Context, args json.RawMessage) (string, error)
}

func (t toolFunc) Spec() Spec { return t.spec }

func (t toolFunc) Call(ctx context.Context, args json.RawMessage) (string, error) {
	return t.call(ctx, args)
}

// decodeArgs unmarshals the model's arguments, tolerating an empty or null
// payload for tools whose fields are all optional.
func decodeArgs(raw json.RawMessage, dst any) error {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		trimmed = []byte("{}")
	}
	if err := json.Unmarshal(trimmed, dst); err != nil {
		return fmt.Errorf("invalid arguments: %v", err)
	}
	return nil
}

// root returns the workspace root with symlinks resolved. Callers use it
// both to confine paths and to render results relative to the workspace.
func (o Options) root() (string, error) {
	real, err := filepath.EvalSymlinks(o.Root)
	if err != nil {
		return "", fmt.Errorf("workspace root %s: %v", o.Root, err)
	}
	abs, err := filepath.Abs(real)
	if err != nil {
		return "", fmt.Errorf("workspace root %s: %v", o.Root, err)
	}
	return abs, nil
}

// resolve turns a model-supplied path into a real absolute path inside the
// workspace, or returns errPathEscape.
//
// A relative path is joined onto Root; an absolute path is taken as given.
// Both the candidate and the root are then resolved through symlinks — for
// a path that does not exist yet, the nearest existing parent is resolved
// and the remainder appended — so a symlink inside the workspace that
// points outside it is rejected like any other outside path. The rule
// itself is provider.ResolveWithin, which is also what the permission
// policy judging a provider CLI's own Edit and Write goes through: one
// confinement over two surfaces, rather than two that drift apart.
func (o Options) resolve(p string) (string, error) {
	real, err := provider.ResolveWithin(o.Root, p)
	if err != nil {
		return "", errPathEscape
	}
	return real, nil
}

// resolveWrite is resolve for the tools that change a file: confined to the
// workspace, and then refused for the directories inside it a fix has no
// business writing to. A file under .git/ is not source — a pre-commit hook
// written there is code the commit Sirdar makes would execute — .sirdar/
// holds the run records, the register and the configuration whose
// permission lists decide what this session may do at all, and
// ExtraReserved carries the repository's own core.hooksPath, which is the
// same hook problem wearing an ordinary directory name.
func (o Options) resolveWrite(p string) (string, error) {
	abs, err := o.resolve(p)
	if err != nil {
		return "", err
	}
	if reserved := provider.ReservedWrite(o.Root, abs, o.ExtraReserved); reserved != "" {
		return "", fmt.Errorf("%s is inside %s/, which a fix never writes to", p, reserved)
	}
	return abs, nil
}

// rel renders an absolute path inside the workspace relative to root, so
// tool output never leaks the machine's directory layout.
func rel(root, abs string) string {
	r, err := filepath.Rel(root, abs)
	if err != nil {
		return abs
	}
	return filepath.ToSlash(r)
}

// truncate caps s at max bytes, cutting back to the last line boundary and
// appending a marker naming how many bytes were dropped. The bool reports
// whether anything was cut.
func truncate(s string, max int) (string, bool) {
	if max <= 0 || len(s) <= max {
		return s, false
	}
	cut := s[:max]
	if i := strings.LastIndexByte(cut, '\n'); i >= 0 {
		cut = cut[:i]
	}
	dropped := len(s) - len(cut)
	return cut + "\n[truncated " + strconv.Itoa(dropped) + " bytes]", true
}

// looksBinary reports whether a sniffed prefix contains a NUL byte, the
// test both read_file and the search tools use to skip binary content.
func looksBinary(prefix []byte) bool {
	return bytes.IndexByte(prefix, 0) >= 0
}
