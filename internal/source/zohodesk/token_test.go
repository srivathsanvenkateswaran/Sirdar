package zohodesk

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/source"
)

const (
	testClientID     = "1000.CLIENTID"
	testClientSecret = "client-secret-never-printed"
	testRefreshToken = "refresh-token-never-printed"
)

// accountsServer stands in for the Zoho accounts server. Each call to
// grants issues the next access token in the list and records the form it
// was asked with; a nil entry makes the server reject the grant the way
// Zoho does, with HTTP 200 and an "error" field.
type accountsServer struct {
	t *testing.T

	mu      sync.Mutex
	calls   int
	tokens  []string // access tokens to hand out, in order
	failure string   // when set, every call is rejected with this error code
	forms   []map[string]string
}

func newAccountsServer(t *testing.T, tokens ...string) (*httptest.Server, *accountsServer) {
	t.Helper()
	a := &accountsServer{t: t, tokens: tokens}
	srv := httptest.NewServer(http.HandlerFunc(a.serve))
	t.Cleanup(srv.Close)
	return srv, a
}

func (a *accountsServer) serve(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/oauth/v2/token" {
		a.t.Errorf("accounts path = %q, want /oauth/v2/token", r.URL.Path)
	}
	if r.Method != http.MethodPost {
		a.t.Errorf("accounts method = %q, want POST", r.Method)
	}
	if err := r.ParseForm(); err != nil {
		a.t.Errorf("parse form: %v", err)
	}
	form := map[string]string{}
	for k := range r.PostForm {
		form[k] = r.PostForm.Get(k)
	}

	a.mu.Lock()
	a.calls++
	n := a.calls
	a.forms = append(a.forms, form)
	failure := a.failure
	var token string
	if n-1 < len(a.tokens) {
		token = a.tokens[n-1]
	}
	a.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	if failure != "" {
		// Zoho answers a bad grant with 200 and an error field, not a 4xx.
		fmt.Fprintf(w, `{"error":%q}`, failure)
		return
	}
	if token == "" {
		a.t.Errorf("accounts server called %d times, only %d tokens configured", n, len(a.tokens))
		http.Error(w, "no token", http.StatusInternalServerError)
		return
	}
	fmt.Fprintf(w, `{"access_token":%q,"expires_in":3600,"api_domain":"https://www.zohoapis.in","token_type":"Bearer"}`, token)
}

func (a *accountsServer) count() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.calls
}

func newRefreshing(srv *httptest.Server) *RefreshingToken {
	return &RefreshingToken{
		AccountsURL:  srv.URL,
		ClientID:     testClientID,
		ClientSecret: testClientSecret,
		RefreshToken: testRefreshToken,
		HTTP:         srv.Client(),
	}
}

// TestRefreshingToken_RefreshesOnFirstUse covers the grant Sirdar sends: a
// refresh_token exchange carrying the Self Client's three values, whose
// access_token becomes the token every Desk request goes out with.
func TestRefreshingToken_RefreshesOnFirstUse(t *testing.T) {
	srv, accounts := newAccountsServer(t, "access-1")
	rt := newRefreshing(srv)

	got, err := rt.Token(context.Background())
	if err != nil {
		t.Fatalf("Token: %v", err)
	}
	if got != "access-1" {
		t.Fatalf("Token = %q, want %q", got, "access-1")
	}
	if accounts.count() != 1 {
		t.Fatalf("accounts server calls = %d, want 1", accounts.count())
	}

	want := map[string]string{
		"grant_type":    "refresh_token",
		"client_id":     testClientID,
		"client_secret": testClientSecret,
		"refresh_token": testRefreshToken,
	}
	for k, v := range want {
		if got := accounts.forms[0][k]; got != v {
			t.Errorf("form[%s] = %q, want %q", k, got, v)
		}
	}
}

// TestRefreshingToken_CachesUntilExpiry covers the token being reused: a
// triage run makes dozens of Desk calls, and one refresh has to serve all
// of them.
func TestRefreshingToken_CachesUntilExpiry(t *testing.T) {
	srv, accounts := newAccountsServer(t, "access-1")
	rt := newRefreshing(srv)

	for i := 0; i < 5; i++ {
		got, err := rt.Token(context.Background())
		if err != nil {
			t.Fatalf("Token %d: %v", i, err)
		}
		if got != "access-1" {
			t.Fatalf("Token %d = %q, want %q", i, got, "access-1")
		}
	}
	if accounts.count() != 1 {
		t.Fatalf("accounts server calls = %d, want 1: the token was not cached", accounts.count())
	}
}

