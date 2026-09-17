package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/osopen"
	"github.com/srivathsanvenkateswaran/sirdar/internal/prompt"
)

// ErrNoSuchPlaybook is a playbook name the workspace does not have — or a
// name that could never be one. It becomes 404: a name that cannot address
// a file addresses nothing.
var ErrNoSuchPlaybook = errors.New("app: no such playbook")

// ErrPlaybookExists is AddPlaybook asked for a name that is already taken.
// It becomes 409: the caller can edit that playbook, or pick another name.
var ErrPlaybookExists = errors.New("app: playbook already exists")

// playbookName is the shape a new playbook's filename must have: the two
// digits internal/prompt orders the files by, a lowercase slug, and .md.
// The prefix is not decoration — LoadPlaybooks sorts by filename, so it is
// the only thing deciding what the agent reads first.
var playbookName = regexp.MustCompile(`^[0-9]{2}-[a-z0-9-]+\.md$`)

// ValidPlaybookName reports whether name is one a new playbook may be
// created under. An existing file keeps whatever name it already has, so
// the service checks membership of the directory as well; this is the
// early answer the HTTP layer gives a caller that made one up.
func ValidPlaybookName(name string) bool { return playbookName.MatchString(name) }

// ledeMax is how much of a playbook's first paragraph reaches the list. A
// row is one line of prose beside a title, not a preview pane.
const ledeMax = 160

// trashDir holds a deleted playbook. A delete moves the file here rather
// than unlinking it: these are files an operator wrote by hand over
// months, and the UI's confirm sentence is the only thing between a
// mis-aimed click and them.
const trashDir = ".trash"

// PlaybookSummary is one playbook as the Settings list draws it: what it is
// called, what it says it is about, and how big and how old it is. The body
// is not here — a list of five playbooks would otherwise carry every word
// of all five.
type PlaybookSummary struct {
	// Name is the file's own name, ".md" included. It is the id every
	// other method takes.
	Name string `json:"name"`
	// File is the path relative to the workspace root, for an operator
	// who wants to find it outside the app.
	File string `json:"file"`
	// Title is the first H1, or the name when the file has none.
	Title string `json:"title"`
	// Lede is the first paragraph, cut at 160 characters.
	Lede string `json:"lede"`
	// Order is the filename's numeric prefix, "" when it has none. The
	// list is drawn in this order because the prompt is.
	Order      string    `json:"order"`
	Bytes      int64     `json:"bytes"`
	ModifiedAt time.Time `json:"modifiedAt"`
}

// playbooksDir resolves the workspace's playbooks directory and reports
// whether the app may write in it.
//
// The path is configurable (`playbooks:` in config.yaml, `.sirdar/playbooks`
// by default) so reading honours whatever the workspace set. Writing does
// not: Sirdar scaffolds files under `.sirdar/` and that is the only part of
// a workspace it may put a file into, so a workspace pointing `playbooks:`
// at its own source tree gets a read-only list rather than an editor
// quietly writing into the repository.
func (s *Service) playbooksDir(wsID string) (dir, root string, writable bool, err error) {
	ws, cfg, err := s.load(wsID)
	if err != nil {
		return "", "", false, err
	}
	dir = cfg.ExpandPath(cfg.Playbooks)
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(ws.Root, dir)
	}
	sirdar := filepath.Join(ws.Root, ".sirdar")
	writable = dir == sirdar || strings.HasPrefix(dir, sirdar+string(filepath.Separator))
	return filepath.Clean(dir), ws.Root, writable, nil
}

