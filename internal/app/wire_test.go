package app

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/srivathsanvenkateswaran/sirdar/internal/config"
	"github.com/srivathsanvenkateswaran/sirdar/internal/source/freshdesk"
	"github.com/srivathsanvenkateswaran/sirdar/internal/source/gorgias"
	"github.com/srivathsanvenkateswaran/sirdar/internal/source/helpscout"
	"github.com/srivathsanvenkateswaran/sirdar/internal/source/hubspot"
	"github.com/srivathsanvenkateswaran/sirdar/internal/source/intercom"
	"github.com/srivathsanvenkateswaran/sirdar/internal/source/linear"
	"github.com/srivathsanvenkateswaran/sirdar/internal/source/zendesk"
	"github.com/srivathsanvenkateswaran/sirdar/internal/source/zohodesk"
)

// The two doctor cases below live here, beside the code they cover: this
// package owns the checks now, and cmd/sirdar only prints them.
//
// envResolver resolves env: refs from a map, so a test never has to put a
// credential in the process environment.
func envResolver(vars map[string]string) config.Resolver {
	return config.Resolver{Env: func(name string) (string, bool) {
		v, ok := vars[name]
		return v, ok
	}}
}

func TestZohoTokenSource_StaticToken(t *testing.T) {
	sc := &config.SourceConfig{
		Adapter: "zohodesk",
		OrgID:   "1",
		BaseURL: "https://desk.zoho.in",
		Token:   "env:ZOHO_TOKEN",
	}
	ts, err := ZohoTokenSource(sc, envResolver(map[string]string{"ZOHO_TOKEN": "access-1"}))
	if err != nil {
		t.Fatalf("zohoTokenSource: %v", err)
	}
	got, err := ts.Token(context.Background())
	if err != nil || got != "access-1" {
		t.Fatalf("Token() = %q, %v", got, err)
	}
}

func TestZohoTokenSource_OAuthGrant(t *testing.T) {
	sc := &config.SourceConfig{
		Adapter: "zohodesk",
		OrgID:   "1",
		BaseURL: "https://desk.zoho.in",
		Auth: &config.OAuthConfig{
			ClientID:     "env:ZOHO_CLIENT_ID",
			ClientSecret: "env:ZOHO_CLIENT_SECRET",
			RefreshToken: "env:ZOHO_REFRESH_TOKEN",
		},
	}
	ts, err := ZohoTokenSource(sc, envResolver(map[string]string{
		"ZOHO_CLIENT_ID":     "1000.clientid",
		"ZOHO_CLIENT_SECRET": "shhh",
		"ZOHO_REFRESH_TOKEN": "1000.refresh",
	}))
	if err != nil {
		t.Fatalf("zohoTokenSource: %v", err)
	}
	rt, ok := ts.(*zohodesk.RefreshingToken)
	if !ok {
		t.Fatalf("token source is %T, want *zohodesk.RefreshingToken", ts)
	}
	// accountsUrl was left unset, so it has to come from baseUrl: sending
	// a .in refresh token to the .com accounts server would just fail.
	if rt.AccountsURL != "https://accounts.zoho.in" {
		t.Errorf("AccountsURL = %q, want the India accounts server", rt.AccountsURL)
	}
	if rt.ClientID != "1000.clientid" || rt.ClientSecret != "shhh" || rt.RefreshToken != "1000.refresh" {
		t.Errorf("grant was not resolved: %+v", rt)
	}
}

// TestZohoTokenSource_MissingCredentialNamesTheKey covers the error an
// operator sees when a Keychain entry or a variable is not there: it has to
// say which of the three is missing.
func TestZohoTokenSource_MissingCredentialNamesTheKey(t *testing.T) {
	sc := &config.SourceConfig{
		Adapter: "zohodesk",
		OrgID:   "1",
		BaseURL: "https://desk.zoho.in",
		Auth: &config.OAuthConfig{
			ClientID:     "env:ZOHO_CLIENT_ID",
			ClientSecret: "env:ZOHO_CLIENT_SECRET",
			RefreshToken: "env:ZOHO_REFRESH_TOKEN",
		},
	}
	_, err := ZohoTokenSource(sc, envResolver(map[string]string{"ZOHO_CLIENT_ID": "1000.clientid"}))
	if err == nil {
		t.Fatal("want an error for an unresolvable credential")
	}
	if !strings.Contains(err.Error(), "auth.clientSecret") {
		t.Fatalf("error does not name the missing key: %v", err)
	}
}

func TestZohoTokenSource_UnknownDataCentre(t *testing.T) {
	sc := &config.SourceConfig{
		Adapter: "zohodesk",
		OrgID:   "1",
		BaseURL: "https://desk.example.local",
		Auth: &config.OAuthConfig{
			ClientID:     "env:ZOHO_CLIENT_ID",
			ClientSecret: "env:ZOHO_CLIENT_SECRET",
			RefreshToken: "env:ZOHO_REFRESH_TOKEN",
		},
	}
	_, err := ZohoTokenSource(sc, envResolver(map[string]string{
		"ZOHO_CLIENT_ID":     "1000.clientid",
		"ZOHO_CLIENT_SECRET": "shhh",
		"ZOHO_REFRESH_TOKEN": "1000.refresh",
	}))
	if err == nil || !strings.Contains(err.Error(), "accountsUrl") {
		t.Fatalf("got %v", err)
	}
}

