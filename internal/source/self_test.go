package source

import "testing"

func TestResolvesSelfNamesTheAdaptersThatKnowTheirOwnAccount(t *testing.T) {
	for _, adapter := range []string{"jira", "linear", "azdo", "rally", "servicenow"} {
		if !ResolvesSelf(adapter) {
			t.Errorf("ResolvesSelf(%q) = false: that adapter resolves the filter itself", adapter)
		}
	}
	// exec is the one that matters: an external adapter is handed the
	// filter verbatim and has no account of its own to resolve it against.
	for _, adapter := range []string{"exec", "", "zohodesk", "unknown"} {
		if ResolvesSelf(adapter) {
			t.Errorf("ResolvesSelf(%q) = true", adapter)
		}
	}
	if !ResolvesSelf("  jira  ") {
		t.Error("a name with stray space around it was not recognised")
	}
}

func TestIsSelf(t *testing.T) {
	for _, s := range []string{"me", "ME", " me "} {
		if !IsSelf(s) {
			t.Errorf("IsSelf(%q) = false", s)
		}
	}
	for _, s := range []string{"", "mel", "sri@acme.com", "me@acme.com"} {
		if IsSelf(s) {
			t.Errorf("IsSelf(%q) = true", s)
		}
	}
}
