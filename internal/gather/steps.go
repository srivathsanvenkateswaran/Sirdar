package gather

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/srivathsanvenkateswaran/sirdar/internal/config"
	"github.com/srivathsanvenkateswaran/sirdar/internal/mcpclient"
	"github.com/srivathsanvenkateswaran/sirdar/internal/procgroup"
	"github.com/srivathsanvenkateswaran/sirdar/internal/provider"
)

// maxInvocations caps how many times one step runs. A step naming two
// variables of three values each would otherwise run nine times, and the
// tenth value is rarely the one that matters.
const maxInvocations = 5

// maxValueBytes is the longest value the value rule accepts.
const maxValueBytes = 128

// stderrTail is how much of a failing command's stderr is kept for its
// row's reason.
const stderrTail = 2048

// templateRef finds a "{{name}}" reference, the same shape config
// validation accepts.
var templateRef = regexp.MustCompile(`\{\{\s*([^{}]*?)\s*\}\}`)

// shellBare is what ShellQuote leaves unquoted.
var shellBare = regexp.MustCompile(`^[A-Za-z0-9_./:=@%+,-]+$`)

// Values finds a variable's values in the run's text: each distinct
// capture of its pattern, first seen first, across the sources it names.
// A capture that fails the value rule is returned in rejected and never
// used. At most v.Max values are kept.
func Values(v config.GatherVariable, in Input) (values []string, rejected []string) {
	re, err := regexp.Compile(v.Pattern)
	if err != nil {
		return nil, nil
	}
	seen := map[string]bool{}
	for _, src := range v.From {
		for _, m := range re.FindAllStringSubmatch(sourceText(src, in), -1) {
			if len(m) < 2 || seen[m[1]] {
				continue
			}
			seen[m[1]] = true
			if !ValidValue(m[1]) {
				rejected = append(rejected, m[1])
				continue
			}
			values = append(values, m[1])
			if len(values) == v.Max {
				return values, rejected
			}
		}
	}
	return values, rejected
}

// sourceText is the text one gather source names.
func sourceText(src string, in Input) string {
	b := in.Bundle
	var parts []string
	switch src {
	case "ticket":
		if b.Tracker != nil {
			parts = append(parts, b.Tracker.Title, b.Tracker.Description)
		}
		if b.Helpdesk != nil {
			parts = append(parts, b.Helpdesk.Subject)
		}
		if b.Reported != nil {
			parts = append(parts, b.Reported.Description, strings.Join(b.Reported.Links, "\n"))
		}
	case "thread":
		for _, m := range b.Thread {
			parts = append(parts, m.Text)
		}
	case "slack":
		parts = append(parts, in.Slack)
	case "instruction":
		parts = append(parts, in.Instruction)
	}
	return strings.Join(parts, "\n")
}

// ValidValue is the rule every value taken from a ticket must pass before
// it reaches an argument, and config cannot change it. A command's line is
// judged by the allow-list as text and run as argv, and this rule is what
// makes the two read the same: with no whitespace, quote, backtick, shell
// operator or backslash in a value, the line splits where the argv does.
// The leading "-" is refused so a value can never be read as a flag.
func ValidValue(s string) bool {
	if len(s) == 0 || len(s) > maxValueBytes || strings.HasPrefix(s, "-") {
		return false
	}
	for _, r := range s {
		if unicode.IsSpace(r) || unicode.IsControl(r) || strings.ContainsRune("'\"`$;|&<>\\", r) {
			return false
		}
	}
	return true
}

// Render substitutes values into a parsed argv, element by element, so a
// value lands inside one argument whatever it contains. An element that
// starts with "~" in the config is expanded with expand after
// substitution; a value can never introduce one. A reference in argv[0]
// is an error: what runs is never chosen by the ticket.
func Render(argv []string, values map[string]string, expand func(string) string) ([]string, error) {
	out := make([]string, len(argv))
	for i, elem := range argv {
		if i == 0 && strings.Contains(elem, "{{") {
			return nil, fmt.Errorf("gather: %q: the program to run cannot come from a variable", elem)
		}
		s, err := substitute(elem, values)
		if err != nil {
			return nil, err
		}
		if strings.HasPrefix(elem, "~") && expand != nil {
			s = expand(s)
		}
		out[i] = s
	}
	return out, nil
}

// RenderArgs substitutes values into the string leaves of an MCP step's
// argument tree and marshals the result. The arguments are never built as
// text, so a value is always one JSON string.
func RenderArgs(args map[string]any, values map[string]string) (json.RawMessage, error) {
	tree, err := renderTree(args, values)
	if err != nil {
		return nil, err
	}
	if tree == nil {
		tree = map[string]any{}
	}
	return json.Marshal(tree)
}