// TestDoctorReportsTheOAuthGrant covers the line `sirdar doctor` prints for
// the auth path: one real refresh against the accounts server, reported
// with the lifetime it came back with, and then the usual Desk probe.
func TestDoctorReportsTheOAuthGrant(t *testing.T) {
	accounts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/oauth/v2/token" {
			http.Error(w, "no", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"access_token":"access-1","expires_in":3600}`)
	}))
	t.Cleanup(accounts.Close)

	desk := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Zoho-oauthtoken access-1" {
			t.Errorf("desk Authorization = %q", got)
		}
		// Ticket id 0 cannot exist; 404 is the answer doctor treats as
		// proof the credentials and the transport work.
		http.Error(w, `{"errorCode":"UNPROCESSABLE_ENTITY"}`, http.StatusNotFound)
	}))
	t.Cleanup(desk.Close)

	t.Setenv("ZOHO_CLIENT_ID", "1000.clientid")
	t.Setenv("ZOHO_CLIENT_SECRET", "shhh")
	t.Setenv("ZOHO_REFRESH_TOKEN", "1000.refresh")

	sc := &config.SourceConfig{
		Adapter: "zohodesk",
		OrgID:   "1",
		BaseURL: desk.URL,
		Auth: &config.OAuthConfig{
			ClientID:     "env:ZOHO_CLIENT_ID",
			ClientSecret: "env:ZOHO_CLIENT_SECRET",
			RefreshToken: "env:ZOHO_REFRESH_TOKEN",
			AccountsURL:  accounts.URL,
		},
	}

	checks := checkSource(context.Background(), &config.Config{}, "sources.helpdesk (zohodesk)", sc)
	if len(checks) != 2 {
		t.Fatalf("checks = %+v, want the oauth check and the desk probe", checks)
	}
	if checks[0].Name != "zoho oauth" || !checks[0].OK {
		t.Fatalf("oauth check: %+v", checks[0])
	}
	if checks[0].Detail != "access token obtained, expires in 3600s" {
		t.Fatalf("oauth detail = %q", checks[0].Detail)
	}
	if !checks[1].OK {
		t.Fatalf("desk probe: %+v", checks[1])
	}
}

// TestDoctorReportsARejectedGrant covers the failure an operator most
// often hits: a refresh token that has been revoked. The reason the
// accounts server gave has to reach the report; the secrets must not.
func TestDoctorReportsARejectedGrant(t *testing.T) {
	accounts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"error":"invalid_code"}`)
	}))
	t.Cleanup(accounts.Close)

	t.Setenv("ZOHO_CLIENT_ID", "1000.clientid")
	t.Setenv("ZOHO_CLIENT_SECRET", "shhh")
	t.Setenv("ZOHO_REFRESH_TOKEN", "1000.refresh")

	sc := &config.SourceConfig{
		Adapter: "zohodesk",
		OrgID:   "1",
		BaseURL: "https://desk.zoho.in",
		Auth: &config.OAuthConfig{
			ClientID:     "env:ZOHO_CLIENT_ID",
			ClientSecret: "env:ZOHO_CLIENT_SECRET",
			RefreshToken: "env:ZOHO_REFRESH_TOKEN",
			AccountsURL:  accounts.URL,
		},
	}

	checks := checkSource(context.Background(), &config.Config{}, "sources.helpdesk (zohodesk)", sc)
	if len(checks) == 0 || checks[0].Name != "zoho oauth" || checks[0].OK {
		t.Fatalf("want a failing oauth check, got %+v", checks)
	}
	if !strings.Contains(checks[0].Detail, "invalid_code") {
		t.Errorf("detail does not carry the reason: %q", checks[0].Detail)
	}
	for _, secret := range []string{"shhh", "1000.refresh"} {
		for _, c := range checks {
			if strings.Contains(c.Detail, secret) {
				t.Fatalf("doctor printed a secret: %q", c.Detail)
			}
		}
	}
}

// --- built-in tracker adapters ---

// builtinTrackerSources are one valid configuration per built-in tracker,
// each naming its credentials as env: refs.
func builtinTrackerSources() map[string]*config.SourceConfig {
	return map[string]*config.SourceConfig{
		"jira": {
			Adapter:  "jira",
			BaseURL:  "https://acme.atlassian.net",
			Email:    "you@acme.com",
			APIToken: "env:JIRA_TOKEN",
		},
		"linear": {
			Adapter: "linear",
			APIKey:  "env:LINEAR_KEY",
			TeamKey: "ENG",
		},
		"azdo": {
			Adapter: "azdo",
			OrgURL:  "https://dev.azure.com/acme",
			Project: "Payments",
			PAT:     "env:AZDO_PAT",
		},
		"rally": {
			Adapter:   "rally",
			BaseURL:   config.RallyDefaultBaseURL,
			APIKey:    "env:RALLY_KEY",
			Workspace: "12345",
		},
	}
}

var builtinCreds = map[string]string{
	"JIRA_TOKEN": "jira-secret",
	"LINEAR_KEY": "lin_api_secret",
	"AZDO_PAT":   "azdo-secret",
	"RALLY_KEY":  "rally-secret",
}

// TestNewBuiltinTracker covers what the wiring owes each adapter: a client
// built from the config, and the conversation view where the adapter has
// one.
func TestNewBuiltinTracker(t *testing.T) {
	for name, sc := range builtinTrackerSources() {
		t.Run(name, func(t *testing.T) {
			tracker, helpdesk, err := newBuiltinTracker(sc, envResolver(builtinCreds))
			if err != nil {
				t.Fatalf("newBuiltinTracker: %v", err)
			}
			if tracker == nil {
				t.Fatal("tracker is nil")
			}
			if helpdesk == nil {
				t.Fatal("adapter exposes Helpdesk(), so the wiring must carry it")
			}
			if _, ok := tracker.(pinger); !ok {
				t.Fatalf("%T does not implement Ping, so doctor has no probe", tracker)
			}
		})
	}
}

// TestNewBuiltinTrackerResolvesTheSecret proves the ref was resolved rather
// than passed through: Linear is the one adapter that keeps its key on an
// exported field, so it is the one that can be checked without a round trip.
func TestNewBuiltinTrackerResolvesTheSecret(t *testing.T) {
	sc := builtinTrackerSources()["linear"]
	tracker, _, err := newBuiltinTracker(sc, envResolver(builtinCreds))
	if err != nil {
		t.Fatalf("newBuiltinTracker: %v", err)
	}
	c, ok := tracker.(*linear.Client)
	if !ok {
		t.Fatalf("tracker is %T, want *linear.Client", tracker)
	}
	if c.APIKey != "lin_api_secret" {
		t.Errorf("APIKey = %q, want the resolved secret", c.APIKey)
	}
	if c.TeamKey != "ENG" {
		t.Errorf("TeamKey = %q, want ENG", c.TeamKey)
	}
}

