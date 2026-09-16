package config

import (
	"fmt"
	"os/exec"
	"strings"
	"sync"
)

// MeConfig is the top-level `me:` block: who the person running Sirdar is,
// in every spelling the systems they read write them as.
//
// A tracker writes an address, a helpdesk writes a display name, a git
// remote writes a third thing, and none of them agree. Email is the one
// address; Names are the display names a ticket may carry ("Srivathsan V");
// Aliases are the usernames ("sriv", "svenkat"). All three are compared
// case-insensitively, so they are written the way they read rather than the
// way a machine would fold them.
type MeConfig struct {
	Email   string   `yaml:"email,omitempty"`
	Names   []string `yaml:"names,omitempty"`
	Aliases []string `yaml:"aliases,omitempty"`
}

// Empty reports whether the block names nobody.
func (m MeConfig) Empty() bool {
	return trimmed(m.Email) == "" && len(nonEmpty(m.Names)) == 0 && len(nonEmpty(m.Aliases)) == 0
}

// validateMe checks the `me:` block. Only the address is checked, and
// only for the one mistake that makes the whole block do nothing: a name
// written where the address goes. A name with a space or a username is
// what Names and Aliases are for, and a tracker that writes accounts that
// way still matches through them.
func validateMe(m *MeConfig) error {
	email := trimmed(m.Email)
	if email == "" {
		return nil
	}
	if !strings.Contains(email, "@") || strings.ContainsAny(email, " \t") {
		return fmt.Errorf("config: me.email: must be an email address, got %q; display names go in me.names and usernames in me.aliases", m.Email)
	}
	return nil
}

// Identity sources, in the order Self resolves them. They cross to the UI
// on ConfigSummary.me.source and read out in the doctor row, so they are
// the words an operator sees rather than internal names.
const (
	// IdentityFromMe is the `me:` block.
	IdentityFromMe = "me"
	// IdentityFromWebhooks is webhooks.match.assignee — an address
	// written out, or "me" resolved against the source credentials.
	IdentityFromWebhooks = "webhooks"
	// IdentityFromSources is the account email on sources.tracker or
	// sources.helpdesk, for a workspace that configures no match block.
	IdentityFromSources = "sources"
	// IdentityFromGit is the workspace repository's own git config.
	IdentityFromGit = "git"
)

// Identity is who the workspace's own work belongs to, in every spelling
// something might write it as, and where that was read from.
//
// Names holds the display names and the usernames together: nothing that
// matches cares which of the two a spelling came from, and keeping them
// apart past the read would only make every caller join them again.
type Identity struct {
	Email  string   `json:"email"`
	Names  []string `json:"names,omitempty"`
	Source string   `json:"source,omitempty"`
}

// Empty reports whether the identity names nobody. An empty identity
// matches nothing rather than everything: a workspace that cannot say who
// the reader is should show no run as theirs, not every run.
func (i Identity) Empty() bool { return i.Email == "" && len(i.Names) == 0 }

// Display is the identity in one line: the address when there is one, else
// the first name. It is what a status line, a doctor row and an empty-state
// sentence say.
func (i Identity) Display() string {
	if i.Email != "" {
		return i.Email
	}
	if len(i.Names) > 0 {
		return i.Names[0]
	}
	return ""
}

// Matches reports whether a spelling a tracker or helpdesk wrote names this
// person. Every rule is in SameSpelling; this tries them against the
// address and each name in turn.
func (i Identity) Matches(spelling string) bool {
	if i.Empty() {
		return false
	}
	if SameSpelling(i.Email, spelling) {
		return true
	}
	for _, n := range i.Names {
		if SameSpelling(n, spelling) {
			return true
		}
	}
	return false
}

// SameSpelling reports whether two spellings name one person.
//
// Case never matters, and neither does space a form let through: both
// sides are trimmed and their internal runs of space collapsed first. Two
// addresses have to agree in full — sri@acme.com is not sri@other.com — but
// a bare name matches the address it is the local part of, which is what a
// tracker naming accounts "sri" and a config that knows sri@acme.com need
// to agree on. Between two bare spellings, and between a bare spelling and
// an address's local part, dots and underscores read as spaces, so
// "srivathsan.v" is "Srivathsan V".
func SameSpelling(a, b string) bool {
	fa, fb := fold(a), fold(b)
	if fa == "" || fb == "" {
		return false
	}
	if fa == fb {
		return true
	}
	aAddr, bAddr := strings.Contains(fa, "@"), strings.Contains(fb, "@")
	if aAddr && bAddr {
		// Two addresses are two accounts unless they are the same one.
		return false
	}
	if aAddr {
		fa = localPart(fa)
	}
	if bAddr {
		fb = localPart(fb)
	}
	return fa == fb || spaced(fa) == spaced(fb)
}

