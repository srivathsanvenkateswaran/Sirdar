package repos

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// table is testdata/mentions.json, which the frontend's test reads too.
type table struct {
	Repos    []Repo `json:"repos"`
	Mentions []struct {
		Text string    `json:"text"`
		Want []Mention `json:"want"`
	} `json:"mentions"`
	Asks []struct {
		Text string `json:"text"`
		Want []Ask  `json:"want"`
	} `json:"asks"`
}

func loadTable(t *testing.T) table {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "mentions.json"))
	if err != nil {
		t.Fatal(err)
	}
	var tb table
	if err := json.Unmarshal(data, &tb); err != nil {
		t.Fatal(err)
	}
	return tb
}

func TestMentionsTable(t *testing.T) {
	tb := loadTable(t)
	for _, c := range tb.Mentions {
		got := Mentions(c.Text, tb.Repos)
		if len(got) == 0 && len(c.Want) == 0 {
			continue
		}
		if !reflect.DeepEqual(got, c.Want) {
			t.Errorf("Mentions(%q)\n got  %+v\n want %+v", c.Text, got, c.Want)
		}
	}
}

func TestAsksTable(t *testing.T) {
	tb := loadTable(t)
	for _, c := range tb.Asks {
		got := Asks(c.Text, tb.Repos)
		if len(got) == 0 && len(c.Want) == 0 {
			continue
		}
		if !reflect.DeepEqual(got, c.Want) {
			t.Errorf("Asks(%q)\n got  %+v\n want %+v", c.Text, got, c.Want)
		}
	}
}

func TestPhrasesAndReasons(t *testing.T) {
	cases := map[string]string{
		Mention{Name: "Acme.Web", Status: StatusCompanion}.Phrase():                                  "mentions Acme.Web (companion repo)",
		Mention{Name: "Acme.Web", Status: StatusUnknown}.Phrase():                                    "mentions Acme.Web (not configured — add it under repos:)",
		Mention{Name: "api", Status: StatusWorkspace}.Phrase():                                       "",
		Ask{Phrase: "Acme.Web", Status: StatusUnknown}.Reason():                                      "Acme.Web is not a configured repository",
		Ask{Phrase: "POS", Status: StatusAmbiguous, Candidates: []string{"A.POS", "B.POS"}}.Reason(): "POS matches more than one repository: A.POS, B.POS",
		Ask{Phrase: "web", Name: "Acme.Web", Status: StatusCompanion}.Reason():                       "",
	}
	for got, want := range cases {
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	}
}

func TestSlug(t *testing.T) {
	cases := map[string]string{
		"git@github.com:Acme/Web.git":          "github.com/acme/web",
		"ssh://git@github.com/acme/web.git":    "github.com/acme/web",
		"https://github.com/acme/web":          "github.com/acme/web",
		"https://user@github.com/acme/web.git": "github.com/acme/web",
		"https://gitlab.example.com/g/sub/r":   "gitlab.example.com/sub/r",
		"/srv/git/web.git":                     "",
		"":                                     "",
		"C:/repos/web":                         "",
	}
	for in, want := range cases {
		if got := Slug(in); got != want {
			t.Errorf("Slug(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFixTarget(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "web"), 0o755); err != nil {
		t.Fatal(err)
	}
	companion := filepath.Join(t.TempDir(), "Acme.Web")
	list := []Repo{
		{Name: "api", Path: root, Workspace: true},
		{Name: "Acme.Web", Path: companion},
		{Name: "web", Path: filepath.Join(t.TempDir(), "web")},
	}
	cases := []struct {
		files []string
		want  string
	}{
		{[]string{"src/orders/total.go"}, ""},
		{[]string{"src/a.go", "Acme.Web/src/app/cart.ts"}, "Acme.Web"},
		{[]string{"acme.web/src/app/cart.ts"}, "Acme.Web"},
		{[]string{filepath.Join(companion, "src", "x.ts")}, "Acme.Web"},
		// The workspace has a web/ of its own, so web/… is the workspace's.
		{[]string{"web/index.html"}, ""},
		{nil, ""},
	}
	for _, c := range cases {
		got, ok := FixTarget(c.files, list, root)
		if got.Name != c.want || ok != (c.want != "") {
			t.Errorf("FixTarget(%v) = %q, %v; want %q", c.files, got.Name, ok, c.want)
		}
	}
}

// git runs one git command in dir for a test fixture, a throwaway
// repository the test made. Its commits are made with the machine's own
// identity as it stands; the test skips on a machine with none rather than
// inventing one.
func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func TestReadFactsAndOrigin(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	upstream := t.TempDir()
	git(t, upstream, "init", "-q", "-b", "main")
	if err := exec.Command("git", "-C", upstream, "var", "GIT_AUTHOR_IDENT").Run(); err != nil {
		t.Skip("git has no author identity configured in this environment")
	}
	git(t, upstream, "commit", "-q", "--allow-empty", "-m", "one")

	clone := filepath.Join(t.TempDir(), "clone")
	git(t, filepath.Dir(clone), "clone", "-q", upstream, clone)
	git(t, upstream, "commit", "-q", "--allow-empty", "-m", "two")
	git(t, upstream, "commit", "-q", "--allow-empty", "-m", "three")
	// The test fetches its own throwaway clone, so the clone has
	// something to be behind; ReadFacts itself never fetches.
	git(t, clone, "fetch", "-q")

	f := ReadFacts(context.Background(), clone)
	if !f.Exists || !f.Git || f.Branch != "main" || f.Upstream != "origin/main" || f.Behind != 2 || f.Ahead != 0 {
		t.Fatalf("facts = %+v", f)
	}
	if f.FetchedAt.IsZero() {
		t.Error("FetchedAt is zero after a fetch")
	}
	if s := f.Summary(f.FetchedAt.Add(3 * time.Hour)); s != "main · 2 behind origin/main · fetched 3 hours ago" {
		t.Errorf("summary %q", s)
	}
	if got := Origin(clone); got != upstream {
		t.Errorf("Origin = %q, want %q", got, upstream)
	}

	plain := t.TempDir()
	if f := ReadFacts(context.Background(), plain); !f.Exists || f.Git || f.Summary(time.Now()) != "a plain directory" {
		t.Errorf("plain directory facts %+v", f)
	}
	if f := ReadFacts(context.Background(), filepath.Join(plain, "missing")); f.Exists || f.Summary(time.Now()) != "not found" {
		t.Errorf("missing facts %+v", f)
	}
	if !strings.Contains(Facts{Exists: true, Git: true, Branch: "dev"}.Summary(time.Now()), "no upstream · never fetched") {
		t.Error("summary of a branch with no upstream")
	}
}
