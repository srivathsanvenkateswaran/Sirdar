package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/srivathsanvenkateswaran/sirdar/internal/config"
)

// slackTo sends every request to srv while keeping the slack.com URL the
// client built, so the host pin still judges the real host.
type slackTo struct{ srv *httptest.Server }

func (s slackTo) RoundTrip(req *http.Request) (*http.Response, error) {
	target, _ := url.Parse(s.srv.URL)
	out := req.Clone(req.Context())
	out.URL.Scheme, out.URL.Host = target.Scheme, target.Host
	return http.DefaultTransport.RoundTrip(out)
}

func TestDoctorSlackRow(t *testing.T) {
	cfg := &config.Config{}
	if c := slackCheck(context.Background(), cfg, nil); !c.OK || !strings.Contains(c.Detail, "not configured") {
		t.Fatalf("unset: %+v", c)
	}

	cfg.Sources.Slack = &config.SlackConfig{Token: "env:SIRDAR_TEST_SLACK_TOKEN"}
	t.Setenv("SIRDAR_TEST_SLACK_TOKEN", "")
	if c := slackCheck(context.Background(), cfg, nil); c.OK || !strings.Contains(c.Detail, "SIRDAR_TEST_SLACK_TOKEN") {
		t.Fatalf("unresolvable token: %+v", c)
	}

	t.Setenv("SIRDAR_TEST_SLACK_TOKEN", "xoxp-doctor")
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		if r.URL.Path != "/api/auth.test" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"ok":true,"user":"sam","team":"Acme"}`))
	}))
	defer srv.Close()
	c := slackCheck(context.Background(), cfg, &http.Client{Transport: slackTo{srv}})
	if !c.OK || c.Detail != "reachable as sam in Acme" || auth != "Bearer xoxp-doctor" {
		t.Fatalf("ok: %+v (auth %q)", c, auth)
	}
	if strings.Contains(c.Detail, "xoxp") {
		t.Fatal("the row printed the token")
	}

	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"ok":false,"error":"invalid_auth"}`))
	}))
	defer bad.Close()
	if c := slackCheck(context.Background(), cfg, &http.Client{Transport: slackTo{bad}}); c.OK || !strings.Contains(c.Detail, "invalid_auth") {
		t.Fatalf("refused: %+v", c)
	}
}