func renderTree(v any, values map[string]string) (any, error) {
	switch t := v.(type) {
	case map[string]any:
		if t == nil {
			return nil, nil
		}
		out := make(map[string]any, len(t))
		for k, sub := range t {
			if strings.Contains(k, "{{") {
				return nil, fmt.Errorf("gather: map key %q cannot reference a variable", k)
			}
			r, err := renderTree(sub, values)
			if err != nil {
				return nil, err
			}
			out[k] = r
		}
		return out, nil
	case []any:
		out := make([]any, len(t))
		for i, sub := range t {
			r, err := renderTree(sub, values)
			if err != nil {
				return nil, err
			}
			out[i] = r
		}
		return out, nil
	case string:
		return substitute(t, values)
	default:
		return v, nil
	}
}

// substitute replaces every "{{name}}" in s with its value.
func substitute(s string, values map[string]string) (string, error) {
	var missing string
	out := templateRef.ReplaceAllStringFunc(s, func(ref string) string {
		name := templateRef.FindStringSubmatch(ref)[1]
		v, ok := values[name]
		if !ok && missing == "" {
			missing = name
		}
		return v
	})
	if missing != "" {
		return "", fmt.Errorf("gather: %q names %q, which has no value", s, missing)
	}
	return out, nil
}

// ShellQuote quotes one argument the way a POSIX shell reads it back:
// bare when it is made only of characters no shell treats specially,
// single-quoted otherwise.
func ShellQuote(arg string) string {
	if shellBare.MatchString(arg) {
		return arg
	}
	return "'" + strings.ReplaceAll(arg, "'", `'\''`) + "'"
}

// DisplayLine is an argv as one shell-quoted line: what the allow-list
// judges and what a person reads. It is never handed to a shell.
func DisplayLine(argv []string) string {
	q := make([]string, len(argv))
	for i, a := range argv {
		q[i] = ShellQuote(a)
	}
	return strings.Join(q, " ")
}