// playbookPath resolves one playbook's file inside dir.
//
// name has to be a plain filename: the regexp allows no separator, and an
// existing file's name is read back off the directory rather than taken
// from the caller, so nothing the caller sends is ever joined onto dir
// without being a base name first. `..`, an absolute path and a name with
// a separator in it are all refused as naming no playbook.
func playbookPath(dir, name string) (string, error) {
	if name == "" || name != filepath.Base(name) || name == "." || name == ".." {
		return "", fmt.Errorf("%w: %q is not a playbook filename", ErrNoSuchPlaybook, name)
	}
	if strings.ContainsAny(name, `/\`) || strings.HasPrefix(name, ".") {
		return "", fmt.Errorf("%w: %q is not a playbook filename", ErrNoSuchPlaybook, name)
	}
	if !strings.HasSuffix(name, ".md") {
		return "", fmt.Errorf("%w: %q is not a markdown file", ErrNoSuchPlaybook, name)
	}
	return filepath.Join(dir, name), nil
}

// summarise reads one playbook's file into the row the list draws.
func summarise(dir, root, name string) (PlaybookSummary, error) {
	path, err := playbookPath(dir, name)
	if err != nil {
		return PlaybookSummary{}, err
	}
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return PlaybookSummary{}, fmt.Errorf("%w: %q", ErrNoSuchPlaybook, name)
		}
		return PlaybookSummary{}, err
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return PlaybookSummary{}, err
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		rel = path
	}
	title, lede := titleAndLede(string(body))
	if title == "" {
		title = strings.TrimSuffix(name, ".md")
	}
	return PlaybookSummary{
		Name:       name,
		File:       filepath.ToSlash(rel),
		Title:      title,
		Lede:       lede,
		Order:      orderOf(name),
		Bytes:      info.Size(),
		ModifiedAt: info.ModTime().UTC(),
	}, nil
}

// orderOf returns the filename's numeric prefix — "10" of "10-helpdesk.md"
// — or "" for a file that has none.
func orderOf(name string) string {
	if len(name) >= 3 && name[2] == '-' && name[0] >= '0' && name[0] <= '9' && name[1] >= '0' && name[1] <= '9' {
		return name[:2]
	}
	return ""
}

// titleAndLede reads a playbook's first H1 and its first paragraph.
//
// The lede skips headings, list bullets, quotes and fences: what is wanted
// is the sentence that says what the playbook is for, and a playbook that
// opens on a bullet list has none.
func titleAndLede(body string) (title, lede string) {
	var para []string
	for _, raw := range strings.Split(body, "\n") {
		line := strings.TrimSpace(raw)
		if strings.HasPrefix(line, "#") {
			if title == "" && strings.HasPrefix(line, "# ") {
				title = strings.TrimSpace(strings.TrimPrefix(line, "# "))
			}
			if len(para) > 0 {
				break
			}
			continue
		}
		if line == "" {
			if len(para) > 0 {
				break
			}
			continue
		}
		if strings.HasPrefix(line, "```") || strings.HasPrefix(line, "---") {
			if len(para) > 0 {
				break
			}
			continue
		}
		if len(para) == 0 && (strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* ") ||
			strings.HasPrefix(line, "> ") || strings.HasPrefix(line, "|")) {
			continue
		}
		para = append(para, line)
	}
	return title, clip(strings.Join(para, " "), ledeMax)
}

// clip cuts s to at most n characters, on a word boundary where there is
// one, and marks the cut with an ellipsis.
func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	cut := string(runes[:n])
	if i := strings.LastIndex(cut, " "); i > n/2 {
		cut = cut[:i]
	}
	return strings.TrimRight(cut, " ,;:") + "…"
}

// listPlaybookNames returns the *.md files directly inside dir, in the
// filename order internal/prompt loads them in. A directory that is not
// there is not an error: a workspace that never ran `sirdar init` has no
// playbooks, which is an empty list rather than a failure.
func listPlaybookNames(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var names []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".md") || strings.HasPrefix(name, ".") {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}

// Playbooks lists the workspace's playbooks in the order the prompt loads
// them. Nothing here is the file's body.
func (s *Service) Playbooks(wsID string) ([]PlaybookSummary, error) {
	dir, root, _, err := s.playbooksDir(wsID)
	if err != nil {
		return nil, err
	}
	names, err := listPlaybookNames(dir)
	if err != nil {
		return nil, err
	}
	out := make([]PlaybookSummary, 0, len(names))
	for _, name := range names {
		row, err := summarise(dir, root, name)
		if err != nil {
			// A file that vanished between the listing and the stat is
			// not an error for the other four.
			if errors.Is(err, ErrNoSuchPlaybook) {
				continue
			}
			return nil, err
		}
		out = append(out, row)
	}
	return out, nil
}

// Playbook returns one playbook's markdown, exactly as the prompt reads it.
func (s *Service) Playbook(wsID, name string) (string, error) {
	dir, _, _, err := s.playbooksDir(wsID)
	if err != nil {
		return "", err
	}
	path, err := playbookPath(dir, name)
	if err != nil {
		return "", err
	}
	body, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("%w: %q", ErrNoSuchPlaybook, name)
		}
		return "", err
	}
	return string(body), nil
}

