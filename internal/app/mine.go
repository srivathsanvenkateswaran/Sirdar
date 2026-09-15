package app

import (
	"strings"

	"github.com/srivathsanvenkateswaran/sirdar/internal/config"
)

// SelfOf is who a workspace's own work belongs to, in the spelling a
// ticket carries it: whatever `webhooks.match.assignee` names — "me"
// resolved the way the hook filter resolves it, or the address written out
// there — and, for a workspace that configures no match at all, the account
// email its tracker or helpdesk credentials belong to.
//
// It is the same resolver the hook filter and `queue --assignee me` use, so
// a run the board calls the reader's own is a run a hook would have started
// for them. It is empty for a workspace whose sources authenticate with a
// token that names nobody — a Linear API key, an Azure DevOps PAT — and an
// empty self makes every run not-mine rather than everyone's.
func SelfOf(cfg *config.Config) string {
	if cfg == nil {
		return ""
	}
	if a, err := cfg.MatchAssignee(); err == nil {
		if a = strings.TrimSpace(a); a != "" {
			return a
		}
	}
	return cfg.SelfIdentity()
}

// selfIn is SelfOf for the workspace rooted at root. A configuration
// that cannot be read names nobody rather than failing the read it is part
// of: a broken config.yaml should not empty the board.
func selfIn(root string) string {
	cfg, err := config.Load(root)
	if err != nil {
		return ""
	}
	return SelfOf(cfg)
}

// SameAssignee reports whether two spellings name one person.
//
// Case never matters: a tracker writes the address the way the account was
// created and a config the way somebody typed it. A bare local part matches
// the address it is the local part of, which is what a tracker naming
// accounts "sri" and a config that knows sri@acme.com need to agree on; two
// addresses, though, have to match in full, so sri@acme.com is not
// sri@other.com.
func SameAssignee(a, b string) bool {
	a, b = strings.TrimSpace(a), strings.TrimSpace(b)
	if a == "" || b == "" {
		return false
	}
	if strings.EqualFold(a, b) {
		return true
	}
	aAddr, bAddr := strings.Contains(a, "@"), strings.Contains(b, "@")
	if aAddr == bAddr {
		return false
	}
	if aAddr {
		return strings.EqualFold(localPart(a), b)
	}
	return strings.EqualFold(a, localPart(b))
}

// localPart is everything before the "@" of an address.
func localPart(addr string) string {
	if i := strings.Index(addr, "@"); i > 0 {
		return addr[:i]
	}
	return addr
}