// TestNewBuiltinTrackerMissingCredentialNamesTheKey covers the error an
// operator sees when the variable or Keychain entry is not there.
func TestNewBuiltinTrackerMissingCredentialNamesTheKey(t *testing.T) {
	sc := builtinTrackerSources()["jira"]
	_, _, err := newBuiltinTracker(sc, envResolver(nil))
	if err == nil {
		t.Fatal("want an error when the credential cannot be resolved")
	}
	if !strings.Contains(err.Error(), "apiToken") || !strings.Contains(err.Error(), "env:JIRA_TOKEN") {
		t.Fatalf("the error must name the key and the ref, got %v", err)
	}
}

func TestBuiltinEndpoint(t *testing.T) {
	srcs := builtinTrackerSources()
	for adapter, want := range map[string]string{
		"jira":   "https://acme.atlassian.net",
		"linear": linear.DefaultEndpoint,
		"azdo":   "https://dev.azure.com/acme/Payments",
		"rally":  config.RallyDefaultBaseURL,
	} {
		if got := builtinEndpoint(srcs[adapter]); got != want {
			t.Errorf("builtinEndpoint(%s) = %q, want %q", adapter, got, want)
		}
	}
}

// TestBuildDepsBuiltinTrackerServesHelpdesk: a tracker that carries the
// conversation on the issue itself fills the helpdesk role when nothing
// else claims it.
func TestBuildDepsBuiltinTrackerServesHelpdesk(t *testing.T) {
	for name, value := range builtinCreds {
		t.Setenv(name, value)
	}
	cfg := &config.Config{Provider: "claude", Root: t.TempDir()}
	cfg.Sources.Tracker = builtinTrackerSources()["jira"]

	deps, cleanup, err := BuildDeps(cfg, "", "", io.Discard)
	defer cleanup()
	if err != nil {
		t.Fatalf("BuildDeps: %v", err)
	}
	if deps.Tracker == nil {
		t.Fatal("tracker was not wired")
	}
	if deps.Helpdesk == nil {
		t.Fatal("the jira adapter's helpdesk view was not used for the helpdesk role")
	}
}

// TestBuildDepsConfiguredHelpdeskWins: an operator who named a separate
// helpdesk meant the thread to come from there, so the tracker's own view
// must not displace it.
func TestBuildDepsConfiguredHelpdeskWins(t *testing.T) {
	for name, value := range builtinCreds {
		t.Setenv(name, value)
	}
	t.Setenv("ZOHO_TOKEN", "access-1")
	cfg := &config.Config{Provider: "claude", Root: t.TempDir()}
	cfg.Sources.Tracker = builtinTrackerSources()["jira"]
	cfg.Sources.Helpdesk = &config.SourceConfig{
		Adapter: "zohodesk",
		OrgID:   "1",
		BaseURL: "https://desk.zoho.in",
		Token:   "env:ZOHO_TOKEN",
	}

	deps, cleanup, err := BuildDeps(cfg, "", "", io.Discard)
	defer cleanup()
	if err != nil {
		t.Fatalf("BuildDeps: %v", err)
	}
	if _, ok := deps.Helpdesk.(*zohodesk.Client); !ok {
		t.Fatalf("helpdesk is %T, want the configured Zoho Desk client", deps.Helpdesk)
	}
}

// --- built-in helpdesk adapters (zendesk, freshdesk) ---

// TestNewBuiltinHelpdeskZendeskBasicAuth proves the resolved apiToken, not
// the env: ref, is what reaches the wire: the request the client actually
// sends carries the secret the resolver handed back.
func TestNewBuiltinHelpdeskZendeskBasicAuth(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)

	sc := &config.SourceConfig{
		Adapter:   "zendesk",
		Subdomain: "acme",
		BaseURL:   srv.URL,
		Email:     "agent@acme.com",
		APIToken:  "env:ZENDESK_TOKEN",
	}
	hd, err := newBuiltinHelpdesk(sc, envResolver(map[string]string{"ZENDESK_TOKEN": "tok-1"}))
	if err != nil {
		t.Fatalf("newBuiltinHelpdesk: %v", err)
	}
	if _, ok := hd.(*zendesk.Client); !ok {
		t.Fatalf("helpdesk is %T, want *zendesk.Client", hd)
	}
	p, ok := hd.(pinger)
	if !ok {
		t.Fatal("zendesk.Client does not implement Ping, so doctor has no probe")
	}
	if err := p.Ping(context.Background()); err != nil {
		t.Fatalf("Ping: %v", err)
	}
	want := "Basic " + base64.StdEncoding.EncodeToString([]byte("agent@acme.com/token:tok-1"))
	if gotAuth != want {
		t.Fatalf("Authorization = %q, want %q", gotAuth, want)
	}
}

// TestNewBuiltinHelpdeskZendeskOAuth covers the alternative auth form: an
// oauthToken ref resolved and sent as a Bearer token.
func TestNewBuiltinHelpdeskZendeskOAuth(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)

	sc := &config.SourceConfig{
		Adapter:    "zendesk",
		Subdomain:  "acme",
		BaseURL:    srv.URL,
		OAuthToken: "env:ZENDESK_OAUTH",
	}
	hd, err := newBuiltinHelpdesk(sc, envResolver(map[string]string{"ZENDESK_OAUTH": "oauth-1"}))
	if err != nil {
		t.Fatalf("newBuiltinHelpdesk: %v", err)
	}
	if err := hd.(pinger).Ping(context.Background()); err != nil {
		t.Fatalf("Ping: %v", err)
	}
	if gotAuth != "Bearer oauth-1" {
		t.Fatalf("Authorization = %q, want Bearer oauth-1", gotAuth)
	}
}

