package app

import (
	"testing"

	"github.com/srivathsanvenkateswaran/sirdar/internal/config"
	"github.com/srivathsanvenkateswaran/sirdar/internal/store"
)

// withTracker is a configuration whose tracker credentials belong to email.
func withTracker(email string) *config.Config {
	cfg := &config.Config{}
	cfg.Sources.Tracker = &config.SourceConfig{Adapter: "jira", Email: email}
	return cfg
}

func TestSelfOfResolvesTheSameAccountTheHookFilterDoes(t *testing.T) {
	cases := []struct {
		name string
		cfg  func() *config.Config
		want string
	}{
		{
			name: "me resolves to the tracker account",
			cfg: func() *config.Config {
				c := withTracker("sri@acme.com")
				c.Webhooks.Match.Assignee = config.SelfAssignee
				return c
			},
			want: "sri@acme.com",
		},
		{
			name: "an address written out is taken as written",
			cfg: func() *config.Config {
				c := withTracker("sri@acme.com")
				c.Webhooks.Match.Assignee = "rana@acme.com"
				return c
			},
			want: "rana@acme.com",
		},
		{
			name: "no match block falls back to the account email",
			cfg:  func() *config.Config { return withTracker("sri@acme.com") },
			want: "sri@acme.com",
		},
		{
			name: "the helpdesk account stands in when the tracker names nobody",
			cfg: func() *config.Config {
				c := &config.Config{}
				c.Sources.Tracker = &config.SourceConfig{Adapter: "linear"}
				c.Sources.Helpdesk = &config.SourceConfig{Adapter: "zendesk", Email: "sri@acme.com"}
				return c
			},
			want: "sri@acme.com",
		},
		{
			name: "a token that names nobody leaves self empty",
			cfg: func() *config.Config {
				c := &config.Config{}
				c.Sources.Tracker = &config.SourceConfig{Adapter: "linear"}
				c.Webhooks.Match.Assignee = config.SelfAssignee
				return c
			},
			want: "",
		},
		{
			name: "a nil config names nobody",
			cfg:  func() *config.Config { return nil },
			want: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := SelfOf(tc.cfg()); got != tc.want {
				t.Fatalf("SelfOf = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSameAssignee(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"sri@acme.com", "sri@acme.com", true},
		{"SRI@Acme.com", "sri@acme.com", true},
		{"  sri@acme.com  ", "sri@acme.com", true},
		// A tracker that names accounts by their local part still matches.
		{"sri", "sri@acme.com", true},
		{"sri@acme.com", "SRI", true},
		// Two addresses have to agree in full.
		{"sri@acme.com", "sri@other.com", false},
		{"sri@acme.com", "rana@acme.com", false},
		// Two bare names are compared as they stand.
		{"Sri Venkateswaran", "sri venkateswaran", true},
		{"sri", "rana", false},
		// Nobody matches nobody.
		{"", "sri@acme.com", false},
		{"sri@acme.com", "", false},
		{"", "", false},
	}
	for _, tc := range cases {
		if got := SameAssignee(tc.a, tc.b); got != tc.want {
			t.Errorf("SameAssignee(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestSummaryForReadsTheAssigneeOffTheBundle(t *testing.T) {
	state := store.State{RunID: "20260910T090000Z-aaaa", Key: "OMNI-1"}

	t.Run("the tracker's assignee wins", func(t *testing.T) {
		dir := fakeRunDir(t, `{"Tracker":{"Key":"OMNI-1","Title":"Export times out","Assignee":" sri@acme.com "},
			"Helpdesk":{"ID":"h1","Fields":{"assignee":"rana@acme.com"}}}`)
		got := SummaryFor(dir, state, "SRI@ACME.COM")
		if got.Assignee != "sri@acme.com" {
			t.Fatalf("Assignee = %q, want the tracker's", got.Assignee)
		}
		if !got.Mine {
			t.Fatal("Mine = false for the reader's own ticket")
		}
	})

	t.Run("the helpdesk's owner stands in", func(t *testing.T) {
		dir := fakeRunDir(t, `{"Helpdesk":{"ID":"h1","Subject":"Export","Fields":{"assignee":"rana@acme.com"}}}`)
		got := SummaryFor(dir, state, "sri@acme.com")
		if got.Assignee != "rana@acme.com" {
			t.Fatalf("Assignee = %q, want the helpdesk's", got.Assignee)
		}
		if got.Mine {
			t.Fatal("Mine = true for somebody else's ticket")
		}
	})

	t.Run("a bundle that names nobody draws no avatar", func(t *testing.T) {
		dir := fakeRunDir(t, `{"Tracker":{"Key":"OMNI-1","Title":"Export times out"}}`)
		got := SummaryFor(dir, state, "sri@acme.com")
		if got.Assignee != "" {
			t.Fatalf("Assignee = %q, want empty", got.Assignee)
		}
		if got.Mine {
			t.Fatal("Mine = true for a ticket assigned to nobody")
		}
	})

	t.Run("a run with no bundle at all", func(t *testing.T) {
		dir := fakeRunDir(t, "")
		got := SummaryFor(dir, state, "sri@acme.com")
		if got.Assignee != "" || got.Mine {
			t.Fatalf("Assignee = %q, Mine = %v, want empty and false", got.Assignee, got.Mine)
		}
	})

	t.Run("a workspace that knows no self owns nothing", func(t *testing.T) {
		dir := fakeRunDir(t, `{"Tracker":{"Key":"OMNI-1","Assignee":"sri@acme.com"}}`)
		got := SummaryAt(dir, state)
		if got.Assignee != "sri@acme.com" {
			t.Fatalf("Assignee = %q, want the tracker's", got.Assignee)
		}
		if got.Mine {
			t.Fatal("Mine = true with nobody to match against")
		}
	})
}
