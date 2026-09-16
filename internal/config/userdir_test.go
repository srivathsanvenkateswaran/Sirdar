package config

import (
	"os"
	"path/filepath"
	"testing"
)

// env builds a getenv for userDir from a map, so a case can name only the
// variables it cares about.
func env(pairs map[string]string) func(string) string {
	return func(name string) string { return pairs[name] }
}

func TestUserDirPrefersAnExistingDotSirdar(t *testing.T) {
	home := t.TempDir()
	if err := os.Mkdir(filepath.Join(home, userDirName), 0o755); err != nil {
		t.Fatal(err)
	}
	data := t.TempDir()

	// Even with XDG_DATA_HOME pointing somewhere else, the directory that
	// already holds the operator's registry is the one that answers.
	got := userDir("linux", home, env(map[string]string{"XDG_DATA_HOME": data}))
	if want := filepath.Join(home, userDirName); got != want {
		t.Fatalf("userDir = %q, want %q", got, want)
	}
}

func TestUserDirHonoursXDGDataHomeOnLinux(t *testing.T) {
	home := t.TempDir()
	data := t.TempDir()

	got := userDir("linux", home, env(map[string]string{"XDG_DATA_HOME": data}))
	if want := filepath.Join(data, "sirdar"); got != want {
		t.Fatalf("userDir = %q, want %q", got, want)
	}
}

func TestUserDirIgnoresARelativeXDGDataHome(t *testing.T) {
	home := t.TempDir()

	// The spec says a relative $XDG_DATA_HOME is invalid and must be
	// ignored, which is also the only safe reading: joining a relative
	// path would put the registry under whatever directory the app
	// happened to be launched from.
	got := userDir("linux", home, env(map[string]string{"XDG_DATA_HOME": "relative/data"}))
	if want := filepath.Join(home, userDirName); got != want {
		t.Fatalf("userDir = %q, want %q", got, want)
	}
}

func TestUserDirFallsBackToDotSirdarWithoutXDG(t *testing.T) {
	home := t.TempDir()

	for _, goos := range []string{"linux", "freebsd", "openbsd"} {
		got := userDir(goos, home, env(nil))
		if want := filepath.Join(home, userDirName); got != want {
			t.Fatalf("%s: userDir = %q, want %q", goos, got, want)
		}
	}
}

func TestUserDirIgnoresXDGOnDarwinAndWindows(t *testing.T) {
	home := t.TempDir()
	data := t.TempDir()

	// macOS and Windows have their own conventions and neither is
	// freedesktop's; a variable set by a shell profile ported between
	// machines must not move the registry there.
	for _, goos := range []string{"darwin", "windows"} {
		got := userDir(goos, home, env(map[string]string{"XDG_DATA_HOME": data}))
		if want := filepath.Join(home, userDirName); got != want {
			t.Fatalf("%s: userDir = %q, want %q", goos, got, want)
		}
	}
}

func TestUserDirReturnsAnAbsolutePath(t *testing.T) {
	dir, err := UserDir()
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(dir) {
		t.Fatalf("UserDir = %q, want an absolute path", dir)
	}
}
