package prompt

import (
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// --- Frontmatter ---

func TestParsePlaybookFrontmatter(t *testing.T) {
	data := "---\nkinds: [triage, session]\nwhen: [database, \"new relic\"]\nabout: Read-only MySQL.\n---\nDirect access lets you confirm state.\n"
	p := ParsePlaybook("40-database", "/ws/.sirdar/playbooks/40-database.md", data)

	if !reflect.DeepEqual(p.Kinds, []string{"triage", "session"}) {
		t.Errorf("Kinds = %q", p.Kinds)
	}
	if !reflect.DeepEqual(p.When, []string{"database", "new relic"}) {
		t.Errorf("When = %q", p.When)
	}
	if p.About != "Read-only MySQL." {
		t.Errorf("About = %q", p.About)
	}
	if p.Body != "Direct access lets you confirm state.\n" {
		t.Errorf("Body = %q", p.Body)
	}
	if p.FrontmatterErr != "" {
		t.Errorf("FrontmatterErr = %q", p.FrontmatterErr)
	}
	if p.Name != "40-database" || p.Path != "/ws/.sirdar/playbooks/40-database.md" {
		t.Errorf("Name, Path = %q, %q", p.Name, p.Path)
	}
}

func TestParsePlaybookCRLFFrontmatter(t *testing.T) {
	data := "---\r\nwhen: [ log , \"\" ]\r\n---\r\nBody.\r\n"
	p := ParsePlaybook("20-logs", "/p", data)
	if p.FrontmatterErr != "" || !reflect.DeepEqual(p.When, []string{"log"}) || p.Body != "Body.\r\n" {
		t.Errorf("got %+v", p)
	}
}

func TestParsePlaybookWithoutFrontmatter(t *testing.T) {
	data := "The thread is the customer's account.\n\n## How\n"
	p := ParsePlaybook("10-helpdesk", "/p", data)
	if p.Kinds != nil || p.When != nil {
		t.Errorf("Kinds, When = %q, %q, want nil", p.Kinds, p.When)
	}
	if p.About != "The thread is the customer's account." {
		t.Errorf("About = %q", p.About)
	}
	if p.Body != data {
		t.Errorf("Body = %q, want it unchanged", p.Body)
	}
	if p.FrontmatterErr != "" {
		t.Errorf("FrontmatterErr = %q", p.FrontmatterErr)
	}
}

func TestParsePlaybookBadFrontmatterFailsOpen(t *testing.T) {
	cases := []struct {
		name     string
		data     string
		wantErr  string // exact; "" means any non-empty error
		wantBody string
	}{
		{
			name:     "unknown key",
			data:     "---\nwhn: [x]\n---\nBody line.\n",
			wantBody: "Body line.\n",
		},
		{
			name:     "unknown kind",
			data:     "---\nkinds: [triage, nope]\nwhen: [db]\n---\nBody line.\n",
			wantErr:  `unknown kind "nope"`,
			wantBody: "Body line.\n",
		},
		{
			name:     "no closing line",
			data:     "---\nkinds: [triage]\nBody line.\n",
			wantErr:  "no closing ---",
			wantBody: "---\nkinds: [triage]\nBody line.\n",
		},
		{
			name:     "multi-line about",
			data:     "---\nabout: \"one\\ntwo\"\n---\nBody line.\n",
			wantErr:  "about is more than one line",
			wantBody: "Body line.\n",
		},
		{
			name:     "long about",
			data:     "---\nabout: " + strings.Repeat("a", 161) + "\n---\nBody line.\n",
			wantErr:  "about is longer than 160 characters",
			wantBody: "Body line.\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := ParsePlaybook("x", "/p", tc.data)
			if p.FrontmatterErr == "" || (tc.wantErr != "" && p.FrontmatterErr != tc.wantErr) {
				t.Errorf("FrontmatterErr = %q, want %q", p.FrontmatterErr, tc.wantErr)
			}
			if p.Kinds != nil || p.When != nil {
				t.Errorf("Kinds, When = %q, %q, want nil: a bad block must load the file as always-on", p.Kinds, p.When)
			}
			if p.Body != tc.wantBody {
				t.Errorf("Body = %q, want %q", p.Body, tc.wantBody)
			}
			if p.About != "Body line." && tc.name != "no closing line" {
				t.Errorf("About = %q, want the lede", p.About)
			}
		})
	}

	bad := ParsePlaybook("x", "/p", "---\nkinds: [triage, nope]\n---\nBody.\n")
	got := FrontmatterWarnings([]Playbook{{Name: "ok"}, bad})
	want := []string{`playbook x: frontmatter ignored: unknown kind "nope"`}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("FrontmatterWarnings = %q, want %q", got, want)
	}
}

