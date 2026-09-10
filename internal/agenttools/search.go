package agenttools

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	// defaultGrepResults is grep's match budget when the model asks for no
	// explicit maxResults.
	defaultGrepResults = 200
	// maxGrepFileBytes skips files too large to be worth scanning line by
	// line (build artefacts, data dumps).
	maxGrepFileBytes = 5 << 20
)

// skipDirs are never descended into by grep: version control, dependency
// and build trees, and Sirdar's own run directory (which holds transcripts
// of earlier runs and would feed the model its own output).
var skipDirs = map[string]bool{
	".git":         true,
	"node_modules": true,
	"vendor":       true,
	"dist":         true,
}

// skipRelPaths are workspace-relative directories skipped by grep in
// addition to skipDirs.
var skipRelPaths = []string{".sirdar/runs"}

// rgPath is the ripgrep binary used when it is on PATH. Tests set it to ""
// to force the Go fallback.
var rgPath = lookRipgrep()

// grepTimeout bounds one ripgrep invocation.
var grepTimeout = 30 * time.Second

func lookRipgrep() string {
	p, err := exec.LookPath("rg")
	if err != nil {
		return ""
	}
	return p
}

var grepSchema = json.RawMessage(`{
  "$schema": "http://json-schema.org/draft-07/schema#",
  "type": "object",
  "additionalProperties": false,
  "properties": {
    "pattern": {"type": "string", "description": "Regular expression (Go/RE2 syntax) matched against each line."},
    "path": {"type": "string", "description": "File or directory to search, relative to the workspace root. Defaults to the whole workspace."},
    "glob": {"type": "string", "description": "Only search files whose path matches this glob, e.g. \"**/*.go\" or \"*.yaml\"."},
    "maxResults": {"type": "integer", "minimum": 1, "description": "Maximum number of matching lines to return. Defaults to 200."}
  },
  "required": ["pattern"]
}`)

func (o Options) grepTool() Tool {
	return toolFunc{
		spec: Spec{
			Name:        "grep",
			Description: "Search the workspace for lines matching a regular expression. Returns \"path:line:text\" rows, paths relative to the workspace root. Binary files, .git, node_modules, vendor, dist and .sirdar/runs are skipped.",
			Parameters:  grepSchema,
		},
		call: o.grep,
	}
}

func (o Options) grep(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		Pattern    string `json:"pattern"`
		Path       string `json:"path"`
		Glob       string `json:"glob"`
		MaxResults int    `json:"maxResults"`
	}
	if err := decodeArgs(args, &a); err != nil {
		return "", err
	}
	if strings.TrimSpace(a.Pattern) == "" {
		return "", errors.New("grep: pattern is required")
	}
	re, err := regexp.Compile(a.Pattern)
	if err != nil {
		return "", fmt.Errorf("grep: invalid pattern: %v", err)
	}
	searchPath := a.Path
	if strings.TrimSpace(searchPath) == "" {
		searchPath = "."
	}
	target, err := o.resolve(searchPath)
	if err != nil {
		return "", err
	}
	root, err := o.root()
	if err != nil {
		return "", err
	}
	max := a.MaxResults
	if max < 1 {
		max = defaultGrepResults
	}
	globPattern := strings.TrimPrefix(filepath.ToSlash(strings.TrimSpace(a.Glob)), "./")

	lines, truncated, err := o.searchRipgrep(ctx, root, target, a.Pattern, globPattern, max)
	if err != nil {
		lines, truncated, err = o.searchWalk(ctx, root, target, re, globPattern, max)
		if err != nil {
			return "", fmt.Errorf("grep: %v", err)
		}
	}
	if len(lines) == 0 {
		return "[no matches]", nil
	}
	body := strings.Join(lines, "\n") + "\n"
	if truncated {
		body += "[truncated: result limit " + strconv.Itoa(max) + " reached]\n"
	}
	out, _ := truncate(body, o.MaxOutputBytes)
	return out, nil
}

// searchRipgrep runs `rg -n --no-heading`. It returns an error whenever
// ripgrep is unavailable or exits abnormally, which makes the caller fall
// back to the Go walk.
func (o Options) searchRipgrep(ctx context.Context, root, target, pattern, globPattern string, max int) ([]string, bool, error) {
	if rgPath == "" {
		return nil, false, errors.New("ripgrep not available")
	}
	runCtx, cancel := context.WithTimeout(ctx, grepTimeout)
	defer cancel()

	args := []string{"-n", "--no-heading", "--color=never", "--no-ignore", "--hidden"}
	for name := range skipDirs {
		args = append(args, "-g", "!"+name)
	}
	for _, p := range skipRelPaths {
		args = append(args, "-g", "!"+p)
	}
	if globPattern != "" {
		args = append(args, "-g", globPattern)
	}
	args = append(args, "-e", pattern, "--", target)

	cmd := exec.CommandContext(runCtx, rgPath, args...)
	cmd.Dir = root
	cmd.Env = execEnv()
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		var exitErr *exec.ExitError
		// Exit code 1 is ripgrep's "no matches", which is a result, not a
		// failure. Anything else means fall back to the Go walk.
		if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
			return nil, false, fmt.Errorf("ripgrep: %v: %s", err, strings.TrimSpace(stderr.String()))
		}
	}
	var out []string
	truncated := false
	scanner := bufio.NewScanner(&stdout)
	scanner.Buffer(make([]byte, 0, 64<<10), maxLineBytes)
	for scanner.Scan() {
		if len(out) >= max {
			truncated = true
			break
		}
		line := scanner.Text()
		// ripgrep echoes the absolute path it was given; render it
		// relative to the workspace root like the Go walk does.
		if i := strings.IndexByte(line, ':'); i > 0 && filepath.IsAbs(line[:i]) {
			line = rel(root, line[:i]) + line[i:]
		}
		out = append(out, line)
	}
	if err := scanner.Err(); err != nil {
		return nil, false, err
	}
	return out, truncated, nil
}

