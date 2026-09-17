package app

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// playbookService returns a service over one workspace whose playbooks
// directory holds the files given, and the workspace id to call it with.
func playbookService(t *testing.T, files map[string]string) (*Service, string, string) {
	t.Helper()
	root := newWorkspace(t)
	dir := filepath.Join(root, ".sirdar", "playbooks")
	// newWorkspace scaffolds one file of its own; a case that names its
	// own set gets only those.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if err := os.Remove(filepath.Join(dir, e.Name())); err != nil {
			t.Fatal(err)
		}
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	svc := New(newRegistry(t, root), BuildDeps, Options{})
	return svc, WorkspaceID(root), dir
}

const helpdeskPlaybook = `# Helpdesk

The helpdesk is the customer's own words. Read the whole thread before the tracker ticket: the tracker is a summary somebody else already made.

- Attachments are downloaded beside the bundle.
`

func TestPlaybooksListsInPromptOrderWithTitleAndLede(t *testing.T) {
	svc, ws, _ := playbookService(t, map[string]string{
		"20-logs.md":     "# Logs\n\nLoki keeps 30 days.\n",
		"10-helpdesk.md": helpdeskPlaybook,
		"notes.txt":      "not a playbook",
	})

	rows, err := svc.Playbooks(ws)
	if err != nil {
		t.Fatalf("Playbooks: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows %+v, want the two markdown files", rows)
	}
	if rows[0].Name != "10-helpdesk.md" || rows[1].Name != "20-logs.md" {
		t.Fatalf("order %s, %s: want the filename order the prompt loads in", rows[0].Name, rows[1].Name)
	}
	first := rows[0]
	if first.Title != "Helpdesk" {
		t.Errorf("title %q, want the first H1", first.Title)
	}
	if !strings.HasPrefix(first.Lede, "The helpdesk is the customer's own words.") {
		t.Errorf("lede %q, want the first paragraph", first.Lede)
	}
	if len([]rune(first.Lede)) > ledeMax+1 {
		t.Errorf("lede is %d characters, want at most %d plus the ellipsis", len([]rune(first.Lede)), ledeMax)
	}
	if first.Order != "10" {
		t.Errorf("order %q, want the filename prefix", first.Order)
	}
	if first.File != ".sirdar/playbooks/10-helpdesk.md" {
		t.Errorf("file %q, want the path relative to the workspace root", first.File)
	}
	if first.Bytes != int64(len(helpdeskPlaybook)) {
		t.Errorf("bytes %d, want %d", first.Bytes, len(helpdeskPlaybook))
	}
	if first.ModifiedAt.IsZero() {
		t.Error("modifiedAt is zero")
	}
}

func TestPlaybooksFallsBackToTheFilenameForATitle(t *testing.T) {
	svc, ws, _ := playbookService(t, map[string]string{"30-apm.md": "Traces are sampled at 1%.\n"})

	rows, err := svc.Playbooks(ws)
	if err != nil {
		t.Fatal(err)
	}
	if rows[0].Title != "30-apm" {
		t.Errorf("title %q, want the filename without .md", rows[0].Title)
	}
	if rows[0].Lede != "Traces are sampled at 1%." {
		t.Errorf("lede %q", rows[0].Lede)
	}
}