// writePlaybook is the one place a playbook file is written.
//
// The write is atomic: the body goes to a temporary file beside the target
// and is renamed over it, so a crash or a full disk leaves the playbook the
// agent had rather than half of the new one. The temporary name starts with
// a dot and does not end in .md, so a run that loads the directory while
// this is in flight does not read it.
func writePlaybook(path, body string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("app: create playbooks dir %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".playbook-*.tmp")
	if err != nil {
		return fmt.Errorf("app: write playbook %s: %w", filepath.Base(path), err)
	}
	name := tmp.Name()
	defer os.Remove(name) // a no-op once the rename has happened
	if _, err := tmp.WriteString(body); err != nil {
		tmp.Close()
		return fmt.Errorf("app: write playbook %s: %w", filepath.Base(path), err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("app: write playbook %s: %w", filepath.Base(path), err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("app: write playbook %s: %w", filepath.Base(path), err)
	}
	if err := os.Chmod(name, 0o644); err != nil {
		return fmt.Errorf("app: write playbook %s: %w", filepath.Base(path), err)
	}
	if err := os.Rename(name, path); err != nil {
		return fmt.Errorf("app: write playbook %s: %w", filepath.Base(path), err)
	}
	return nil
}

// writableDir resolves the workspace's playbooks directory for a write,
// refusing one configured outside `.sirdar/`.
func (s *Service) writableDir(wsID string) (dir, root string, err error) {
	dir, root, writable, err := s.playbooksDir(wsID)
	if err != nil {
		return "", "", err
	}
	if !writable {
		return "", "", fmt.Errorf(
			"%w: this workspace keeps its playbooks at %s, outside .sirdar; edit them there",
			ErrUnsupported, dir)
	}
	return dir, root, nil
}

// SavePlaybook writes a playbook's body and answers with the row the list
// draws afterwards. The name must be one a playbook could be called, or one
// of the files already there.
func (s *Service) SavePlaybook(wsID, name, body string) (PlaybookSummary, error) {
	dir, root, err := s.writableDir(wsID)
	if err != nil {
		return PlaybookSummary{}, err
	}
	path, err := playbookPath(dir, name)
	if err != nil {
		return PlaybookSummary{}, err
	}
	if !ValidPlaybookName(name) {
		// An existing file keeps whatever it is called — the scaffold's
		// own names, a file an operator wrote before the rule existed —
		// so only a name nothing is filed under has to match the pattern.
		if _, statErr := os.Stat(path); statErr != nil {
			return PlaybookSummary{}, fmt.Errorf(
				"%w: %q: a new playbook is named like 20-logs.md — two digits, a hyphen, a lowercase slug, .md",
				ErrNoSuchPlaybook, name)
		}
	}
	if err := writePlaybook(path, body); err != nil {
		return PlaybookSummary{}, err
	}
	return summarise(dir, root, name)
}

// AddPlaybook creates a playbook. A name already taken is refused rather
// than written over: the editor is how an existing one is changed.
func (s *Service) AddPlaybook(wsID, name, body string) (PlaybookSummary, error) {
	dir, root, err := s.writableDir(wsID)
	if err != nil {
		return PlaybookSummary{}, err
	}
	if !ValidPlaybookName(name) {
		return PlaybookSummary{}, fmt.Errorf(
			"%w: %q: a playbook is named like 20-logs.md — two digits, a hyphen, a lowercase slug, .md",
			ErrNoSuchPlaybook, name)
	}
	path, err := playbookPath(dir, name)
	if err != nil {
		return PlaybookSummary{}, err
	}
	if _, err := os.Stat(path); err == nil {
		return PlaybookSummary{}, fmt.Errorf("%w: %q", ErrPlaybookExists, name)
	} else if !os.IsNotExist(err) {
		return PlaybookSummary{}, err
	}
	if err := writePlaybook(path, body); err != nil {
		return PlaybookSummary{}, err
	}
	return summarise(dir, root, name)
}

// DeletePlaybook moves a playbook into `.sirdar/playbooks/.trash/` under a
// timestamped name rather than unlinking it. The prompt stops loading it
// either way — the loader reads *.md directly inside the directory — and an
// operator who deleted the wrong one still has the file.
func (s *Service) DeletePlaybook(wsID, name string) error {
	dir, _, err := s.writableDir(wsID)
	if err != nil {
		return err
	}
	path, err := playbookPath(dir, name)
	if err != nil {
		return err
	}
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("%w: %q", ErrNoSuchPlaybook, name)
		}
		return err
	}
	trash := filepath.Join(dir, trashDir)
	if err := os.MkdirAll(trash, 0o755); err != nil {
		return fmt.Errorf("app: create %s: %w", trash, err)
	}
	stamp := s.now().UTC().Format("20060102T150405Z")
	if err := os.Rename(path, filepath.Join(trash, name+"."+stamp)); err != nil {
		return fmt.Errorf("app: move playbook %s to the trash: %w", name, err)
	}
	return nil
}

// ScaffoldPlaybooks writes the starting set `sirdar init` scaffolds, never
// overwriting a file that is already there, and answers with the list as it
// stands afterwards. It is what the empty state's button runs.
func (s *Service) ScaffoldPlaybooks(wsID string) ([]PlaybookSummary, error) {
	dir, _, err := s.writableDir(wsID)
	if err != nil {
		return nil, err
	}
	if err := scaffoldPlaybooks(dir); err != nil {
		return nil, err
	}
	return s.Playbooks(wsID)
}

// OpenPlaybook hands one playbook's file to the operator's own editor.
//
// Unlike the desktop's OpenConfig this is a Service method, because
// `sirdar serve` serves the same Settings page and the operator is sitting
// at the machine the server runs on. The route that reaches it is refused
// on a listener other machines can reach; see internal/httpapi.
func (s *Service) OpenPlaybook(wsID, name string) error {
	dir, _, _, err := s.playbooksDir(wsID)
	if err != nil {
		return err
	}
	path, err := playbookPath(dir, name)
	if err != nil {
		return err
	}
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("%w: %q", ErrNoSuchPlaybook, name)
		}
		return err
	}
	return openPlaybookFile(path)
}

// openPlaybookFile is the last step, a variable so a test records the path
// instead of launching an editor on the machine running the tests.
var openPlaybookFile = osopen.Open

// scaffoldPlaybooks is the same scaffold `sirdar init` runs, a variable so
// a test can stand in for it.
var scaffoldPlaybooks = prompt.ScaffoldPlaybooks
