package app

import (
	"context"
	"strings"
	"testing"

	"github.com/srivathsanvenkateswaran/sirdar/internal/config"
	"github.com/srivathsanvenkateswaran/sirdar/internal/provider"
)

// present builds a PATH lookup that finds exactly the named programs.
func present(names ...string) func(string) bool {
	set := map[string]bool{}
	for _, n := range names {
		set[n] = true
	}
	return func(name string) bool { return set[name] }
}

func TestPlatformCheckLinuxFullyEquipped(t *testing.T) {
	c := platformCheckFor("linux", present("xdg-open", "secret-tool"))

	if !c.OK || c.Level == string(provider.LevelWarn) {
		t.Fatalf("a Linux box with everything installed warned: %+v", c)
	}
	for _, want := range []string{"linux", "xdg-open opens", "secret-tool", "WebKitGTK 4.1"} {
		if !strings.Contains(c.Detail, want) {
			t.Errorf("detail %q does not mention %q", c.Detail, want)
		}
	}
}

func TestPlatformCheckWarnsWhenXdgOpenIsMissing(t *testing.T) {
	c := platformCheckFor("linux", present("secret-tool"))

	if c.Level != string(provider.LevelWarn) {
		t.Fatalf("level = %q, want a warning when xdg-open is absent: %+v", c.Level, c)
	}
	if !strings.Contains(c.Detail, "xdg-utils") {
		t.Errorf("detail %q does not say which package supplies xdg-open", c.Detail)
	}
}

func TestPlatformCheckNamesPassWhenSecretToolIsAbsent(t *testing.T) {
	c := platformCheckFor("linux", present("xdg-open", "pass"))

	if c.Level == string(provider.LevelWarn) {
		t.Errorf("a working pass(1) store warned: %+v", c)
	}
	if !strings.Contains(c.Detail, "pass(1)") {
		t.Errorf("detail %q does not name pass(1)", c.Detail)
	}
}

func TestPlatformCheckExplainsAMissingSecretStoreWithoutWarning(t *testing.T) {
	c := platformCheckFor("linux", present("xdg-open"))

	// A workspace that actually uses a keychain: ref fails in its own
	// sources row. This row explains why, and must not raise a second
	// alarm on a workspace that uses env: or file: refs throughout.
	if c.Level == string(provider.LevelWarn) {
		t.Errorf("a missing credential helper warned on its own: %+v", c)
	}
	for _, want := range []string{"libsecret-tools", "env:, file: or cmd:"} {
		if !strings.Contains(c.Detail, want) {
			t.Errorf("detail %q does not mention %q", c.Detail, want)
		}
	}
}

func TestPlatformCheckDarwinAndWindowsNeedNothingInstalled(t *testing.T) {
	for _, tc := range []struct{ goos, opener, store string }{
		{"darwin", "open", "login keychain"},
		{"windows", "rundll32", "Credential Manager"},
	} {
		c := platformCheckFor(tc.goos, present(tc.opener))
		if c.Level == string(provider.LevelWarn) {
			t.Errorf("%s warned: %+v", tc.goos, c)
		}
		if !strings.Contains(c.Detail, tc.store) {
			t.Errorf("%s: detail %q does not name %q", tc.goos, c.Detail, tc.store)
		}
		if strings.Contains(c.Detail, "WebKitGTK") {
			t.Errorf("%s: detail %q mentions a Linux-only runtime", tc.goos, c.Detail)
		}
	}
}

func TestPlatformCheckWarnsWherePlatformHasNoOpener(t *testing.T) {
	c := platformCheckFor("plan9", present())

	if c.Level != string(provider.LevelWarn) {
		t.Fatalf("level = %q, want a warning on a platform with no opener: %+v", c.Level, c)
	}
	if !strings.Contains(c.Detail, "env:, file: or cmd:") {
		t.Errorf("detail %q does not say what to use instead of keychain:", c.Detail)
	}
}

// The doctor report is what an operator reads first, so the row has to be
// in it, not merely available.
func TestRunDoctorIncludesThePlatformRow(t *testing.T) {
	cfg, err := config.Load(newWorkspace(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range RunDoctor(context.Background(), cfg) {
		if c.Name == "platform" {
			if c.Level == "" {
				t.Errorf("the platform row carries no level: %+v", c)
			}
			return
		}
	}
	t.Fatal("RunDoctor reported no platform row")
}