func TestPlaybooksOnAWorkspaceWithNoDirectoryIsEmptyNotAnError(t *testing.T) {
	svc, ws, dir := playbookService(t, nil)
	if err := os.Remove(dir); err != nil {
		t.Fatal(err)
	}

	rows, err := svc.Playbooks(ws)
	if err != nil {
		t.Fatalf("Playbooks with no directory: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("rows %+v, want none", rows)
	}
}

func TestPlaybookReadsTheBodyAndRefusesAnUnknownName(t *testing.T) {
	svc, ws, _ := playbookService(t, map[string]string{"10-helpdesk.md": helpdeskPlaybook})

	body, err := svc.Playbook(ws, "10-helpdesk.md")
	if err != nil {
		t.Fatalf("Playbook: %v", err)
	}
	if body != helpdeskPlaybook {
		t.Errorf("body %q, want the file exactly", body)
	}
	if _, err := svc.Playbook(ws, "99-nope.md"); !errors.Is(err, ErrNoSuchPlaybook) {
		t.Errorf("Playbook on a name nothing is filed under: %v, want ErrNoSuchPlaybook", err)
	}
}

// TestPlaybookRefusesTraversal is the guard the whole feature rests on: the
// name reaches the service straight off an HTTP path segment, already
// unescaped, so anything that is not one plain filename has to be refused
// before it is joined onto the directory.
func TestPlaybookRefusesTraversal(t *testing.T) {
	svc, ws, dir := playbookService(t, map[string]string{"10-helpdesk.md": helpdeskPlaybook})
	outside := filepath.Join(filepath.Dir(filepath.Dir(dir)), "secret.md")
	if err := os.WriteFile(outside, []byte("# Secret\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	names := []string{
		"../secret.md",
		"../../secret.md",
		filepath.Join("..", "..", "secret.md"),
		"..",
		".",
		"",
		".trash",
		"sub/10-helpdesk.md",
		outside,
	}
	for _, name := range names {
		if _, err := svc.Playbook(ws, name); !errors.Is(err, ErrNoSuchPlaybook) {
			t.Errorf("Playbook(%q) = %v, want ErrNoSuchPlaybook", name, err)
		}
		if _, err := svc.SavePlaybook(ws, name, "# no"); !errors.Is(err, ErrNoSuchPlaybook) {
			t.Errorf("SavePlaybook(%q) = %v, want ErrNoSuchPlaybook", name, err)
		}
		if err := svc.DeletePlaybook(ws, name); !errors.Is(err, ErrNoSuchPlaybook) {
			t.Errorf("DeletePlaybook(%q) = %v, want ErrNoSuchPlaybook", name, err)
		}
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatalf("the file outside the playbooks directory was touched: %v", err)
	}
	if body, _ := os.ReadFile(outside); string(body) != "# Secret\n" {
		t.Fatalf("the file outside the playbooks directory was written: %q", body)
	}
}

func TestSavePlaybookWritesAtomicallyAndReportsTheRowAfterwards(t *testing.T) {
	svc, ws, dir := playbookService(t, map[string]string{"10-helpdesk.md": helpdeskPlaybook})

	row, err := svc.SavePlaybook(ws, "10-helpdesk.md", "# Helpdesk\n\nRead the thread first.\n")
	if err != nil {
		t.Fatalf("SavePlaybook: %v", err)
	}
	if row.Lede != "Read the thread first." {
		t.Errorf("lede %q, want the new first paragraph", row.Lede)
	}
	body, err := os.ReadFile(filepath.Join(dir, "10-helpdesk.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "# Helpdesk\n\nRead the thread first.\n" {
		t.Errorf("file %q", body)
	}
	// Nothing half-written is left beside it: the temporary file is
	// renamed over the target, not left for the next `sirdar init` to
	// wonder about.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("directory holds %d entries, want only the playbook", len(entries))
	}
}

func TestSavePlaybookRefusesANewNameThatIsNotShaped(t *testing.T) {
	svc, ws, _ := playbookService(t, map[string]string{"legacy-notes.md": "# Legacy\n"})

	if _, err := svc.SavePlaybook(ws, "Logs.md", "# Logs\n"); !errors.Is(err, ErrNoSuchPlaybook) {
		t.Errorf("SavePlaybook on an unshaped new name: %v, want ErrNoSuchPlaybook", err)
	}
	// A file that is already there keeps whatever it is called.
	if _, err := svc.SavePlaybook(ws, "legacy-notes.md", "# Legacy\n\nStill here.\n"); err != nil {
		t.Errorf("SavePlaybook on an existing file with an old-style name: %v", err)
	}
}

func TestAddPlaybookCreatesOneAndRefusesANameAlreadyTaken(t *testing.T) {
	svc, ws, dir := playbookService(t, map[string]string{"10-helpdesk.md": helpdeskPlaybook})

	row, err := svc.AddPlaybook(ws, "40-database.md", "# Database\n\nRead replicas lag.\n")
	if err != nil {
		t.Fatalf("AddPlaybook: %v", err)
	}
	if row.Name != "40-database.md" || row.Title != "Database" {
		t.Errorf("row %+v", row)
	}
	if _, err := os.Stat(filepath.Join(dir, "40-database.md")); err != nil {
		t.Fatalf("the file was not written: %v", err)
	}

	if _, err := svc.AddPlaybook(ws, "10-helpdesk.md", "# Overwritten\n"); !errors.Is(err, ErrPlaybookExists) {
		t.Errorf("AddPlaybook on a name already taken: %v, want ErrPlaybookExists", err)
	}
	body, _ := os.ReadFile(filepath.Join(dir, "10-helpdesk.md"))
	if string(body) != helpdeskPlaybook {
		t.Error("a refused AddPlaybook wrote over the existing playbook")
	}

	for _, bad := range []string{"logs.md", "1-logs.md", "10-Logs.md", "10-logs", "10-logs.txt", "10_logs.md"} {
		if _, err := svc.AddPlaybook(ws, bad, "# no\n"); !errors.Is(err, ErrNoSuchPlaybook) {
			t.Errorf("AddPlaybook(%q) = %v, want the name refused", bad, err)
		}
	}
}

func TestDeletePlaybookMovesItToTheTrash(t *testing.T) {
	svc, ws, dir := playbookService(t, map[string]string{"10-helpdesk.md": helpdeskPlaybook})
	svc.opts.Now = func() time.Time { return time.Date(2026, 9, 17, 14, 25, 30, 0, time.UTC) }

	if err := svc.DeletePlaybook(ws, "10-helpdesk.md"); err != nil {
		t.Fatalf("DeletePlaybook: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "10-helpdesk.md")); !os.IsNotExist(err) {
		t.Fatalf("the playbook is still in place: %v", err)
	}
	kept := filepath.Join(dir, ".trash", "10-helpdesk.md.20260917T142530Z")
	body, err := os.ReadFile(kept)
	if err != nil {
		t.Fatalf("the playbook was unlinked rather than kept: %v", err)
	}
	if string(body) != helpdeskPlaybook {
		t.Errorf("trashed body %q", body)
	}
	// The trash is not a playbook: neither the list nor the prompt's own
	// loader reads a directory or a file that does not end in .md.
	rows, err := svc.Playbooks(ws)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("rows %+v, want none once the only playbook is deleted", rows)
	}
	if err := svc.DeletePlaybook(ws, "10-helpdesk.md"); !errors.Is(err, ErrNoSuchPlaybook) {
		t.Errorf("deleting it twice: %v, want ErrNoSuchPlaybook", err)
	}
}

func TestScaffoldPlaybooksWritesTheStartingSetAndSkipsExistingFiles(t *testing.T) {
	svc, ws, dir := playbookService(t, map[string]string{"10-helpdesk.md": "# Mine\n\nDo not overwrite me.\n"})

	rows, err := svc.ScaffoldPlaybooks(ws)
	if err != nil {
		t.Fatalf("ScaffoldPlaybooks: %v", err)
	}
	if len(rows) < 2 {
		t.Fatalf("rows %+v, want the scaffolded set", rows)
	}
	body, err := os.ReadFile(filepath.Join(dir, "10-helpdesk.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "# Mine\n\nDo not overwrite me.\n" {
		t.Error("the scaffold wrote over a playbook that was already there")
	}
}

func TestOpenPlaybookHandsTheFileToTheDesktopAndRefusesAnUnknownName(t *testing.T) {
	svc, ws, dir := playbookService(t, map[string]string{"10-helpdesk.md": helpdeskPlaybook})
	var opened []string
	original := openPlaybookFile
	openPlaybookFile = func(target string) error {
		opened = append(opened, target)
		return nil
	}
	t.Cleanup(func() { openPlaybookFile = original })

	if err := svc.OpenPlaybook(ws, "10-helpdesk.md"); err != nil {
		t.Fatalf("OpenPlaybook: %v", err)
	}
	if len(opened) != 1 || opened[0] != filepath.Join(dir, "10-helpdesk.md") {
		t.Fatalf("opened %v", opened)
	}
	if err := svc.OpenPlaybook(ws, "../secret.md"); !errors.Is(err, ErrNoSuchPlaybook) {
		t.Errorf("OpenPlaybook on a traversal: %v, want ErrNoSuchPlaybook", err)
	}
	if err := svc.OpenPlaybook(ws, "99-nope.md"); !errors.Is(err, ErrNoSuchPlaybook) {
		t.Errorf("OpenPlaybook on an unknown name: %v, want ErrNoSuchPlaybook", err)
	}
	if len(opened) != 1 {
		t.Fatalf("a refused OpenPlaybook opened %v", opened)
	}
}

// TestWritesRefusedWhenPlaybooksLiveOutsideSirdar pins the one thing this
// feature must not do: Sirdar scaffolds files under .sirdar and writes
// nothing else in a workspace, so a workspace pointing `playbooks:` at its
// own source tree gets a list it can read and no editor.
func TestWritesRefusedWhenPlaybooksLiveOutsideSirdar(t *testing.T) {
	root := newWorkspace(t)
	config := filepath.Join(root, ".sirdar", "config.yaml")
	body, err := os.ReadFile(config)
	if err != nil {
		t.Fatal(err)
	}
	moved := strings.Replace(string(body), "playbooks: .sirdar/playbooks", "playbooks: docs/playbooks", 1)
	if moved == string(body) {
		t.Fatal("the fixture config no longer names a playbooks directory")
	}
	if err := os.WriteFile(config, []byte(moved), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "docs", "playbooks")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "10-helpdesk.md"), []byte(helpdeskPlaybook), 0o644); err != nil {
		t.Fatal(err)
	}
	svc := New(newRegistry(t, root), BuildDeps, Options{})
	ws := WorkspaceID(root)

	rows, err := svc.Playbooks(ws)
	if err != nil {
		t.Fatalf("Playbooks on a configured directory: %v", err)
	}
	if len(rows) != 1 || rows[0].Name != "10-helpdesk.md" {
		t.Fatalf("rows %+v, want the configured directory read", rows)
	}
	if _, err := svc.SavePlaybook(ws, "10-helpdesk.md", "# no\n"); !errors.Is(err, ErrUnsupported) {
		t.Errorf("SavePlaybook outside .sirdar: %v, want ErrUnsupported", err)
	}
	if _, err := svc.AddPlaybook(ws, "50-code.md", "# no\n"); !errors.Is(err, ErrUnsupported) {
		t.Errorf("AddPlaybook outside .sirdar: %v, want ErrUnsupported", err)
	}
	if err := svc.DeletePlaybook(ws, "10-helpdesk.md"); !errors.Is(err, ErrUnsupported) {
		t.Errorf("DeletePlaybook outside .sirdar: %v, want ErrUnsupported", err)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "10-helpdesk.md")); string(got) != helpdeskPlaybook {
		t.Error("a refused write reached the file anyway")
	}
}

func TestPlaybookLedeIsCutAtTheLimit(t *testing.T) {
	long := strings.Repeat("the export times out on the second page ", 12)
	svc, ws, _ := playbookService(t, map[string]string{"10-helpdesk.md": "# Helpdesk\n\n" + long + "\n"})

	rows, err := svc.Playbooks(ws)
	if err != nil {
		t.Fatal(err)
	}
	lede := rows[0].Lede
	if len([]rune(lede)) > ledeMax+1 {
		t.Fatalf("lede is %d characters: %q", len([]rune(lede)), lede)
	}
	if !strings.HasSuffix(lede, "…") {
		t.Errorf("a cut lede %q does not say it was cut", lede)
	}
}