// TestNewBuiltinHelpdeskZendeskMissingCredentialNamesTheKey covers the
// error an operator sees when the apiToken variable is not there.
func TestNewBuiltinHelpdeskZendeskMissingCredentialNamesTheKey(t *testing.T) {
	sc := &config.SourceConfig{
		Adapter:   "zendesk",
		Subdomain: "acme",
		Email:     "agent@acme.com",
		APIToken:  "env:ZENDESK_TOKEN",
	}
	_, err := newBuiltinHelpdesk(sc, envResolver(nil))
	if err == nil {
		t.Fatal("want an error when the credential cannot be resolved")
	}
	if !strings.Contains(err.Error(), "apiToken") || !strings.Contains(err.Error(), "env:ZENDESK_TOKEN") {
		t.Fatalf("the error must name the key and the ref, got %v", err)
	}
}

// withDefaultTransport points http.DefaultTransport at rt for the duration
// of the test, restoring the original after. newBuiltinHelpdesk builds its
// own *http.Client with no Transport set, so this is the only way to steer
// its requests at a local test server: freshdesk.Config carries only a
// Domain, with no baseUrl override field, and always talks https.
func withDefaultTransport(t *testing.T, rt http.RoundTripper) {
	t.Helper()
	orig := http.DefaultTransport
	http.DefaultTransport = rt
	t.Cleanup(func() { http.DefaultTransport = orig })
}

// TestNewBuiltinHelpdeskFreshdesk proves the resolved apiKey reaches the
// wire as Basic auth username, with the literal "X" password Freshdesk's
// API expects.
func TestNewBuiltinHelpdeskFreshdesk(t *testing.T) {
	var gotAuth string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)
	withDefaultTransport(t, srv.Client().Transport)

	sc := &config.SourceConfig{
		Adapter: "freshdesk",
		Domain:  srv.Listener.Addr().String(),
		APIKey:  "env:FRESHDESK_KEY",
	}
	hd, err := newBuiltinHelpdesk(sc, envResolver(map[string]string{"FRESHDESK_KEY": "key-1"}))
	if err != nil {
		t.Fatalf("newBuiltinHelpdesk: %v", err)
	}
	if _, ok := hd.(*freshdesk.Client); !ok {
		t.Fatalf("helpdesk is %T, want *freshdesk.Client", hd)
	}
	if err := hd.(pinger).Ping(context.Background()); err != nil {
		t.Fatalf("Ping: %v", err)
	}
	want := "Basic " + base64.StdEncoding.EncodeToString([]byte("key-1:X"))
	if gotAuth != want {
		t.Fatalf("Authorization = %q, want %q", gotAuth, want)
	}
}

// TestNewBuiltinHelpdeskFreshdeskMissingCredentialNamesTheKey covers the
// error an operator sees when the apiKey variable is not there.
func TestNewBuiltinHelpdeskFreshdeskMissingCredentialNamesTheKey(t *testing.T) {
	sc := &config.SourceConfig{
		Adapter: "freshdesk",
		Domain:  "acme.freshdesk.com",
		APIKey:  "env:FRESHDESK_KEY",
	}
	_, err := newBuiltinHelpdesk(sc, envResolver(nil))
	if err == nil {
		t.Fatal("want an error when the credential cannot be resolved")
	}
	if !strings.Contains(err.Error(), "apiKey") || !strings.Contains(err.Error(), "env:FRESHDESK_KEY") {
		t.Fatalf("the error must name the key and the ref, got %v", err)
	}
}

// --- Gorgias ---

