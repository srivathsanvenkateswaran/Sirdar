package app

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/srivathsanvenkateswaran/sirdar/internal/config"
)

// ErrNoSuchWorkspace is returned for an id no registered workspace carries.
var ErrNoSuchWorkspace = errors.New("app: no such workspace")

// Workspace is one registered Sirdar workspace, as the UI sees it. Only
// Root is persisted; the rest is read back from the workspace's config.
type Workspace struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Root     string `json:"root"`
	Provider string `json:"provider"`
	Model    string `json:"model"`
	NotesDir string `json:"notesDir"`
}

// Registry is the list of workspace roots at Path, by default
// ~/.sirdar/workspaces.json. A missing file is an empty registry, not an
// error: nothing has been added yet.
type Registry struct {
	Path string

	mu sync.Mutex
}

// DefaultRegistryPath is ~/.sirdar/workspaces.json.
func DefaultRegistryPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("app: home directory: %w", err)
	}
	return filepath.Join(home, ".sirdar", "workspaces.json"), nil
}

// registryFile is the on-disk shape: a list of roots and nothing else, so
// a workspace's provider and model always come from its own config.
type registryFile struct {
	Workspaces []registryEntry `json:"workspaces"`
}

type registryEntry struct {
	Root string `json:"root"`
}

// WorkspaceID is the stable id of a root: the first 12 hex digits of its
// SHA-1. It is derived, not stored, so the registry file stays hand-editable.
func WorkspaceID(root string) string {
	sum := sha1.Sum([]byte(root))
	return hex.EncodeToString(sum[:])[:12]
}

// absRoot cleans a root into the form the registry stores and hashes.
func absRoot(root string) (string, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("app: workspace path %s: %w", root, err)
	}
	return filepath.Clean(abs), nil
}

// read loads the file. The caller holds the lock.
func (r *Registry) read() (registryFile, error) {
	var f registryFile
	data, err := os.ReadFile(r.Path)
	if err != nil {
		if os.IsNotExist(err) {
			return f, nil
		}
		return f, fmt.Errorf("app: read workspaces: %w", err)
	}
	if len(data) == 0 {
		return f, nil
	}
	if err := json.Unmarshal(data, &f); err != nil {
		return f, fmt.Errorf("app: parse %s: %w", r.Path, err)
	}
	return f, nil
}

// write persists the file atomically. The caller holds the lock.
func (r *Registry) write(f registryFile) error {
	if f.Workspaces == nil {
		f.Workspaces = []registryEntry{}
	}
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return fmt.Errorf("app: marshal workspaces: %w", err)
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(r.Path), 0o755); err != nil {
		return fmt.Errorf("app: create %s: %w", filepath.Dir(r.Path), err)
	}
	tmp := r.Path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("app: write workspaces: %w", err)
	}
	if err := os.Rename(tmp, r.Path); err != nil {
		return fmt.Errorf("app: replace workspaces: %w", err)
	}
	return nil
}

// describe fills in what the workspace's own configuration says. A root
// whose config no longer loads still lists, with the config-derived fields
// empty, so a broken workspace can be seen and removed from the UI.
func describe(root string) Workspace {
	ws := Workspace{ID: WorkspaceID(root), Name: filepath.Base(root), Root: root}
	cfg, err := config.Load(root)
	if err != nil {
		return ws
	}
	if cfg.Workspace != "" {
		ws.Name = cfg.Workspace
	}
	ws.Provider = string(cfg.Provider)
	ws.Model = cfg.Model
	ws.NotesDir = cfg.ExpandPath(cfg.Notes.Dir)
	return ws
}

// List returns every registered workspace in file order.
func (r *Registry) List() ([]Workspace, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	f, err := r.read()
	if err != nil {
		return nil, err
	}
	out := make([]Workspace, 0, len(f.Workspaces))
	for _, e := range f.Workspaces {
		if e.Root == "" {
			continue
		}
		out = append(out, describe(e.Root))
	}
	return out, nil
}

// Add registers root after checking that it is a real Sirdar workspace.
// Registering the same root twice is an error rather than a silent no-op,
// so the UI can say why nothing happened.
func (r *Registry) Add(root string) (Workspace, error) {
	abs, err := absRoot(root)
	if err != nil {
		return Workspace{}, err
	}
	if _, err := config.Load(abs); err != nil {
		return Workspace{}, err
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	f, err := r.read()
	if err != nil {
		return Workspace{}, err
	}
	for _, e := range f.Workspaces {
		if e.Root == abs {
			return Workspace{}, fmt.Errorf("app: %s is already registered", abs)
		}
	}
	f.Workspaces = append(f.Workspaces, registryEntry{Root: abs})
	if err := r.write(f); err != nil {
		return Workspace{}, err
	}
	return describe(abs), nil
}

// Remove drops the workspace with this id.
func (r *Registry) Remove(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	f, err := r.read()
	if err != nil {
		return err
	}
	kept := make([]registryEntry, 0, len(f.Workspaces))
	found := false
	for _, e := range f.Workspaces {
		if WorkspaceID(e.Root) == id {
			found = true
			continue
		}
		kept = append(kept, e)
	}
	if !found {
		return fmt.Errorf("%w: %s", ErrNoSuchWorkspace, id)
	}
	f.Workspaces = kept
	return r.write(f)
}

// Find returns the workspace with this id.
func (r *Registry) Find(id string) (Workspace, error) {
	list, err := r.List()
	if err != nil {
		return Workspace{}, err
	}
	for _, ws := range list {
		if ws.ID == id {
			return ws, nil
		}
	}
	return Workspace{}, fmt.Errorf("%w: %s", ErrNoSuchWorkspace, id)
}