// TestRefreshingToken_RefreshesBeforeExpiry drives the clock instead of
// waiting an hour. The token must be replaced while it still has a little
// life left, not after it has none: a request that leaves with three
// seconds of validity can arrive with none.
func TestRefreshingToken_RefreshesBeforeExpiry(t *testing.T) {
	srv, accounts := newAccountsServer(t, "access-1", "access-2")
	rt := newRefreshing(srv)

	base := time.Date(2026, 9, 10, 9, 0, 0, 0, time.UTC)
	now := base
	rt.Now = func() time.Time { return now }

	if got, err := rt.Token(context.Background()); err != nil || got != "access-1" {
		t.Fatalf("first Token = %q, %v", got, err)
	}

	// 30 minutes in: still fresh, no refresh.
	now = base.Add(30 * time.Minute)
	if got, err := rt.Token(context.Background()); err != nil || got != "access-1" {
		t.Fatalf("mid-life Token = %q, %v", got, err)
	}
	if accounts.count() != 1 {
		t.Fatalf("accounts calls = %d, want 1 at mid-life", accounts.count())
	}

	// 59m30s in: inside the 60s leeway, so it refreshes even though the
	// stated expiry has not arrived.
	now = base.Add(3570 * time.Second)
	got, err := rt.Token(context.Background())
	if err != nil {
		t.Fatalf("late Token: %v", err)
	}
	if got != "access-2" {
		t.Fatalf("late Token = %q, want the refreshed %q", got, "access-2")
	}
	if accounts.count() != 2 {
		t.Fatalf("accounts calls = %d, want 2", accounts.count())
	}
}

// TestRefreshingToken_FailureIsAnAuthError covers a revoked or mistyped
// grant. The operator needs the accounts server's reason; nobody needs the
// client secret or the refresh token, and neither may appear.
func TestRefreshingToken_FailureIsAnAuthError(t *testing.T) {
	srv, accounts := newAccountsServer(t)
	accounts.failure = "invalid_client"
	rt := newRefreshing(srv)

	_, err := rt.Token(context.Background())
	if err == nil {
		t.Fatal("want an error for a rejected grant")
	}
	var serr *source.Error
	if !errors.As(err, &serr) {
		t.Fatalf("error type %T, want *source.Error: %v", err, err)
	}
	if serr.Code != source.Auth {
		t.Fatalf("code = %q, want %q", serr.Code, source.Auth)
	}
	if !strings.Contains(serr.Message, "invalid_client") {
		t.Errorf("message does not carry the accounts server's reason: %q", serr.Message)
	}
	for _, secret := range []string{testClientSecret, testRefreshToken} {
		if strings.Contains(serr.Message, secret) {
			t.Fatalf("the error repeats a secret: %q", serr.Message)
		}
	}
}

// TestRefreshingToken_ExpiresInDrivesTTL covers what doctor reports.
func TestRefreshingToken_ExpiresInDrivesTTL(t *testing.T) {
	srv, _ := newAccountsServer(t, "access-1")
	rt := newRefreshing(srv)
	now := time.Date(2026, 9, 10, 9, 0, 0, 0, time.UTC)
	rt.Now = func() time.Time { return now }

	_, ttl, err := rt.TokenWithExpiry(context.Background())
	if err != nil {
		t.Fatalf("TokenWithExpiry: %v", err)
	}
	if ttl != 3600*time.Second {
		t.Fatalf("ttl = %v, want 1h", ttl)
	}
}