// TestNewBuiltinHelpdeskGorgias proves the resolved apiKey reaches the wire
// as the HTTP Basic password, with the configured login email as the
// username, and that the client is pointed at the configured account host.
func TestNewBuiltinHelpdeskGorgias(t *testing.T) {
	var mu sync.Mutex
	var gotAuth, gotPath string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		mu.Unlock()
		w.Write([]byte(`{"id":31,"domain":"acme.gorgias.com"}`))
	}))
	t.Cleanup(srv.Close)
	withDefaultTransport(t, srv.Client().Transport)

	sc := &config.SourceConfig{
		Adapter: "gorgias",
		BaseURL: "https://" + srv.Listener.Addr().String(),
		Email:   "ops@acme.com",
		APIKey:  "env:GORGIAS_KEY",
	}
	hd, err := newBuiltinHelpdesk(sc, envResolver(map[string]string{"GORGIAS_KEY": "key-1"}))
	if err != nil {
		t.Fatalf("newBuiltinHelpdesk: %v", err)
	}
	if _, ok := hd.(*gorgias.Client); !ok {
		t.Fatalf("helpdesk is %T, want *gorgias.Client", hd)
	}
	if err := hd.(pinger).Ping(context.Background()); err != nil {
		t.Fatalf("Ping: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	want := "Basic " + base64.StdEncoding.EncodeToString([]byte("ops@acme.com:key-1"))
	if gotAuth != want {
		t.Fatalf("Authorization = %q, want the email as username and the resolved key as password", gotAuth)
	}
	if gotPath != "/api/account" {
		t.Fatalf("Ping path = %q, want the account endpoint", gotPath)
	}
}

func TestNewBuiltinHelpdeskGorgiasMissingCredentialNamesTheKey(t *testing.T) {
	sc := &config.SourceConfig{
		Adapter: "gorgias",
		Account: "acme",
		Email:   "ops@acme.com",
		APIKey:  "env:GORGIAS_KEY",
	}
	_, err := newBuiltinHelpdesk(sc, envResolver(nil))
	if err == nil {
		t.Fatal("want an error when the credential cannot be resolved")
	}
	if !strings.Contains(err.Error(), "apiKey") || !strings.Contains(err.Error(), "env:GORGIAS_KEY") {
		t.Fatalf("the error must name the key and the ref, got %v", err)
	}
}

// TestBuildDepsGorgiasHelpdesk covers sources.helpdesk wiring end to end
// through BuildDeps, the same path a real command takes, in the account
// form an operator actually writes.
func TestBuildDepsGorgiasHelpdesk(t *testing.T) {
	t.Setenv("GORGIAS_KEY", "key-1")
	cfg := &config.Config{Provider: "claude", Root: t.TempDir()}
	cfg.Sources.Helpdesk = &config.SourceConfig{
		Adapter: "gorgias",
		Account: "acme",
		Email:   "ops@acme.com",
		APIKey:  "env:GORGIAS_KEY",
	}

	deps, cleanup, err := BuildDeps(cfg, "", "", io.Discard)
	defer cleanup()
	if err != nil {
		t.Fatalf("BuildDeps: %v", err)
	}
	if _, ok := deps.Helpdesk.(*gorgias.Client); !ok {
		t.Fatalf("helpdesk is %T, want *gorgias.Client", deps.Helpdesk)
	}
}

// TestBuiltinHelpdeskProbeGorgias covers the doctor row: it names the login
// email the connection authenticates as and never the API key.
func TestBuiltinHelpdeskProbeGorgias(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"id":31}`))
	}))
	t.Cleanup(srv.Close)
	withDefaultTransport(t, srv.Client().Transport)
	t.Setenv("GORGIAS_KEY", "key-1")

	sc := &config.SourceConfig{
		Adapter: "gorgias",
		BaseURL: "https://" + srv.Listener.Addr().String(),
		Email:   "ops@acme.com",
		APIKey:  "env:GORGIAS_KEY",
	}
	check := builtinHelpdeskProbe(context.Background(), "sources.helpdesk (gorgias)", sc)
	if !check.OK {
		t.Fatalf("check: %+v", check)
	}
	if check.Detail != "reachable as ops@acme.com" {
		t.Fatalf("detail = %q, want it to name the login email", check.Detail)
	}
	if strings.Contains(check.Detail, "key-1") {
		t.Fatalf("doctor printed the secret: %q", check.Detail)
	}
}

// TestBuildDepsZendeskHelpdesk covers sources.helpdesk wiring end to end
// through buildDeps, the same path a real command takes.
func TestBuildDepsZendeskHelpdesk(t *testing.T) {
	t.Setenv("ZENDESK_TOKEN", "tok-1")
	cfg := &config.Config{Provider: "claude", Root: t.TempDir()}
	cfg.Sources.Helpdesk = &config.SourceConfig{
		Adapter:   "zendesk",
		Subdomain: "acme",
		Email:     "agent@acme.com",
		APIToken:  "env:ZENDESK_TOKEN",
	}

	deps, cleanup, err := BuildDeps(cfg, "", "", io.Discard)
	defer cleanup()
	if err != nil {
		t.Fatalf("BuildDeps: %v", err)
	}
	if _, ok := deps.Helpdesk.(*zendesk.Client); !ok {
		t.Fatalf("helpdesk is %T, want *zendesk.Client", deps.Helpdesk)
	}
}

// TestBuiltinHelpdeskProbeZendesk covers the doctor row: it names who the
// connection authenticates as and never the token.
func TestBuiltinHelpdeskProbeZendesk(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)
	t.Setenv("ZENDESK_TOKEN", "tok-1")

	sc := &config.SourceConfig{
		Adapter:   "zendesk",
		Subdomain: "acme",
		BaseURL:   srv.URL,
		Email:     "agent@acme.com",
		APIToken:  "env:ZENDESK_TOKEN",
	}
	check := builtinHelpdeskProbe(context.Background(), "sources.helpdesk (zendesk)", sc)
	if !check.OK {
		t.Fatalf("check: %+v", check)
	}
	if check.Detail != "reachable as agent@acme.com" {
		t.Fatalf("detail = %q, want it to name the email", check.Detail)
	}
	if strings.Contains(check.Detail, "tok-1") {
		t.Fatalf("doctor printed the secret: %q", check.Detail)
	}
}

// TestBuiltinHelpdeskProbeZendeskOAuth covers the oauth-form doctor row:
// "oauth" stands in for an email that does not exist for that auth form.
func TestBuiltinHelpdeskProbeZendeskOAuth(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)
	t.Setenv("ZENDESK_OAUTH", "oauth-1")

	sc := &config.SourceConfig{
		Adapter:    "zendesk",
		Subdomain:  "acme",
		BaseURL:    srv.URL,
		OAuthToken: "env:ZENDESK_OAUTH",
	}
	check := builtinHelpdeskProbe(context.Background(), "sources.helpdesk (zendesk)", sc)
	if !check.OK || check.Detail != "reachable as oauth" {
		t.Fatalf("check: %+v", check)
	}
}

// TestBuiltinHelpdeskProbeFreshdesk covers the Freshdesk doctor row.
func TestBuiltinHelpdeskProbeFreshdesk(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)
	withDefaultTransport(t, srv.Client().Transport)
	t.Setenv("FRESHDESK_KEY", "key-1")

	sc := &config.SourceConfig{
		Adapter: "freshdesk",
		Domain:  srv.Listener.Addr().String(),
		APIKey:  "env:FRESHDESK_KEY",
	}
	check := builtinHelpdeskProbe(context.Background(), "sources.helpdesk (freshdesk)", sc)
	if !check.OK {
		t.Fatalf("check: %+v", check)
	}
	if check.Detail != "reachable as "+sc.Domain {
		t.Fatalf("detail = %q, want it to name the domain", check.Detail)
	}
	if strings.Contains(check.Detail, "key-1") {
		t.Fatalf("doctor printed the secret: %q", check.Detail)
	}
}

// openAIConfig is the workspace shape `provider: openai` needs: an
// endpoint, a credential reference, and the pricing a run is costed with.
func openAIConfig(baseURL string) *config.Config {
	cfg := &config.Config{Provider: "openai", Model: ""}
	cfg.OpenAI = &config.OpenAIConfig{
		BaseURL:          baseURL,
		APIKey:           "env:OPENROUTER_API_KEY",
		Model:            "qwen/qwen3-coder",
		MaxContextTokens: 64000,
		Price:            &config.PriceConfig{InputPerMTok: 0.2, OutputPerMTok: 0.8},
	}
	return cfg
}

// TestOpenAIProviderResolvesTheKeyAndNeverPrintsIt covers the wiring for
// `provider: openai`: the credential reference is resolved here, the
// resolved key authenticates the endpoint, and nothing doctor prints
// carries it.
func TestOpenAIProviderResolvesTheKeyAndNeverPrintsIt(t *testing.T) {
	const secret = "sk-or-v1-not-a-real-key"
	var seen string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Get("Authorization")
		if r.URL.Path != "/models" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":[{"id":"qwen/qwen3-coder"}]}`)
	}))
	defer srv.Close()

	cfg := openAIConfig(srv.URL)
	p, err := ProviderFor(cfg, envResolver(map[string]string{"OPENROUTER_API_KEY": secret}))
	if err != nil {
		t.Fatalf("ProviderFor: %v", err)
	}
	if p.Name() != "openai" {
		t.Fatalf("Name() = %q", p.Name())
	}

	checks := p.Doctor(context.Background(), "/no/such/binary")
	if len(checks) != 2 || !checks[0].OK || !checks[1].OK {
		t.Fatalf("doctor checks = %+v", checks)
	}
	if seen != "Bearer "+secret {
		t.Errorf("Authorization = %q, want the resolved key", seen)
	}
	for _, c := range checks {
		if strings.Contains(c.Detail, secret) {
			t.Fatalf("doctor printed the api key: %q", c.Detail)
		}
	}
}

