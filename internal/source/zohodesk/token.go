package zohodesk

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/source"
)

// TokenSource supplies the access token every Zoho Desk request carries.
// A call may block while a new token is minted, so it takes a context.
type TokenSource interface {
	Token(ctx context.Context) (string, error)
}

// StaticToken is an access token supplied whole by the operator. Zoho Desk
// access tokens expire an hour after they are issued, so this only serves a
// run somebody is watching; an unattended one wants RefreshingToken.
type StaticToken string

// Token implements TokenSource.
func (s StaticToken) Token(context.Context) (string, error) { return string(s), nil }

// Refresher is implemented by a TokenSource whose token can stop working
// before its recorded expiry — revoked at the console, or superseded. The
// client calls Invalidate with the token Desk rejected, rather than
// clearing the cache outright, so a token another goroutine has just
// fetched is not thrown away with it.
type Refresher interface {
	Invalidate(token string)
}

// refreshLeeway is how far ahead of a token's stated expiry it is treated
// as spent. A request that leaves with three seconds of validity left can
// arrive after they are gone; a minute is enough slack that it cannot.
const refreshLeeway = 60 * time.Second

// defaultTokenLifetime is what an access token is assumed to be worth when
// the accounts server does not say. Zoho's documented lifetime is an hour.
const defaultTokenLifetime = 3600 * time.Second

// RefreshingToken mints Zoho Desk access tokens from a Self Client's
// refresh token, caching the current one in memory and exchanging it for a
// new one when it is within refreshLeeway of expiry, or when Desk rejects
// it. The refresh token itself never leaves this struct: it goes to the
// accounts server and nowhere else, and no error message repeats it.
type RefreshingToken struct {
	AccountsURL  string // e.g. https://accounts.zoho.in
	ClientID     string
	ClientSecret string
	RefreshToken string
	HTTP         *http.Client

	// Now is the clock, so a test can drive a token to expiry without
	// waiting an hour. Nil means time.Now.
	Now func() time.Time

	mu        sync.Mutex
	access    string
	expiresAt time.Time
}

// Token implements TokenSource: it returns the cached access token while it
// has more than refreshLeeway of life left, and otherwise fetches a new one.
func (r *RefreshingToken) Token(ctx context.Context) (string, error) {
	token, _, err := r.TokenWithExpiry(ctx)
	return token, err
}

// TokenWithExpiry returns an access token and how long it remains valid,
// which `sirdar doctor` reports so an operator can see the grant working
// rather than infer it from a Desk call that happened to succeed.
func (r *RefreshingToken) TokenWithExpiry(ctx context.Context) (string, time.Duration, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := r.now()
	if r.access != "" && now.Add(refreshLeeway).Before(r.expiresAt) {
		return r.access, r.expiresAt.Sub(now), nil
	}
	if err := r.refreshLocked(ctx); err != nil {
		return "", 0, err
	}
	return r.access, r.expiresAt.Sub(r.now()), nil
}

// Invalidate implements Refresher: it drops the cached token if it is still
// the one that was rejected.
func (r *RefreshingToken) Invalidate(token string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.access == token {
		r.access = ""
		r.expiresAt = time.Time{}
	}
}

func (r *RefreshingToken) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

func (r *RefreshingToken) client() *http.Client {
	if r.HTTP != nil {
		return r.HTTP
	}
	return &http.Client{Timeout: 30 * time.Second}
}

// tokenResponse is the accounts server's reply. A failed grant comes back
// with HTTP 200 and an "error" field, not a 4xx, so both have to be read.
type tokenResponse struct {
	AccessToken string  `json:"access_token"`
	ExpiresIn   float64 `json:"expires_in"`
	Error       string  `json:"error"`
}

// refreshLocked exchanges the refresh token for a new access token. The
// caller holds r.mu.
func (r *RefreshingToken) refreshLocked(ctx context.Context) error {
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("client_id", r.ClientID)
	form.Set("client_secret", r.ClientSecret)
	form.Set("refresh_token", r.RefreshToken)

	endpoint := strings.TrimSuffix(r.AccountsURL, "/") + "/oauth/v2/token"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return authError("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := r.client().Do(req)
	if err != nil {
		return authError("POST %s: %v", endpoint, err)
	}
	defer resp.Body.Close()

	body, readErr := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var tr tokenResponse
	// A body that will not parse is not quoted back: the request carried
	// the client secret and the refresh token, and an accounts server that
	// is having a bad day is exactly the one that might echo them.
	parsed := readErr == nil && json.Unmarshal(body, &tr) == nil

	switch {
	case tr.Error != "":
		return authError("refresh rejected: %s", tr.Error)
	case resp.StatusCode < 200 || resp.StatusCode >= 300:
		return authError("POST %s: %d", endpoint, resp.StatusCode)
	case !parsed:
		return authError("POST %s: unreadable response", endpoint)
	case tr.AccessToken == "":
		return authError("POST %s: no access token in response", endpoint)
	}

	lifetime := time.Duration(tr.ExpiresIn) * time.Second
	if lifetime <= 0 {
		lifetime = defaultTokenLifetime
	}
	r.access = tr.AccessToken
	r.expiresAt = r.now().Add(lifetime)
	return nil
}

// authError builds the source.Error a refresh failure surfaces as. Every
// caller of it must have already checked that the values it interpolates
// hold no part of the grant.
func authError(format string, args ...any) *source.Error {
	return &source.Error{Code: source.Auth, Message: "zoho oauth: " + fmt.Sprintf(format, args...)}
}

var (
	_ TokenSource = StaticToken("")
	_ TokenSource = (*RefreshingToken)(nil)
	_ Refresher   = (*RefreshingToken)(nil)
)
