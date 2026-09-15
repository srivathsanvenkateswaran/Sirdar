package eval

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/config"
	runner "github.com/srivathsanvenkateswaran/sirdar/internal/run"
	"github.com/srivathsanvenkateswaran/sirdar/internal/source"
	"github.com/srivathsanvenkateswaran/sirdar/internal/ticket"
)

// The synthetic ticket: opened at 09:00, picked up at 10:00, fixed and
// confirmed after that. A retrospective bundle must end at the pickup.
var (
	retroOpened = time.Date(2026, 3, 2, 9, 0, 0, 0, time.UTC)
	retroPickup = retroOpened.Add(time.Hour)
	retroPRMade = retroOpened.Add(90 * time.Minute)
)

const retroPRURL = "https://github.com/acme/omni/pull/482"

// retroTracker is an adapter with no status history at all, which is what
// most of the stdio adapters are.
type retroTracker struct{}

func (retroTracker) Get(ctx context.Context, key string) (ticket.TrackerTicket, error) {
	return ticket.TrackerTicket{
		Key:         key,
		Title:       "Export times out",
		Description: "CSV export times out over 500 rows. Fixed by " + retroPRURL + ".",
		HelpdeskRef: "555",
		Fields:      map[string]string{"prs": retroPRURL, "epic": "OMNI-100"},
	}, nil
}

func (retroTracker) List(ctx context.Context, f source.ListFilter) ([]ticket.TrackerTicket, error) {
	return nil, nil
}

// historyTracker is the same adapter with a changelog, so it also
// implements source.Transitioner.
type historyTracker struct {
	retroTracker
	transitions []source.Transition
}

func (h historyTracker) Transitions(ctx context.Context, key string) ([]source.Transition, error) {
	return h.transitions, nil
}

type retroHelpdesk struct{}

func (retroHelpdesk) Get(ctx context.Context, id string) (ticket.HelpdeskTicket, error) {
	return ticket.HelpdeskTicket{ID: id, Subject: "Export never finishes"}, nil
}

func (retroHelpdesk) Threads(ctx context.Context, id string) (ticket.Thread, error) {
	return ticket.Thread{
		{At: retroOpened, Author: "Customer", Role: ticket.RoleCustomer, Text: "Export never finishes."},
		{At: retroPickup.Add(time.Hour), Author: "L2", Role: ticket.RoleAgent, Text: "Shipped in PR #482: " + retroPRURL},
		{At: retroPickup.Add(2 * time.Hour), Author: "Customer", Role: ticket.RoleCustomer, Text: "Confirmed."},
	}, nil
}

func (retroHelpdesk) Attachments(ctx context.Context, id, dir string) ([]ticket.Attachment, error) {
	return nil, nil
}

// fakeGH puts a gh on PATH that answers the three calls AddRetro makes, and
// nothing else. body is the script's whole behaviour, so a test can make it
// refuse `auth status` or disappear entirely.
func fakeGH(t *testing.T, body string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake gh is a shell script")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "gh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
}

// ghScript answers `auth status`, `pr view` and `pr diff` the way a logged-in
// gh does for one merged pull request.
const ghScript = `
if [ "$1" = "auth" ]; then exit 0; fi
if [ "$1" = "pr" ] && [ "$2" = "view" ]; then
  echo '{"createdAt":"2026-03-02T10:30:00Z","baseRefOid":"deadbeefcafef00d1234567890abcdef12345678","mergeCommit":{"oid":"0123456789abcdef0123456789abcdef01234567"},"files":[{"path":"internal/export/csv.go"},{"path":"internal/export/csv_test.go"}]}'
  exit 0
fi
if [ "$1" = "pr" ] && [ "$2" = "diff" ]; then
  echo "diff --git a/internal/export/csv.go b/internal/export/csv.go"
  exit 0
fi
echo "unexpected: $*" >&2
exit 1
`

func retroFetcher(tracker source.Tracker) *runner.Fetcher {
	return &runner.Fetcher{Config: &config.Config{}, Tracker: tracker, Helpdesk: retroHelpdesk{}}
}

