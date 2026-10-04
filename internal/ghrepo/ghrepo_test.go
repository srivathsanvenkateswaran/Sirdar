package ghrepo

import (
	"context"
	"os/exec"
	"reflect"
	"testing"
)

func TestMentioned(t *testing.T) {
	text := "PR: <https://github.com/acme-co/Billing.Service/pull/412|#412>\n" +
		"also https://github.com/acme-co/billing.service/blob/main/x.cs and git@github.com:acme-co/web-app.git\n" +
		"see https://github.com/orgs/acme-co/projects/1 and https://github.com/user-attachments/assets/abc"
	want := []string{"acme-co/Billing.Service", "acme-co/web-app"}
	if got := Mentioned(text); !reflect.DeepEqual(got, want) {
		t.Fatalf("Mentioned = %v, want %v", got, want)
	}
}

func TestFromRemote(t *testing.T) {
	for in, want := range map[string]string{
		"git@github.com:acme-co/web-app.git":     "acme-co/web-app",
		"https://github.com/acme-co/web-app.git": "acme-co/web-app",
		"https://github.com/acme-co/web-app\n":   "acme-co/web-app",
		"git@gitlab.com:acme-co/web-app.git":     "",
		"":                                       "",
	} {
		if got := FromRemote(in); got != want {
			t.Errorf("FromRemote(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestForeign(t *testing.T) {
	text := "the fix is in https://github.com/acme-co/Billing.Service/pull/412, and https://github.com/acme-co/web-app/pull/9"
	if got := Foreign(text, "acme-co/WEB-APP"); !reflect.DeepEqual(got, []string{"acme-co/Billing.Service"}) {
		t.Fatalf("Foreign = %v", got)
	}
	if got := Foreign(text, ""); got != nil {
		t.Fatalf("no origin, nothing foreign: %v", got)
	}
}

func TestOrigin(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	dir := t.TempDir()
	for _, args := range [][]string{{"init", "-q"}, {"remote", "add", "origin", "git@github.com:acme-co/web-app.git"}} {
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	if got := Origin(context.Background(), dir); got != "acme-co/web-app" {
		t.Fatalf("Origin = %q", got)
	}
	if got := Origin(context.Background(), t.TempDir()); got != "" {
		t.Fatalf("no repository, no origin: %q", got)
	}
}