// TestClient_RetriesOnceAfter401 covers an access token Desk stops
// honouring mid-run — revoked, or invalidated by another client refreshing
// the same grant. One refresh and one replay should carry the request
// through rather than failing the whole triage.
func TestClient_RetriesOnceAfter401(t *testing.T) {
	srv, accounts := newAccountsServer(t, "stale-token", "fresh-token")
	rt := newRefreshing(srv)

	var mu sync.Mutex
	var seen []string
	desk := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := strings.TrimPrefix(r.Header.Get("Authorization"), "Zoho-oauthtoken ")
		mu.Lock()
		seen = append(seen, auth)
		mu.Unlock()
		if auth != "fresh-token" {
			http.Error(w, `{"errorCode":"UNAUTHORIZED"}`, http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"555","subject":"payment stuck"}`)
	}))
	t.Cleanup(desk.Close)

	c := New(desk.URL, testOrgID, rt)
	c.HTTP = desk.Client()

	got, err := c.Get(context.Background(), "555")
	if err != nil {
		t.Fatalf("Get after a 401: %v", err)
	}
	if got.Subject != "payment stuck" {
		t.Fatalf("subject = %q", got.Subject)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 2 {
		t.Fatalf("desk requests = %d (%v), want 2: the 401 was not retried", len(seen), seen)
	}
	if seen[0] != "stale-token" || seen[1] != "fresh-token" {
		t.Fatalf("desk saw tokens %v, want the stale one then the refreshed one", seen)
	}
	if accounts.count() != 2 {
		t.Fatalf("accounts calls = %d, want 2", accounts.count())
	}
}

// TestClient_401SurvivesTheRetry covers a token that is not the problem:
// after one refresh and one replay the 401 stands, and it must surface as
// an auth error rather than being retried forever.
func TestClient_401SurvivesTheRetry(t *testing.T) {
	srv, accounts := newAccountsServer(t, "t1", "t2")
	rt := newRefreshing(srv)

	var mu sync.Mutex
	requests := 0
	desk := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests++
		mu.Unlock()
		http.Error(w, `{"errorCode":"UNAUTHORIZED"}`, http.StatusUnauthorized)
	}))
	t.Cleanup(desk.Close)

	c := New(desk.URL, testOrgID, rt)
	c.HTTP = desk.Client()

	_, err := c.Get(context.Background(), "555")
	var serr *source.Error
	if !errors.As(err, &serr) || serr.Code != source.Auth {
		t.Fatalf("err = %v, want a source.Error with code auth", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if requests != 2 {
		t.Fatalf("desk requests = %d, want exactly 2 (one retry, no more)", requests)
	}
	if accounts.count() != 2 {
		t.Fatalf("accounts calls = %d, want 2", accounts.count())
	}
}

// TestStaticToken_IsNotRetried covers the token: path. There is nothing to
// refresh, so a 401 is the answer, and the request must not be replayed.
func TestStaticToken_IsNotRetried(t *testing.T) {
	var mu sync.Mutex
	requests := 0
	desk := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests++
		mu.Unlock()
		http.Error(w, `{"errorCode":"UNAUTHORIZED"}`, http.StatusUnauthorized)
	}))
	t.Cleanup(desk.Close)

	c := NewWithToken(desk.URL, testOrgID, testToken)
	c.HTTP = desk.Client()

	_, err := c.Get(context.Background(), "555")
	var serr *source.Error
	if !errors.As(err, &serr) || serr.Code != source.Auth {
		t.Fatalf("err = %v, want a source.Error with code auth", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if requests != 1 {
		t.Fatalf("desk requests = %d, want 1: a static token has nothing to refresh", requests)
	}
}

// TestRefreshingToken_ConcurrentTokenCallsRefreshOnce covers -race and the
// batch case: several runs asking at once must not each mint a token.
func TestRefreshingToken_ConcurrentTokenCallsRefreshOnce(t *testing.T) {
	srv, accounts := newAccountsServer(t, "access-1")
	rt := newRefreshing(srv)

	var wg sync.WaitGroup
	errs := make([]error, 8)
	tokens := make([]string, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			tokens[i], errs[i] = rt.Token(context.Background())
		}(i)
	}
	wg.Wait()

	for i := range errs {
		if errs[i] != nil {
			t.Fatalf("goroutine %d: %v", i, errs[i])
		}
		if tokens[i] != "access-1" {
			t.Fatalf("goroutine %d got %q", i, tokens[i])
		}
	}
	if accounts.count() != 1 {
		t.Fatalf("accounts calls = %d, want 1", accounts.count())
	}
}
