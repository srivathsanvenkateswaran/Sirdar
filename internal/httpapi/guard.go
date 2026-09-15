package httpapi

import (
	"mime"
	"net"
	"net/http"
	"net/url"
	"path"
	"strings"
)

// The API has no authentication: it answers to whoever can reach the
// listener, which on a loopback bind is every program on the machine — and
// every page the operator's browser has open. A page cannot read a response
// from another origin, but it can *send* a request: an HTML form posting to
// http://127.0.0.1:7777/api/workspaces/ws1/fix would start an agent session
// that writes code and opens a pull request, and the operator would see
// nothing but a tab that navigated away.
//
// Four checks close that, all of them on what the browser itself says about
// where the request came from and where it thinks it is going:
//
//   - Host must actually name this listener: loopback, when the listener
//     is loopback-only, or the address it was configured with otherwise.
//     Comparing Origin to r.Host (below) is only meaningful once r.Host
//     itself is trusted — otherwise a DNS-rebinding page (a hostname that
//     resolves to 127.0.0.1 while its page still runs as that hostname's
//     origin) sends Host and Origin that agree with each other and neither
//     names this server's real address.
//   - Content-Type must be application/json. A form post can only send
//     application/x-www-form-urlencoded, multipart/form-data or text/plain;
//     anything else needs fetch/XHR, which is already subject to CORS.
//   - Origin, when the browser sends one, must be this server's own or the
//     Wails shell's.
//   - Sec-Fetch-Site must not say the request came from another site.
//
// A caller with no browser in the way — curl, the CLI's own tests, a script
// the operator wrote — sends no Origin and no Sec-Fetch-Site and is
// unaffected, which is deliberate: this is a cross-site defence, not
// authentication, and nothing here stops a program already running as the
// operator.
//
// The hook routes are exempt. They authenticate each delivery by signature
// or shared secret, they are posted to by trackers rather than browsers,
// and their bodies are the tracker's own content type.

// wailsOrigins are the origins the Wails shell's webview loads the UI from:
// a custom scheme on macOS and Linux, and a mapped loopback host on Windows
// (see wails/v2 internal/frontend/desktop/*/frontend.go).
//
// The desktop app does not in fact reach this server — desktop/frontend
// picks createWailsTransport() whenever window.go.main.Bridge is bound, and
// calls the Go side in process — so nothing here is load-bearing for it
// today. They are allowed explicitly so that a shell which ever does fall
// back to the HTTP transport keeps working instead of failing with a 403
// nobody can place.
var wailsOrigins = map[string]bool{
	"wails://wails":           true, // macOS (WKWebView), Linux (WebKit2GTK)
	"http://wails.localhost":  true, // Windows (WebView2)
	"https://wails.localhost": true,
}

// wailsHosts is the Host header value each origin in wailsOrigins would
// carry, derived from it so the two lists cannot drift apart.
var wailsHosts = func() map[string]bool {
	m := make(map[string]bool, len(wailsOrigins))
	for origin := range wailsOrigins {
		if u, err := url.Parse(origin); err == nil && u.Host != "" {
			m[strings.ToLower(u.Host)] = true
		}
	}
	return m
}()

// LoopbackOnly tells the server whether its listener can be reached only
// from this machine. It is the bind decision `sirdar serve` made, not a
// guess this package could make from a request: the fix route writes code
// and opens pull requests, and that is refused when a remote caller could
// be the one asking.
//
// Without it the server assumes the listener is reachable and refuses the
// fix route, so a shell that never thought about the question fails closed.
func LoopbackOnly(v bool) Option {
	return func(s *server) { s.loopbackOnly = v }
}

// ListenAddr tells the guard the host:port `sirdar serve` bound to. It only
// matters on a listener that is not loopback-only (see LoopbackOnly): there
// the guard cannot expect Host to name loopback, so instead it pins Host to
// the address the operator actually configured, when that address names
// one host rather than every interface (a wildcard bind such as ":7777" or
// "0.0.0.0:7777" answers on many, so there is nothing to pin to).
//
// The zero value — what a caller that never thought about this passes —
// leaves the check with nothing to compare against, which skips it rather
// than refusing every request on a listener nobody described.
func ListenAddr(hostport string) Option {
	return func(s *server) { s.listenAddr = hostport }
}

// safeMethod reports whether the method only reads. These carry no guard:
// a GET has nothing to change, and the responses are already unreadable
// cross-origin without CORS headers, which this server never sends.
func safeMethod(m string) bool {
	return m == http.MethodGet || m == http.MethodHead || m == http.MethodOptions
}

// guard runs the cross-site checks, writing the refusal itself and
// returning false when the request must not reach a handler.
func (s *server) guard(w http.ResponseWriter, r *http.Request) bool {
	if safeMethod(r.Method) {
		return true
	}
	exempt, ok := hooksExempt(w, r)
	if !ok {
		return false // hooksExempt already wrote the refusal.
	}
	if exempt {
		return true
	}
	if !s.validHost(w, r) {
		return false
	}
	// Sec-Fetch-Site is the browser's own account of the relationship
	// between the page and this server. "same-site" is refused as well as
	// "cross-site": a sibling host that resolves to loopback is a different
	// origin, and nothing the operator runs needs to post from one.
	switch r.Header.Get("Sec-Fetch-Site") {
	case "cross-site", "same-site":
		writeError(w, http.StatusForbidden, "forbidden",
			"this endpoint does not accept requests from another site")
		return false
	}
	if origin := r.Header.Get("Origin"); origin != "" && !s.allowedOrigin(r, origin) {
		writeError(w, http.StatusForbidden, "forbidden",
			"this endpoint does not accept requests from "+origin)
		return false
	}
	return jsonContentType(w, r)
}

