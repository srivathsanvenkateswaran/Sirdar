package app

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/srivathsanvenkateswaran/sirdar/internal/config"
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