// --- Matching ---

func TestMatchKeyword(t *testing.T) {
	cases := []struct {
		text, keyword string
		want          bool
	}{
		{"Database is down", "database", true},
		{"databases", "database", false},
		{"the db-sync job", "db", true},
		{"الخطأ في التقرير", "خطأ", true},
		{"New   Relic trace", "new relic", true},
		{"NEWRELIC", "newrelic", true},
		{"error500", "500", false},
		{"HTTP 500.", "500", true},
		{"feedback db", "db", true},
	}
	for _, tc := range cases {
		if got := MatchKeyword(tc.text, tc.keyword); got != tc.want {
			t.Errorf("MatchKeyword(%q, %q) = %v, want %v", tc.text, tc.keyword, got, tc.want)
		}
	}
}

// --- Selection ---

func sizedPlaybook(name string, size int, kinds, when []string) Playbook {
	return Playbook{
		Name:  name,
		Body:  strings.Repeat("x", size),
		Kinds: kinds,
		When:  when,
		About: "About " + name + ".",
		Path:  "/ws/.sirdar/playbooks/" + name + ".md",
	}
}

func fiveScoped() []Playbook {
	return []Playbook{
		sizedPlaybook("10-a", 100, nil, nil),
		sizedPlaybook("20-b", 100, nil, []string{"log"}),
		sizedPlaybook("30-c", 100, nil, []string{"slow"}),
		sizedPlaybook("40-d", 100, []string{"fix"}, nil),
		sizedPlaybook("50-e", 100, nil, []string{"db"}),
	}
}

func names(ps []Playbook) []string {
	out := []string{}
	for _, p := range ps {
		out = append(out, p.Name)
	}
	return out
}

func listedNames(ls []Listed) []string {
	out := []string{}
	for _, l := range ls {
		out = append(out, fmt.Sprintf("%s(%q)", l.Playbook.Name, l.Matched))
	}
	return out
}

func TestSelectPlaybooks(t *testing.T) {
	sel := SelectPlaybooks(fiveScoped(), "triage", "log slow db", 250)
	if got := names(sel.Loaded); !reflect.DeepEqual(got, []string{"10-a", "20-b"}) {
		t.Errorf("budget 250: Loaded = %q", got)
	}
	if got := listedNames(sel.Listed); !reflect.DeepEqual(got, []string{`30-c("slow")`, `50-e("db")`}) {
		t.Errorf("budget 250: Listed = %q", got)
	}

	sel = SelectPlaybooks(fiveScoped(), "triage", "log slow db", 0)
	if got := names(sel.Loaded); !reflect.DeepEqual(got, []string{"10-a", "20-b", "30-c", "50-e"}) {
		t.Errorf("budget 0: Loaded = %q", got)
	}
	if len(sel.Listed) != 0 {
		t.Errorf("budget 0: Listed = %q", listedNames(sel.Listed))
	}

	sel = SelectPlaybooks(fiveScoped(), "triage", "nothing", 250)
	if got := names(sel.Loaded); !reflect.DeepEqual(got, []string{"10-a"}) {
		t.Errorf("no match: Loaded = %q", got)
	}
	if got := listedNames(sel.Listed); !reflect.DeepEqual(got, []string{`20-b("")`, `30-c("")`, `50-e("")`}) {
		t.Errorf("no match: Listed = %q", got)
	}

	sel = SelectPlaybooks(fiveScoped(), "triage", "log slow db", 50)
	if got := names(sel.Loaded); !reflect.DeepEqual(got, []string{"10-a"}) {
		t.Errorf("budget 50: Loaded = %q, want the always-on playbook kept", got)
	}

	sel = SelectPlaybooks(fiveScoped(), "fix", "", 0)
	if got := names(sel.Loaded); !reflect.DeepEqual(got, []string{"10-a", "40-d"}) {
		t.Errorf("kind fix: Loaded = %q", got)
	}
}