// hooksExempt reports whether r's path names a hook route once normalized,
// refusing outright when the path cannot be trusted enough to answer that
// question at all.
//
// The path is checked against path.Clean, not the raw one: prefix-matching
// r.URL.Path directly would let "/hooks/../api/workspaces/ws1/fix" claim
// the hooks exemption on the strength of its first segment, whatever a
// ServeMux redirect might have done with it afterwards. A literal ".."
// cannot survive path.Clean on a rooted path, so its presence in the raw,
// still-escaped path — "%2e%2e" included — is refused directly rather than
// silently resolved away.
func hooksExempt(w http.ResponseWriter, r *http.Request) (exempt, ok bool) {
	if strings.Contains(strings.ToLower(r.URL.EscapedPath()), "%2e") {
		writeError(w, http.StatusForbidden, "forbidden", "malformed path")
		return false, false
	}
	clean := path.Clean(r.URL.Path)
	for _, seg := range strings.Split(clean, "/") {
		if seg == ".." {
			writeError(w, http.StatusForbidden, "forbidden", "malformed path")
			return false, false
		}
	}
	return clean == "/hooks" || strings.HasPrefix(clean, "/hooks/"), true
}

// validHost reports whether r.Host is one this server should trust an
// Origin comparison against, writing the refusal itself when it is not.
//
// On a loopback listener the threat is DNS rebinding: a page served from
// attacker-controlled.example, resolved to 127.0.0.1, can send both
// Host: attacker-controlled.example and Origin: http://attacker-controlled.example
// — the two agree with each other, so comparing Origin to Host proves
// nothing unless Host itself is already known to name loopback.
//
// On an explicit --allow-remote listener there is no fixed address for a
// rebinding attempt to land on, so the check instead pins Host to the
// address the operator configured (see ListenAddr), when that address
// names one host rather than every interface.
func (s *server) validHost(w http.ResponseWriter, r *http.Request) bool {
	ok := isLoopbackHost(r.Host)
	if !s.loopbackOnly {
		ok = s.matchesListenAddr(r.Host)
	}
	if !ok {
		writeError(w, http.StatusForbidden, "forbidden",
			"this endpoint does not accept requests naming host "+r.Host)
	}
	return ok
}

// isLoopbackHost reports whether host — an http.Request.Host value, so
// "host", "host:port" or a bracketed IPv6 literal with either — names
// loopback: localhost, an address in 127.0.0.0/8, ::1, or one of the Wails
// shell's hosts.
func isLoopbackHost(host string) bool {
	h := hostnameOf(host)
	if h == "" {
		return false
	}
	if strings.EqualFold(h, "localhost") || wailsHosts[strings.ToLower(h)] {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

// matchesListenAddr reports whether host equals the configured listener
// address, or whether that address has no single hostname to compare
// against — see ListenAddr.
func (s *server) matchesListenAddr(host string) bool {
	if !addrHasHostname(s.listenAddr) {
		return true
	}
	return strings.EqualFold(host, s.listenAddr)
}

// addrHasHostname reports whether hostport names one specific host rather
// than nothing (the zero value, from a caller that never called ListenAddr)
// or every interface (a wildcard bind such as ":7777" or "0.0.0.0:7777").
func addrHasHostname(hostport string) bool {
	if hostport == "" {
		return false
	}
	host, _, err := net.SplitHostPort(hostport)
	if err != nil {
		host = hostport
	}
	host = strings.Trim(host, "[]")
	if host == "" {
		return false
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsUnspecified() {
		return false
	}
	return true
}

// hostnameOf returns the host part of an http.Request.Host value, stripping
// IPv6 brackets. Go's server never brackets a bare hostname or IPv4
// literal, so this only has work to do for "[::1]" and "[::1]:7777".
func hostnameOf(host string) string {
	if h, _, err := net.SplitHostPort(host); err == nil {
		return h
	}
	return strings.Trim(host, "[]")
}

// allowedOrigin reports whether origin is one this server answers to: the
// origin it is being reached on, or the Wails shell's.
//
// Host and port must match exactly; the scheme need only be http or https.
// A browser reaching an operator's listener through a TLS-terminating
// reverse proxy sends https:// while the listener itself speaks plain HTTP,
// and refusing that would break the UI for no gain — an attacker's page
// cannot be served from this host and port whichever scheme it uses.
func (s *server) allowedOrigin(r *http.Request, origin string) bool {
	if wailsOrigins[strings.ToLower(origin)] {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		// "null" — a sandboxed iframe or a data: URL — lands here.
		return false
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return false
	}
	return strings.EqualFold(u.Host, r.Host)
}

// jsonContentType requires a body this API could have sent itself. A
// request with no body and no declared type is let through: cancel, the
// workspace delete and a resume that answers no question all send none.
func jsonContentType(w http.ResponseWriter, r *http.Request) bool {
	ct := r.Header.Get("Content-Type")
	if ct == "" && r.ContentLength == 0 {
		return true
	}
	mt, _, err := mime.ParseMediaType(ct)
	if err != nil || mt != "application/json" {
		writeError(w, http.StatusUnsupportedMediaType, "unsupported_media_type",
			"this endpoint accepts application/json only")
		return false
	}
	return true
}
