package app

import (
	"net/url"
	"path/filepath"
	"strings"

	"github.com/srivathsanvenkateswaran/sirdar/internal/config"
	"github.com/srivathsanvenkateswaran/sirdar/internal/source/linear"
	"github.com/srivathsanvenkateswaran/sirdar/internal/source/servicenow"
	"github.com/srivathsanvenkateswaran/sirdar/internal/store"
)

// SourcesSummary names the two ticket sources a workspace reads from, so a
// screen can draw the tracker's or the helpdesk's own mark beside a ticket
// number and say which product the number belongs to. A role the workspace
// has not configured is absent.
type SourcesSummary struct {
	Tracker  *SourceSummary `json:"tracker,omitempty"`
	Helpdesk *SourceSummary `json:"helpdesk,omitempty"`
	// QueueTypes is the tracker queue's effective type filter, already
	// resolved: the configured list, or the default when the workspace
	// names none. A screen reads it to say why a lane is empty — "no bug
	// tickets" rather than "nothing at all" — and never to apply the
	// filter, which the service has already done.
	QueueTypes []string `json:"queueTypes"`
}

// SourceSummary is one configured source as the UI names it.
//
// Adapter is the config value ("jira", "zohodesk", "exec"). Name is a
// display name: the adapter's product name, or for an exec adapter the
// first label of the ticket URL's host, title-cased — "Janus" for a
// tracker whose tickets live at janus.example.com — falling back to the
// command's own name without its "sirdar-" prefix when no run has recorded
// a ticket URL yet. Host is the host tickets are read from, and nothing
// more: no path, no credential, no port the operator did not write.
type SourceSummary struct {
	Adapter string `json:"adapter"`
	Name    string `json:"name"`
	Host    string `json:"host"`
}

// SourceHints is what the run directories can add to a config summary: the
// ticket URLs the newest bundle recorded, which is the only place an exec
// adapter's host is written down.
type SourceHints struct {
	TrackerURL  string
	HelpdeskURL string
}

// productNames is the display name of each built-in adapter.
var productNames = map[string]string{
	"jira":       "Jira",
	"linear":     "Linear",
	"azdo":       "Azure DevOps",
	"rally":      "Rally",
	"servicenow": "ServiceNow",
	"zohodesk":   "Zoho Desk",
	"zendesk":    "Zendesk",
	"freshdesk":  "Freshdesk",
	"helpscout":  "Help Scout",
	"intercom":   "Intercom",
	"hubspot":    "HubSpot",
	"front":      "Front",
	"gorgias":    "Gorgias",
}

func summariseSources(cfg *config.Config, hints SourceHints) SourcesSummary {
	return SourcesSummary{
		Tracker:    summariseSource(cfg.Sources.Tracker, hints.TrackerURL),
		Helpdesk:   summariseSource(cfg.Sources.Helpdesk, hints.HelpdeskURL),
		QueueTypes: cfg.QueueTypes(),
	}
}

// summariseSource is one source's summary, or nil when the role is not
// configured. ticketURL is the newest bundle's URL for the role, consulted
// only by the exec adapter, whose config names a command and no host.
func summariseSource(sc *config.SourceConfig, ticketURL string) *SourceSummary {
	if sc == nil {
		return nil
	}
	out := &SourceSummary{Adapter: sc.Adapter, Name: productNames[sc.Adapter], Host: sourceHost(sc)}
	if sc.Adapter == "exec" {
		out.Host = hostOf(ticketURL)
		out.Name = execName(out.Host, sc.Command)
	}
	return out
}

// sourceHost is where a built-in adapter reads its tickets from, derived the
// way the adapter itself derives it: an account identifier becomes the
// account's host, a fixed API becomes that API's host, and a configured
// baseUrl wins wherever the adapter accepts one.
func sourceHost(sc *config.SourceConfig) string {
	if h := hostOf(sc.BaseURL); h != "" {
		return h
	}
	switch sc.Adapter {
	case "linear":
		return hostOf(linear.DefaultEndpoint)
	case "azdo":
		return hostOf(sc.OrgURL)
	case "rally":
		return hostOf(config.RallyDefaultBaseURL)
	case "servicenow":
		base, _ := servicenow.ResolveBaseURL(sc.Instance, sc.BaseURL)
		return hostOf(base)
	case "zohodesk":
		return "desk.zoho.com"
	case "zendesk":
		if sc.Subdomain != "" {
			return strings.ToLower(sc.Subdomain) + ".zendesk.com"
		}
	case "freshdesk":
		return hostOf(sc.Domain)
	case "gorgias":
		if sc.Account != "" {
			return strings.ToLower(sc.Account) + ".gorgias.com"
		}
	case "helpscout":
		return "api.helpscout.net"
	case "intercom":
		return "api.intercom.io"
	case "hubspot":
		return "api.hubapi.com"
	case "front":
		return "api2.frontapp.com"
	}
	return ""
}

// hostOf is a URL's host, lower-cased, or "" for anything that is not one.
// A bare host with no scheme is accepted too, since that is how freshdesk's
// domain is written.
func hostOf(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return ""
	}
	return strings.ToLower(u.Hostname())
}

// execName names an exec adapter for a screen: the first label of the host
// its tickets live on, else the command's own name without the "sirdar-"
// prefix every plugin here carries. Title-cased either way, since it stands
// where "Jira" or "Zoho Desk" would.
func execName(host, command string) string {
	if label, _, _ := strings.Cut(host, "."); label != "" {
		return titleCase(label)
	}
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return ""
	}
	base := filepath.Base(fields[0])
	base = strings.TrimSuffix(base, filepath.Ext(base))
	base = strings.TrimPrefix(base, "sirdar-")
	return titleCase(base)
}

// titleCase upper-cases the first letter of each word, splitting on the
// separators a host label or a binary name can carry.
func titleCase(s string) string {
	words := strings.FieldsFunc(s, func(r rune) bool { return r == '-' || r == '_' || r == ' ' })
	for i, w := range words {
		words[i] = strings.ToUpper(w[:1]) + w[1:]
	}
	return strings.Join(words, " ")
}

// sourceHintsIn reads the ticket URLs off the newest bundles under root
// that recorded any, looking at no more than a few runs: an exec adapter's
// host is not in the config, and the bundle is where a run wrote it down.
func sourceHintsIn(root string) SourceHints {
	var hints SourceHints
	states, err := store.List(root, "")
	if err != nil {
		return hints
	}
	const lookBack = 20
	for i, st := range states {
		if i >= lookBack || (hints.TrackerURL != "" && hints.HelpdeskURL != "") {
			break
		}
		b := bundleAt(runDir(root, st.Key, st.RunID))
		if b == nil {
			continue
		}
		if hints.TrackerURL == "" && b.Tracker != nil {
			hints.TrackerURL = b.Tracker.URL
		}
		if hints.HelpdeskURL == "" && b.Helpdesk != nil {
			hints.HelpdeskURL = b.Helpdesk.URL
		}
	}
	return hints
}
