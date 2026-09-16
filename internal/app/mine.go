package app

import (
	"github.com/srivathsanvenkateswaran/sirdar/internal/config"
)

// IdentityOf is who a workspace's own work belongs to, in every spelling
// something might write it as: the `me:` block, else whatever
// `webhooks.match.assignee` names, else the account the tracker or helpdesk
// credentials belong to, else the workspace repository's own git committer.
// `config.Config.Self` is the resolver and the order is documented there.
//
// It is the same answer the hook filter and `queue --assignee me` reach, so
// a run the board calls the reader's own is a run a hook would have started
// for them. It is empty for a workspace whose sources authenticate with a
// token that names nobody and which is not a git repository — and an empty
// identity makes every run not-mine rather than everyone's.
func IdentityOf(cfg *config.Config) config.Identity { return cfg.Self() }

// SelfOf is IdentityOf in one line: the address, else the display name,
// else "". It is what a status line or an empty-state sentence says, and
// what a tracker filter is given for an adapter that cannot resolve "me".
func SelfOf(cfg *config.Config) string { return IdentityOf(cfg).Display() }

// selfIn is IdentityOf for the workspace rooted at root. A configuration
// that cannot be read names nobody rather than failing the read it is part
// of: a broken config.yaml should not empty the board.
func selfIn(root string) config.Identity {
	cfg, err := config.Load(root)
	if err != nil {
		return config.Identity{}
	}
	return IdentityOf(cfg)
}

// SameAssignee reports whether two spellings name one person. The rules are
// config.SameSpelling's; this is the name the rest of the app knows them by.
func SameAssignee(a, b string) bool { return config.SameSpelling(a, b) }
