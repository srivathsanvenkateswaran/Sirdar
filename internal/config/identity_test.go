package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// stubGit makes the git fallback answer with what the test says, and puts
// the real reader back afterwards.
func stubGit(t *testing.T, email, name string) {
	t.Helper()
	was := gitConfigValue
	gitConfigValue = func(_, key string) string {
		switch key {
		case "user.email":
			return email
		case "user.name":
			return name
		}
		return ""
	}
	t.Cleanup(func() { gitConfigValue = was })
}

func TestSelfResolvesInOrder(t *testing.T) {
	// Every case has a git identity to fall through to, so the order is
	// what each assertion is about rather than what happened to be set.
	stubGit(t, "git@acme.com", "Git Committer")

	cases := []struct {
		name string
		cfg  func() *Config
		want Identity
	}{
		{
			name: "the me block wins over everything",
			cfg: func() *Config {
				c := &Config{Root: "/ws"}
				c.Me = MeConfig{Email: "sri@acme.com", Names: []string{"Srivathsan V"}, Aliases: []string{"sriv"}}
				c.Webhooks.Match.Assignee = "rana@acme.com"
				return c
			},
			want: Identity{Email: "sri@acme.com", Names: []string{"Srivathsan V", "sriv"}, Source: IdentityFromMe},
		},
		{
			name: "a me block of names alone still names somebody",
			cfg: func() *Config {
				c := &Config{Root: "/ws"}
				c.Me = MeConfig{Names: []string{" Srivathsan V ", "  "}}
				return c
			},
			want: Identity{Names: []string{"Srivathsan V"}, Source: IdentityFromMe},
		},
		{
			name: "an assignee written out comes next",
			cfg: func() *Config {
				c := &Config{Root: "/ws"}
				c.Webhooks.Match.Assignee = "rana@acme.com"
				return c
			},
			want: Identity{Email: "rana@acme.com", Source: IdentityFromWebhooks},
		},
		{
			name: "an assignee that is a name, not an address",
			cfg: func() *Config {
				c := &Config{Root: "/ws"}
				c.Webhooks.Match.Assignee = "Noura A"
				return c
			},
			want: Identity{Names: []string{"Noura A"}, Source: IdentityFromWebhooks},
		},
		{
			name: "assignee me resolves against the source credentials",
			cfg: func() *Config {
				c := &Config{Root: "/ws"}
				c.Sources.Tracker = &SourceConfig{Adapter: "jira", Email: "sri@acme.com"}
				c.Webhooks.Match.Assignee = SelfAssignee
				return c
			},
			want: Identity{Email: "sri@acme.com", Source: IdentityFromWebhooks},
		},
		{
			name: "no match block at all reads the source credentials",
			cfg: func() *Config {
				c := &Config{Root: "/ws"}
				c.Sources.Helpdesk = &SourceConfig{Adapter: "zendesk", Email: "sri@acme.com"}
				return c
			},
			want: Identity{Email: "sri@acme.com", Source: IdentityFromSources},
		},
		{
			name: "a workspace that configures none of it falls to git",
			cfg:  func() *Config { return &Config{Root: "/ws"} },
			want: Identity{Email: "git@acme.com", Names: []string{"Git Committer"}, Source: IdentityFromGit},
		},
		{
			name: "assignee me with credentials that name nobody falls to git",
			cfg: func() *Config {
				c := &Config{Root: "/ws"}
				c.Sources.Tracker = &SourceConfig{Adapter: "linear"}
				c.Webhooks.Match.Assignee = SelfAssignee
				return c
			},
			want: Identity{Email: "git@acme.com", Names: []string{"Git Committer"}, Source: IdentityFromGit},
		},
		{
			name: "a nil config names nobody",
			cfg:  func() *Config { return nil },
			want: Identity{},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.cfg().Self()
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("Self() = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestSelfIsEmptyWhenNothingNamesAnybody(t *testing.T) {
	stubGit(t, "", "")
	c := &Config{Root: "/ws"}
	if got := c.Self(); !got.Empty() {
		t.Fatalf("Self() = %+v, want nobody", got)
	}
	if got := c.Self().Display(); got != "" {
		t.Fatalf("Display() = %q, want empty", got)
	}
}

func TestSelfReadsGitOncePerLoadedConfig(t *testing.T) {
	reads := 0
	was := gitConfigValue
	gitConfigValue = func(_, key string) string {
		reads++
		if key == "user.email" {
			return "sri@acme.com"
		}
		return ""
	}
	t.Cleanup(func() { gitConfigValue = was })

	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".sirdar"), 0o755); err != nil {
		t.Fatal(err)
	}
	body := "workspace: demo\nprovider: claude\nnotes:\n  dir: notes\n"
	if err := os.WriteFile(filepath.Join(root, ".sirdar", "config.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		if got := cfg.Self().Email; got != "sri@acme.com" {
			t.Fatalf("Self().Email = %q", got)
		}
	}
	// Two keys, one read each: user.email and user.name.
	if reads != 2 {
		t.Fatalf("git was read %d times, want 2", reads)
	}
}

func TestLoadReadsTheMeBlock(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".sirdar"), 0o755); err != nil {
		t.Fatal(err)
	}
	body := "workspace: demo\nprovider: claude\nnotes:\n  dir: notes\n" +
		"me:\n  email: sri@acme.com\n  names:\n    - Srivathsan V\n  aliases:\n    - sriv\n"
	if err := os.WriteFile(filepath.Join(root, ".sirdar", "config.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	want := Identity{Email: "sri@acme.com", Names: []string{"Srivathsan V", "sriv"}, Source: IdentityFromMe}
	if got := cfg.Self(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Self() = %+v, want %+v", got, want)
	}
}

func TestValidateMeRefusesANameInTheEmailField(t *testing.T) {
	c := &Config{}
	c.Me = MeConfig{Email: "Srivathsan V"}
	if err := validateMe(&c.Me); err == nil {
		t.Fatal("a display name in me.email was accepted")
	}
	c.Me = MeConfig{Email: " sri@acme.com "}
	if err := validateMe(&c.Me); err != nil {
		t.Fatalf("validateMe: %v", err)
	}
	c.Me = MeConfig{Names: []string{"Srivathsan V"}}
	if err := validateMe(&c.Me); err != nil {
		t.Fatalf("validateMe with no address: %v", err)
	}
}

func TestSameSpelling(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		// Case and stray space never matter.
		{"sri@acme.com", "sri@acme.com", true},
		{"SRI@Acme.com", "sri@acme.com", true},
		{"  sri@acme.com  ", "sri@acme.com", true},
		{"Sri  Venkateswaran", "sri venkateswaran", true},
		// Two addresses have to agree in full.
		{"sri@acme.com", "sri@other.com", false},
		{"sri@acme.com", "rana@acme.com", false},
		// A bare name against the address it is the local part of.
		{"sri", "sri@acme.com", true},
		{"sri@acme.com", "SRI", true},
		{"rana", "sri@acme.com", false},
		// Dots and underscores in a local part read as spaces.
		{"Srivathsan V", "srivathsan.v@silq.net", true},
		{"srivathsan.v@silq.net", "srivathsan v", true},
		{"Srivathsan V", "srivathsan_v@silq.net", true},
		{"srivathsan.v", "Srivathsan V", true},
		{"Noura A", "srivathsan.v@silq.net", false},
		// Nobody matches nobody.
		{"", "sri@acme.com", false},
		{"sri@acme.com", "", false},
		{"", "", false},
		{"   ", "sri@acme.com", false},
	}
	for _, tc := range cases {
		if got := SameSpelling(tc.a, tc.b); got != tc.want {
			t.Errorf("SameSpelling(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestIdentityMatches(t *testing.T) {
	id := Identity{
		Email: "srivathsan.v@silq.net",
		Names: []string{"Srivathsan V", "sriv"},
	}
	yes := []string{
		"srivathsan.v@silq.net",
		"SRIVATHSAN.V@SILQ.NET",
		"srivathsan.v",
		"Srivathsan V",
		"  srivathsan   v  ",
		"sriv",
		"SRIV",
	}
	for _, s := range yes {
		if !id.Matches(s) {
			t.Errorf("Matches(%q) = false, want true", s)
		}
	}
	// A name matches any address whose local part spells it, domain and
	// all — the same rule that lets a bare "sri" match sri@acme.com.
	if !id.Matches("srivathsan.v@other.net") {
		t.Error(`Matches("srivathsan.v@other.net") = false, want true`)
	}
	no := []string{"", "  ", "noura@silq.net", "Noura A", "sri"}
	for _, s := range no {
		if id.Matches(s) {
			t.Errorf("Matches(%q) = true, want false", s)
		}
	}
	if (Identity{}).Matches("sri@acme.com") {
		t.Error("an empty identity matched somebody")
	}
}

func TestIdentityDisplayPrefersTheAddress(t *testing.T) {
	if got := (Identity{Email: "sri@acme.com", Names: []string{"Srivathsan V"}}).Display(); got != "sri@acme.com" {
		t.Fatalf("Display() = %q", got)
	}
	if got := (Identity{Names: []string{"Srivathsan V"}}).Display(); got != "Srivathsan V" {
		t.Fatalf("Display() = %q", got)
	}
}