func TestSelectionSummary(t *testing.T) {
	sel := SelectPlaybooks(fiveScoped(), "triage", "log slow db", 250)
	if got, want := sel.Summary(), "Playbooks: loaded 10-a, 20-b; listed 30-c, 50-e"; got != want {
		t.Errorf("Summary = %q, want %q", got, want)
	}
	sel = SelectPlaybooks(fiveScoped(), "triage", "log slow db", 0)
	sel.Loaded = sel.Loaded[:1]
	if got, want := sel.Summary(), "Playbooks: loaded 10-a"; got != want {
		t.Errorf("Summary = %q, want %q", got, want)
	}
	if got, want := (Selection{}).Summary(), "Playbooks: loaded none"; got != want {
		t.Errorf("Summary = %q, want %q", got, want)
	}
}

// --- Listing ---

func TestOtherPlaybooksListing(t *testing.T) {
	loaded := []Playbook{{Name: "10-helpdesk", Body: "Read the thread.\n"}}
	listed := []Listed{
		{Playbook: Playbook{Name: "40-database", About: "Read-only MySQL.", Path: "/ws/pb/40-database.md"}, Matched: "database"},
		{Playbook: Playbook{Name: "30-apm", Path: "/ws/pb/30-apm.md"}},
	}
	got := playbooksSection(loaded, listed)
	want := "# Playbooks\n\n" +
		"## 10-helpdesk\n\nRead the thread.\n\n" +
		"## Other playbooks\n\n" +
		"On file but not loaded for this run. Read one with your file tool when its subject comes up.\n\n" +
		"- 40-database — Read-only MySQL. Matched \"database\"; read before querying. `/ws/pb/40-database.md`\n" +
		"- 30-apm `/ws/pb/30-apm.md`"
	if got != want {
		t.Errorf("playbooksSection:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}

	if got := playbooksSection(nil, nil); got != "# Playbooks" {
		t.Errorf("empty playbooksSection = %q, want the bare heading", got)
	}
	if got := knowledgeSection(nil, listed); !strings.HasPrefix(got, "# Workspace knowledge — consult when relevant\n\n## Other playbooks\n\n") {
		t.Errorf("knowledgeSection with only listed playbooks:\n%s", got)
	}
}

func TestKnowledgeSectionEmptyIsOmitted(t *testing.T) {
	if got := knowledgeSection(nil, nil); got != "" {
		t.Errorf("knowledgeSection(nil, nil) = %q, want \"\"", got)
	}
}

// --- The recorded sizes ---

// filler is deterministic text of exactly n bytes. The bodies stand in for
// a real workspace's playbooks; only their sizes are taken from it.
func filler(n int) string {
	return strings.Repeat("lorem ipsum ", n/12+1)[:n]
}

// TestScopedBudgetOnRecordedSizes replays the selection on seven playbooks
// with the sizes and frontmatter of the workspace the budget was measured
// on (spec §1.7). A session whose text names no topic loads the three
// always-on files and lists the rest; one whose instruction names the
// database, New Relic and an Arabic error word matches three more, and the
// 16 KiB budget lists all three rather than loading 12 KiB more.
func TestScopedBudgetOnRecordedSizes(t *testing.T) {
	dir := t.TempDir()
	type spec struct {
		name  string
		size  int
		kinds string
		when  string
	}
	files := []spec{
		{"00-environment", 2498, "[triage, rca]", ""},
		{"10-helpdesk", 3815, "", ""},
		{"20-logs", 3556, "", "[log, logs, victorialogs, grafana, loki, error, errors, exception, crash, 500, 502, 503, 504, خطأ]"},
		{"30-apm", 3500, "", "[newrelic, new relic, apm, slow, latency, timeout, performance, trace, بطء, بطيء]"},
		{"40-database", 4941, "", "[database, mysql, sql, query, missing, duplicate, report, balance]"},
		{"50-code", 5018, "[triage, rca, session, fix]", ""},
		{"60-tenancy-and-vocabulary", 4323, "", ""},
	}
	for _, f := range files {
		kinds := f.kinds
		if kinds == "" {
			kinds = "[triage, rca, session]"
		}
		head := "---\nkinds: " + kinds + "\n"
		if f.when != "" {
			head += "when: " + f.when + "\n"
		}
		head += "---\n"
		write(t, filepath.Join(dir, f.name+".md"), head+filler(f.size))
	}
	all, err := LoadPlaybooks(dir)
	if err != nil {
		t.Fatal(err)
	}
	for i, p := range all {
		if p.FrontmatterErr != "" || len(p.Body) != files[i].size {
			t.Fatalf("%s: FrontmatterErr %q, body %d bytes, want %d", p.Name, p.FrontmatterErr, len(p.Body), files[i].size)
		}
	}

	// (a) A session on a ticket whose thread names no topic.
	sel := SelectPlaybooks(all, "session", "Investigate this ticket\nالعميل يسأل عن الفاتورة", 16384)
	got := knowledgeSection(sel.Loaded, sel.Listed)
	if len(got) > 16384+1000 {
		t.Errorf("knowledge section is %d bytes, want at most %d", len(got), 16384+1000)
	}
	listing := got[strings.Index(got, "## Other playbooks"):]
	for _, name := range []string{"20-logs", "30-apm", "40-database"} {
		if !strings.Contains(listing, "- "+name) {
			t.Errorf("listing does not name %s:\n%s", name, listing)
		}
	}
	if strings.Contains(got, "00-environment") {
		t.Error("a session prompt carries 00-environment, which is for triage and rca only")
	}

	// (b) An instruction that names every tool.
	text := "Investigate this ticket. Use the database MCPs and the New Relic MCP. خطأ"
	sel = SelectPlaybooks(all, "session", text, 16384)
	if got := names(sel.Loaded); !reflect.DeepEqual(got, []string{"10-helpdesk", "50-code", "60-tenancy-and-vocabulary"}) {
		t.Errorf("budget 16384: Loaded = %q", got)
	}
	if got := listedNames(sel.Listed); !reflect.DeepEqual(got, []string{`20-logs("خطأ")`, `30-apm("new relic")`, `40-database("database")`}) {
		t.Errorf("budget 16384: Listed = %q", got)
	}
	section := knowledgeSection(sel.Loaded, sel.Listed)
	for _, name := range []string{"20-logs", "30-apm", "40-database"} {
		if !strings.Contains(section, "- "+name+" — ") || !strings.Contains(section, "Matched \"") {
			t.Errorf("%s is not listed with its match:\n%s", name, section)
		}
	}

	sel = SelectPlaybooks(all, "session", text, 0)
	if got := names(sel.Loaded); len(got) != 6 || len(sel.Listed) != 0 {
		t.Errorf("budget 0: Loaded = %q, Listed = %q, want all six loaded", got, listedNames(sel.Listed))
	}
}

// --- Built-in defaults ---

func TestBuiltinPlaybooksCarryFrontmatter(t *testing.T) {
	dir := t.TempDir()
	if err := ScaffoldPlaybooks(dir); err != nil {
		t.Fatal(err)
	}
	all, err := LoadPlaybooks(dir)
	if err != nil {
		t.Fatal(err)
	}
	triageRCASession := []string{"triage", "rca", "session"}
	want := map[string]Playbook{
		"10-helpdesk": {Kinds: triageRCASession, About: "Reading the helpdesk thread, and what L1 already told the customer."},
		"20-logs": {Kinds: triageRCASession,
			When:  []string{"log", "logs", "error", "errors", "exception", "crash", "stack", "500", "502", "503", "504"},
			About: "Query syntax, control queries, and timezones for application logs."},
		"30-apm": {Kinds: triageRCASession,
			When:  []string{"slow", "latency", "timeout", "performance", "trace", "span", "apm"},
			About: "Traces and spans: retention, control queries, comparing with a known-good trace."},
		"40-database": {Kinds: triageRCASession,
			When:  []string{"data", "database", "db", "sql", "query", "record", "records", "missing", "duplicate", "report", "total", "balance"},
			About: "Read-only database checks: resolve the record first, cite query and row count."},
		"50-code": {Kinds: []string{"session", "triage", "rca", "fix"}, About: "Cite file:line, follow the call path, check sibling paths and tests."},
	}
	if len(all) != len(want) {
		t.Fatalf("got %d playbooks, want %d", len(all), len(want))
	}
	for _, p := range all {
		w := want[p.Name]
		if p.FrontmatterErr != "" {
			t.Errorf("%s: FrontmatterErr %q", p.Name, p.FrontmatterErr)
		}
		if !reflect.DeepEqual(p.Kinds, w.Kinds) || !reflect.DeepEqual(p.When, w.When) || p.About != w.About {
			t.Errorf("%s: Kinds %q When %q About %q, want %q %q %q", p.Name, p.Kinds, p.When, p.About, w.Kinds, w.When, w.About)
		}
		if strings.HasPrefix(p.Body, "---") {
			t.Errorf("%s: the frontmatter is still in the body", p.Name)
		}
	}
}