// Self is who this workspace's own work belongs to.
//
// The `me:` block first, because it is the one place an operator writes
// this down on purpose. Then webhooks.match.assignee, which is the same
// question asked for the hook filter — an address written out, or "me"
// resolved against the source credentials — and, for a workspace with no
// match block at all, those credentials on their own. Last the repository's
// own `git config user.email`/`user.name`, which is what makes a workspace
// that configures none of the above still know the reader: they are the
// person committing to it. Then nobody.
func (c *Config) Self() Identity {
	if c == nil {
		return Identity{}
	}
	if id := c.selfFromMe(); !id.Empty() {
		return id
	}
	if id := c.selfFromWebhooks(); !id.Empty() {
		return id
	}
	return c.gitIdentity()
}

func (c *Config) selfFromMe() Identity {
	if c.Me.Empty() {
		return Identity{}
	}
	names := append(nonEmpty(c.Me.Names), nonEmpty(c.Me.Aliases)...)
	return Identity{Email: trimmed(c.Me.Email), Names: names, Source: IdentityFromMe}
}

// selfFromWebhooks reads the hook filter's own answer. MatchAssignee
// resolves "me" against the source credentials and errors when nothing
// says who that is; an error here is simply nobody, since the caller is a
// board and not the hook route.
func (c *Config) selfFromWebhooks() Identity {
	if a, err := c.MatchAssignee(); err == nil {
		if a = trimmed(a); a != "" {
			return identityOfSpelling(a, IdentityFromWebhooks)
		}
	}
	// No match block at all, and the credentials still name an account:
	// that is who "me" would have resolved to, so it is who the reader
	// is. There are no webhooks to credit it to.
	if self := trimmed(c.SelfIdentity()); self != "" {
		return identityOfSpelling(self, IdentityFromSources)
	}
	return Identity{}
}

// identityOfSpelling reads one written-out spelling as an identity: an
// address is the address, anything else is a name.
func identityOfSpelling(s, source string) Identity {
	if strings.Contains(s, "@") {
		return Identity{Email: s, Source: source}
	}
	return Identity{Names: []string{s}, Source: source}
}

// gitCache holds the one `git config` read a loaded configuration makes.
// It is a pointer on Config so that copying a Config copies no lock, and
// it is nil on a Config built by hand, which then reads git each time —
// there is no load to be cached per.
type gitCache struct {
	once sync.Once
	id   Identity
}

// gitIdentity is the workspace repository's own committer, read once per
// loaded configuration. A directory that is not a repository, a git that is
// not installed and a repository with no identity configured are all
// nobody, not an error: this is the last fallback, and its failure is the
// empty identity the caller already handles.
//
// It only ever reads. Sirdar never writes a git identity anywhere.
func (c *Config) gitIdentity() Identity {
	if c.Root == "" {
		return Identity{}
	}
	if c.git == nil {
		return readGitIdentity(c.Root)
	}
	c.git.once.Do(func() { c.git.id = readGitIdentity(c.Root) })
	return c.git.id
}

// gitConfigValue is the reader, swapped out in tests.
var gitConfigValue = func(root, key string) string {
	cmd := exec.Command("git", "-C", root, "config", "--get", key)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return trimmed(string(out))
}

func readGitIdentity(root string) Identity {
	email := gitConfigValue(root, "user.email")
	name := gitConfigValue(root, "user.name")
	if email == "" && name == "" {
		return Identity{}
	}
	id := Identity{Email: email, Source: IdentityFromGit}
	if name != "" {
		id.Names = []string{name}
	}
	return id
}

// fold is a spelling compared: trimmed, lower-cased, and with every run of
// whitespace inside it collapsed to one space.
func fold(s string) string { return strings.ToLower(strings.Join(strings.Fields(s), " ")) }

// spaced is fold with dots and underscores read as spaces, which is how an
// address's local part spells a display name: "srivathsan.v" is
// "srivathsan v".
func spaced(s string) string {
	return fold(strings.Map(func(r rune) rune {
		if r == '.' || r == '_' {
			return ' '
		}
		return r
	}, s))
}

// localPart is everything before the "@" of an address.
func localPart(addr string) string {
	if i := strings.Index(addr, "@"); i > 0 {
		return addr[:i]
	}
	return addr
}

func trimmed(s string) string { return strings.TrimSpace(s) }

// nonEmpty trims each entry and drops the ones that were only space.
func nonEmpty(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s = trimmed(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}
