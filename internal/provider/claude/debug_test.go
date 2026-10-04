package claude

import (
	"bytes"
	"testing"
)

func TestDebugCommand(t *testing.T) {
	var b bytes.Buffer
	debugCommand(&b, "", "/w", "claude", []string{"-p"})
	debugCommand(&b, "0", "/w", "claude", []string{"-p"})
	if b.Len() != 0 {
		t.Fatalf("SIRDAR_DEBUG unset or 0 writes nothing: %q", b.String())
	}
	debugCommand(&b, "1", "/a dir", "claude", []string{"-p", "--json-schema", `{"a":"it's"}`, "--max-turns", "6"})
	want := `sirdar debug: (cd '/a dir' && claude -p --json-schema '{"a":"it'\''s"}' --max-turns 6)` + "\n"
	if b.String() != want {
		t.Fatalf("got  %q\nwant %q", b.String(), want)
	}
}