// TestOpenAIProviderMissingKeyNamesTheReference covers the error an
// operator sees when the variable is not set: the reference, never a
// guess at the value.
func TestOpenAIProviderMissingKeyNamesTheReference(t *testing.T) {
	_, err := ProviderFor(openAIConfig("https://api.example/v1"), envResolver(nil))
	if err == nil {
		t.Fatal("want an error for an unresolvable api key")
	}
	if !strings.Contains(err.Error(), "openai.apiKey") || !strings.Contains(err.Error(), "OPENROUTER_API_KEY") {
		t.Fatalf("error does not name the reference: %v", err)
	}
}

// TestOpenAIProviderNeedsItsBlock covers a workspace that selects the
// provider and configures nothing.
func TestOpenAIProviderNeedsItsBlock(t *testing.T) {
	_, err := ProviderFor(&config.Config{Provider: "openai"}, envResolver(nil))
	if err == nil || !strings.Contains(err.Error(), "openai block") {
		t.Fatalf("err = %v", err)
	}
}

// TestACPProviderNeedsACommand covers a workspace that selects the
// provider with no block, and with a block naming no agent.
func TestACPProviderNeedsACommand(t *testing.T) {
	_, err := ProviderFor(&config.Config{Provider: "acp"}, envResolver(nil))
	if err == nil || !strings.Contains(err.Error(), "acp block") {
		t.Fatalf("err = %v", err)
	}
	_, err = ProviderFor(&config.Config{Provider: "acp", ACP: &config.ACPConfig{}}, envResolver(nil))
	if err == nil || !strings.Contains(err.Error(), "acp.command") {
		t.Fatalf("err = %v", err)
	}
}

// TestACPProviderIsBuiltFromTheBlock checks the wiring hands the adapter
// the configured agent.
func TestACPProviderIsBuiltFromTheBlock(t *testing.T) {
	p, err := ProviderFor(&config.Config{
		Provider: "acp",
		ACP:      &config.ACPConfig{Command: "gemini", Args: []string{"--experimental-acp"}},
	}, envResolver(nil))
	if err != nil {
		t.Fatalf("ProviderFor: %v", err)
	}
	if p.Name() != "acp" {
		t.Fatalf("Name = %q, want acp", p.Name())
	}
}

// TestProviderForRejectsAnUnknownName keeps the message listing every
// provider a workspace may name.
func TestProviderForRejectsAnUnknownName(t *testing.T) {
	_, err := ProviderFor(&config.Config{Provider: "gemini"}, envResolver(nil))
	if err == nil || !strings.Contains(err.Error(), "claude, codex, openai, acp or qwen") {
		t.Fatalf("err = %v", err)
	}
}

// TestQwenProviderWithoutABlock covers the ordinary case: a workspace that
// names the provider and nothing else drives the CLI against the login the
// operator already gave it.
func TestQwenProviderWithoutABlock(t *testing.T) {
	p, err := ProviderFor(&config.Config{Provider: "qwen"}, envResolver(nil))
	if err != nil {
		t.Fatalf("ProviderFor: %v", err)
	}
	if p.Name() != "qwen" {
		t.Fatalf("Name() = %q", p.Name())
	}
	checks := p.Doctor(context.Background(), "/no/such/binary")
	if len(checks) != 2 || checks[0].OK {
		t.Fatalf("doctor checks = %+v", checks)
	}
	if !strings.Contains(checks[1].Detail, "own login") {
		t.Fatalf("endpoint check = %+v", checks[1])
	}
}

// TestQwenProviderResolvesTheKeyAndKeepsItOutOfDoctor is the credential
// half: the reference is resolved once, the secret stays in memory, and
// the report an operator pastes into a ticket never carries it.
func TestQwenProviderResolvesTheKeyAndKeepsItOutOfDoctor(t *testing.T) {
	const secret = "sk-qwen-secret"
	cfg := &config.Config{Provider: "qwen", Qwen: &config.QwenConfig{
		BaseURL: "https://api.example/v1",
		Model:   "qwen3-coder-plus",
		APIKey:  "env:DASHSCOPE_API_KEY",
	}}
	p, err := ProviderFor(cfg, envResolver(map[string]string{"DASHSCOPE_API_KEY": secret}))
	if err != nil {
		t.Fatalf("ProviderFor: %v", err)
	}
	for _, c := range p.Doctor(context.Background(), "/no/such/binary") {
		if strings.Contains(c.Detail, secret) {
			t.Fatalf("doctor printed the api key: %q", c.Detail)
		}
	}
}