// searchWalk is the dependency-free fallback: a filepath.WalkDir with the
// same skip list, NUL sniffing for binaries, and the same output shape.
func (o Options) searchWalk(ctx context.Context, root, target string, re *regexp.Regexp, globPattern string, max int) ([]string, bool, error) {
	var out []string
	truncated := false

	walkErr := filepath.WalkDir(target, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		r := rel(root, p)
		if d.IsDir() {
			if p == target {
				return nil
			}
			if skipDirs[d.Name()] || isSkippedRel(r) {
				return fs.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		if globPattern != "" && !matchGlob(globPattern, r) {
			return nil
		}
		if info, statErr := d.Info(); statErr == nil && info.Size() > maxGrepFileBytes {
			return nil
		}
		matches, fileErr := grepFile(p, r, re, max-len(out))
		if fileErr != nil {
			return nil
		}
		out = append(out, matches...)
		if len(out) >= max {
			truncated = true
			return fs.SkipAll
		}
		return nil
	})
	if walkErr != nil {
		return nil, false, walkErr
	}
	return out, truncated, nil
}

func isSkippedRel(r string) bool {
	for _, p := range skipRelPaths {
		if r == p || strings.HasPrefix(r, p+"/") {
			return true
		}
	}
	return false
}

func grepFile(abs, display string, re *regexp.Regexp, budget int) ([]string, error) {
	if budget <= 0 {
		return nil, nil
	}
	f, err := os.Open(abs)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	reader := bufio.NewReaderSize(f, 64<<10)
	prefix, _ := reader.Peek(sniffBytes)
	if looksBinary(prefix) {
		return nil, nil
	}
	var out []string
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64<<10), maxLineBytes)
	line := 0
	for scanner.Scan() {
		line++
		text := scanner.Text()
		if !re.MatchString(text) {
			continue
		}
		out = append(out, display+":"+strconv.Itoa(line)+":"+text)
		if len(out) >= budget {
			break
		}
	}
	if err := scanner.Err(); err != nil {
		return out, nil
	}
	return out, nil
}

// matchGlob reports whether a slash-separated path matches pattern, where
// "**" spans any number of path segments (including none) and "*", "?" and
// character classes match within one segment, as path.Match defines them.
// A pattern without a "/" also matches against the base name alone, so
// "*.go" finds Go files at any depth.
func matchGlob(pattern, name string) bool {
	pattern = filepath.ToSlash(pattern)
	name = filepath.ToSlash(name)
	if matchSegments(splitPath(pattern), splitPath(name)) {
		return true
	}
	if !strings.Contains(pattern, "/") {
		if ok, err := path.Match(pattern, path.Base(name)); err == nil && ok {
			return true
		}
	}
	return false
}

func splitPath(p string) []string {
	p = strings.Trim(p, "/")
	if p == "" {
		return nil
	}
	return strings.Split(p, "/")
}

// matchSegments matches a split pattern against a split path, with "**"
// spanning any number of segments.
//
// Every (pattern position, name position) pair is decided once and cached.
// Without that, a pattern carrying several "**" segments re-explores the
// same suffixes exponentially — and the model chooses these patterns, so
// "**/**/**/*.go" against a deep tree must not be able to wedge a run.
func matchSegments(pattern, name []string) bool {
	memo := make(map[[2]int]bool)
	var match func(pi, ni int) bool
	match = func(pi, ni int) bool {
		if pi == len(pattern) {
			return ni == len(name)
		}
		key := [2]int{pi, ni}
		if cached, ok := memo[key]; ok {
			return cached
		}
		result := false
		switch {
		case pattern[pi] == "**":
			for i := ni; i <= len(name); i++ {
				if match(pi+1, i) {
					result = true
					break
				}
			}
		case ni < len(name):
			if ok, err := path.Match(pattern[pi], name[ni]); err == nil && ok {
				result = match(pi+1, ni+1)
			}
		}
		memo[key] = result
		return result
	}
	return match(0, 0)
}