// stepRefs is the sorted set of variables a step references.
func stepRefs(s config.GatherStep) []string {
	seen := map[string]bool{}
	add := func(str string) {
		for _, m := range templateRef.FindAllStringSubmatch(str, -1) {
			seen[m[1]] = true
		}
	}
	for _, elem := range s.Command {
		add(elem)
	}
	if s.MCP != nil {
		var walk func(v any)
		walk = func(v any) {
			switch t := v.(type) {
			case map[string]any:
				for _, sub := range t {
					walk(sub)
				}
			case []any:
				for _, sub := range t {
					walk(sub)
				}
			case string:
				add(t)
			}
		}
		walk(s.MCP.Args)
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// product is the cartesian product of the referenced variables' values,
// first variable outermost, capped at maxInvocations.
func product(refs []string, vals map[string][]string) []map[string]string {
	combos := []map[string]string{{}}
	for _, name := range refs {
		var next []map[string]string
		for _, c := range combos {
			for _, v := range vals[name] {
				m := make(map[string]string, len(c)+1)
				for k, x := range c {
					m[k] = x
				}
				m[name] = v
				next = append(next, m)
			}
		}
		combos = next
	}
	if len(combos) > maxInvocations {
		combos = combos[:maxInvocations]
	}
	return combos
}

// valuesLine is a combination as the file header and row name show it.
func valuesLine(refs []string, combo map[string]string) string {
	parts := make([]string, 0, len(refs))
	for _, r := range refs {
		parts = append(parts, r+"="+combo[r])
	}
	return strings.Join(parts, ", ")
}

func stepKind(s config.GatherStep) string {
	if s.MCP != nil {
		return KindMCP
	}
	return KindCommand
}

// planSteps lays out the configured steps for this run's kind.
func (g *gatherer) planSteps() (jobs []job, settled []int) {
	cache := map[string][]string{}
	rejects := map[string][]string{}
	valuesOf := func(name string) []string {
		if v, ok := cache[name]; ok {
			return v
		}
		vals, rej := Values(g.cfg.Gather.Variables[name], g.in)
		cache[name], rejects[name] = vals, rej
		return vals
	}

	for _, s := range g.cfg.Gather.Steps {
		s := s
		if !contains(s.Kinds, g.in.Kind) {
			continue
		}
		refs := stepRefs(s)
		vals := map[string][]string{}
		var note []string
		empty := ""
		for _, r := range refs {
			vals[r] = valuesOf(r)
			for _, v := range rejects[r] {
				note = append(note, fmt.Sprintf("value %q rejected", v))
			}
			if len(vals[r]) == 0 && empty == "" {
				empty = r
			}
		}
		rejectNote := strings.Join(note, "; ")

		if empty != "" {
			i := g.addRow(Entry{
				Name:   s.Name,
				Kind:   stepKind(s),
				Status: StatusSkipped,
				Reason: joinReasons("no "+empty+" in the ticket", rejectNote),
			})
			settled = append(settled, i)
			continue
		}

		for n, combo := range product(refs, vals) {
			name := s.Name
			for _, r := range refs {
				name += "-" + fileSafe(combo[r])
			}
			e := Entry{Name: name, Kind: stepKind(s)}
			if n == 0 {
				e.Reason = rejectNote
			}
			i := g.addRow(e)
			combo := combo
			jobs = append(jobs, job{rows: []int{i}, run: func(ctx context.Context, g *gatherer) {
				if !g.startRow(ctx, i) {
					return
				}
				if s.MCP != nil {
					g.runMCP(ctx, i, s, refs, combo)
				} else {
					g.runCommand(ctx, i, s, refs, combo)
				}
			}})
		}
	}
	return jobs, settled
}

func contains(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

// stepPolicy is the rule a command step's line meets: exactly a read-only
// session's shell rule, with gather.allow on top of permissions.bash, no
// operator to ask and no grants to consult.
func stepPolicy(cfg *config.Config, runDir, bundleDir string) *provider.PermissionPolicy {
	allow := append(append([]string(nil), cfg.Permissions.Bash...), cfg.Gather.Allow...)
	return &provider.PermissionPolicy{
		BashAllow: allow,
		Root:      cfg.Root,
		ReadRoots: []string{runDir, bundleDir},
		ReadAlso:  cfg.ReadAlso(),
	}
}

// decideLine judges a command line the way a session's Bash call is.
func decideLine(p *provider.PermissionPolicy, line string) provider.Decision {
	input, _ := json.Marshal(map[string]string{"command": line})
	return p.Decide("Bash", input)
}

// batchFile reports whether a resolved program is a Windows batch file.
// Windows runs one through cmd.exe, which parses the arguments again by
// its own rules, so the argv the allow-list judged is not what runs.
func batchFile(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return ext == ".bat" || ext == ".cmd"
}

// judgeCommand is everything that decides whether a rendered command may
// run, short of running it: the allow-list, then where its program
// resolves. It returns the resolved program, or the status and reason to
// settle the row with instead.
func (g *gatherer) judgeCommand(p *provider.PermissionPolicy, argv []string) (bin, status, reason string) {
	if d := decideLine(p, DisplayLine(argv)); !d.Allow {
		return "", StatusDenied, d.Message
	}
	bin, err := lookPath(argv[0], g.in.Env, g.in.GOOS)
	if err != nil {
		return "", StatusFailed, argv[0] + " is not on PATH"
	}
	if g.in.GOOS == "windows" && batchFile(bin) {
		return "", StatusDenied, "a batch file would be run through cmd.exe"
	}
	return bin, "", ""
}

// runCommand runs one invocation of a command step.
func (g *gatherer) runCommand(ctx context.Context, i int, s config.GatherStep, refs []string, combo map[string]string) {
	start := g.now()
	g.mu.Lock()
	e := g.entries[i]
	g.mu.Unlock()
	note := e.Reason
	e.Reason = ""

	finish := func(status, reason string) {
		e.Status = status
		e.Reason = joinReasons(reason, note)
		e.TookMs = g.tookMs(start)
		g.settle(i, e)
	}

	argv, err := Render(s.Command, combo, g.cfg.ExpandPath)
	if err != nil {
		finish(StatusFailed, err.Error())
		return
	}
	line := DisplayLine(argv)
	e.Call = line
	e.Summary = clipRunes(line, 120)

	bin, status, reason := g.judgeCommand(stepPolicy(g.cfg, g.in.RunDir, g.in.BundleDir), argv)
	if status != "" {
		finish(status, reason)
		return
	}

	res := g.exec(ctx, time.Duration(s.TimeoutSeconds)*time.Second, bin, argv[1:], g.cfg.Gather.MaxBytes)
	status, reason = res.verdict(s.TimeoutSeconds)
	out := res.out.kept()
	if status == StatusOK && res.out.total > len(out) {
		status = StatusTruncated
	}
	if status == StatusFailed && out == "" {
		finish(status, reason)
		return
	}
	e.TookMs = g.tookMs(start)
	g.writeRow(&e, fileHeader{
		Name:   e.Name,
		Call:   e.Call,
		Values: valuesLine(refs, combo),
		TookMs: e.TookMs,
		Status: status,
		Reason: joinReasons(reason, note),
	}, out, res.out.total)
	finish(e.Status, joinReasons(e.Reason, reason))
}

// writeRow writes a row's file and records it on e. A write that fails
// turns the row into a failure saying so.
func (g *gatherer) writeRow(e *Entry, h fileHeader, out string, total int) {
	name := e.Name + ".md"
	if err := g.writeFile(name, renderFile(h, out, total)); err != nil {
		e.Status, e.Reason = StatusFailed, "write context/"+name+": "+err.Error()
		return
	}
	e.File = "context/" + name
	e.Bytes = len(out)
	e.Status = h.Status
}

// runMCP runs one invocation of an MCP step.
func (g *gatherer) runMCP(ctx context.Context, i int, s config.GatherStep, refs []string, combo map[string]string) {
	start := g.now()
	g.mu.Lock()
	e := g.entries[i]
	g.mu.Unlock()
	note := e.Reason
	e.Reason = ""

	finish := func(status, reason string) {
		e.Status = status
		e.Reason = joinReasons(reason, note)
		e.TookMs = g.tookMs(start)
		g.settle(i, e)
	}

	args, err := RenderArgs(s.MCP.Args, combo)
	if err != nil {
		finish(StatusFailed, err.Error())
		return
	}
	e.Call = s.MCP.Server + "." + s.MCP.Tool + "(" + string(args) + ")"
	e.Summary = clipRunes(mcpSummary(s.MCP.Server, s.MCP.Tool, args), 120)

	entry, status, reason := g.judgeMCP(s.MCP.Server, s.MCP.Tool)
	if status != "" {
		finish(status, reason)
		return
	}

	sctx, cancel := context.WithTimeout(ctx, time.Duration(s.TimeoutSeconds)*time.Second)
	text, isErr, err := mcpclient.CallOnce(sctx, entry, s.MCP.Tool, args)
	timedOut := sctx.Err() != nil
	cancel()
	if err != nil {
		switch {
		case ctx.Err() != nil:
			finish(StatusFailed, "gather deadline")
		case timedOut:
			finish(StatusFailed, fmt.Sprintf("timed out after %ds", s.TimeoutSeconds))
		default:
			finish(StatusFailed, firstLine(err.Error(), 200))
		}
		return
	}

	limit := g.cfg.Gather.MaxBytes
	out := clipBytes(text, limit, false)
	status = StatusOK
	reason = ""
	if len(out) < len(text) {
		status = StatusTruncated
	}
	if isErr {
		status, reason = StatusFailed, firstLine(text, 200)
	}
	e.TookMs = g.tookMs(start)
	g.writeRow(&e, fileHeader{
		Name:   e.Name,
		Call:   e.Call,
		Values: valuesLine(refs, combo),
		TookMs: e.TookMs,
		Status: status,
		Reason: joinReasons(reason, note),
	}, out, len(text))
	finish(e.Status, joinReasons(e.Reason, reason))
}

// judgeMCP gives an MCP tool the verdict a run would, before its server is
// started, and then finds the server.
func (g *gatherer) judgeMCP(server, tool string) (mcpclient.Entry, string, string) {
	v := provider.DecideMCPTool(mcpclient.ToolName(server, tool), g.cfg.Permissions.MCP)
	if !v.Allow {
		return mcpclient.Entry{}, StatusDenied, v.Reason()
	}
	g.invOnce.Do(func() {
		g.inv, _, g.invErr = mcpclient.Inventory(g.cfg.Root, g.in.Env, !g.cfg.WorkspaceOnlyMCP())
	})
	if g.invErr != nil {
		return mcpclient.Entry{}, StatusFailed, firstLine(g.invErr.Error(), 200)
	}
	for _, e := range g.inv {
		if e.Name != server {
			continue
		}
		if !e.Usable() {
			return mcpclient.Entry{}, StatusFailed, e.Note
		}
		return e, "", ""
	}
	return mcpclient.Entry{}, StatusFailed, "mcp server " + server + " is not configured"
}

// mcpSummary is an MCP call as a person reads it: the top-level arguments
// in key order, strings bare and anything else as compact JSON.
func mcpSummary(server, tool string, args json.RawMessage) string {
	var m map[string]json.RawMessage
	_ = json.Unmarshal(args, &m)
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		var s string
		if err := json.Unmarshal(m[k], &s); err == nil {
			parts = append(parts, k+"="+s)
			continue
		}
		parts = append(parts, k+"="+string(m[k]))
	}
	return server + " " + tool + "(" + strings.Join(parts, ", ") + ")"
}

// execResult is how one process ended.
type execResult struct {
	out      *capture
	stderr   *tail
	err      error
	timedOut bool // the step's own timeout
	deadline bool // the whole gather's deadline
}

// verdict turns how a process ended into a row status and reason.
func (r execResult) verdict(timeoutSeconds int) (string, string) {
	switch {
	case r.deadline:
		return StatusFailed, "gather deadline"
	case r.timedOut:
		return StatusFailed, fmt.Sprintf("timed out after %ds", timeoutSeconds)
	case r.err != nil:
		var exit *exec.ExitError
		if errors.As(r.err, &exit) {
			reason := fmt.Sprintf("exit %d", exit.ExitCode())
			if l := firstLine(string(r.stderr.buf), 200); l != "" {
				reason += ": " + l
			}
			return StatusFailed, reason
		}
		return StatusFailed, firstLine(r.err.Error(), 200)
	}
	return StatusOK, ""
}

// exec runs one program with no shell, no stdin and the run's own
// environment, in the workspace root, in a process group of its own so a
// timeout takes down whatever it started too. Stdout is kept to max bytes
// and counted beyond that; stderr keeps its tail.
func (g *gatherer) exec(ctx context.Context, timeout time.Duration, bin string, args []string, max int) execResult {
	sctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	res := execResult{out: &capture{max: max}, stderr: &tail{max: stderrTail}}
	cmd := exec.CommandContext(sctx, bin, args...)
	cmd.Dir = g.cfg.Root
	cmd.Env = g.in.Env
	cmd.Stdin = nil
	cmd.Stdout = res.out
	cmd.Stderr = res.stderr
	procgroup.Setup(cmd)
	cmd.Cancel = func() error { return procgroup.Kill(cmd) }
	cmd.WaitDelay = 2 * time.Second
	res.err = cmd.Run()
	if res.err != nil && sctx.Err() != nil {
		if ctx.Err() != nil {
			res.deadline = true
		} else {
			res.timedOut = true
		}
	}
	return res
}

// lookPath finds a program the way the run's own children would: on the
// PATH in env, not this process's. A name with a directory in it is taken
// as given. On Windows the PATHEXT extensions are tried, which is how a
// bare "tool" turns out to be tool.bat.
func lookPath(name string, env []string, goos string) (string, error) {
	windows := goos == "windows"
	exts := []string{""}
	if windows && filepath.Ext(name) == "" {
		exts = nil
		pathext := envValue(env, "PATHEXT", true)
		if pathext == "" {
			pathext = ".COM;.EXE;.BAT;.CMD"
		}
		for _, e := range strings.Split(pathext, ";") {
			if e = strings.TrimSpace(e); e != "" {
				exts = append(exts, strings.ToLower(e))
			}
		}
	}
	try := func(base string) (string, bool) {
		for _, ext := range exts {
			p := base + ext
			info, err := os.Stat(p)
			if err != nil || info.IsDir() {
				continue
			}
			if !windows && info.Mode()&0o111 == 0 {
				continue
			}
			return p, true
		}
		return "", false
	}

	if strings.ContainsAny(name, `/\`) {
		if p, ok := try(name); ok {
			return p, nil
		}
		return "", fmt.Errorf("%s: not found", name)
	}
	path, ok := envLookup(env, "PATH", windows)
	if !ok {
		path = os.Getenv("PATH")
	}
	sep := string(os.PathListSeparator)
	if windows {
		sep = ";"
	}
	for _, dir := range strings.Split(path, sep) {
		if dir == "" {
			continue
		}
		if p, ok := try(filepath.Join(dir, name)); ok {
			return p, nil
		}
	}
	return "", fmt.Errorf("%s: not found on PATH", name)
}

// envValue is the last value env gives key, "" when none.
func envValue(env []string, key string, fold bool) string {
	v, _ := envLookup(env, key, fold)
	return v
}

// envLookup finds key in env, the last entry winning as it does for a
// child. fold makes the match case-insensitive, as Windows' is.
func envLookup(env []string, key string, fold bool) (string, bool) {
	val, found := "", false
	for _, kv := range env {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		if k == key || (fold && strings.EqualFold(k, key)) {
			val, found = v, true
		}
	}
	return val, found
}