// A retrospective entry is the ticket as the engineer found it plus the
// ground truth for scoring: four files, and every field of retro.json
// filled in from the pull request and the cutoff.
func TestAddRetroWritesTheEntryAndItsGroundTruth(t *testing.T) {
	fakeGH(t, ghScript)
	golden := t.TempDir()

	added, err := AddRetro(context.Background(), retroFetcher(retroTracker{}), "OMNI-7", RetroOptions{
		GoldenDir: golden,
		PRURLs:    []string{retroPRURL},
	})
	if err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(filepath.Join(golden, "OMNI-7", "retro.json"))
	if err != nil {
		t.Fatal(err)
	}
	var got Retro
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	want := Retro{
		Key:        "OMNI-7",
		AsOf:       retroPRMade,
		BaseCommit: "deadbeefcafef00d1234567890abcdef12345678",
		PRURLs:     []string{retroPRURL},
		PRDiff:     "pr.diff",
		PRFiles:    []string{"internal/export/csv.go", "internal/export/csv_test.go"},
		// description 1 + prs field 1; the thread's two references are on
		// a message the cutoff dropped before it could be scanned.
		Redacted: Redacted{PRLinks: 2, CommentsDropped: 2, AttachmentsDropped: 0},
	}
	if !got.AsOf.Equal(want.AsOf) {
		t.Errorf("asOf = %s, want %s", got.AsOf.Format(time.RFC3339), want.AsOf.Format(time.RFC3339))
	}
	got.AsOf, want.AsOf = time.Time{}, time.Time{}
	if diff := jsonOf(t, got); diff != jsonOf(t, want) {
		t.Errorf("retro.json =\n%s\nwant\n%s", diff, jsonOf(t, want))
	}
	// The derived cutoff is the pull request's created_at, and the entry
	// says so, because an operator who disagrees needs to know why.
	if !strings.Contains(added.AsOfSource, "created_at") {
		t.Errorf("AsOfSource = %q", added.AsOfSource)
	}

	if body, err := os.ReadFile(filepath.Join(golden, "OMNI-7", "pr.diff")); err != nil {
		t.Fatal(err)
	} else if !strings.Contains(string(body), "diff --git") || !strings.Contains(string(body), retroPRURL) {
		t.Errorf("pr.diff = %q", body)
	}
	if body, err := os.ReadFile(filepath.Join(golden, "OMNI-7", "expected.json")); err != nil {
		t.Fatal(err)
	} else if strings.TrimSpace(string(body)) != "{}" {
		t.Errorf("expected.json = %q, want an empty object", body)
	}

	// And the bundle itself holds none of the answer.
	thread, err := os.ReadFile(filepath.Join(added.BundleDir, "thread.md"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(thread), "482") || strings.Contains(string(thread), "Confirmed") {
		t.Errorf("thread.md carries the fix:\n%s", thread)
	}
	ticketJSON, err := os.ReadFile(filepath.Join(added.BundleDir, "ticket.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(ticketJSON), "github.com") {
		t.Errorf("ticket.json still names the pull request:\n%s", ticketJSON)
	}
	if !strings.Contains(string(ticketJSON), ticket.RedactionMarker) {
		t.Errorf("ticket.json carries no redaction marker:\n%s", ticketJSON)
	}
	if _, err := os.Stat(filepath.Join(added.BundleDir, "manifest.json")); err != nil {
		t.Errorf("the bundle has no manifest: %v", err)
	}
}

// A tracker that reports when the engineer picked the ticket up beats the
// pull request's created_at, because it is the earlier of the two and the
// hour between them is an hour of triage the session should not be handed.
func TestAddRetroPrefersTheEarlierInProgressTransition(t *testing.T) {
	fakeGH(t, ghScript)
	golden := t.TempDir()

	tracker := historyTracker{transitions: []source.Transition{
		{At: retroOpened, From: "", To: "Open"},
		{At: retroPickup, From: "Open", To: "In Progress"},
		{At: retroPickup.Add(3 * time.Hour), From: "In Progress", To: "Done"},
	}}
	added, err := AddRetro(context.Background(), retroFetcher(tracker), "OMNI-7", RetroOptions{
		GoldenDir: golden,
		PRURLs:    []string{retroPRURL},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !added.Retro.AsOf.Equal(retroPickup) {
		t.Errorf("asOf = %s, want the in_progress transition at %s",
			added.Retro.AsOf.Format(time.RFC3339), retroPickup.Format(time.RFC3339))
	}
	if !strings.Contains(added.AsOfSource, "in_progress") {
		t.Errorf("AsOfSource = %q", added.AsOfSource)
	}
}

// An explicit --as-of wins over both, whichever direction it moves the
// cutoff in.
func TestAddRetroTakesAnExplicitAsOf(t *testing.T) {
	fakeGH(t, ghScript)
	golden := t.TempDir()

	want := retroOpened.Add(15 * time.Minute)
	added, err := AddRetro(context.Background(), retroFetcher(retroTracker{}), "OMNI-7", RetroOptions{
		GoldenDir: golden,
		PRURLs:    []string{retroPRURL},
		AsOf:      want,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !added.Retro.AsOf.Equal(want) || added.AsOfSource != "--as-of" {
		t.Fatalf("asOf = %s (%s)", added.Retro.AsOf.Format(time.RFC3339), added.AsOfSource)
	}
}

// Two pull requests: the base commit is the earlier one's, and the changed
// files are the union.
func TestAddRetroTakesTheEarliestPullRequestsBase(t *testing.T) {
	fakeGH(t, `
if [ "$1" = "auth" ]; then exit 0; fi
if [ "$1" = "pr" ] && [ "$2" = "view" ]; then
  case "$3" in
    *"/pull/482") echo '{"createdAt":"2026-03-02T10:30:00Z","baseRefOid":"aaa1111111111111111111111111111111111111","mergeCommit":{"oid":"c1"},"files":[{"path":"a.go"}]}' ;;
    *"/pull/489") echo '{"createdAt":"2026-03-01T08:00:00Z","baseRefOid":"bbb2222222222222222222222222222222222222","mergeCommit":{"oid":"c2"},"files":[{"path":"b.go"},{"path":"a.go"}]}' ;;
  esac
  exit 0
fi
if [ "$1" = "pr" ] && [ "$2" = "diff" ]; then echo "diff --git a/x b/x"; exit 0; fi
exit 1
`)
	golden := t.TempDir()
	urls := []string{retroPRURL, "https://github.com/acme/omni/pull/489"}

	added, err := AddRetro(context.Background(), retroFetcher(retroTracker{}), "OMNI-7", RetroOptions{
		GoldenDir: golden,
		PRURLs:    urls,
	})
	if err != nil {
		t.Fatal(err)
	}
	if added.Retro.BaseCommit != "bbb2222222222222222222222222222222222222" {
		t.Errorf("baseCommit = %q, want the earlier pull request's base", added.Retro.BaseCommit)
	}
	if !added.Retro.AsOf.Equal(time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC)) {
		t.Errorf("asOf = %s, want the earlier created_at", added.Retro.AsOf.Format(time.RFC3339))
	}
	if strings.Join(added.Retro.PRFiles, ",") != "a.go,b.go" {
		t.Errorf("prFiles = %v, want the deduplicated union", added.Retro.PRFiles)
	}
	// Both diffs are there, each named by the pull request it came from.
	body, err := os.ReadFile(added.DiffPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range urls {
		if !strings.Contains(string(body), u) {
			t.Errorf("pr.diff does not name %s:\n%s", u, body)
		}
	}
}

// gh missing and gh logged out are both the operator's to fix, and both are
// said plainly rather than surfacing as an empty diff.
func TestAddRetroFailsClearlyWithoutAUsableGH(t *testing.T) {
	for _, tc := range []struct {
		name, script, want string
	}{
		{name: "gh is not installed", script: "", want: "not on PATH"},
		{name: "gh is logged out", script: "echo 'You are not logged into any GitHub hosts.' >&2; exit 1\n", want: "not authenticated"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.script == "" {
				if runtime.GOOS == "windows" {
					t.Skip("the fake gh is a shell script")
				}
				t.Setenv("PATH", t.TempDir())
			} else {
				fakeGH(t, tc.script)
			}
			golden := t.TempDir()

			_, err := AddRetro(context.Background(), retroFetcher(retroTracker{}), "OMNI-7", RetroOptions{
				GoldenDir: golden,
				PRURLs:    []string{retroPRURL},
			})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to mention %q", err, tc.want)
			}
			if _, statErr := os.Stat(filepath.Join(golden, "OMNI-7")); !os.IsNotExist(statErr) {
				t.Errorf("a failed add left a golden entry behind: %v", statErr)
			}
		})
	}
}

// The pull request goes to gh as an argument, so anything that is not a URL
// is refused rather than handed over to be read as a flag or as a pull
// request in whatever repository the working directory happens to be.
func TestAddRetroRefusesAnArgumentThatIsNotAURL(t *testing.T) {
	fakeGH(t, ghScript)
	for _, bad := range []string{"482", "--repo=acme/omni", "-R"} {
		_, err := AddRetro(context.Background(), retroFetcher(retroTracker{}), "OMNI-7", RetroOptions{
			GoldenDir: t.TempDir(),
			PRURLs:    []string{bad},
		})
		if err == nil || !strings.Contains(err.Error(), "not a pull request URL") {
			t.Errorf("--pr %q: err = %v", bad, err)
		}
	}
}

// --retro without a pull request has no ground truth and no cutoff, and is
// refused before anything is read.
func TestAddRetroNeedsAPullRequest(t *testing.T) {
	_, err := AddRetro(context.Background(), retroFetcher(retroTracker{}), "OMNI-7", RetroOptions{GoldenDir: t.TempDir()})
	if err == nil || !strings.Contains(err.Error(), "at least one --pr") {
		t.Fatalf("err = %v", err)
	}
}

func jsonOf(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
