package agenttools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// maxWriteBytes bounds one write_file call. A fix edits source, and a model
// that decides to paste a megabyte of generated content into a file is
// making a mistake nobody wants committed.
const maxWriteBytes = 2 << 20

var writeFileSchema = json.RawMessage(`{
  "$schema": "http://json-schema.org/draft-07/schema#",
  "type": "object",
  "additionalProperties": false,
  "properties": {
    "path": {"type": "string", "description": "File to write, relative to the workspace root (an absolute path must still be inside it). Parent directories are created."},
    "content": {"type": "string", "description": "The file's complete new content. Writing replaces the file; use edit_file to change part of one."}
  },
  "required": ["path", "content"]
}`)

var editFileSchema = json.RawMessage(`{
  "$schema": "http://json-schema.org/draft-07/schema#",
  "type": "object",
  "additionalProperties": false,
  "properties": {
    "path": {"type": "string", "description": "File to edit, relative to the workspace root (an absolute path must still be inside it)."},
    "old": {"type": "string", "description": "The exact text to replace. It must appear in the file exactly once."},
    "new": {"type": "string", "description": "The text to put in its place."}
  },
  "required": ["path", "old", "new"]
}`)

// WriteSet returns the two writing tools a fix run adds to the read-only
// set: write_file and edit_file. Both confine their path argument to Root
// the same way every read does — through the real, symlink-resolved path —
// so a symlink inside the workspace pointing out of it cannot be written
// through, and neither can an absolute path.
//
// They are returned separately rather than folded into ReadOnlySet because
// which set a session gets is the whole of the difference between a triage
// run and a fix run: a caller has to ask for the writes by name.
func WriteSet(o Options) []Tool {
	o = o.normalized()
	return []Tool{
		o.writeFileTool(),
		o.editFileTool(),
	}
}

func (o Options) writeFileTool() Tool {
	return toolFunc{
		spec: Spec{
			Name:        "write_file",
			Description: "Create or replace a file in the workspace with the given content. Parent directories are created. To change part of an existing file, prefer edit_file.",
			Parameters:  writeFileSchema,
		},
		call: o.writeFile,
	}
}

func (o Options) writeFile(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := decodeArgs(args, &a); err != nil {
		return "", err
	}
	if strings.TrimSpace(a.Path) == "" {
		return "", errors.New("write_file: path is required")
	}
	if len(a.Content) > maxWriteBytes {
		return "", fmt.Errorf("write_file: content is %d bytes, over the %d byte limit", len(a.Content), maxWriteBytes)
	}
	abs, err := o.resolveWrite(a.Path)
	if err != nil {
		return "", fmt.Errorf("write_file: %v", err)
	}
	if info, statErr := os.Stat(abs); statErr == nil && info.IsDir() {
		return "", fmt.Errorf("write_file: %s is a directory", a.Path)
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return "", fmt.Errorf("write_file: %v", err)
	}
	if err := os.WriteFile(abs, []byte(a.Content), 0o644); err != nil {
		return "", fmt.Errorf("write_file: %v", err)
	}
	root, err := o.root()
	if err != nil {
		return "", err
	}
	return "wrote " + rel(root, abs) + " (" + strconv.Itoa(len(a.Content)) + " bytes)", nil
}

func (o Options) editFileTool() Tool {
	return toolFunc{
		spec: Spec{
			Name:        "edit_file",
			Description: "Replace one exact occurrence of a string in a workspace file. The old text must appear exactly once; include enough surrounding context to make it unique.",
			Parameters:  editFileSchema,
		},
		call: o.editFile,
	}
}

func (o Options) editFile(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		Path string `json:"path"`
		Old  string `json:"old"`
		New  string `json:"new"`
	}
	if err := decodeArgs(args, &a); err != nil {
		return "", err
	}
	if strings.TrimSpace(a.Path) == "" {
		return "", errors.New("edit_file: path is required")
	}
	if a.Old == "" {
		return "", errors.New("edit_file: old is required; use write_file to create a file")
	}
	if a.Old == a.New {
		return "", errors.New("edit_file: old and new are identical")
	}
	abs, err := o.resolveWrite(a.Path)
	if err != nil {
		return "", fmt.Errorf("edit_file: %v", err)
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return "", fmt.Errorf("edit_file: %v", err)
	}
	if looksBinary(data) {
		return "", fmt.Errorf("edit_file: %s looks like a binary file", a.Path)
	}
	body := string(data)
	switch n := strings.Count(body, a.Old); {
	case n == 0:
		return "", fmt.Errorf("edit_file: the old text does not appear in %s", a.Path)
	case n > 1:
		return "", fmt.Errorf("edit_file: the old text appears %d times in %s; include more context so it is unique", n, a.Path)
	}
	updated := strings.Replace(body, a.Old, a.New, 1)
	info, err := os.Stat(abs)
	mode := os.FileMode(0o644)
	if err == nil {
		mode = info.Mode().Perm()
	}
	if err := os.WriteFile(abs, []byte(updated), mode); err != nil {
		return "", fmt.Errorf("edit_file: %v", err)
	}
	root, err := o.root()
	if err != nil {
		return "", err
	}
	return "edited " + rel(root, abs), nil
}