// TestQwenProviderMissingKeyNamesTheReference covers the error an operator
// sees when the variable is not set: the reference, never a guess at the
// value.
func TestQwenProviderMissingKeyNamesTheReference(t *testing.T) {
	cfg := &config.Config{Provider: "qwen", Qwen: &config.QwenConfig{
		BaseURL: "https://api.example/v1",
		Model:   "m",
		APIKey:  "env:DASHSCOPE_API_KEY",
	}}
	_, err := ProviderFor(cfg, envResolver(nil))
	if err == nil {
		t.Fatal("want an error for an unresolvable api key")
	}
	if !strings.Contains(err.Error(), "qwen.apiKey") || !strings.Contains(err.Error(), "DASHSCOPE_API_KEY") {
		t.Fatalf("error does not name the reference: %v", err)
	}
}

// --- built-in helpdesk adapters (helpscout, intercom, hubspot) ---

// rewriteTransport sends every request to addr while leaving the request's
// own URL (and so the adapter's host checks, which run before the
// transport) untouched. Help Scout, Intercom and HubSpot each talk to one
// fixed vendor host over https with no baseUrl override to point at a test
// server, so this is how their wiring gets exercised for real.
type rewriteTransport struct {
	addr string
	base http.RoundTripper
}

func (rt rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	r2 := req.Clone(req.Context())
	r2.URL.Host = rt.addr
	return rt.base.RoundTrip(r2)
}

// helpdeskTestServer starts a TLS server, routes every built-in helpdesk
// request to it, and returns it.
func helpdeskTestServer(t *testing.T, h http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewTLSServer(h)
	t.Cleanup(srv.Close)
	withDefaultTransport(t, rewriteTransport{addr: srv.Listener.Addr().String(), base: srv.Client().Transport})
	return srv
}

// TestNewBuiltinHelpdeskHelpScout proves the resolved client id and secret,
// not the env: refs, are what reach Help Scout's token endpoint, and that
// the token it hands back is what the API call then carries.
func TestNewBuiltinHelpdeskHelpScout(t *testing.T) {
	var mu sync.Mutex
	var gotID, gotSecret, gotAuth string
	helpdeskTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.URL.Path == "/v2/oauth2/token" {
			_ = r.ParseForm()
			gotID = r.PostFormValue("client_id")
			gotSecret = r.PostFormValue("client_secret")
			w.Write([]byte(`{"token_type":"bearer","access_token":"minted-1","expires_in":172800}`))
			return
		}
		gotAuth = r.Header.Get("Authorization")
		w.Write([]byte(`{"_embedded":{"mailboxes":[]}}`))
	})

	sc := &config.SourceConfig{
		Adapter:      "helpscout",
		ClientID:     "env:HS_ID",
		ClientSecret: "env:HS_SECRET",
	}
	hd, err := newBuiltinHelpdesk(sc, envResolver(map[string]string{"HS_ID": "id-1", "HS_SECRET": "secret-1"}))
	if err != nil {
		t.Fatalf("newBuiltinHelpdesk: %v", err)
	}
	if _, ok := hd.(*helpscout.Client); !ok {
		t.Fatalf("helpdesk is %T, want *helpscout.Client", hd)
	}
	p, ok := hd.(pinger)
	if !ok {
		t.Fatal("helpscout.Client does not implement Ping, so doctor has no probe")
	}
	if err := p.Ping(context.Background()); err != nil {
		t.Fatalf("Ping: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if gotID != "id-1" || gotSecret != "secret-1" {
		t.Fatalf("token grant got (%q, %q), want the resolved pair", gotID, gotSecret)
	}
	if gotAuth != "Bearer minted-1" {
		t.Fatalf("Authorization = %q, want the minted token", gotAuth)
	}
}

func TestNewBuiltinHelpdeskHelpScoutMissingCredentialNamesTheKey(t *testing.T) {
	sc := &config.SourceConfig{
		Adapter:      "helpscout",
		ClientID:     "env:HS_ID",
		ClientSecret: "env:HS_SECRET",
	}
	_, err := newBuiltinHelpdesk(sc, envResolver(nil))
	if err == nil {
		t.Fatal("want an error when the credential cannot be resolved")
	}
	if !strings.Contains(err.Error(), "clientId") || !strings.Contains(err.Error(), "env:HS_ID") {
		t.Fatalf("the error must name the key and the ref, got %v", err)
	}
}

// TestNewBuiltinHelpdeskIntercom proves the resolved access token reaches
// the wire as a bearer header, alongside the pinned API version.
func TestNewBuiltinHelpdeskIntercom(t *testing.T) {
	var mu sync.Mutex
	var gotAuth, gotVersion string
	helpdeskTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		gotAuth = r.Header.Get("Authorization")
		gotVersion = r.Header.Get("Intercom-Version")
		mu.Unlock()
		w.Write([]byte(`{"type":"admin","app":{"id_code":"abc"}}`))
	})

	sc := &config.SourceConfig{Adapter: "intercom", AccessToken: "env:INTERCOM_TOKEN"}
	hd, err := newBuiltinHelpdesk(sc, envResolver(map[string]string{"INTERCOM_TOKEN": "ic-1"}))
	if err != nil {
		t.Fatalf("newBuiltinHelpdesk: %v", err)
	}
	if _, ok := hd.(*intercom.Client); !ok {
		t.Fatalf("helpdesk is %T, want *intercom.Client", hd)
	}
	if err := hd.(pinger).Ping(context.Background()); err != nil {
		t.Fatalf("Ping: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if gotAuth != "Bearer ic-1" {
		t.Fatalf("Authorization = %q, want Bearer ic-1", gotAuth)
	}
	if gotVersion == "" {
		t.Fatal("Intercom-Version header was not sent")
	}
}

