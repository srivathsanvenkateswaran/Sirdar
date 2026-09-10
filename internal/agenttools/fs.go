package agenttools

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	// sniffBytes is how much of a file is inspected for NUL bytes before it
	// is treated as text.
	sniffBytes = 8000
	// defaultReadLimit is read_file's line budget when the model asks for
	// no explicit limit.
	defaultReadLimit = 2000
	// maxLineBytes bounds a single line so a minified bundle cannot exhaust
	// memory.
	maxLineBytes = 1 << 20
	// maxDirEntries bounds one list_dir listing.
	maxDirEntries = 2000
	// maxGlobResults bounds one glob result set.
	maxGlobResults = 2000
)

var readFileSchema = json.RawMessage(`{
  "$schema": "http://json-schema.org/draft-07/schema#",
  "type": "object",
  "additionalProperties": false,
  "properties": {
    "path": {"type": "string", "description": "File to read, relative to the workspace root (an absolute path must still be inside it)."},
    "offset": {"type": "integer", "minimum": 1, "description": "1-based line number to start at. Defaults to 1."},
    "limit": {"type": "integer", "minimum": 1, "description": "Maximum number of lines to return. Defaults to 2000."}
  },
  "required": ["path"]
}`)

func (o Options) readFileTool() Tool {
	return toolFunc{
		spec: Spec{
			Name:        "read_file",
			Description: "Read a text file from the workspace. Returns one line per row as \"<line number>\\t<text>\", numbered from 1 for the whole file so the numbers survive an offset. Binary files are refused; use bash for those.",
			Parameters:  readFileSchema,
		},
		call: o.readFile,
	}
}

func (o Options) readFile(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		Path   string `json:"path"`
		Offset int    `json:"offset"`
		Limit  int    `json:"limit"`
	}
	if err := decodeArgs(args, &a); err != nil {
		return "", err
	}
	if strings.TrimSpace(a.Path) == "" {
		return "", errors.New("read_file: path is required")
	}
	abs, err := o.resolve(a.Path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("read_file: %v", err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("read_file: %s is a directory", a.Path)
	}

	f, err := os.Open(abs)
	if err != nil {
		return "", fmt.Errorf("read_file: %v", err)
	}
	defer f.Close()

	reader := bufio.NewReaderSize(f, 64<<10)
	prefix, _ := reader.Peek(sniffBytes)
	if looksBinary(prefix) {
		return "", fmt.Errorf("read_file: %s looks like a binary file", a.Path)
	}

	offset := a.Offset
	if offset < 1 {
		offset = 1
	}
	limit := a.Limit
	if limit < 1 {
		limit = defaultReadLimit
	}

	var b strings.Builder
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64<<10), maxLineBytes)
	line := 0
	shown := 0
	for scanner.Scan() {
		line++
		if line < offset {
			continue
		}
		if shown >= limit {
			b.WriteString("[truncated: line limit " + strconv.Itoa(limit) + " reached]\n")
			break
		}
		b.WriteString(strconv.Itoa(line))
		b.WriteByte('\t')
		b.WriteString(scanner.Text())
		b.WriteByte('\n')
		shown++
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("read_file: %v", err)
	}
	if shown == 0 {
		return fmt.Sprintf("[no lines: %s has %d lines, offset was %d]", a.Path, line, offset), nil
	}
	out, _ := truncate(b.String(), o.MaxOutputBytes)
	return out, nil
}

var listDirSchema = json.RawMessage(`{
  "$schema": "http://json-schema.org/draft-07/schema#",
  "type": "object",
  "additionalProperties": false,
  "properties": {
    "path": {"type": "string", "description": "Directory to list, relative to the workspace root. Use \".\" for the root itself."}
  },
  "required": ["path"]
}`)

func (o Options) listDirTool() Tool {
	return toolFunc{
		spec: Spec{
			Name:        "list_dir",
			Description: "List one directory in the workspace, sorted by name, hidden entries included. Directories carry a trailing \"/\". Does not recurse; use glob or grep for that.",
			Parameters:  listDirSchema,
		},
		call: o.listDir,
	}
}

func (o Options) listDir(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		Path string `json:"path"`
	}
	if err := decodeArgs(args, &a); err != nil {
		return "", err
	}
	if strings.TrimSpace(a.Path) == "" {
		return "", errors.New("list_dir: path is required")
	}
	abs, err := o.resolve(a.Path)
	if err != nil {
		return "", err
	}
	entries, err := os.ReadDir(abs)
	if err != nil {
		return "", fmt.Errorf("list_dir: %v", err)
	}
	var b strings.Builder
	for i, e := range entries {
		if i >= maxDirEntries {
			b.WriteString("[truncated: " + strconv.Itoa(len(entries)-maxDirEntries) + " more entries]\n")
			break
		}
		b.WriteString(e.Name())
		if e.IsDir() {
			b.WriteByte('/')
		}
		b.WriteByte('\n')
	}
	if b.Len() == 0 {
		return "[empty directory]", nil
	}
	out, _ := truncate(b.String(), o.MaxOutputBytes)
	return out, nil
}

var globSchema = json.RawMessage(`{
  "$schema": "http://json-schema.org/draft-07/schema#",
  "type": "object",
  "additionalProperties": false,
  "properties": {
    "pattern": {"type": "string", "description": "Glob matched against paths relative to the workspace root, e.g. \"**/*.go\" or \"cmd/*/main.go\". \"**\" spans directories, \"*\" and \"?\" stay within one path segment."}
  },
  "required": ["pattern"]
}`)

func (o Options) globTool() Tool {
	return toolFunc{
		spec: Spec{
			Name:        "glob",
			Description: "Find files in the workspace whose path matches a glob. Returns paths relative to the workspace root, sorted, at most 2000 of them. The .git directory is never searched.",
			Parameters:  globSchema,
		},
		call: o.glob,
	}
}

func (o Options) glob(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		Pattern string `json:"pattern"`
	}
	if err := decodeArgs(args, &a); err != nil {
		return "", err
	}
	pattern := strings.TrimSpace(a.Pattern)
	if pattern == "" {
		return "", errors.New("glob: pattern is required")
	}
	pattern = strings.TrimPrefix(filepath.ToSlash(pattern), "./")

	root, err := o.root()
	if err != nil {
		return "", err
	}
	var matches []string
	truncated := false
	walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// An unreadable directory is skipped, not fatal: the model
			// should still see whatever else matched.
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if d.IsDir() {
			if path != root && d.Name() == ".git" {
				return fs.SkipDir
			}
			return nil
		}
		if len(matches) >= maxGlobResults {
			truncated = true
			return fs.SkipAll
		}
		r := rel(root, path)
		if matchGlob(pattern, r) {
			matches = append(matches, r)
		}
		return nil
	})
	if walkErr != nil {
		return "", fmt.Errorf("glob: %v", walkErr)
	}
	if len(matches) == 0 {
		return "[no matches]", nil
	}
	body := strings.Join(matches, "\n") + "\n"
	if truncated {
		body += "[truncated: result limit " + strconv.Itoa(maxGlobResults) + " reached]\n"
	}
	out, _ := truncate(body, o.MaxOutputBytes)
	return out, nil
}
