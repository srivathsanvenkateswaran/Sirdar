package provider

import (
	"encoding/json"
	"testing"
)

// TestFixPolicyAllowsTheEditingTools is the whole difference between the
// two modes: a fix may edit the workspace, a triage may not, and both are
// judged by the same policy object.
func TestFixPolicyAllowsTheEditingTools(t *testing.T) {
	fix := FixPolicy("/work", []string{"git *", "go test*"}, nil)
	triage := &PermissionPolicy{BashAllow: []string{"git log*"}, Root: "/work"}

	for _, tool := range []string{"Edit", "Write", "MultiEdit", "write_file", "edit_file"} {
		if d := fix.Decide(tool, nil); !d.Allow {
			t.Errorf("fix policy denied %s: %s", tool, d.Message)
		}
		if d := triage.Decide(tool, nil); d.Allow {
			t.Errorf("triage policy allowed %s", tool)
		}
	}
}

// TestFixPolicyStillRefusesEverythingElse guards the half of the rule that
// is easy to lose: "write-enabled" is two named tools wider than read-only,
// not open season.
func TestFixPolicyStillRefusesEverythingElse(t *testing.T) {
	fix := FixPolicy("/work", []string{"git *"}, nil)

	if d := fix.Decide("NotebookEdit", nil); d.Allow {
		t.Error("fix policy allowed NotebookEdit; nothing in the flow edits a notebook")
	}
	if d := fix.Decide("SomeNewTool", nil); d.Allow {
		t.Error("fix policy allowed an unknown tool")
	}
	if d := fix.Decide("Read", nil); !d.Allow {
		t.Errorf("fix policy denied Read: %s", d.Message)
	}
}

// TestFixPolicyUsesItsOwnBashList: a fix's shell allow-list is
// permissions.fixBash, so widening it for builds does not widen what a
// triage run may do.
func TestFixPolicyUsesItsOwnBashList(t *testing.T) {
	fix := FixPolicy("/work", []string{"go test*", "git *"}, nil)

	bash := func(cmd string) Decision {
		in, _ := json.Marshal(map[string]string{"command": cmd})
		return fix.Decide("Bash", in)
	}
	if d := bash("go test ./..."); !d.Allow {
		t.Errorf("fix policy denied an allow-listed build command: %s", d.Message)
	}
	if d := bash("rm -rf /"); d.Allow {
		t.Error("fix policy allowed a command outside its list")
	}
	if d := bash("git show > /tmp/out"); d.Allow {
		t.Error("fix policy allowed a redirection it cannot see through")
	}
}

func TestModeIsFix(t *testing.T) {
	if !ModeFix.IsFix() {
		t.Error("ModeFix.IsFix() is false")
	}
	for _, m := range []Mode{"", ModeTriage} {
		if m.IsFix() {
			t.Errorf("%q.IsFix() is true; the zero value must mean triage", m)
		}
	}
	var nilPolicy *PermissionPolicy
	if nilPolicy.IsFix() {
		t.Error("a nil policy reported itself as a fix policy")
	}
}