func TestNewBuiltinHelpdeskIntercomMissingCredentialNamesTheKey(t *testing.T) {
	sc := &config.SourceConfig{Adapter: "intercom", AccessToken: "env:INTERCOM_TOKEN"}
	_, err := newBuiltinHelpdesk(sc, envResolver(nil))
	if err == nil {
		t.Fatal("want an error when the credential cannot be resolved")
	}
	if !strings.Contains(err.Error(), "accessToken") || !strings.Contains(err.Error(), "env:INTERCOM_TOKEN") {
		t.Fatalf("the error must name the key and the ref, got %v", err)
	}
}

// TestNewBuiltinHelpdeskHubSpot proves the resolved private-app token
// reaches the wire as a bearer header.
func TestNewBuiltinHelpdeskHubSpot(t *testing.T) {
	var mu sync.Mutex
	var gotAuth, gotPath string
	helpdeskTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		mu.Unlock()
		w.Write([]byte(`{"portalId":1234567}`))
	})

	sc := &config.SourceConfig{Adapter: "hubspot", AccessToken: "env:HUBSPOT_TOKEN"}
	hd, err := newBuiltinHelpdesk(sc, envResolver(map[string]string{"HUBSPOT_TOKEN": "pat-1"}))
	if err != nil {
		t.Fatalf("newBuiltinHelpdesk: %v", err)
	}
	if _, ok := hd.(*hubspot.Client); !ok {
		t.Fatalf("helpdesk is %T, want *hubspot.Client", hd)
	}
	if err := hd.(pinger).Ping(context.Background()); err != nil {
		t.Fatalf("Ping: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if gotAuth != "Bearer pat-1" {
		t.Fatalf("Authorization = %q, want Bearer pat-1", gotAuth)
	}
	if gotPath != "/account-info/v3/details" {
		t.Fatalf("Ping path = %q, want the account details endpoint", gotPath)
	}
}

func TestNewBuiltinHelpdeskHubSpotMissingCredentialNamesTheKey(t *testing.T) {
	sc := &config.SourceConfig{Adapter: "hubspot", AccessToken: "env:HUBSPOT_TOKEN"}
	_, err := newBuiltinHelpdesk(sc, envResolver(nil))
	if err == nil {
		t.Fatal("want an error when the credential cannot be resolved")
	}
	if !strings.Contains(err.Error(), "accessToken") || !strings.Contains(err.Error(), "env:HUBSPOT_TOKEN") {
		t.Fatalf("the error must name the key and the ref, got %v", err)
	}
}

// TestBuildDepsFixedHostHelpdesks covers sources.helpdesk wiring end to end
// through BuildDeps for each of the three, the same path a real command
// takes.
func TestBuildDepsFixedHostHelpdesks(t *testing.T) {
	t.Setenv("HS_ID", "id-1")
	t.Setenv("HS_SECRET", "secret-1")
	t.Setenv("INTERCOM_TOKEN", "ic-1")
	t.Setenv("HUBSPOT_TOKEN", "pat-1")

	for name, tc := range map[string]struct {
		sc   *config.SourceConfig
		want string
	}{
		"helpscout": {
			sc:   &config.SourceConfig{Adapter: "helpscout", ClientID: "env:HS_ID", ClientSecret: "env:HS_SECRET"},
			want: "*helpscout.Client",
		},
		"intercom": {
			sc:   &config.SourceConfig{Adapter: "intercom", AccessToken: "env:INTERCOM_TOKEN"},
			want: "*intercom.Client",
		},
		"hubspot": {
			sc:   &config.SourceConfig{Adapter: "hubspot", AccessToken: "env:HUBSPOT_TOKEN"},
			want: "*hubspot.Client",
		},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := &config.Config{Provider: "claude", Root: t.TempDir()}
			cfg.Sources.Helpdesk = tc.sc
			deps, cleanup, err := BuildDeps(cfg, "", "", io.Discard)
			defer cleanup()
			if err != nil {
				t.Fatalf("BuildDeps: %v", err)
			}
			if got := fmt.Sprintf("%T", deps.Helpdesk); got != tc.want {
				t.Fatalf("helpdesk is %s, want %s", got, tc.want)
			}
		})
	}
}

// TestBuiltinHelpdeskProbeFixedHostAdapters covers the doctor rows: each
// names the kind of grant it authenticated with and never the secret.
func TestBuiltinHelpdeskProbeFixedHostAdapters(t *testing.T) {
	helpdeskTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v2/oauth2/token" {
			w.Write([]byte(`{"token_type":"bearer","access_token":"minted-1","expires_in":172800}`))
			return
		}
		w.Write([]byte(`{"portalId":1234567}`))
	})
	t.Setenv("HS_ID", "id-1")
	t.Setenv("HS_SECRET", "secret-1")
	t.Setenv("INTERCOM_TOKEN", "ic-1")
	t.Setenv("HUBSPOT_TOKEN", "pat-1")

	for name, tc := range map[string]struct {
		sc     *config.SourceConfig
		detail string
		secret string
	}{
		"helpscout": {
			sc:     &config.SourceConfig{Adapter: "helpscout", ClientID: "env:HS_ID", ClientSecret: "env:HS_SECRET"},
			detail: "reachable as the Help Scout app",
			secret: "secret-1",
		},
		"intercom": {
			sc:     &config.SourceConfig{Adapter: "intercom", AccessToken: "env:INTERCOM_TOKEN"},
			detail: "reachable as the workspace access token",
			secret: "ic-1",
		},
		"hubspot": {
			sc:     &config.SourceConfig{Adapter: "hubspot", AccessToken: "env:HUBSPOT_TOKEN"},
			detail: "reachable as the private app token",
			secret: "pat-1",
		},
	} {
		t.Run(name, func(t *testing.T) {
			check := builtinHelpdeskProbe(context.Background(), "sources.helpdesk ("+name+")", tc.sc)
			if !check.OK {
				t.Fatalf("check: %+v", check)
			}
			if check.Detail != tc.detail {
				t.Fatalf("detail = %q, want %q", check.Detail, tc.detail)
			}
			if strings.Contains(check.Detail, tc.secret) {
				t.Fatalf("doctor printed the secret: %q", check.Detail)
			}
		})
	}
}
